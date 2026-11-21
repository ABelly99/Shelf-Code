package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

type Category struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	ParentID        *int64   `json:"parent_id"`
	Color           string   `json:"color"`
	SortOrder       int64    `json:"sort_order"`
	CodePrefix      string   `json:"code_prefix"`
	NextSeq         int64    `json:"next_seq"`
	DefaultFormat   string   `json:"default_format"`
	UniqueItems     bool     `json:"unique_items"`
	LabelTemplateID *int64   `json:"label_template_id"`
	HiddenBuiltins  []string `json:"hidden_builtins"`
	VariantFields   []string `json:"variant_fields"`
	Count           int64    `json:"count"`
}

func loadCategories() ([]Category, error) {
	rows, err := db.Query(`SELECT c.id, c.name, c.parent_id, c.color, c.sort_order, c.code_prefix, c.next_seq, c.default_format, c.unique_items, c.label_template_id, c.hidden_builtins, c.variant_fields,
		(SELECT COUNT(*) FROM items i WHERE i.category_id=c.id AND i.deleted_at IS NULL)
		FROM categories c ORDER BY c.sort_order, c.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		var parent, label sql.NullInt64
		var hidden, variants string
		rows.Scan(&c.ID, &c.Name, &parent, &c.Color, &c.SortOrder, &c.CodePrefix, &c.NextSeq, &c.DefaultFormat, &c.UniqueItems, &label, &hidden, &variants, &c.Count)
		c.ParentID = nullPtr(parent)
		c.LabelTemplateID = nullPtr(label)
		json.Unmarshal([]byte(hidden), &c.HiddenBuiltins)
		json.Unmarshal([]byte(variants), &c.VariantFields)
		if c.HiddenBuiltins == nil {
			c.HiddenBuiltins = []string{}
		}
		if c.VariantFields == nil {
			c.VariantFields = []string{}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := loadCategories()
	if err != nil {
		sendErr(w, err)
		return
	}
	var uncategorized int64
	db.QueryRow(`SELECT COUNT(*) FROM items WHERE category_id IS NULL AND deleted_at IS NULL`).Scan(&uncategorized)
	writeJSON(w, map[string]any{"categories": cats, "uncategorized": uncategorized})
}

func handleSaveCategory(w http.ResponseWriter, r *http.Request) {
	var c Category
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id, _ := pathID(r)
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		writeErr(w, 400, "Name is required")
		return
	}
	if c.ParentID != nil && id > 0 {
		// No loops: a category can't be inside itself or its own children.
		if contains(categoryDescendants(id, categoryParents()), *c.ParentID) {
			writeErr(w, 400, "A category can't be placed inside itself or one of its sub-categories")
			return
		}
	}
	if c.NextSeq < 1 {
		c.NextSeq = 1
	}
	if c.HiddenBuiltins == nil {
		c.HiddenBuiltins = []string{}
	}
	if c.VariantFields == nil {
		c.VariantFields = []string{}
	}
	hidden, _ := json.Marshal(c.HiddenBuiltins)
	variants, _ := json.Marshal(c.VariantFields)
	if id == 0 {
		var maxOrder int64
		db.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM categories`).Scan(&maxOrder)
		res, err := db.Exec(`INSERT INTO categories(name, parent_id, color, sort_order, code_prefix, next_seq, default_format, unique_items, label_template_id, hidden_builtins, variant_fields, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.Name, c.ParentID, c.Color, maxOrder, strings.TrimSpace(c.CodePrefix), c.NextSeq, c.DefaultFormat, c.UniqueItems, c.LabelTemplateID, string(hidden), string(variants), now())
		if err != nil {
			sendErr(w, err)
			return
		}
		id, _ = res.LastInsertId()
	} else {
		_, err := db.Exec(`UPDATE categories SET name=?, parent_id=?, color=?, code_prefix=?, next_seq=?, default_format=?, unique_items=?, label_template_id=?, hidden_builtins=?, variant_fields=? WHERE id=?`,
			c.Name, c.ParentID, c.Color, strings.TrimSpace(c.CodePrefix), c.NextSeq, c.DefaultFormat, c.UniqueItems, c.LabelTemplateID, string(hidden), string(variants), id)
		if err != nil {
			sendErr(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"id": id})
}

func handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	var parent sql.NullInt64
	tx.QueryRow(`SELECT parent_id FROM categories WHERE id=?`, id).Scan(&parent)
	// Items and sub-categories move up to the parent (or become uncategorized). Field values stay in the items.
	tx.Exec(`UPDATE items SET category_id=? WHERE category_id=?`, nullable(parent), id)
	tx.Exec(`UPDATE categories SET parent_id=? WHERE parent_id=?`, nullable(parent), id)
	if _, err := tx.Exec(`DELETE FROM categories WHERE id=?`, id); err != nil {
		sendErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func nullable(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func handleReorderCategories(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Order []struct {
			ID       int64  `json:"id"`
			ParentID *int64 `json:"parent_id"`
		} `json:"order"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	for i, o := range b.Order {
		tx.Exec(`UPDATE categories SET sort_order=?, parent_id=? WHERE id=?`, i, o.ParentID, o.ID)
	}
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- fields ----------

var validFieldTypes = map[string]bool{
	"text": true, "long_text": true, "number": true, "money": true, "select": true, "multi_select": true,
	"boolean": true, "date": true, "rating": true, "color": true, "url": true, "identifier": true, "age": true, "size": true,
}

var nonKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

func makeKey(label string) string {
	k := strings.Trim(nonKeyChars.ReplaceAllString(strings.ToLower(label), "_"), "_")
	if k == "" {
		k = "field"
	}
	if k[0] >= '0' && k[0] <= '9' {
		k = "f_" + k
	}
	return k
}

func handleListFields(w http.ResponseWriter, r *http.Request) {
	f, err := loadAllFields()
	if err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"fields": f})
}

