package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// dbLock guards the global db handle. Requests take a read lock; restore takes the write lock
// so it can close, replace and reopen the database file.
var (
	dbLock sync.RWMutex
	db     *sql.DB
)

const schemaVersion = 2

func dbPath() string { return filepath.Join(dataDir, "shelfcode.db") }

func openDB() error {
	// WAL: readers never block the writer. _txlock=immediate: a transaction takes the write lock
	// when it starts, so two writers queue up (busy_timeout) instead of failing halfway.
	d, err := sql.Open("sqlite", dbPath()+"?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return err
	}
	d.SetMaxOpenConns(6)
	if err := d.Ping(); err != nil {
		d.Close()
		return err
	}
	db = d
	return migrate()
}

func closeDB() {
	if db != nil {
		db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		db.Close()
		db = nil
	}
}

var schemaV1 = []string{
	`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS categories (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		parent_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		color TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0,
		code_prefix TEXT NOT NULL DEFAULT '',
		next_seq INTEGER NOT NULL DEFAULT 1,
		default_format TEXT NOT NULL DEFAULT '',
		unique_items INTEGER NOT NULL DEFAULT 0,
		label_template_id INTEGER,
		hidden_builtins TEXT NOT NULL DEFAULT '[]',
		variant_fields TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS fields (
		id INTEGER PRIMARY KEY,
		category_id INTEGER REFERENCES categories(id) ON DELETE CASCADE,
		key TEXT NOT NULL,
		label TEXT NOT NULL,
		type TEXT NOT NULL,
		options TEXT NOT NULL DEFAULT '{}',
		required INTEGER NOT NULL DEFAULT 0,
		uniq INTEGER NOT NULL DEFAULT 0,
		as_code INTEGER NOT NULL DEFAULT 0,
		show_in_list INTEGER NOT NULL DEFAULT 0,
		show_on_label INTEGER NOT NULL DEFAULT 0,
		grp TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0,
		default_value TEXT NOT NULL DEFAULT '',
		help_text TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS items (
		id INTEGER PRIMARY KEY,
		code TEXT NOT NULL UNIQUE,
		barcode_format TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
		description TEXT NOT NULL DEFAULT '',
		price INTEGER,
		cost INTEGER,
		quantity INTEGER NOT NULL DEFAULT 1,
		low_stock_at INTEGER,
		status TEXT NOT NULL DEFAULT 'in_stock',
		location TEXT NOT NULL DEFAULT '',
		supplier TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		custom TEXT NOT NULL DEFAULT '{}',
		extra_codes TEXT NOT NULL DEFAULT '[]',
		parent_id INTEGER REFERENCES items(id) ON DELETE SET NULL,
		notes TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		deleted_at TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS items_cat ON items(category_id)`,
	`CREATE INDEX IF NOT EXISTS items_status ON items(status)`,
	`CREATE INDEX IF NOT EXISTS items_name ON items(name)`,
	`CREATE INDEX IF NOT EXISTS items_updated ON items(updated_at)`,
	`CREATE INDEX IF NOT EXISTS items_deleted ON items(deleted_at)`,
	`CREATE INDEX IF NOT EXISTS items_parent ON items(parent_id)`,
	// Every scannable code (main code, extra codes, "acts as code" fields) lives here.
	// The primary key makes codes unique across all of them.
	`CREATE TABLE IF NOT EXISTS code_index (
		code TEXT PRIMARY KEY,
		code_lower TEXT NOT NULL,
		item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		source TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS code_index_lower ON code_index(code_lower)`,
	`CREATE INDEX IF NOT EXISTS code_index_item ON code_index(item_id)`,
	`CREATE TABLE IF NOT EXISTS images (
		id INTEGER PRIMARY KEY,
		item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		file TEXT NOT NULL,
		thumb TEXT NOT NULL,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS images_item ON images(item_id)`,
	// Generic JSON documents: label templates, print jobs, saved views.
	`CREATE TABLE IF NOT EXISTS docs (
		id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		data TEXT NOT NULL DEFAULT '{}',
		sort_order INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS docs_kind ON docs(kind)`,
	`CREATE TABLE IF NOT EXISTS stock_movements (
		id INTEGER PRIMARY KEY,
		item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		change INTEGER NOT NULL,
		reason TEXT NOT NULL,
		price_each INTEGER,
		note TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS stock_item ON stock_movements(item_id)`,
	`CREATE INDEX IF NOT EXISTS stock_created ON stock_movements(created_at)`,
	`CREATE TABLE IF NOT EXISTS activity (
		id INTEGER PRIMARY KEY,
		item_id INTEGER,
		action TEXT NOT NULL,
		details TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS activity_item ON activity(item_id)`,
	`CREATE INDEX IF NOT EXISTS activity_created ON activity(created_at)`,
}

