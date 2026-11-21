package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- money parsing ----------

// parseMoney turns "1,234.56", "$12", "12,50" (decimal comma) into cents.
func parseMoney(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' || r == '-' {
			return r
		}
		return -1
	}, s)
	if s == "" {
		return 0, false
	}
	dec := settingString("decimal_sep")
	if dec == "," {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	} else {
		// "12,50" with no dot and exactly 2 digits after the comma: treat the comma as decimal.
		if !strings.Contains(s, ".") && strings.Count(s, ",") == 1 && len(s)-strings.Index(s, ",") == 3 {
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(f * 100)), true
}

func centsString(c *int64) string {
	if c == nil {
		return ""
	}
	v := *c
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// unwrapExcel removes the ="..." wrapper used to keep leading zeros in Excel.
func unwrapExcel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `="`) && strings.HasSuffix(s, `"`) && len(s) >= 3 {
		return s[2 : len(s)-1]
	}
	return s
}

func decodeCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		// Probably Windows-1252 (Excel "CSV" on Windows): map bytes to runes.
		var b strings.Builder
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		data = []byte(b.String())
	}
	// Guess the delimiter from the first line.
	first := string(data)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	delim := ','
	if strings.Count(first, ";") > strings.Count(first, ",") {
		delim = ';'
	} else if strings.Count(first, "\t") > strings.Count(first, ",") {
		delim = '\t'
	}
	rd := csv.NewReader(bytes.NewReader(data))
	rd.Comma = delim
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	return rd.ReadAll()
}

var builtinImport = map[string]string{
	"code": "code", "barcode": "code", "sku": "code", "item code": "code", "upc": "code",
	"barcode_format": "barcode_format", "format": "barcode_format",
	"name": "name", "title": "name", "item": "name", "product": "name", "item name": "name",
	"category": "category", "description": "description", "desc": "description",
	"price": "price", "retail": "price", "sale price": "price", "cost": "cost", "cost price": "cost",
	"quantity": "quantity", "qty": "quantity", "stock": "quantity",
	"low_stock_at": "low_stock_at", "low stock": "low_stock_at", "reorder level": "low_stock_at",
	"status": "status", "location": "location", "supplier": "supplier", "vendor": "supplier",
	"tags": "tags", "extra_codes": "extra_codes", "notes": "notes",
}

