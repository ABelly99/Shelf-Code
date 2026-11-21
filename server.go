package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

//go:embed templates/*.json
var templatesFS embed.FS

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

func writeErrData(w http.ResponseWriter, status int, data map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 50<<20))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return errors.New("bad request body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// trusted: may change network settings. On a desktop that is only the PC itself; on a server
// (no screen) every visitor who got past the PIN is the admin.
func trusted(r *http.Request) bool { return serverMode || isLoopback(r) }

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---------- LAN PIN ----------

var sessionSecret = func() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}()

func pinToken(pin string) string {
	h := sha256.Sum256(append(append([]byte{}, sessionSecret...), []byte("pin:"+pin)...))
	return hex.EncodeToString(h[:16])
}

func pinOK(r *http.Request) bool {
	pin := settingString("lan_pin")
	if pin == "" || (!serverMode && isLoopback(r)) {
		// On a server, loopback is usually a reverse proxy, so it gets no free pass.
		return true
	}
	c, err := r.Cookie("shelfcode_pin")
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(pinToken(pin))) == 1
}

func handlePinLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pin string `json:"pin"`
	}
	readJSON(r, &body)
	pin := settingString("lan_pin")
	if pin == "" || subtle.ConstantTimeCompare([]byte(body.Pin), []byte(pin)) != 1 {
		time.Sleep(700 * time.Millisecond)
		writeErr(w, 403, "Wrong PIN")
		return
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{Name: "shelfcode_pin", Value: pinToken(pin), Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 60 * 60 * 24 * 30})
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- middleware ----------

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")

		dbLock.RLock()
		lanOn := settingBool("lan_enabled")
		dbLock.RUnlock()
		if !serverMode && !isLoopback(r) && !lanOn {
			http.Error(w, "SHELFCODE: access from other devices is turned off (Settings → Network on the main PC).", http.StatusForbidden)
			return
		}
		api := strings.HasPrefix(r.URL.Path, "/api/")
		if api {
			// Block other websites from calling the API: they cannot send this custom header
			// without a CORS preflight, which this server never approves.
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Shelfcode") != "1" {
				writeErr(w, 403, "missing X-Shelfcode header")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) && !sameOrigin(o, r.Header.Get("X-Forwarded-Host")) {
				writeErr(w, 403, "cross-origin request refused")
				return
			}
		}
		if r.URL.Path == "/api/pin" || r.URL.Path == "/api/pin-required" {
			next.ServeHTTP(w, r)
			return
		}
		dbLock.RLock()
		ok := pinOK(r)
		dbLock.RUnlock()
		if !ok {
			if api || strings.HasPrefix(r.URL.Path, "/img/") {
				writeErr(w, 401, "pin required")
				return
			}
			// Static files are fine to serve; the app shows the PIN screen.
		}
		if api || strings.HasPrefix(r.URL.Path, "/img/") || strings.HasPrefix(r.URL.Path, "/backup-file/") {
			if r.URL.Path == "/api/events" {
				// Long-lived stream: do not hold the db lock.
				next.ServeHTTP(w, r)
				return
			}
			dbLock.RLock()
			defer dbLock.RUnlock()
			if db == nil {
				writeErr(w, 503, "database is being restored, try again in a moment")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin, host string) bool {
	if host == "" {
		return false
	}
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	return strings.EqualFold(o, host)
}

func handlePinRequired(w http.ResponseWriter, r *http.Request) {
	dbLock.RLock()
	defer dbLock.RUnlock()
	writeJSON(w, map[string]any{"required": !pinOK(r)})
}

// ---------- routes ----------

func routes() http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		static.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /img/{file}", func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.PathValue("file"))
		if name == "." || strings.ContainsAny(name, `/\`) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		http.ServeFile(w, r, filepath.Join(imagesDir(), name))
	})

	mux.HandleFunc("GET /api/pin-required", handlePinRequired)
	mux.HandleFunc("POST /api/pin", handlePinLogin)
	mux.HandleFunc("GET /api/events", handleEvents)
	mux.HandleFunc("POST /api/focus", handleFocus)
	mux.HandleFunc("GET /api/info", handleInfo)

	mux.HandleFunc("GET /api/settings", handleGetSettings)
	mux.HandleFunc("PUT /api/settings", handlePutSettings)

	mux.HandleFunc("GET /api/lookup", handleLookup)
	mux.HandleFunc("GET /api/codes/check", handleCodeCheck)
	mux.HandleFunc("GET /api/codes/next", handleCodeNext)

	mux.HandleFunc("GET /api/items", handleListItems)
	mux.HandleFunc("GET /api/items/{id}", handleGetItem)
	mux.HandleFunc("POST /api/items", handleCreateItem)
	mux.HandleFunc("PUT /api/items/{id}", handleUpdateItem)
	mux.HandleFunc("DELETE /api/items/{id}", handleDeleteItem)
	mux.HandleFunc("POST /api/items/{id}/restore", handleRestoreItem)
	mux.HandleFunc("POST /api/items/{id}/duplicate", handleDuplicateItem)
	mux.HandleFunc("POST /api/items/{id}/variants", handleVariants)
	mux.HandleFunc("POST /api/items/bulk", handleBulk)
	mux.HandleFunc("GET /api/facets", handleFacets)
	mux.HandleFunc("POST /api/trash/empty", handleEmptyTrash)

	mux.HandleFunc("POST /api/items/{id}/images", handleUploadImage)
	mux.HandleFunc("PUT /api/items/{id}/images", handleReorderImages)
	mux.HandleFunc("DELETE /api/images/{id}", handleDeleteImage)

	mux.HandleFunc("GET /api/categories", handleListCategories)
	mux.HandleFunc("POST /api/categories", handleSaveCategory)
	mux.HandleFunc("PUT /api/categories/{id}", handleSaveCategory)
	mux.HandleFunc("DELETE /api/categories/{id}", handleDeleteCategory)
	mux.HandleFunc("POST /api/categories/reorder", handleReorderCategories)
	mux.HandleFunc("GET /api/fields", handleListFields)
	mux.HandleFunc("POST /api/fields", handleSaveField)
	mux.HandleFunc("PUT /api/fields/{id}", handleSaveField)
	mux.HandleFunc("DELETE /api/fields/{id}", handleDeleteField)
	mux.HandleFunc("POST /api/fields/reorder", handleReorderFields)
	mux.HandleFunc("GET /api/templates", handleListTemplates)
	mux.HandleFunc("POST /api/templates/apply", handleApplyTemplate)
	mux.HandleFunc("GET /api/categories/{id}/export", handleExportCategory)

	mux.HandleFunc("GET /api/docs/{kind}", handleListDocs)
	mux.HandleFunc("POST /api/docs/{kind}", handleSaveDoc)
	mux.HandleFunc("PUT /api/docs/{kind}/{id}", handleSaveDoc)
	mux.HandleFunc("DELETE /api/docs/{kind}/{id}", handleDeleteDoc)

	mux.HandleFunc("POST /api/stock", handleStock)
	mux.HandleFunc("POST /api/stock/batch", handleStockBatch)
	mux.HandleFunc("GET /api/stock", handleListStock)
	mux.HandleFunc("GET /api/activity", handleListActivity)
	mux.HandleFunc("GET /api/dashboard", handleDashboard)
	mux.HandleFunc("GET /api/reports", handleReports)
	mux.HandleFunc("POST /api/printed", handlePrinted)

	mux.HandleFunc("POST /api/import/preview", handleImportPreview)
	mux.HandleFunc("POST /api/import/run", handleImportRun)
	mux.HandleFunc("GET /api/export", handleExport)

	mux.HandleFunc("GET /api/backups", handleListBackups)
	mux.HandleFunc("POST /api/backups", handleCreateBackup)
	mux.HandleFunc("POST /api/backups/restore", handleRestoreBackup)
	mux.HandleFunc("POST /api/backups/upload", handleUploadBackup)
	mux.HandleFunc("GET /backup-file/{name}", handleDownloadBackup)

	mux.HandleFunc("GET /api/scanner", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, scanner.snapshot()) })
	mux.HandleFunc("POST /api/scanner/connect", handleScannerConnect)
	mux.HandleFunc("POST /api/scanner/disconnect", func(w http.ResponseWriter, r *http.Request) {
		scanner.Disconnect()
		setSetting(db, "scanner_autoconnect", false)
		logLine(cYellow, "SCANNER", "Disconnected (from the web page).")
		writeJSON(w, scanner.snapshot())
	})
	mux.HandleFunc("POST /api/scanner/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Code string `json:"code"`
		}
		readJSON(r, &b)
		writeJSON(w, map[string]any{"delivered": deliverScan(strings.TrimSpace(b.Code), "test")})
	})

	return middleware(mux)
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	var items, size int64
	db.QueryRow(`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`).Scan(&items)
	db.QueryRow(`SELECT page_count*page_size FROM pragma_page_count(), pragma_page_size()`).Scan(&size)
	writeJSON(w, map[string]any{
		"version":     appVersion,
		"data_dir":    dataDir,
		"items":       items,
		"db_bytes":    size,
		"urls":        serverURLs(),
		"local":       trusted(r),
		"server_mode": serverMode,
		"started_at":  startedAt.Format(time.RFC3339),
	})
}

func handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := getSettings()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !trusted(r) {
		s["lan_pin"] = ""
		if settingString("lan_pin") != "" {
			s["lan_pin_set"] = true
		}
	}
	writeJSON(w, s)
}

// Keys a page may change. Anything else (schema_version, last_backup_day…) stays server-owned.
var writableSettings = map[string]bool{}

func init() {
	for k := range defaultSettings {
		writableSettings[k] = true
	}
	delete(writableSettings, "last_backup_day")
}

func handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Network settings can only be changed from the main PC itself (or by the admin of a server).
	if !trusted(r) {
		delete(body, "lan_enabled")
		delete(body, "lan_pin")
		delete(body, "backup_extra_dir")
	}
	tx, err := db.Begin()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	for k, v := range body {
		if !writableSettings[k] {
			continue
		}
		if err := setSetting(tx, k, normalizeJSONNumbers(v)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	handleGetSettings(w, r)
}

// json.Number -> float64/int64 so settings round-trip as plain numbers.
func normalizeJSONNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, vv := range x {
			x[k] = normalizeJSONNumbers(vv)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = normalizeJSONNumbers(vv)
		}
		return x
	}
	return v
}

func handleScannerConnect(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Site string `json:"site"`
		Code string `json:"code"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if b.Site == "" {
		b.Site = settingString("scanner_relay_url")
	}
	if err := scanner.Connect(b.Site, b.Code); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	code, _ := normalizeConnectCode(b.Code)
	setSetting(db, "scanner_relay_url", strings.TrimSpace(b.Site))
	setSetting(db, "scanner_code", code)
	setSetting(db, "scanner_autoconnect", true)
	logLine(cCyan, "SCANNER", "Connecting with code "+prettyConnectCode(code)+" (from the web page)…")
	writeJSON(w, scanner.snapshot())
}