func migrate() error {
	if _, err := db.Exec(schemaV1[0]); err != nil {
		return err
	}
	v := 0
	var s string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='schema_version'`).Scan(&s); err == nil {
		fmt.Sscan(s, &v)
	}
	if v >= schemaVersion {
		return nil
	}
	if v > 0 {
		// Back up before changing an existing database.
		if _, err := makeBackup("before-upgrade"); err != nil {
			return fmt.Errorf("backup before upgrade failed: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if v < 1 {
		for _, q := range schemaV1 {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("schema: %w", err)
			}
		}
	}
	if v < 2 {
		// 1.0.0 generated counting-up codes (PH-000001). New default: random numbers.
		// Only replace the old default; a pattern someone chose stays.
		if _, err := tx.Exec(`DELETE FROM settings WHERE key='code_pattern' AND value='"{PREFIX}{SEQ:6}"'`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO settings(key,value) VALUES('schema_version',?)`, fmt.Sprint(schemaVersion)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return seedDefaults()
}

// ---------- settings ----------

var defaultSettings = map[string]any{
	"store_name":          "",
	"currency_symbol":     "$",
	"currency_after":      false,
	"decimal_sep":         ".",
	"thousands_sep":       ",",
	"unit":                "mm",
	"theme":               "system",
	"density":             "compact",
	"default_format":      "code128",
	"code_pattern":        defaultCodePattern,
	"global_prefix":       "SC-",
	"global_next_seq":     1,
	"lookup_ignore_case":  false,
	"lookup_keep_text":    false,
	"lookup_nomatch":      "none",    // none | sound | flash
	"lookup_unknown":      "nothing", // nothing | offer
	"scan_threshold_ms":   35,
	"scan_min_length":     4,
	"scan_beep":           false,
	"scanner_relay_url":   "https://barcode-scanner.abelly99.workers.dev",
	"scanner_code":        "",
	"scanner_autoconnect": true,
	"paper":               "letter",
	"page_margin_mm":      6,
	"page_gap_mm":         2,
	"cut_lines":           true,
	"calibration":         map[string]any{},
	"default_label_id":    0,
	"statuses": []map[string]string{
		{"key": "in_stock", "label": "In stock", "color": "success"},
		{"key": "reserved", "label": "Reserved", "color": "warning"},
		{"key": "sold", "label": "Sold", "color": "muted"},
		{"key": "archived", "label": "Archived", "color": "muted"},
	},
	"hide_cost":         false,
	"default_quantity":  1,
	"default_low_stock": nil,
	"trash_days":        30,
	"backup_keep":       14,
	"backup_extra_dir":  "",
	"lan_enabled":       true,
	"lan_pin":           "",
	"onboarded":         false,
	"auto_update":       true,
	"last_print_job":    nil,
	"last_backup_day":   "",
}

func getSettings() (map[string]any, error) {
	out := map[string]any{}
	for k, v := range defaultSettings {
		out[k] = v
	}
	rows, err := db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		if k == "schema_version" {
			continue
		}
		var x any
		if json.Unmarshal([]byte(v), &x) == nil {
			out[k] = x
		}
	}
	return out, rows.Err()
}

func getSetting(key string) any {
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err == nil {
		var x any
		if json.Unmarshal([]byte(v), &x) == nil {
			return x
		}
	}
	return defaultSettings[key]
}