// POST /api/import/preview  {csv: "..."}
func handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		CSV string `json:"csv"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rows, err := decodeCSV([]byte(b.CSV))
	if err != nil || len(rows) == 0 {
		writeErr(w, 400, "Could not read this CSV file")
		return
	}
	headers := rows[0]
	fields, _ := loadAllFields()
	suggest := []string{}
	for _, h := range headers {
		hl := strings.ToLower(strings.TrimSpace(h))
		m := ""
		if strings.HasPrefix(hl, "custom.") {
			m = "custom." + strings.TrimPrefix(hl, "custom.")
		} else if v, ok := builtinImport[hl]; ok {
			m = v
		} else {
			for _, f := range fields {
				if strings.EqualFold(f.Label, strings.TrimSpace(h)) || f.Key == makeKey(h) {
					m = "custom." + f.Key
					break
				}
			}
			if m == "" {
				m = "new:" + strings.TrimSpace(h)
			}
		}
		suggest = append(suggest, m)
	}
	sample := rows[1:]
	if len(sample) > 20 {
		sample = sample[:20]
	}
	writeJSON(w, map[string]any{"headers": headers, "sample": sample, "rows": len(rows) - 1, "suggest": suggest})
}

// POST /api/import/run {csv, mapping:[...per column], mode:"create|update|both", category_id, dry_run}
func handleImportRun(w http.ResponseWriter, r *http.Request) {
	var b struct {
		CSV        string   `json:"csv"`
		Mapping    []string `json:"mapping"`
		Mode       string   `json:"mode"`
		CategoryID *int64   `json:"category_id"`
		DryRun     bool     `json:"dry_run"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rows, err := decodeCSV([]byte(b.CSV))
	if err != nil || len(rows) < 2 {
		writeErr(w, 400, "No rows to import")
		return
	}
	if !b.DryRun {
		if _, err := makeBackup("before-import"); err != nil {
			writeErr(w, 500, "Backup before import failed: "+err.Error())
			return
		}
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()

	// Create fields for "new:" columns (text fields in the target category, or global).
	for i, m := range b.Mapping {
		if strings.HasPrefix(m, "new:") {
			label := strings.TrimSpace(strings.TrimPrefix(m, "new:"))
			if label == "" {
				b.Mapping[i] = ""
				continue
			}
			key := makeKey(label)
			var exists int
			if tx.QueryRow(`SELECT 1 FROM fields WHERE key=?`, key).Scan(&exists) != nil {
				var so int64
				tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM fields`).Scan(&so)
				tx.Exec(`INSERT INTO fields(category_id, key, label, type, options, show_in_list, sort_order) VALUES(?,?,?,'text','{}',0,?)`, b.CategoryID, key, label, so)
			}
			b.Mapping[i] = "custom." + key
		}
	}
	all := []Field{}
	{
		frows, err := tx.Query(`SELECT id, category_id, key, label, type, options, required, uniq, as_code, show_in_list, show_on_label, grp, sort_order, default_value, help_text FROM fields ORDER BY sort_order, id`)
		if err == nil {
			for frows.Next() {
				var f Field
				var opts string
				var cat *int64
				var c2 any
				frows.Scan(&f.ID, &c2, &f.Key, &f.Label, &f.Type, &opts, &f.Required, &f.Unique, &f.AsCode, &f.ShowInList, &f.ShowOnLabel, &f.Group, &f.SortOrder, &f.DefaultValue, &f.HelpText)
				if n, ok := c2.(int64); ok {
					cat = &n
				}
				f.CategoryID = cat
				json.Unmarshal([]byte(opts), &f.Options)
				all = append(all, f)
			}
			frows.Close()
		}
	}
	ftype := map[string]string{}
	for _, f := range all {
		ftype[f.Key] = f.Type
	}
	catByName := map[string]int64{}
	{
		crows, _ := tx.Query(`SELECT id, name, COALESCE(parent_id,0) FROM categories`)
		type cr struct {
			id, parent int64
			name       string
		}
		list := []cr{}
		for crows.Next() {
			var c cr
			crows.Scan(&c.id, &c.name, &c.parent)
			list = append(list, c)
		}
		crows.Close()
		names := map[int64]cr{}
		for _, c := range list {
			names[c.id] = c
		}
		for _, c := range list {
			catByName[strings.ToLower(c.name)] = c.id
			path := c.name
			p := c.parent
			for depth := 0; p != 0 && depth < 20; depth++ {
				path = names[p].name + " > " + path
				p = names[p].parent
			}
			catByName[strings.ToLower(path)] = c.id
		}
	}

	type rowErr struct {
		Row   int    `json:"row"`
		Error string `json:"error"`
	}
	errs := []rowErr{}
	created, updated, skipped := 0, 0, 0
	for ri, row := range rows[1:] {
		rowNum := ri + 2
		if len(strings.Join(row, "")) == 0 {
			continue
		}
		get := func(target string) (string, bool) {
			for ci, m := range b.Mapping {
				if m == target && ci < len(row) {
					return unwrapExcel(row[ci]), true
				}
			}
			return "", false
		}
		code, _ := get("code")
		code = strings.TrimSpace(code)
		var existing *Item
		if code != "" {
			var id int64
			if tx.QueryRow(`SELECT id FROM items WHERE code=?`, code).Scan(&id) == nil {
				existing, _ = loadItem(tx, id)
			}
		}
		if existing != nil && b.Mode == "create" {
			skipped++
			continue
		}
		if existing == nil && b.Mode == "update" {
			skipped++
			continue
		}
		it := &Item{Quantity: 1, Custom: map[string]any{}, Tags: []string{}, ExtraCodes: []ExtraCode{}}
		if existing != nil {
			it = existing
		} else {
			it.CategoryID = b.CategoryID
			if dq := settingInt("default_quantity"); dq > 0 {
				it.Quantity = dq
			}
		}
		var perr error
		for ci, m := range b.Mapping {
			if m == "" || ci >= len(row) {
				continue
			}
			val := unwrapExcel(row[ci])
			switch m {
			case "code":
				it.Code = val
			case "barcode_format":
				if val != "" {
					it.BarcodeFormat = strings.ToLower(val)
				}
			case "name":
				it.Name = val
			case "category":
				if val == "" {
					continue
				}
				if id, ok := catByName[strings.ToLower(strings.TrimSpace(val))]; ok {
					it.CategoryID = &id
				} else {
					// Create missing categories on the fly (supports "Parent > Child").
					var parent *int64
					path := ""
					for _, part := range strings.Split(val, ">") {
						part = strings.TrimSpace(part)
						if part == "" {
							continue
						}
						if path != "" {
							path += " > "
						}
						path += part
						if id, ok := catByName[strings.ToLower(path)]; ok {
							idc := id
							parent = &idc
							continue
						}
						res, err := tx.Exec(`INSERT INTO categories(name, parent_id, created_at) VALUES(?,?,?)`, part, parent, now())
						if err != nil {
							perr = err
							break
						}
						nid, _ := res.LastInsertId()
						catByName[strings.ToLower(path)] = nid
						catByName[strings.ToLower(part)] = nid
						parent = &nid
					}
					it.CategoryID = parent
				}
			case "description":
				it.Description = val
			case "price", "cost":
				if val == "" {
					if m == "price" {
						it.Price = nil
					} else {
						it.Cost = nil
					}
					continue
				}
				c, ok := parseMoney(val)
				if !ok {
					perr = fmt.Errorf("%s %q is not an amount", m, val)
					break
				}
				if m == "price" {
					it.Price = &c
				} else {
					it.Cost = &c
				}
			case "quantity", "low_stock_at":
				if val == "" {
					if m == "low_stock_at" {
						it.LowStockAt = nil
					}
					continue
				}
				n, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", ""), 64)
				if err != nil {
					perr = fmt.Errorf("%s %q is not a number", m, val)
					break
				}
				if m == "quantity" {
					it.Quantity = int64(n)
				} else {
					v := int64(n)
					it.LowStockAt = &v
				}
			case "status":
				if val != "" {
					it.Status = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(val), " ", "_"))
				}
			case "location":
				it.Location = val
			case "supplier":
				it.Supplier = val
			case "notes":
				it.Notes = val
			case "tags":
				it.Tags = []string{}
				for _, t := range strings.Split(val, ";") {
					if t = strings.TrimSpace(t); t != "" {
						it.Tags = append(it.Tags, t)
					}
				}
			case "extra_codes":
				it.ExtraCodes = []ExtraCode{}
				for _, p := range strings.Split(val, ";") {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					kind, code := "other", p
					if i := strings.Index(p, ":"); i > 0 {
						kind, code = p[:i], p[i+1:]
					}
					it.ExtraCodes = append(it.ExtraCodes, ExtraCode{Kind: kind, Code: strings.TrimSpace(code)})
				}
			default:
				if strings.HasPrefix(m, "custom.") {
					key := strings.TrimPrefix(m, "custom.")
					if val == "" {
						delete(it.Custom, key)
						continue
					}
					switch ftype[key] {
					case "money":
						if c, ok := parseMoney(val); ok {
							it.Custom[key] = c
						} else {
							perr = fmt.Errorf("%s %q is not an amount", key, val)
						}
					case "number", "rating":
						if f, err := strconv.ParseFloat(strings.ReplaceAll(val, ",", ""), 64); err == nil {
							it.Custom[key] = f
						} else {
							perr = fmt.Errorf("%s %q is not a number", key, val)
						}
					case "boolean":
						lv := strings.ToLower(val)
						it.Custom[key] = lv == "1" || lv == "yes" || lv == "true" || lv == "y" || lv == "x"
					case "multi_select":
						it.Custom[key] = val
					case "date":
						if d, ok := parseDate(val); ok {
							it.Custom[key] = d
						} else {
							perr = fmt.Errorf("%s %q is not a date", key, val)
						}
					default:
						it.Custom[key] = val
					}
				}
			}
			if perr != nil {
				break
			}
		}
		if perr == nil && it.Code == "" {
			var cat int64
			if it.CategoryID != nil {
				cat = *it.CategoryID
			}
			code, seq, format, err := nextCodeTx(tx, cat)
			if err != nil {
				perr = err
			} else {
				it.Code = code
				if it.BarcodeFormat == "" {
					it.BarcodeFormat = format
				}
				bumpSeqTx(tx, it.CategoryID, seq)
			}
		}
		if perr == nil {
			tx.Exec(`SAVEPOINT improw`)
			if _, err := saveItem(tx, it, all, true); err != nil {
				perr = err
				tx.Exec(`ROLLBACK TO improw`)
			}
			tx.Exec(`RELEASE improw`)
		}
		if perr != nil {
			if len(errs) < 500 {
				errs = append(errs, rowErr{rowNum, perr.Error()})
			}
			continue
		}
		if existing != nil {
			updated++
		} else {
			created++
		}
	}
	if !b.DryRun {
		logActivity(tx, 0, "imported", map[string]any{"created": created, "updated": updated, "errors": len(errs)})
		if err := tx.Commit(); err != nil {
			sendErr(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"created": created, "updated": updated, "skipped": skipped, "errors": errs, "dry_run": b.DryRun})
}

func parseDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006/01/02", "01/02/2006", "1/2/2006", "02.01.2006", "2006-01-02T15:04:05", "Jan 2, 2006", "2 Jan 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

// GET /api/export?format=csv|json&ids=…&category=…&excel=1
func handleExport(w http.ResponseWriter, r *http.Request) {
	fields, _ := loadAllFields()
	q, err := buildListQuery(r, fields)
	if err != nil {
		sendErr(w, err)
		return
	}
	where := " WHERE " + strings.Join(q.where, " AND ")
	rows, err := db.Query(`SELECT `+itemCols+itemFrom+where+` ORDER BY `+orderBy(r, fields), q.args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	items := []*Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	stamp := time.Now().Format("2006-01-02_1504")
	if r.URL.Query().Get("format") == "json" {
		for _, it := range items {
			it.Images = loadImages(it.ID)
		}
		cats, _ := loadCategories()
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="shelfcode-export-%s.json"`, stamp))
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", " ")
		enc.Encode(map[string]any{"exported_at": now(), "app": "SHELFCODE " + appVersion, "categories": cats, "fields": fields, "items": items})
		return
	}
	excel := r.URL.Query().Get("excel") != "0"
	catPath := map[int64]string{}
	{
		cats, _ := loadCategories()
		byID := map[int64]Category{}
		for _, c := range cats {
			byID[c.ID] = c
		}
		for _, c := range cats {
			p := c.Name
			cur := c
			for depth := 0; cur.ParentID != nil && depth < 20; depth++ {
				cur = byID[*cur.ParentID]
				p = cur.Name + " > " + p
			}
			catPath[c.ID] = p
		}
	}
	// Custom columns: every key used by an exported item, in field order.
	used := map[string]bool{}
	for _, it := range items {
		for k := range it.Custom {
			used[k] = true
		}
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, f := range fields {
		if used[f.Key] && !seen[f.Key] {
			keys = append(keys, f.Key)
			seen[f.Key] = true
		}
	}
	for k := range used {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(&buf)
	head := []string{"code", "barcode_format", "name", "category", "price", "cost", "quantity", "low_stock_at", "status", "location", "supplier", "tags", "description", "extra_codes", "notes"}
	for _, k := range keys {
		head = append(head, "custom."+k)
	}
	cw.Write(head)
	codeCell := func(s string) string {
		// Keep leading zeros and long numbers intact when the file is opened in Excel.
		if excel && s != "" && strings.Trim(s, "0123456789") == "" && (s[0] == '0' || len(s) > 11) {
			return `="` + s + `"`
		}
		return s
	}
	ftypes := map[string]string{}
	for _, f := range fields {
		if _, ok := ftypes[f.Key]; !ok {
			ftypes[f.Key] = f.Type
		}
	}
	for _, it := range items {
		cat := ""
		if it.CategoryID != nil {
			cat = catPath[*it.CategoryID]
		}
		low := ""
		if it.LowStockAt != nil {
			low = strconv.FormatInt(*it.LowStockAt, 10)
		}
		ex := []string{}
		for _, e := range it.ExtraCodes {
			ex = append(ex, e.Kind+":"+e.Code)
		}
		rec := []string{codeCell(it.Code), it.BarcodeFormat, it.Name, cat, centsString(it.Price), centsString(it.Cost), strconv.FormatInt(it.Quantity, 10), low, it.Status, it.Location, it.Supplier, strings.Join(it.Tags, ";"), it.Description, strings.Join(ex, ";"), it.Notes}
		for _, k := range keys {
			v, ok := it.Custom[k]
			if !ok {
				rec = append(rec, "")
				continue
			}
			if ftypes[k] == "money" {
				if c, ok := toInt64(v); ok {
					rec = append(rec, centsString(&c))
					continue
				}
			}
			switch x := v.(type) {
			case []any:
				parts := []string{}
				for _, p := range x {
					parts = append(parts, fmt.Sprint(p))
				}
				rec = append(rec, strings.Join(parts, ";"))
			case float64:
				rec = append(rec, strconv.FormatFloat(x, 'f', -1, 64))
			case string:
				rec = append(rec, codeCell(x))
			default:
				rec = append(rec, fmt.Sprint(x))
			}
		}
		cw.Write(rec)
	}
	cw.Flush()
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="shelfcode-export-%s.csv"`, stamp))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	io.Copy(w, &buf)
}
