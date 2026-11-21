package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func randName() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var imageExt = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif"}

func saveUpload(r *http.Request, field string, itemID int64, suffix string) (string, error) {
	f, _, err := r.FormFile(field)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ct := http.DetectContentType(head[:n])
	ext, ok := imageExt[ct]
	if !ok {
		// DetectContentType knows webp only in newer versions; check the RIFF header by hand.
		if n >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP" {
			ext = ".webp"
		} else {
			return "", badReq("This file type is not supported. Use JPG, PNG or WebP.")
		}
	}
	name := strings.ReplaceAll(strings.ToLower(filepathSafe(itemID)), " ", "") + "_" + randName() + suffix + ext
	out, err := os.Create(filepath.Join(imagesDir(), name))
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := out.Write(head[:n]); err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.LimitReader(f, 25<<20)); err != nil {
		return "", err
	}
	return name, nil
}

func filepathSafe(id int64) string { return "item" + itoa(id) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// POST /api/items/{id}/images  multipart: full, thumb (both resized in the browser)
func handleUploadImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "upload too large or broken")
		return
	}
	var x int
	if db.QueryRow(`SELECT 1 FROM items WHERE id=?`, id).Scan(&x) != nil {
		writeErr(w, 404, "Item not found")
		return
	}
	full, err := saveUpload(r, "full", id, "")
	if err != nil {
		sendErr(w, err)
		return
	}
	thumb, err := saveUpload(r, "thumb", id, "_t")
	if err != nil {
		thumb = full
	}
	var so int64
	db.QueryRow(`SELECT COALESCE(MAX(sort_order),-1)+1 FROM images WHERE item_id=?`, id).Scan(&so)
	res, err := db.Exec(`INSERT INTO images(item_id, file, thumb, sort_order, created_at) VALUES(?,?,?,?,?)`, id, full, thumb, so, now())
	if err != nil {
		sendErr(w, err)
		return
	}
	imgID, _ := res.LastInsertId()
	db.Exec(`UPDATE items SET updated_at=? WHERE id=?`, now(), id)
	writeJSON(w, map[string]any{"id": imgID, "url": "/img/" + full, "thumb": "/img/" + thumb})
}

// PUT /api/items/{id}/images {ids:[...]}  (first = primary)
func handleReorderImages(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	for i, imgID := range b.IDs {
		tx.Exec(`UPDATE images SET sort_order=? WHERE id=? AND item_id=?`, i, imgID, id)
	}
	tx.Exec(`UPDATE items SET updated_at=? WHERE id=?`, now(), id)
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

func handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var file, thumb string
	var itemID int64
	if db.QueryRow(`SELECT file, thumb, item_id FROM images WHERE id=?`, id).Scan(&file, &thumb, &itemID) != nil {
		writeErr(w, 404, "not found")
		return
	}
	db.Exec(`DELETE FROM images WHERE id=?`, id)
	db.Exec(`UPDATE items SET updated_at=? WHERE id=?`, now(), itemID)
	removeImageFile(file)
	removeImageFile(thumb)
	writeJSON(w, map[string]any{"ok": true})
}

// removeImageFile deletes a file only when no image row still points at it (copies share files).
func removeImageFile(name string) {
	if name == "" {
		return
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM images WHERE file=? OR thumb=?`, name, name).Scan(&n)
	if n == 0 {
		os.Remove(filepath.Join(imagesDir(), filepath.Base(name)))
	}
}

func copyImages(from, to int64) {
	rows, err := db.Query(`SELECT file, thumb, sort_order FROM images WHERE item_id=? ORDER BY sort_order, id`, from)
	if err != nil {
		return
	}
	type img struct {
		f, t string
		so   int64
	}
	list := []img{}
	for rows.Next() {
		var i img
		rows.Scan(&i.f, &i.t, &i.so)
		list = append(list, i)
	}
	rows.Close()
	for _, i := range list {
		// Files are shared between copies; removeImageFile only deletes when unused.
		db.Exec(`INSERT INTO images(item_id, file, thumb, sort_order, created_at) VALUES(?,?,?,?,?)`, to, i.f, i.t, i.so, now())
	}
}