func settingString(key string) string {
	if s, ok := getSetting(key).(string); ok {
		return s
	}
	return ""
}

func settingBool(key string) bool {
	b, _ := getSetting(key).(bool)
	return b
}

func settingInt(key string) int64 {
	switch v := getSetting(key).(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func setSetting(e execer, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = e.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(b))
	return err
}

func now() string { return time.Now().Format("2006-01-02T15:04:05") }

func ensureDirs() error {
	for _, d := range []string{dataDir, imagesDir(), backupsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func imagesDir() string  { return filepath.Join(dataDir, "images") }
func backupsDir() string { return filepath.Join(dataDir, "backups") }

// ---------- default label templates ----------

func seedDefaults() error {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM docs WHERE kind='label'`).Scan(&n)
	if n > 0 {
		return nil
	}
	type el = map[string]any
	labels := []struct {
		name string
		w, h float64
		els  []el
	}{
		{"Tiny 25×15 (code only)", 25, 15, []el{
			{"type": "barcode", "show_text": true},
		}},
		{"Small 38×25", 38, 25, []el{
			{"type": "text", "source": "name", "pos": "top", "size": 7, "bold": true, "lines": 1, "align": "center"},
			{"type": "barcode", "show_text": true},
		}},
		{"Medium 50×30", 50, 30, []el{
			{"type": "text", "source": "name", "pos": "top", "size": 8, "bold": true, "lines": 1, "align": "center"},
			{"type": "text", "source": "price", "pos": "top", "size": 10, "bold": true, "lines": 1, "align": "center"},
			{"type": "barcode", "show_text": true},
		}},
		{"Hang tag 70×40", 70, 40, []el{
			{"type": "text", "source": "store", "pos": "top", "size": 6, "bold": false, "lines": 1, "align": "center"},
			{"type": "text", "source": "name", "pos": "top", "size": 9, "bold": true, "lines": 1, "align": "center"},
			{"type": "text", "source": "field:size", "pos": "top", "size": 8, "bold": false, "lines": 1, "align": "center", "prefix": "Size "},
			{"type": "text", "source": "field:color", "pos": "top", "size": 7, "bold": false, "lines": 1, "align": "center"},
			{"type": "barcode", "show_text": true},
			{"type": "text", "source": "price", "pos": "bottom", "size": 12, "bold": true, "lines": 1, "align": "center"},
		}},
		{"Phone sticker 50×25", 50, 25, []el{
			{"type": "text", "source": "field:model", "pos": "top", "size": 7, "bold": true, "lines": 1, "align": "left"},
			{"type": "text", "source": "field:storage", "pos": "top", "size": 6, "bold": false, "lines": 1, "align": "left", "suffix": ""},
			{"type": "text", "source": "field:grade", "pos": "top", "size": 6, "bold": false, "lines": 1, "align": "left", "prefix": "Grade "},
			{"type": "barcode", "show_text": true},
			{"type": "text", "source": "price", "pos": "bottom", "size": 8, "bold": true, "lines": 1, "align": "right"},
		}},
		{"Shelf label 90×35", 90, 35, []el{
			{"type": "text", "source": "name", "pos": "top", "size": 10, "bold": true, "lines": 2, "align": "left"},
			{"type": "text", "source": "price", "pos": "top", "size": 18, "bold": true, "lines": 1, "align": "right"},
			{"type": "barcode", "show_text": true},
		}},
		{"QR card 40×40", 40, 40, []el{
			{"type": "barcode", "show_text": true, "format": "qrcode"},
			{"type": "text", "source": "name", "pos": "bottom", "size": 7, "bold": true, "lines": 1, "align": "center"},
		}},
	}
	for i, l := range labels {
		data, _ := json.Marshal(map[string]any{"w": l.w, "h": l.h, "elements": l.els, "format": ""})
		if _, err := db.Exec(`INSERT INTO docs(kind,name,data,sort_order,updated_at) VALUES('label',?,?,?,?)`, l.name, string(data), i, now()); err != nil {
			return err
		}
	}
	return nil
}
