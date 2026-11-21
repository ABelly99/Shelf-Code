package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ExtraCode struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

type ImageRef struct {
	ID    int64  `json:"id"`
	URL   string `json:"url"`
	Thumb string `json:"thumb"`
}

type Item struct {
	ID            int64          `json:"id"`
	Code          string         `json:"code"`
	BarcodeFormat string         `json:"barcode_format"`
	Name          string         `json:"name"`
	CategoryID    *int64         `json:"category_id"`
	CategoryName  string         `json:"category_name,omitempty"`
	Description   string         `json:"description"`
	Price         *int64         `json:"price"`
	Cost          *int64         `json:"cost"`
	Quantity      int64          `json:"quantity"`
	LowStockAt    *int64         `json:"low_stock_at"`
	Status        string         `json:"status"`
	Location      string         `json:"location"`
	Supplier      string         `json:"supplier"`
	Tags          []string       `json:"tags"`
	Custom        map[string]any `json:"custom"`
	ExtraCodes    []ExtraCode    `json:"extra_codes"`
	ParentID      *int64         `json:"parent_id"`
	Notes         string         `json:"notes"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	DeletedAt     *string        `json:"deleted_at"`
	Thumb         string         `json:"thumb,omitempty"`
	Images        []ImageRef     `json:"images,omitempty"`
	// Only on input: the sequence number the code was generated from.
	GenSeq *int64 `json:"gen_seq,omitempty"`
}

const itemCols = `i.id, i.code, i.barcode_format, i.name, i.category_id, COALESCE(c.name,''), i.description, i.price, i.cost,
	i.quantity, i.low_stock_at, i.status, i.location, i.supplier, i.tags, i.custom, i.extra_codes, i.parent_id, i.notes,
	i.created_at, i.updated_at, i.deleted_at,
	COALESCE((SELECT thumb FROM images im WHERE im.item_id=i.id ORDER BY sort_order, id LIMIT 1),'')`

const itemFrom = ` FROM items i LEFT JOIN categories c ON c.id=i.category_id `

type scanner2 interface{ Scan(dest ...any) error }

func scanItem(s scanner2) (*Item, error) {
	var it Item
	var cat, price, cost, low, parent sql.NullInt64
	var deleted sql.NullString
	var tags, custom, extra, thumb string
	err := s.Scan(&it.ID, &it.Code, &it.BarcodeFormat, &it.Name, &cat, &it.CategoryName, &it.Description, &price, &cost,
		&it.Quantity, &low, &it.Status, &it.Location, &it.Supplier, &tags, &custom, &extra, &parent, &it.Notes,
		&it.CreatedAt, &it.UpdatedAt, &deleted, &thumb)
	if err != nil {
		return nil, err
	}
	it.CategoryID = nullPtr(cat)
	it.Price = nullPtr(price)
	it.Cost = nullPtr(cost)
	it.LowStockAt = nullPtr(low)
	it.ParentID = nullPtr(parent)
	if deleted.Valid {
		it.DeletedAt = &deleted.String
	}
	json.Unmarshal([]byte(tags), &it.Tags)
	json.Unmarshal([]byte(custom), &it.Custom)
	json.Unmarshal([]byte(extra), &it.ExtraCodes)
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if it.Custom == nil {
		it.Custom = map[string]any{}
	}
	if it.ExtraCodes == nil {
		it.ExtraCodes = []ExtraCode{}
	}
	if thumb != "" {
		it.Thumb = "/img/" + thumb
	}
	return &it, nil
}

func nullPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func loadItem(q interface {
	QueryRow(string, ...any) *sql.Row
}, id int64) (*Item, error) {
	it, err := scanItem(q.QueryRow(`SELECT `+itemCols+itemFrom+` WHERE i.id=?`, id))
	if err != nil {
		return nil, err
	}
	return it, nil
}

func loadImages(itemID int64) []ImageRef {
	rows, err := db.Query(`SELECT id, file, thumb FROM images WHERE item_id=? ORDER BY sort_order, id`, itemID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []ImageRef{}
	for rows.Next() {
		var r ImageRef
		var f, t string
		rows.Scan(&r.ID, &f, &t)
		r.URL, r.Thumb = "/img/"+f, "/img/"+t
		out = append(out, r)
	}
	return out
}

// ---------- fields / categories helpers ----------

type Field struct {
	ID           int64          `json:"id"`
	CategoryID   *int64         `json:"category_id"`
	Key          string         `json:"key"`
	Label        string         `json:"label"`
	Type         string         `json:"type"`
	Options      map[string]any `json:"options"`
	Required     bool           `json:"required"`
	Unique       bool           `json:"unique"`
	AsCode       bool           `json:"as_code"`
	ShowInList   bool           `json:"show_in_list"`
	ShowOnLabel  bool           `json:"show_on_label"`
	Group        string         `json:"group"`
	SortOrder    int64          `json:"sort_order"`
	DefaultValue string         `json:"default_value"`
	HelpText     string         `json:"help_text"`
}

func loadAllFields() ([]Field, error) {
	rows, err := db.Query(`SELECT id, category_id, key, label, type, options, required, uniq, as_code, show_in_list, show_on_label, grp, sort_order, default_value, help_text FROM fields ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Field{}
	for rows.Next() {
		var f Field
		var cat sql.NullInt64
		var opts string
		rows.Scan(&f.ID, &cat, &f.Key, &f.Label, &f.Type, &opts, &f.Required, &f.Unique, &f.AsCode, &f.ShowInList, &f.ShowOnLabel, &f.Group, &f.SortOrder, &f.DefaultValue, &f.HelpText)
		f.CategoryID = nullPtr(cat)
		json.Unmarshal([]byte(opts), &f.Options)
		if f.Options == nil {
			f.Options = map[string]any{}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// categoryParents returns id -> parent id.
func categoryParents() map[int64]int64 {
	out := map[int64]int64{}
	rows, err := db.Query(`SELECT id, COALESCE(parent_id,0) FROM categories`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, p int64
		rows.Scan(&id, &p)
		out[id] = p
	}
	return out
}

func categoryAncestors(id int64, parents map[int64]int64) []int64 {
	chain := []int64{}
	seen := map[int64]bool{}
	for id != 0 && !seen[id] {
		seen[id] = true
		chain = append([]int64{id}, chain...)
		id = parents[id]
	}
	return chain
}

func categoryDescendants(id int64, parents map[int64]int64) []int64 {
	out := []int64{id}
	for changed := true; changed; {
		changed = false
		for c, p := range parents {
			if contains(out, p) && !contains(out, c) {
				out = append(out, c)
				changed = true
			}
		}
	}
	return out
}

func contains(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// fieldsFor: global fields + fields of the category and all its parents.
func fieldsFor(catID *int64, all []Field) []Field {
	var chain []int64
	if catID != nil {
		chain = categoryAncestors(*catID, categoryParents())
	}
	out := []Field{}
	for _, f := range all {
		if f.CategoryID == nil || contains(chain, *f.CategoryID) {
			out = append(out, f)
		}
	}
	return out
}

// ---------- validation & saving ----------

type apiError struct {
	status int
	data   map[string]any
}

func (e *apiError) Error() string { return fmt.Sprint(e.data["error"]) }

func badReq(msg string) error { return &apiError{400, map[string]any{"error": msg}} }

func sendErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeErrData(w, ae.status, ae.data)
		return
	}
	writeErr(w, 500, err.Error())
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return i, err == nil
	}
	return 0, false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

func isEmptyVal(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	}
	return false
}

// cleanCustom validates custom values against field definitions and normalizes their types.
func cleanCustom(custom map[string]any, fields []Field) (map[string]any, error) {
	out := map[string]any{}
	byKey := map[string]Field{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for k, v := range custom {
		f, known := byKey[k]
		if !known {
			// Keep values of fields from another category (e.g. after a category change) so nothing is lost.
			if !isEmptyVal(v) {
				out[k] = normalizeJSONNumbers(v)
			}
			continue
		}
		if isEmptyVal(v) {
			continue
		}
		switch f.Type {
		case "number", "rating":
			n, ok := toFloat(v)
			if !ok {
				return nil, badReq(f.Label + " must be a number")
			}
			out[k] = n
		case "money":
			n, ok := toInt64(v)
			if !ok {
				return nil, badReq(f.Label + " must be an amount")
			}
			out[k] = n
		case "boolean":
			b, ok := v.(bool)
			if !ok {
				b = fmt.Sprint(v) == "true" || fmt.Sprint(v) == "1"
			}
			out[k] = b
		case "multi_select":
			arr := []string{}
			switch x := v.(type) {
			case []any:
				for _, e := range x {
					if s := strings.TrimSpace(fmt.Sprint(e)); s != "" {
						arr = append(arr, s)
					}
				}
			case string:
				for _, e := range strings.Split(x, ";") {
					if s := strings.TrimSpace(e); s != "" {
						arr = append(arr, s)
					}
				}
			}
			if len(arr) > 0 {
				out[k] = arr
			}
		case "date":
			s := strings.TrimSpace(fmt.Sprint(v))
			if _, err := time.Parse("2006-01-02", s); err != nil {
				return nil, badReq(f.Label + " must be a date (YYYY-MM-DD)")
			}
			out[k] = s
		case "age":
			// computed, never stored
		default:
			s := fmt.Sprint(v)
			if f.Type != "long_text" {
				s = strings.TrimSpace(s)
			}
			out[k] = s
		}
	}
	for _, f := range fields {
		if f.Required && f.Type != "age" && f.Type != "boolean" {
			if _, ok := out[f.Key]; !ok {
				return nil, badReq(f.Label + " is required")
			}
		}
	}
	return out, nil
}

type codeEntry struct{ code, source string }

func collectCodes(it *Item, fields []Field) ([]codeEntry, error) {
	codes := []codeEntry{{it.Code, "main"}}
	for _, e := range it.ExtraCodes {
		if e.Code != "" {
			codes = append(codes, codeEntry{e.Code, "extra:" + e.Kind})
		}
	}
	for _, f := range fields {
		if f.AsCode {
			if s, ok := it.Custom[f.Key].(string); ok && s != "" {
				codes = append(codes, codeEntry{s, "field:" + f.Key})
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c.code] {
			return nil, badReq(fmt.Sprintf("The code %q is used twice on this item", c.code))
		}
		seen[c.code] = true
	}
	return codes, nil
}

func codeOwner(tx interface {
	QueryRow(string, ...any) *sql.Row
}, code string, exclude int64) (int64, string, bool) {
	var id int64
	var name string
	var deleted sql.NullString
	err := tx.QueryRow(`SELECT i.id, i.name, i.deleted_at FROM code_index ci JOIN items i ON i.id=ci.item_id WHERE ci.code=? AND ci.item_id<>?`, code, exclude).Scan(&id, &name, &deleted)
	if err != nil {
		return 0, "", false
	}
	if deleted.Valid {
		name += " (in Trash)"
	}
	return id, name, true
}

func writeCodes(tx *sql.Tx, itemID int64, codes []codeEntry) error {
	for _, c := range codes {
		if id, name, taken := codeOwner(tx, c.code, itemID); taken {
			return &apiError{409, map[string]any{
				"error":    fmt.Sprintf("The code %s already belongs to %s", c.code, name),
				"conflict": map[string]any{"code": c.code, "item_id": id, "item_name": name},
			}}
		}
	}
	if _, err := tx.Exec(`DELETE FROM code_index WHERE item_id=?`, itemID); err != nil {
		return err
	}
	for _, c := range codes {
		if _, err := tx.Exec(`INSERT INTO code_index(code, code_lower, item_id, source) VALUES(?,?,?,?)`, c.code, strings.ToLower(c.code), itemID, c.source); err != nil {
			return err
		}
	}
	return nil
}

func normalizeItem(it *Item) {
	// Codes are text and are kept exactly as given (only outer spaces/newlines removed).
	it.Code = strings.TrimSpace(it.Code)
	it.Name = strings.TrimSpace(it.Name)
	it.Location = strings.TrimSpace(it.Location)
	it.Supplier = strings.TrimSpace(it.Supplier)
	if it.Status == "" {
		it.Status = "in_stock"
	}
	if it.BarcodeFormat == "" {
		it.BarcodeFormat = settingString("default_format")
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, t := range it.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			tags = append(tags, t)
		}
	}
	it.Tags = tags
	extra := []ExtraCode{}
	for _, e := range it.ExtraCodes {
		e.Code = strings.TrimSpace(e.Code)
		if e.Code != "" {
			if e.Kind == "" {
				e.Kind = "other"
			}
			extra = append(extra, e)
		}
	}
	it.ExtraCodes = extra
	if it.Custom == nil {
		it.Custom = map[string]any{}
	}
}

func checkUniqueFields(tx *sql.Tx, it *Item, fields []Field) error {
	for _, f := range fields {
		if !f.Unique || f.AsCode {
			continue
		}
		v, ok := it.Custom[f.Key]
		if !ok {
			continue
		}
		var id int64
		var name string
		err := tx.QueryRow(`SELECT id, name FROM items WHERE json_extract(custom, '$.'||?) = ? AND id<>?`, jsonPathKey(f.Key), v, it.ID).Scan(&id, &name)
		if err == nil {
			return &apiError{409, map[string]any{
				"error":    fmt.Sprintf("%s %v already belongs to %s", f.Label, v, name),
				"conflict": map[string]any{"item_id": id, "item_name": name},
			}}
		}
	}
	return nil
}

var keyRe = regexp.MustCompile(`^[a-z0-9_]+$`)

func jsonPathKey(k string) string {
	if keyRe.MatchString(k) {
		return k
	}
	return `"` + strings.ReplaceAll(k, `"`, ``) + `"`
}

// saveItem inserts (it.ID==0) or updates an item inside tx. Returns the id.
func saveItem(tx *sql.Tx, it *Item, allFields []Field, logIt bool) (int64, error) {
	normalizeItem(it)
	if it.Name == "" {
		return 0, badReq("Name is required")
	}
	if it.Code == "" {
		return 0, badReq("Code is required")
	}
	fields := fieldsFor(it.CategoryID, allFields)
	custom, err := cleanCustom(it.Custom, fields)
	if err != nil {
		return 0, err
	}
	it.Custom = custom
	codes, err := collectCodes(it, fields)
	if err != nil {
		return 0, err
	}
	if err := checkUniqueFields(tx, it, fields); err != nil {
		return 0, err
	}
	tags, _ := json.Marshal(it.Tags)
	cust, _ := json.Marshal(it.Custom)
	extra, _ := json.Marshal(it.ExtraCodes)
	ts := now()

	var old *Item
	if it.ID == 0 {
		res, err := tx.Exec(`INSERT INTO items(code, barcode_format, name, category_id, description, price, cost, quantity, low_stock_at, status, location, supplier, tags, custom, extra_codes, parent_id, notes, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"\x00pending", it.BarcodeFormat, it.Name, it.CategoryID, it.Description, it.Price, it.Cost, it.Quantity, it.LowStockAt, it.Status,
			it.Location, it.Supplier, string(tags), string(cust), string(extra), it.ParentID, it.Notes, ts, ts)
		if err != nil {
			return 0, err
		}
		it.ID, _ = res.LastInsertId()
	} else {
		old, err = loadItem(tx, it.ID)
		if err != nil {
			return 0, &apiError{404, map[string]any{"error": "item not found"}}
		}
	}
	if err := writeCodes(tx, it.ID, codes); err != nil {
		return 0, err
	}
	_, err = tx.Exec(`UPDATE items SET code=?, barcode_format=?, name=?, category_id=?, description=?, price=?, cost=?, quantity=?, low_stock_at=?, status=?, location=?, supplier=?, tags=?, custom=?, extra_codes=?, parent_id=?, notes=?, updated_at=? WHERE id=?`,
		it.Code, it.BarcodeFormat, it.Name, it.CategoryID, it.Description, it.Price, it.Cost, it.Quantity, it.LowStockAt, it.Status,
		it.Location, it.Supplier, string(tags), string(cust), string(extra), it.ParentID, it.Notes, ts, it.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, &apiError{409, map[string]any{"error": "That code is already used"}}
		}
		return 0, err
	}
	if logIt {
		if old == nil {
			logActivity(tx, it.ID, "created", map[string]any{"code": it.Code, "name": it.Name})
			if it.Quantity != 0 {
				tx.Exec(`INSERT INTO stock_movements(item_id, change, reason, note, created_at) VALUES(?,?,?,?,?)`, it.ID, it.Quantity, "initial", "", ts)
			}
		} else {
			diff := diffItems(old, it)
			if len(diff) > 0 {
				logActivity(tx, it.ID, "updated", diff)
			}
			if old.Quantity != it.Quantity {
				tx.Exec(`INSERT INTO stock_movements(item_id, change, reason, note, created_at) VALUES(?,?,?,?,?)`, it.ID, it.Quantity-old.Quantity, "adjustment", "Edited on item page", ts)
			}
		}
	}
	return it.ID, nil
}

func diffItems(a, b *Item) map[string]any {
	d := map[string]any{}
	cmp := func(name string, x, y any) {
		xj, _ := json.Marshal(x)
		yj, _ := json.Marshal(y)
		if string(xj) != string(yj) {
			d[name] = []any{x, y}
		}
	}
	cmp("code", a.Code, b.Code)
	cmp("barcode_format", a.BarcodeFormat, b.BarcodeFormat)
	cmp("name", a.Name, b.Name)
	cmp("category_id", a.CategoryID, b.CategoryID)
	cmp("description", a.Description, b.Description)
	cmp("price", a.Price, b.Price)
	cmp("cost", a.Cost, b.Cost)
	cmp("quantity", a.Quantity, b.Quantity)
	cmp("low_stock_at", a.LowStockAt, b.LowStockAt)
	cmp("status", a.Status, b.Status)
	cmp("location", a.Location, b.Location)
	cmp("supplier", a.Supplier, b.Supplier)
	cmp("tags", a.Tags, b.Tags)
	cmp("extra_codes", a.ExtraCodes, b.ExtraCodes)
	cmp("notes", a.Notes, b.Notes)
	keys := map[string]bool{}
	for k := range a.Custom {
		keys[k] = true
	}
	for k := range b.Custom {
		keys[k] = true
	}
	for k := range keys {
		cmp("custom."+k, a.Custom[k], b.Custom[k])
	}
	return d
}

func logActivity(e execer, itemID int64, action string, details any) {
	b, _ := json.Marshal(details)
	var id any = itemID
	if itemID == 0 {
		id = nil
	}
	e.Exec(`INSERT INTO activity(item_id, action, details, created_at) VALUES(?,?,?,?)`, id, action, string(b), now())
}

// ---------- handlers ----------

func handleLookup(w http.ResponseWriter, r *http.Request) {
	// The strict lookup: exact match on a code, or nothing.
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeJSON(w, map[string]any{})
		return
	}
	var id int64
	var err error
	if settingBool("lookup_ignore_case") {
		err = db.QueryRow(`SELECT ci.item_id FROM code_index ci JOIN items i ON i.id=ci.item_id WHERE ci.code_lower=? AND i.deleted_at IS NULL ORDER BY ci.source='main' DESC LIMIT 1`, strings.ToLower(code)).Scan(&id)
	} else {
		err = db.QueryRow(`SELECT ci.item_id FROM code_index ci JOIN items i ON i.id=ci.item_id WHERE ci.code=? AND i.deleted_at IS NULL`, code).Scan(&id)
	}
	if err != nil {
		writeJSON(w, map[string]any{})
		return
	}
	writeJSON(w, map[string]any{"item_id": id})
}

func handleCodeCheck(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	exclude, _ := strconv.ParseInt(r.URL.Query().Get("exclude"), 10, 64)
	if id, name, taken := codeOwner(db, code, exclude); taken {
		writeJSON(w, map[string]any{"free": false, "item_id": id, "item_name": name})
		return
	}
	writeJSON(w, map[string]any{"free": true})
}

var tokenRe = regexp.MustCompile(`\{(PREFIX|SEQ|YYYY|YY|MM|DD|RAND|CHECK)(?::(\d+))?\}`)

const randChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func renderPattern(pattern, prefix string, seq int64, t time.Time) string {
	out := tokenRe.ReplaceAllStringFunc(pattern, func(m string) string {
		sm := tokenRe.FindStringSubmatch(m)
		n, _ := strconv.Atoi(sm[2])
		switch sm[1] {
		case "PREFIX":
			return prefix
		case "SEQ":
			if n == 0 {
				n = 1
			}
			return fmt.Sprintf("%0*d", n, seq)
		case "YYYY":
			return t.Format("2006")
		case "YY":
			return t.Format("06")
		case "MM":
			return t.Format("01")
		case "DD":
			return t.Format("02")
		case "RAND":
			if n == 0 {
				n = 4
			}
			b := make([]byte, n)
			for i := range b {
				b[i] = randChars[rand.IntN(len(randChars))]
			}
			return string(b)
		}
		return m
	})
	if strings.Contains(out, "{CHECK}") {
		out = strings.Replace(out, "{CHECK}", strconv.Itoa(mod10Check(out)), 1)
	}
	return out
}

// mod10Check: GS1-style check digit over the digits present in s.
func mod10Check(s string) int {
	digits := []int{}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		w := 3
		if (len(digits)-1-i)%2 == 1 {
			w = 1
		}
		sum += digits[i] * w
	}
	return (10 - sum%10) % 10
}

// nextCode finds the next free generated code for a category (or the global sequence).
func nextCode(catID int64) (string, int64, string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", 0, "", err
	}
	defer tx.Rollback()
	return nextCodeTx(tx, catID)
}

func codeFormatFor(tx *sql.Tx, catID int64) string {
	var f string
	if catID > 0 && tx.QueryRow(`SELECT default_format FROM categories WHERE id=?`, catID).Scan(&f) == nil && f != "" {
		return f
	}
	return settingString("default_format")
}

// numericCode builds a valid in-store number: EAN-13 starting with 2 (GS1 range 200-299 is reserved for
// in-store use), UPC-A starting with 4 (in-store), EAN-8 starting with 2.
func numericCode(format string, seq int64) string {
	var body string
	switch format {
	case "ean13":
		body = fmt.Sprintf("200%09d", seq)
	case "upca":
		body = fmt.Sprintf("4%010d", seq)
	case "ean8":
		body = fmt.Sprintf("2%06d", seq)
	}
	return body + strconv.Itoa(mod10Check(body))
}

func handleCodeNext(w http.ResponseWriter, r *http.Request) {
	catID, _ := strconv.ParseInt(r.URL.Query().Get("category"), 10, 64)
	code, seq, format, err := nextCode(catID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"code": code, "seq": seq, "format": format})
}

// bumpSeq moves the category/global counter past a generated code that was actually used.
func bumpSeq(tx *sql.Tx, catID *int64, seq int64) { bumpSeqTx(tx, catID, seq) }

func handleGetItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	it, err := loadItem(db, id)
	if err != nil {
		writeErr(w, 404, "Item not found")
		return
	}
	it.Images = loadImages(id)
	// variants & parent summary
	variants := []map[string]any{}
	rows, err := db.Query(`SELECT id, code, name, quantity, price, custom FROM items WHERE parent_id=? AND deleted_at IS NULL ORDER BY id`, id)
	if err == nil {
		for rows.Next() {
			var vid, q int64
			var code, name, custom string
			var price sql.NullInt64
			rows.Scan(&vid, &code, &name, &q, &price, &custom)
			var c map[string]any
			json.Unmarshal([]byte(custom), &c)
			variants = append(variants, map[string]any{"id": vid, "code": code, "name": name, "quantity": q, "price": nullPtr(price), "custom": c})
		}
		rows.Close()
	}
	var parent map[string]any
	if it.ParentID != nil {
		var pname, pcode string
		if db.QueryRow(`SELECT name, code FROM items WHERE id=?`, *it.ParentID).Scan(&pname, &pcode) == nil {
			parent = map[string]any{"id": *it.ParentID, "name": pname, "code": pcode}
		}
	}
	writeJSON(w, map[string]any{"item": it, "variants": variants, "parent": parent})
}

func handleCreateItem(w http.ResponseWriter, r *http.Request) {
	var it Item
	if err := readJSON(r, &it); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	it.ID = 0
	all, err := loadAllFields()
	if err != nil {
		sendErr(w, err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	id, err := saveItem(tx, &it, all, true)
	if err != nil {
		sendErr(w, err)
		return
	}
	if it.GenSeq != nil {
		bumpSeq(tx, it.CategoryID, *it.GenSeq)
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	saved, _ := loadItem(db, id)
	writeJSON(w, map[string]any{"item": saved})
}

func handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Item
		ExpectUpdatedAt string `json:"expect_updated_at"`
		Force           bool   `json:"force"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	it := body.Item
	it.ID = id
	all, err := loadAllFields()
	if err != nil {
		sendErr(w, err)
		return
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	var cur string
	if err := tx.QueryRow(`SELECT updated_at FROM items WHERE id=?`, id).Scan(&cur); err != nil {
		writeErr(w, 404, "Item not found")
		return
	}
	if body.ExpectUpdatedAt != "" && body.ExpectUpdatedAt != cur && !body.Force {
		writeErrData(w, 409, map[string]any{"error": "This item was changed somewhere else (another tab or device) after you opened it.", "stale": true})
		return
	}
	if _, err := saveItem(tx, &it, all, true); err != nil {
		sendErr(w, err)
		return
	}
	if it.GenSeq != nil {
		bumpSeq(tx, it.CategoryID, *it.GenSeq)
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	saved, _ := loadItem(db, id)
	writeJSON(w, map[string]any{"item": saved})
}

func handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if r.URL.Query().Get("permanent") == "1" {
		if err := purgeItems([]int64{id}); err != nil {
			sendErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	tx.Exec(`UPDATE items SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, now(), id)
	logActivity(tx, id, "deleted", map[string]any{})
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

func handleRestoreItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	tx.Exec(`UPDATE items SET deleted_at=NULL WHERE id=?`, id)
	logActivity(tx, id, "restored", map[string]any{})
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

func purgeItems(ids []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	files := []string{}
	for _, id := range ids {
		rows, err := tx.Query(`SELECT file, thumb FROM images WHERE item_id=?`, id)
		if err == nil {
			for rows.Next() {
				var f, t string
				rows.Scan(&f, &t)
				files = append(files, f, t)
			}
			rows.Close()
		}
		tx.Exec(`UPDATE items SET parent_id=NULL WHERE parent_id=?`, id)
		if _, err := tx.Exec(`DELETE FROM items WHERE id=?`, id); err != nil {
			return err
		}
		logActivity(tx, id, "purged", map[string]any{})
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, f := range files {
		removeImageFile(f)
	}
	return nil
}

func handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	ids := []int64{}
	rows, err := db.Query(`SELECT id FROM items WHERE deleted_at IS NOT NULL`)
	if err != nil {
		sendErr(w, err)
		return
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if err := purgeItems(ids); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"purged": len(ids)})
}

func autoEmptyTrash() {
	days := settingInt("trash_days")
	if days <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -int(days)).Format("2006-01-02T15:04:05")
	ids := []int64{}
	rows, err := db.Query(`SELECT id FROM items WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) > 0 {
		purgeItems(ids)
		logLine(cGray, "TRASH", fmt.Sprintf("Removed %d item(s) older than %d days from the Trash.", len(ids), days))
	}
}

func handleDuplicateItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		CopyImages bool `json:"copy_images"`
	}
	readJSON(r, &body)
	src, err := loadItem(db, id)
	if err != nil {
		writeErr(w, 404, "Item not found")
		return
	}
	var cat int64
	if src.CategoryID != nil {
		cat = *src.CategoryID
	}
	code, seq, format, err := nextCode(cat)
	if err != nil {
		sendErr(w, err)
		return
	}
	all, _ := loadAllFields()
	cp := *src
	cp.ID = 0
	cp.Code = code
	cp.BarcodeFormat = format
	cp.Name = src.Name + " (copy)"
	cp.ExtraCodes = []ExtraCode{}
	// Identifier fields (IMEI, serial…) are per-unit: never copy them.
	for _, f := range fieldsFor(cp.CategoryID, all) {
		if f.Type == "identifier" || f.Unique || f.AsCode {
			delete(cp.Custom, f.Key)
		}
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	newID, err := saveItem(tx, &cp, all, true)
	if err != nil {
		sendErr(w, err)
		return
	}
	bumpSeq(tx, cp.CategoryID, seq)
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	if body.CopyImages {
		copyImages(id, newID)
	}
	writeJSON(w, map[string]any{"id": newID})
}

// POST /api/items/{id}/variants  {axes: [{key, values:[...]}], name_pattern?, price?}
func handleVariants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Axes []struct {
			Key    string   `json:"key"`
			Label  string   `json:"label"`
			Values []string `json:"values"`
		} `json:"axes"`
		Quantity   *int64 `json:"quantity"`
		CopyImages bool   `json:"copy_images"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	parent, err := loadItem(db, id)
	if err != nil {
		writeErr(w, 404, "Item not found")
		return
	}
	combos := []map[string]string{{}}
	for _, ax := range body.Axes {
		if len(ax.Values) == 0 {
			continue
		}
		next := []map[string]string{}
		for _, c := range combos {
			for _, v := range ax.Values {
				v = strings.TrimSpace(v)
				if v == "" {
					continue
				}
				m := map[string]string{}
				for k, x := range c {
					m[k] = x
				}
				m[ax.Key] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	if len(combos) == 0 || len(combos[0]) == 0 {
		writeErr(w, 400, "Pick at least one value")
		return
	}
	if len(combos) > 500 {
		writeErr(w, 400, "That would make more than 500 variants")
		return
	}
	all, _ := loadAllFields()
	var cat int64
	if parent.CategoryID != nil {
		cat = *parent.CategoryID
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	created := []int64{}
	for _, combo := range combos {
		v := *parent
		v.ID = 0
		v.ParentID = &parent.ID
		v.ExtraCodes = []ExtraCode{}
		v.Custom = map[string]any{}
		for k, x := range parent.Custom {
			v.Custom[k] = x
		}
		for _, f := range fieldsFor(v.CategoryID, all) {
			if f.Type == "identifier" || f.Unique || f.AsCode {
				delete(v.Custom, f.Key)
			}
		}
		parts := []string{}
		for _, ax := range body.Axes {
			if val, ok := combo[ax.Key]; ok {
				v.Custom[ax.Key] = val
				parts = append(parts, val)
			}
		}
		v.Name = parent.Name + " – " + strings.Join(parts, " / ")
		if body.Quantity != nil {
			v.Quantity = *body.Quantity
		}
		// generate code inside the tx
		code, seq, format, err := nextCodeTx(tx, cat)
		if err != nil {
			sendErr(w, err)
			return
		}
		v.Code = code
		v.BarcodeFormat = format
		nid, err := saveItem(tx, &v, all, true)
		if err != nil {
			sendErr(w, err)
			return
		}
		bumpSeqTx(tx, v.CategoryID, seq)
		created = append(created, nid)
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	if body.CopyImages {
		for _, nid := range created {
			copyImages(id, nid)
		}
	}
	writeJSON(w, map[string]any{"created": created})
}

// nextCodeTx is nextCode but sees codes inserted earlier in the same transaction.
func nextCodeTx(tx *sql.Tx, catID int64) (string, int64, string, error) {
	pattern := settingString("code_pattern")
	if pattern == "" {
		pattern = "{PREFIX}{SEQ:6}"
	}
	prefix := settingString("global_prefix")
	seq := settingInt("global_next_seq")
	var gs string
	if tx.QueryRow(`SELECT value FROM settings WHERE key='global_next_seq'`).Scan(&gs) == nil {
		fmt.Sscan(gs, &seq)
	}
	format := codeFormatFor(tx, catID)
	if catID > 0 {
		var p string
		var s int64
		if tx.QueryRow(`SELECT code_prefix, next_seq FROM categories WHERE id=?`, catID).Scan(&p, &s) == nil && p != "" {
			prefix, seq = p, s
		}
	}
	if seq < 1 {
		seq = 1
	}
	for tries := 0; tries < 100000; tries++ {
		var code string
		if format == "ean13" || format == "upca" || format == "ean8" {
			code = numericCode(format, seq)
		} else {
			code = renderPattern(pattern, prefix, seq, time.Now())
		}
		var x int
		if tx.QueryRow(`SELECT 1 FROM code_index WHERE code=?`, code).Scan(&x) != nil {
			return code, seq, format, nil
		}
		seq++
	}
	return "", 0, "", errors.New("could not find a free code")
}

func bumpSeqTx(tx *sql.Tx, catID *int64, seq int64) {
	if catID != nil {
		var p string
		if tx.QueryRow(`SELECT code_prefix FROM categories WHERE id=?`, *catID).Scan(&p) == nil && p != "" {
			tx.Exec(`UPDATE categories SET next_seq=MAX(next_seq, ?) WHERE id=?`, seq+1, *catID)
			return
		}
	}
	var cur int64
	var gs string
	if tx.QueryRow(`SELECT value FROM settings WHERE key='global_next_seq'`).Scan(&gs) == nil {
		fmt.Sscan(gs, &cur)
	} else {
		cur = settingInt("global_next_seq")
	}
	if cur <= seq {
		setSetting(tx, "global_next_seq", seq+1)
	}
}

// ---------- listing ----------

type listQuery struct {
	where []string
	args  []any
}

func (q *listQuery) add(cond string, args ...any) {
	q.where = append(q.where, cond)
	q.args = append(q.args, args...)
}

type filterSpec struct {
	K  string `json:"k"`
	Op string `json:"op"`
	V  any    `json:"v"`
}

type sortSpec struct {
	K   string `json:"k"`
	Dir string `json:"dir"`
}

var builtinCols = map[string]string{
	"code": "i.code", "name": "i.name", "price": "i.price", "cost": "i.cost", "quantity": "i.quantity",
	"status": "i.status", "location": "i.location", "supplier": "i.supplier", "created_at": "i.created_at",
	"updated_at": "i.updated_at", "category": "c.name", "barcode_format": "i.barcode_format", "deleted_at": "i.deleted_at",
	"low_stock_at": "i.low_stock_at",
}

func buildListQuery(r *http.Request, fields []Field) (*listQuery, error) {
	q := &listQuery{}
	v := r.URL.Query()
	if v.Get("trash") == "1" {
		q.add("i.deleted_at IS NOT NULL")
	} else {
		q.add("i.deleted_at IS NULL")
	}
	if ids := v.Get("ids"); ids != "" {
		parts := strings.Split(ids, ",")
		ph := []string{}
		for _, p := range parts {
			if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
				ph = append(ph, "?")
				q.args = append(q.args, n)
			}
		}
		if len(ph) == 0 {
			ph = []string{"0"}
		}
		q.where = append(q.where, "i.id IN ("+strings.Join(ph, ",")+")")
	}
	if c := v.Get("category"); c != "" {
		if c == "none" {
			q.add("i.category_id IS NULL")
		} else if id, err := strconv.ParseInt(c, 10, 64); err == nil {
			desc := categoryDescendants(id, categoryParents())
			ph := strings.TrimSuffix(strings.Repeat("?,", len(desc)), ",")
			cond := "i.category_id IN (" + ph + ")"
			for _, d := range desc {
				q.args = append(q.args, d)
			}
			q.where = append(q.where, cond)
		}
	}
	if p := v.Get("parent"); p != "" {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			q.add("i.parent_id=?", id)
		}
	}
	if s := strings.TrimSpace(v.Get("q")); s != "" {
		// Browsing filter ("contains"). This is NOT the strict code lookup.
		for _, word := range strings.Fields(strings.ToLower(s)) {
			like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(word) + "%"
			q.add(`(lower(i.name) LIKE ? ESCAPE '\' OR lower(i.code) LIKE ? ESCAPE '\' OR lower(i.description) LIKE ? ESCAPE '\'
				OR lower(i.location) LIKE ? ESCAPE '\' OR lower(i.supplier) LIKE ? ESCAPE '\' OR lower(i.notes) LIKE ? ESCAPE '\'
				OR lower(i.tags) LIKE ? ESCAPE '\' OR lower(i.extra_codes) LIKE ? ESCAPE '\' OR lower(COALESCE(c.name,'')) LIKE ? ESCAPE '\'
				OR EXISTS (SELECT 1 FROM json_each(i.custom) je WHERE lower(CAST(je.value AS TEXT)) LIKE ? ESCAPE '\'))`,
				like, like, like, like, like, like, like, like, like, like)
		}
	}
	if v.Get("low") == "1" {
		q.add("i.low_stock_at IS NOT NULL AND i.quantity <= i.low_stock_at")
	}
	if fs := v.Get("f"); fs != "" {
		var filters []filterSpec
		if err := json.Unmarshal([]byte(fs), &filters); err != nil {
			return nil, badReq("bad filter")
		}
		ftype := map[string]string{}
		for _, f := range fields {
			if _, ok := ftype[f.Key]; !ok {
				ftype[f.Key] = f.Type
			}
		}
		for _, f := range filters {
			var col string
			isMulti := false
			if strings.HasPrefix(f.K, "custom.") {
				key := strings.TrimPrefix(f.K, "custom.")
				col = fmt.Sprintf("json_extract(i.custom, '$.%s')", strings.ReplaceAll(jsonPathKey(key), "'", ""))
				isMulti = ftype[key] == "multi_select"
				if isMulti {
					col = fmt.Sprintf("'$.%s'", strings.ReplaceAll(jsonPathKey(key), "'", ""))
				}
			} else if f.K == "tags" {
				isMulti = true
				col = "tags"
			} else if c, ok := builtinCols[f.K]; ok {
				col = c
			} else {
				continue
			}
			switch f.Op {
			case "in":
				vals, _ := f.V.([]any)
				if len(vals) == 0 {
					continue
				}
				ph := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
				if isMulti {
					if col == "tags" {
						q.where = append(q.where, "EXISTS (SELECT 1 FROM json_each(i.tags) je WHERE je.value IN ("+ph+"))")
					} else {
						q.where = append(q.where, "EXISTS (SELECT 1 FROM json_each(i.custom, "+col+") je WHERE je.value IN ("+ph+"))")
					}
				} else {
					hasEmpty := false
					nonEmpty := []any{}
					for _, x := range vals {
						if s, ok := x.(string); ok && s == "" {
							hasEmpty = true
						} else {
							nonEmpty = append(nonEmpty, x)
						}
					}
					conds := []string{}
					if len(nonEmpty) > 0 {
						conds = append(conds, "CAST("+col+" AS TEXT) IN ("+strings.TrimSuffix(strings.Repeat("?,", len(nonEmpty)), ",")+")")
						for _, x := range nonEmpty {
							q.args = append(q.args, fmt.Sprint(x))
						}
					}
					if hasEmpty {
						conds = append(conds, "("+col+" IS NULL OR "+col+" = '')")
					}
					q.where = append(q.where, "("+strings.Join(conds, " OR ")+")")
					continue
				}
				for _, x := range vals {
					q.args = append(q.args, fmt.Sprint(x))
				}
			case "range":
				rng, _ := f.V.([]any)
				if len(rng) != 2 {
					continue
				}
				if lo, ok := toFloat(rng[0]); ok && rng[0] != nil {
					q.add("CAST("+col+" AS REAL) >= ?", lo)
				}
				if hi, ok := toFloat(rng[1]); ok && rng[1] != nil {
					q.add("CAST("+col+" AS REAL) <= ?", hi)
				}
			case "bool":
				b, _ := f.V.(bool)
				if b {
					q.add(col + " = 1")
				} else {
					q.add("(" + col + " IS NULL OR " + col + " = 0)")
				}
			case "eq":
				q.add(col+" = ?", f.V)
			case "date":
				rng, _ := f.V.([]any)
				if len(rng) == 2 {
					if s, ok := rng[0].(string); ok && s != "" {
						q.add(col+" >= ?", s)
					}
					if s, ok := rng[1].(string); ok && s != "" {
						q.add(col+" <= ?", s+"T99")
					}
				}
			}
		}
	}
	return q, nil
}

func orderBy(r *http.Request, fields []Field) string {
	var sorts []sortSpec
	if s := r.URL.Query().Get("sort"); s != "" {
		json.Unmarshal([]byte(s), &sorts)
	}
	if len(sorts) == 0 {
		sorts = []sortSpec{{"updated_at", "desc"}}
	}
	ftypes := map[string]Field{}
	for _, f := range fields {
		if _, ok := ftypes[f.Key]; !ok {
			ftypes[f.Key] = f
		}
	}
	parts := []string{}
	for _, s := range sorts {
		dir := "ASC"
		if strings.EqualFold(s.Dir, "desc") {
			dir = "DESC"
		}
		var expr string
		if strings.HasPrefix(s.K, "custom.") {
			key := strings.TrimPrefix(s.K, "custom.")
			f := ftypes[key]
			col := fmt.Sprintf("json_extract(i.custom, '$.%s')", strings.ReplaceAll(jsonPathKey(key), "'", ""))
			switch f.Type {
			case "select", "size":
				// Sort in the order the choices are listed (XS < S < M < L…), unknown values last.
				choices := choiceList(f)
				if len(choices) > 0 {
					var b strings.Builder
					b.WriteString("CASE " + col)
					for i, c := range choices {
						fmt.Fprintf(&b, " WHEN '%s' THEN %d", strings.ReplaceAll(c, "'", "''"), i)
					}
					b.WriteString(" ELSE 100000 END")
					expr = b.String()
				} else {
					expr = col + " COLLATE NOCASE"
				}
			case "number", "money", "rating":
				expr = "CAST(" + col + " AS REAL)"
			case "age":
				src, _ := f.Options["from"].(string)
				expr = fmt.Sprintf("json_extract(i.custom, '$.%s')", strings.ReplaceAll(jsonPathKey(src), "'", ""))
				if dir == "ASC" {
					dir = "DESC"
				} else {
					dir = "ASC"
				}
			default:
				expr = col + " COLLATE NOCASE"
			}
			parts = append(parts, "("+col+" IS NULL) ASC", expr+" "+dir)
			continue
		}
		col, ok := builtinCols[s.K]
		if !ok {
			continue
		}
		switch s.K {
		case "name", "location", "supplier", "category":
			col += " COLLATE NOCASE"
		case "code":
			// Natural-ish: shorter codes first within the same prefix (A2 before A10).
			parts = append(parts, "length(i.code) "+dir)
		case "price", "cost":
			parts = append(parts, "("+col+" IS NULL) ASC")
		}
		parts = append(parts, col+" "+dir)
	}
	parts = append(parts, "i.id DESC")
	return strings.Join(parts, ", ")
}

func choiceList(f Field) []string {
	out := []string{}
	if arr, ok := f.Options["choices"].([]any); ok {
		for _, c := range arr {
			out = append(out, fmt.Sprint(c))
		}
	}
	return out
}

func handleListItems(w http.ResponseWriter, r *http.Request) {
	fields, err := loadAllFields()
	if err != nil {
		sendErr(w, err)
		return
	}
	q, err := buildListQuery(r, fields)
	if err != nil {
		sendErr(w, err)
		return
	}
	where := " WHERE " + strings.Join(q.where, " AND ")
	var total int64
	if err := db.QueryRow(`SELECT COUNT(*)`+itemFrom+where, q.args...).Scan(&total); err != nil {
		sendErr(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 100
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	rows, err := db.Query(`SELECT `+itemCols+itemFrom+where+` ORDER BY `+orderBy(r, fields)+fmt.Sprintf(` LIMIT %d OFFSET %d`, limit, (page-1)*limit), q.args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	defer rows.Close()
	items := []*Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			sendErr(w, err)
			return
		}
		items = append(items, it)
	}
	writeJSON(w, map[string]any{"items": items, "total": total, "page": page, "limit": limit})
}

// GET /api/facets?category=… : value counts for filterable fields in the current category.
func handleFacets(w http.ResponseWriter, r *http.Request) {
	fields, _ := loadAllFields()
	// Facets ignore the field filters themselves so all options stay visible.
	r2 := r.Clone(r.Context())
	qv := r.URL.Query()
	qv.Del("f")
	r2.URL.RawQuery = qv.Encode()
	q, err := buildListQuery(r2, fields)
	if err != nil {
		sendErr(w, err)
		return
	}
	where := " WHERE " + strings.Join(q.where, " AND ")
	rows, err := db.Query(`SELECT i.custom, i.status, i.tags, i.location, i.price, i.quantity`+itemFrom+where, q.args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	defer rows.Close()
	counts := map[string]map[string]int{}
	ranges := map[string][2]float64{}
	bump := func(k, v string) {
		if counts[k] == nil {
			counts[k] = map[string]int{}
		}
		counts[k][v]++
	}
	rng := func(k string, f float64) {
		cur, ok := ranges[k]
		if !ok {
			ranges[k] = [2]float64{f, f}
			return
		}
		if f < cur[0] {
			cur[0] = f
		}
		if f > cur[1] {
			cur[1] = f
		}
		ranges[k] = cur
	}
	ftype := map[string]string{}
	for _, f := range fields {
		ftype[f.Key] = f.Type
	}
	n := 0
	for rows.Next() {
		n++
		var custom, status, tags, location string
		var price sql.NullInt64
		var qty int64
		rows.Scan(&custom, &status, &tags, &location, &price, &qty)
		bump("status", status)
		if location != "" {
			bump("location", location)
		}
		var tl []string
		json.Unmarshal([]byte(tags), &tl)
		for _, t := range tl {
			bump("tags", t)
		}
		if price.Valid {
			rng("price", float64(price.Int64))
		}
		rng("quantity", float64(qty))
		var c map[string]any
		json.Unmarshal([]byte(custom), &c)
		for k, v := range c {
			switch ftype[k] {
			case "select", "size", "color", "text", "boolean":
				bump("custom."+k, fmt.Sprint(v))
			case "multi_select":
				if arr, ok := v.([]any); ok {
					for _, x := range arr {
						bump("custom."+k, fmt.Sprint(x))
					}
				}
			case "number", "money", "rating":
				if f, ok := toFloat(v); ok {
					rng("custom."+k, f)
				}
			}
		}
	}
	type kv struct {
		Value string `json:"value"`
		Count int    `json:"count"`
	}
	out := map[string][]kv{}
	for k, m := range counts {
		list := []kv{}
		for v, c := range m {
			list = append(list, kv{v, c})
		}
		sort.Slice(list, func(a, b int) bool {
			if list[a].Count != list[b].Count {
				return list[a].Count > list[b].Count
			}
			return list[a].Value < list[b].Value
		})
		// Plain text fields only make sense as a facet when values repeat.
		if strings.HasPrefix(k, "custom.") && ftype[strings.TrimPrefix(k, "custom.")] == "text" && len(list) > 40 {
			continue
		}
		out[k] = list
	}
	writeJSON(w, map[string]any{"counts": out, "ranges": ranges, "total": n})
}

// POST /api/items/bulk {ids:[], action, value}
func handleBulk(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Key    string  `json:"key"`
		Value  any     `json:"value"`
		Mode   string  `json:"mode"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(b.IDs) == 0 {
		writeErr(w, 400, "nothing selected")
		return
	}
	if b.Action == "purge" {
		if err := purgeItems(b.IDs); err != nil {
			sendErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"changed": len(b.IDs)})
		return
	}
	all, _ := loadAllFields()
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	changed := 0
	for _, id := range b.IDs {
		switch b.Action {
		case "trash":
			tx.Exec(`UPDATE items SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, now(), id)
			logActivity(tx, id, "deleted", map[string]any{"bulk": true})
			changed++
			continue
		case "restore":
			tx.Exec(`UPDATE items SET deleted_at=NULL WHERE id=?`, id)
			logActivity(tx, id, "restored", map[string]any{"bulk": true})
			changed++
			continue
		}
		it, err := loadItem(tx, id)
		if err != nil {
			continue
		}
		switch b.Action {
		case "set":
			if err := applyBulkSet(it, b.Key, b.Value, b.Mode); err != nil {
				sendErr(w, err)
				return
			}
		case "add_tag":
			it.Tags = append(it.Tags, fmt.Sprint(b.Value))
		case "remove_tag":
			out := []string{}
			for _, t := range it.Tags {
				if !strings.EqualFold(t, fmt.Sprint(b.Value)) {
					out = append(out, t)
				}
			}
			it.Tags = out
		default:
			writeErr(w, 400, "unknown action")
			return
		}
		if _, err := saveItem(tx, it, all, true); err != nil {
			var ae *apiError
			if errors.As(err, &ae) {
				ae.data["error"] = fmt.Sprintf("%s (item %s)", ae.data["error"], it.Code)
			}
			sendErr(w, err)
			return
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"changed": changed})
}

func applyBulkSet(it *Item, key string, value any, mode string) error {
	switch key {
	case "status":
		it.Status = fmt.Sprint(value)
	case "location":
		it.Location = fmt.Sprint(value)
	case "supplier":
		it.Supplier = fmt.Sprint(value)
	case "category_id":
		if n, ok := toInt64(value); ok && n > 0 {
			it.CategoryID = &n
		} else {
			it.CategoryID = nil
		}
	case "barcode_format":
		it.BarcodeFormat = fmt.Sprint(value)
	case "low_stock_at":
		if n, ok := toInt64(value); ok {
			it.LowStockAt = &n
		} else {
			it.LowStockAt = nil
		}
	case "price", "cost":
		target := &it.Price
		if key == "cost" {
			target = &it.Cost
		}
		n, ok := toFloat(value)
		if !ok {
			return badReq("enter a number")
		}
		var nv int64
		switch mode {
		case "percent": // value = percent change
			if *target == nil {
				return nil
			}
			nv = int64(float64(**target)*(1+n/100) + 0.5)
		case "add": // value in cents
			if *target == nil {
				return nil
			}
			nv = **target + int64(n)
		default: // set, value in cents
			nv = int64(n)
		}
		*target = &nv
	case "quantity":
		n, ok := toInt64(value)
		if !ok {
			return badReq("enter a whole number")
		}
		it.Quantity = n
	default:
		if strings.HasPrefix(key, "custom.") {
			k := strings.TrimPrefix(key, "custom.")
			if isEmptyVal(value) {
				delete(it.Custom, k)
			} else {
				it.Custom[k] = value
			}
			return nil
		}
		return badReq("can't bulk-edit " + key)
	}
	return nil
}