func handleSaveField(w http.ResponseWriter, r *http.Request) {
	var f Field
	if err := readJSON(r, &f); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id, _ := pathID(r)
	f.Label = strings.TrimSpace(f.Label)
	if f.Label == "" {
		writeErr(w, 400, "Label is required")
		return
	}
	if !validFieldTypes[f.Type] {
		writeErr(w, 400, "Unknown field type")
		return
	}
	if strings.TrimSpace(f.Key) == "" {
		f.Key = makeKey(f.Label)
	} else {
		f.Key = makeKey(f.Key)
	}
	if f.Options == nil {
		f.Options = map[string]any{}
	}
	f.Options = normalizeJSONNumbers(f.Options).(map[string]any)
	if f.AsCode && f.Type != "identifier" && f.Type != "text" {
		f.AsCode = false
	}
	// Key must be unique within the category chain (global, ancestors, descendants).
	all, _ := loadAllFields()
	parents := categoryParents()
	related := []int64{}
	if f.CategoryID != nil {
		related = append(categoryAncestors(*f.CategoryID, parents), categoryDescendants(*f.CategoryID, parents)...)
	}
	for _, o := range all {
		if o.ID == id || o.Key != f.Key {
			continue
		}
		if o.CategoryID == nil || f.CategoryID == nil || contains(related, *o.CategoryID) {
			writeErr(w, 400, fmt.Sprintf("Another field already uses the key %q here. Pick a different key.", f.Key))
			return
		}
	}
	opts, _ := json.Marshal(f.Options)
	var oldKey string
	var oldAsCode bool
	if id > 0 {
		db.QueryRow(`SELECT key, as_code FROM fields WHERE id=?`, id).Scan(&oldKey, &oldAsCode)
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	if id == 0 {
		var maxOrder int64
		tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM fields`).Scan(&maxOrder)
		res, err := tx.Exec(`INSERT INTO fields(category_id, key, label, type, options, required, uniq, as_code, show_in_list, show_on_label, grp, sort_order, default_value, help_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			f.CategoryID, f.Key, f.Label, f.Type, string(opts), f.Required, f.Unique, f.AsCode, f.ShowInList, f.ShowOnLabel, f.Group, maxOrder, f.DefaultValue, f.HelpText)
		if err != nil {
			sendErr(w, err)
			return
		}
		id, _ = res.LastInsertId()
	} else {
		_, err := tx.Exec(`UPDATE fields SET category_id=?, key=?, label=?, type=?, options=?, required=?, uniq=?, as_code=?, show_in_list=?, show_on_label=?, grp=?, default_value=?, help_text=? WHERE id=?`,
			f.CategoryID, f.Key, f.Label, f.Type, string(opts), f.Required, f.Unique, f.AsCode, f.ShowInList, f.ShowOnLabel, f.Group, f.DefaultValue, f.HelpText, id)
		if err != nil {
			sendErr(w, err)
			return
		}
		// Renamed key: move stored values so nothing is lost.
		if oldKey != "" && oldKey != f.Key {
			tx.Exec(`UPDATE items SET custom=json_remove(json_set(custom, '$.'||?, json_extract(custom, '$.'||?)), '$.'||?) WHERE json_extract(custom, '$.'||?) IS NOT NULL`,
				jsonPathKey(f.Key), jsonPathKey(oldKey), jsonPathKey(oldKey), jsonPathKey(oldKey))
		}
	}
	// Turning "acts as code" on or off (or renaming) changes which values are scannable: rebuild those entries.
	if f.AsCode || oldAsCode {
		if err := reindexFieldCodes(tx, oldKey, f); err != nil {
			sendErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": id, "key": f.Key})
}

func reindexFieldCodes(tx *sql.Tx, oldKey string, f Field) error {
	if oldKey != "" {
		tx.Exec(`DELETE FROM code_index WHERE source=?`, "field:"+oldKey)
	}
	tx.Exec(`DELETE FROM code_index WHERE source=?`, "field:"+f.Key)
	if !f.AsCode {
		return nil
	}
	rows, err := tx.Query(`SELECT id, json_extract(custom, '$.'||?) FROM items WHERE json_extract(custom, '$.'||?) IS NOT NULL`, jsonPathKey(f.Key), jsonPathKey(f.Key))
	if err != nil {
		return err
	}
	type pair struct {
		id   int64
		code string
	}
	ps := []pair{}
	for rows.Next() {
		var p pair
		var v any
		rows.Scan(&p.id, &v)
		p.code = strings.TrimSpace(fmt.Sprint(v))
		if p.code != "" {
			ps = append(ps, p)
		}
	}
	rows.Close()
	conflicts := []string{}
	for _, p := range ps {
		if _, err := tx.Exec(`INSERT INTO code_index(code, code_lower, item_id, source) VALUES(?,?,?,?)`, p.code, strings.ToLower(p.code), p.id, "field:"+f.Key); err != nil {
			conflicts = append(conflicts, p.code)
		}
	}
	if len(conflicts) > 0 {
		if len(conflicts) > 5 {
			conflicts = append(conflicts[:5], "…")
		}
		return badReq(fmt.Sprintf("Can't make %s scannable: these values are also used as other codes: %s", f.Label, strings.Join(conflicts, ", ")))
	}
	return nil
}

func handleDeleteField(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var key string
	db.QueryRow(`SELECT key FROM fields WHERE id=?`, id).Scan(&key)
	tx, _ := db.Begin()
	defer tx.Rollback()
	tx.Exec(`DELETE FROM fields WHERE id=?`, id)
	tx.Exec(`DELETE FROM code_index WHERE source=?`, "field:"+key)
	if r.URL.Query().Get("purge") == "1" && key != "" {
		tx.Exec(`UPDATE items SET custom=json_remove(custom, '$.'||?)`, jsonPathKey(key))
	}
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

func handleReorderFields(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	for i, id := range b.IDs {
		tx.Exec(`UPDATE fields SET sort_order=? WHERE id=?`, i, id)
	}
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- templates ----------

type TemplateField struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Type        string         `json:"type"`
	Options     map[string]any `json:"options,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Unique      bool           `json:"unique,omitempty"`
	AsCode      bool           `json:"as_code,omitempty"`
	ShowInList  bool           `json:"show_in_list,omitempty"`
	ShowOnLabel bool           `json:"show_on_label,omitempty"`
	Group       string         `json:"group,omitempty"`
	Help        string         `json:"help,omitempty"`
}

type Template struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Color          string          `json:"color"`
	CodePrefix     string          `json:"code_prefix"`
	DefaultFormat  string          `json:"default_format"`
	UniqueItems    bool            `json:"unique_items"`
	HiddenBuiltins []string        `json:"hidden_builtins"`
	VariantFields  []string        `json:"variant_fields"`
	LabelTemplate  string          `json:"label_template"`
	Fields         []TemplateField `json:"fields"`
	Children       []Template      `json:"children"`
}

func loadTemplates() []Template {
	entries, _ := templatesFS.ReadDir("templates")
	out := []Template{}
	for _, e := range entries {
		b, err := templatesFS.ReadFile("templates/" + e.Name())
		if err != nil {
			continue
		}
		var t Template
		if json.Unmarshal(b, &t) == nil {
			t.ID = strings.TrimSuffix(e.Name(), ".json")
			out = append(out, t)
		}
	}
	order := map[string]int{"clothing": 0, "phones": 1, "electronics": 2, "books": 3, "shoes": 4, "jewelry": 5, "tools": 6, "general": 7}
	sort.Slice(out, func(a, b int) bool { return order[out[a].ID] < order[out[b].ID] })
	return out
}

func handleListTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"templates": loadTemplates()})
}

// POST /api/templates/apply {ids:["clothing"], template:{...custom json}}
func handleApplyTemplate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IDs      []string  `json:"ids"`
		Template *Template `json:"template"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	todo := []Template{}
	byID := map[string]Template{}
	for _, t := range loadTemplates() {
		byID[t.ID] = t
	}
	for _, id := range b.IDs {
		if t, ok := byID[id]; ok {
			todo = append(todo, t)
		}
	}
	if b.Template != nil {
		todo = append(todo, *b.Template)
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	created := []int64{}
	for _, t := range todo {
		id, err := createFromTemplate(tx, t, nil)
		if err != nil {
			sendErr(w, err)
			return
		}
		created = append(created, id)
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"created": created})
}

func createFromTemplate(tx *sql.Tx, t Template, parent *int64) (int64, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return 0, badReq("template has no name")
	}
	// Avoid duplicate names: "Phones", "Phones 2"…
	base := name
	for i := 2; ; i++ {
		var x int
		if tx.QueryRow(`SELECT 1 FROM categories WHERE name=? COLLATE NOCASE`, name).Scan(&x) != nil {
			break
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
	prefix := t.CodePrefix
	if prefix != "" {
		// Keep prefixes distinct so generated codes never clash in meaning.
		p := prefix
		for i := 2; ; i++ {
			var x int
			if tx.QueryRow(`SELECT 1 FROM categories WHERE code_prefix=?`, p).Scan(&x) != nil {
				break
			}
			p = strings.TrimSuffix(prefix, "-") + fmt.Sprint(i) + "-"
		}
		prefix = p
	}
	var labelID any
	if t.LabelTemplate != "" {
		var id int64
		if tx.QueryRow(`SELECT id FROM docs WHERE kind='label' AND name=?`, t.LabelTemplate).Scan(&id) == nil {
			labelID = id
		}
	}
	if t.HiddenBuiltins == nil {
		t.HiddenBuiltins = []string{}
	}
	if t.VariantFields == nil {
		t.VariantFields = []string{}
	}
	hidden, _ := json.Marshal(t.HiddenBuiltins)
	variants, _ := json.Marshal(t.VariantFields)
	var maxOrder int64
	tx.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM categories`).Scan(&maxOrder)
	res, err := tx.Exec(`INSERT INTO categories(name, parent_id, color, sort_order, code_prefix, next_seq, default_format, unique_items, label_template_id, hidden_builtins, variant_fields, created_at) VALUES(?,?,?,?,?,1,?,?,?,?,?,?)`,
		name, parent, t.Color, maxOrder, prefix, t.DefaultFormat, t.UniqueItems, labelID, string(hidden), string(variants), now())
	if err != nil {
		return 0, err
	}
	catID, _ := res.LastInsertId()
	for i, f := range t.Fields {
		if !validFieldTypes[f.Type] {
			continue
		}
		key := f.Key
		if key == "" {
			key = makeKey(f.Label)
		}
		if f.Options == nil {
			f.Options = map[string]any{}
		}
		opts, _ := json.Marshal(f.Options)
		if _, err := tx.Exec(`INSERT INTO fields(category_id, key, label, type, options, required, uniq, as_code, show_in_list, show_on_label, grp, sort_order, default_value, help_text) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'',?)`,
			catID, key, f.Label, f.Type, string(opts), f.Required, f.Unique, f.AsCode, f.ShowInList, f.ShowOnLabel, f.Group, i, f.Help); err != nil {
			return 0, err
		}
	}
	for _, ch := range t.Children {
		if _, err := createFromTemplate(tx, ch, &catID); err != nil {
			return 0, err
		}
	}
	return catID, nil
}

func handleExportCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	cats, _ := loadCategories()
	all, _ := loadAllFields()
	labels := map[int64]string{}
	rows, _ := db.Query(`SELECT id, name FROM docs WHERE kind='label'`)
	if rows != nil {
		for rows.Next() {
			var i int64
			var n string
			rows.Scan(&i, &n)
			labels[i] = n
		}
		rows.Close()
	}
	var build func(cid int64) *Template
	build = func(cid int64) *Template {
		for _, c := range cats {
			if c.ID != cid {
				continue
			}
			t := &Template{Name: c.Name, Color: c.Color, CodePrefix: c.CodePrefix, DefaultFormat: c.DefaultFormat, UniqueItems: c.UniqueItems, HiddenBuiltins: c.HiddenBuiltins, VariantFields: c.VariantFields, Fields: []TemplateField{}, Children: []Template{}}
			if c.LabelTemplateID != nil {
				t.LabelTemplate = labels[*c.LabelTemplateID]
			}
			for _, f := range all {
				if f.CategoryID != nil && *f.CategoryID == cid {
					t.Fields = append(t.Fields, TemplateField{Key: f.Key, Label: f.Label, Type: f.Type, Options: f.Options, Required: f.Required, Unique: f.Unique, AsCode: f.AsCode, ShowInList: f.ShowInList, ShowOnLabel: f.ShowOnLabel, Group: f.Group, Help: f.HelpText})
				}
			}
			for _, ch := range cats {
				if ch.ParentID != nil && *ch.ParentID == cid {
					if sub := build(ch.ID); sub != nil {
						t.Children = append(t.Children, *sub)
					}
				}
			}
			return t
		}
		return nil
	}
	t := build(id)
	if t == nil {
		writeErr(w, 404, "not found")
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.shelfcode-template.json"`, makeKey(t.Name)))
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(t)
}

// ---------- generic docs (label templates, print jobs, saved views) ----------

var docKinds = map[string]bool{"label": true, "printjob": true, "view": true}

func handleListDocs(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !docKinds[kind] {
		writeErr(w, 404, "unknown kind")
		return
	}
	rows, err := db.Query(`SELECT id, name, data, sort_order, updated_at FROM docs WHERE kind=? ORDER BY sort_order, name COLLATE NOCASE`, kind)
	if err != nil {
		sendErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, so int64
		var name, data, upd string
		rows.Scan(&id, &name, &data, &so, &upd)
		var d any
		json.Unmarshal([]byte(data), &d)
		out = append(out, map[string]any{"id": id, "name": name, "data": d, "sort_order": so, "updated_at": upd})
	}
	writeJSON(w, map[string]any{"docs": out})
}

func handleSaveDoc(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !docKinds[kind] {
		writeErr(w, 404, "unknown kind")
		return
	}
	var b struct {
		Name string `json:"name"`
		Data any    `json:"data"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		writeErr(w, 400, "Name is required")
		return
	}
	data, _ := json.Marshal(normalizeJSONNumbers(b.Data))
	id, _ := pathID(r)
	if id == 0 {
		var so int64
		db.QueryRow(`SELECT COALESCE(MAX(sort_order),0)+1 FROM docs WHERE kind=?`, kind).Scan(&so)
		res, err := db.Exec(`INSERT INTO docs(kind, name, data, sort_order, updated_at) VALUES(?,?,?,?,?)`, kind, b.Name, string(data), so, now())
		if err != nil {
			sendErr(w, err)
			return
		}
		id, _ = res.LastInsertId()
	} else {
		if _, err := db.Exec(`UPDATE docs SET name=?, data=?, updated_at=? WHERE id=? AND kind=?`, b.Name, string(data), now(), id, kind); err != nil {
			sendErr(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"id": id})
}

func handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	db.Exec(`DELETE FROM docs WHERE id=? AND kind=?`, id, r.PathValue("kind"))
	if r.PathValue("kind") == "label" {
		db.Exec(`UPDATE categories SET label_template_id=NULL WHERE label_template_id=?`, id)
	}
	writeJSON(w, map[string]any{"ok": true})
}
