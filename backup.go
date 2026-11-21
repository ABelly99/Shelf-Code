package main

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var backupMu = make(chan struct{}, 1)

// makeBackup writes data/backups/shelfcode-<date>-<reason>.zip with the database and all images.
func makeBackup(reason string) (string, error) {
	backupMu <- struct{}{}
	defer func() { <-backupMu }()
	stamp := time.Now().Format("2006-01-02_150405")
	name := fmt.Sprintf("shelfcode-%s-%s.zip", stamp, reason)
	tmpDB := filepath.Join(backupsDir(), "tmp-"+randName()+".db")
	defer os.Remove(tmpDB)
	// VACUUM INTO makes a consistent copy while the app keeps running.
	if _, err := db.Exec(`VACUUM INTO ?`, tmpDB); err != nil {
		return "", err
	}
	path := filepath.Join(backupsDir(), name)
	f, err := os.Create(path + ".part")
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	add := func(src, dst string, compress bool) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		method := zip.Store
		if compress {
			method = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: dst, Method: method, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		return err
	}
	if err := add(tmpDB, "shelfcode.db", true); err != nil {
		zw.Close()
		f.Close()
		os.Remove(path + ".part")
		return "", err
	}
	entries, _ := os.ReadDir(imagesDir())
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Images are already compressed: store them as-is (much faster).
		if err := add(filepath.Join(imagesDir(), e.Name()), "images/"+e.Name(), false); err != nil {
			zw.Close()
			f.Close()
			os.Remove(path + ".part")
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	if err := os.Rename(path+".part", path); err != nil {
		return "", err
	}
	if extra := settingString("backup_extra_dir"); extra != "" {
		if err := copyFile(path, filepath.Join(extra, name)); err != nil {
			logLine(cYellow, "BACKUP", "Could not copy the backup to "+extra+": "+err.Error())
		}
	}
	pruneBackups()
	return name, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type backupInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
	Reason  string `json:"reason"`
}

func listBackups() []backupInfo {
	entries, _ := os.ReadDir(backupsDir())
	out := []backupInfo{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "shelfcode-") || !strings.HasSuffix(n, ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		reason := strings.TrimSuffix(n, ".zip")
		if parts := strings.SplitN(reason, "-", 5); len(parts) == 5 {
			// shelfcode-YYYY-MM-DD_HHMMSS-reason
			reason = parts[4]
		}
		out = append(out, backupInfo{Name: n, Size: info.Size(), Created: info.ModTime().Format("2006-01-02 15:04"), Reason: reason})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name > out[b].Name })
	return out
}

func pruneBackups() {
	keep := int(settingInt("backup_keep"))
	if keep < 1 {
		keep = 14
	}
	daily, auto := 0, 0
	for _, b := range listBackups() {
		switch {
		case b.Reason == "daily":
			daily++
			if daily > keep {
				os.Remove(filepath.Join(backupsDir(), b.Name))
			}
		case strings.HasPrefix(b.Reason, "before-"):
			auto++
			if auto > 10 {
				os.Remove(filepath.Join(backupsDir(), b.Name))
			}
		}
	}
}

func dailyBackup() {
	day := time.Now().Format("2006-01-02")
	if settingString("last_backup_day") == day {
		return
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n)
	if n == 0 {
		return
	}
	name, err := makeBackup("daily")
	if err != nil {
		logLine(cRed, "BACKUP", "Daily backup failed: "+err.Error())
		return
	}
	setSetting(db, "last_backup_day", day)
	logLine(cGray, "BACKUP", "Daily backup saved: "+name)
}

func handleListBackups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"backups": listBackups(), "dir": backupsDir()})
}

func handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	name, err := makeBackup("manual")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	logLine(cGreen, "BACKUP", "Backup saved: "+name)
	writeJSON(w, map[string]any{"name": name})
}

func safeBackupName(n string) (string, bool) {
	n = filepath.Base(n)
	if !strings.HasPrefix(n, "shelfcode-") || !strings.HasSuffix(n, ".zip") || strings.ContainsAny(n, `/\`) {
		return "", false
	}
	return n, true
}

func handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	n, ok := safeBackupName(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+n+`"`)
	http.ServeFile(w, r, filepath.Join(backupsDir(), n))
}

// The restore handlers must not hold the request read-lock while swapping the database,
// so they release it, take the write lock, and re-acquire the read lock before returning.
func handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	n, ok := safeBackupName(b.Name)
	if !ok {
		writeErr(w, 400, "bad backup name")
		return
	}
	items, err := restoreFrom(filepath.Join(backupsDir(), n))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "items": items})
}

func handleUploadBackup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "upload failed")
		return
	}
	defer f.Close()
	name := fmt.Sprintf("shelfcode-%s-uploaded.zip", time.Now().Format("2006-01-02_150405"))
	path := filepath.Join(backupsDir(), name)
	out, err := os.Create(path)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		os.Remove(path)
		writeErr(w, 500, err.Error())
		return
	}
	out.Close()
	items, err := restoreFrom(path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "items": items})
}

// restoreFrom validates the zip, backs up the current data, then swaps it in.
// Called while the handler holds dbLock.RLock (via middleware).
func restoreFrom(zipPath string) (int64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, errors.New("this is not a SHELFCODE backup (.zip)")
	}
	defer zr.Close()
	var dbFile *zip.File
	for _, f := range zr.File {
		if f.Name == "shelfcode.db" {
			dbFile = f
		}
	}
	if dbFile == nil {
		return 0, errors.New("this zip has no shelfcode.db inside, so it is not a SHELFCODE backup")
	}
	// Check the database inside opens and has items.
	tmp := filepath.Join(backupsDir(), "check-"+randName()+".db")
	defer os.Remove(tmp)
	if err := extractTo(dbFile, tmp); err != nil {
		return 0, err
	}
	check, err := sql.Open("sqlite", tmp)
	if err != nil {
		return 0, err
	}
	var items int64
	err = check.QueryRow(`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`).Scan(&items)
	check.Close()
	if err != nil {
		return 0, errors.New("the database in this backup could not be read")
	}
	if _, err := makeBackup("before-restore"); err != nil {
		return 0, fmt.Errorf("could not back up the current data first: %w", err)
	}

	// Swap: drop our read lock, take the write lock so no request touches the db.
	dbLock.RUnlock()
	dbLock.Lock()
	defer func() {
		dbLock.Unlock()
		dbLock.RLock()
	}()
	closeDB()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(dbPath() + suffix)
	}
	if err := copyFile(tmp, dbPath()); err != nil {
		openDB()
		return 0, err
	}
	// Images: replace the folder contents with the ones from the backup.
	entries, _ := os.ReadDir(imagesDir())
	for _, e := range entries {
		os.Remove(filepath.Join(imagesDir(), e.Name()))
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "images/") {
			continue
		}
		base := filepath.Base(f.Name)
		if base == "." || base == "images" || strings.ContainsAny(base, `/\`) || strings.HasPrefix(base, "..") {
			continue
		}
		extractTo(f, filepath.Join(imagesDir(), base))
	}
	if err := openDB(); err != nil {
		return 0, fmt.Errorf("restored database failed to open: %w", err)
	}
	logLine(cGreen, "BACKUP", fmt.Sprintf("Restored %s (%d items).", filepath.Base(zipPath), items))
	return items, nil
}

func extractTo(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 8<<30)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
