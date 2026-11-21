package main

// MCP (Model Context Protocol) server: lets AI assistants (Claude Code, Cursor, VS Code,
// Claude Desktop, Gemini CLI, Codex…) run SHELFCODE with plain-English requests.
//
//   HTTP:  POST /mcp  (Streamable HTTP, JSON responses), "Authorization: Bearer <agent token>"
//   stdio: "shelfcode mcp" — for agents on the same computer. It talks to the running
//          SHELFCODE if there is one (so pages on screen can follow along), otherwise it
//          opens the database itself.
//
// Tools call the app's own HTTP handlers in-process, so the rules (validation, unique
// codes, history, backups) are exactly the same as in the web interface.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// innerMux: the API routes without the browser middleware (set in routes()).
var innerMux http.Handler

var mcpProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const mcpInstructions = `SHELFCODE is a barcode inventory database (items with codes, categories with custom fields, stock, labels).
Start with get_store_overview: it lists categories with their fields, statuses, label designs, and the owner's store notes (preferences you must follow).
Prices are plain numbers in the store currency (19.99). Codes are generated automatically unless you give one.
Changes are recorded in each item's history and can be undone with item_history + revert_change. Deleting moves items to the Trash.
To print labels, use prepare_labels: it opens the ready print sheet in the owner's SHELFCODE window; the owner presses Print.
When the owner states a lasting preference ("always…", "from now on…"), save it with remember_preference.`

// ---------- agent token ----------

func agentToken() string {
	if t := settingString("agent_token"); t != "" {
		return t
	}
	b := make([]byte, 24)
	rand.Read(b)
	t := "sc_" + hex.EncodeToString(b)
	setSetting(db, "agent_token", t)
	return t
}

// ---------- JSON-RPC ----------

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id json.RawMessage, code int, msg string) map[string]any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcErr{code, msg}}
}

type mcpCtx struct {
	base   string // e.g. http://192.168.1.20:8765 (links in answers); "" if unknown
	inProc bool   // stdio without a running server: no open pages to drive
}

// mcpDispatch handles one message; nil for notifications.
func mcpDispatch(m rpcMsg, ctx mcpCtx) map[string]any {
	isNote := len(m.ID) == 0 || string(m.ID) == "null"
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(m.Params, &p)
		ver := mcpProtocols[0]
		for _, v := range mcpProtocols {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		return rpcResult(m.ID, map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "shelfcode", "title": "SHELFCODE", "version": appVersion},
			"instructions":    mcpInstructions,
		})
	case "ping":
		return rpcResult(m.ID, map[string]any{})
	case "tools/list":
		return rpcResult(m.ID, map[string]any{"tools": mcpToolList()})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return rpcError(m.ID, -32602, "bad params")
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		t, ok := mcpTools[p.Name]
		if !ok {
			return rpcError(m.ID, -32602, "unknown tool: "+p.Name)
		}
		out, err := safeRun(t.run, p.Arguments, ctx)
		if err != nil {
			return rpcResult(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true})
		}
		text, _ := json.MarshalIndent(out, "", " ")
		return rpcResult(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}})
	case "resources/list":
		return rpcResult(m.ID, map[string]any{"resources": []any{}})
	case "prompts/list":
		return rpcResult(m.ID, map[string]any{"prompts": []any{}})
	}
	if isNote {
		return nil // notifications/initialized, notifications/cancelled…
	}
	return rpcError(m.ID, -32601, "method not found: "+m.Method)
}

func safeRun(f func(map[string]any, mcpCtx) (any, error), a map[string]any, ctx mcpCtx) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error: %v", r)
		}
	}()
	return f(a, ctx)
}

// handleMCP: POST /mcp (Streamable HTTP transport, JSON responses only).
func handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "SHELFCODE MCP: no server-to-client stream; use POST", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer"))
	if tok == "" || tok != agentToken() {
		w.Header().Set("WWW-Authenticate", `Bearer realm="shelfcode"`)
		writeErr(w, 401, "SHELFCODE: missing or wrong agent token (Settings → AI agents)")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, 400, "bad body")
		return
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	ctx := mcpCtx{base: scheme + "://" + host}
	resp, ok := mcpHandleRaw(body, ctx)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

// mcpHandleRaw handles a single message or a batch; ok=false when there is nothing to answer.
func mcpHandleRaw(body []byte, ctx mcpCtx) ([]byte, bool) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var msgs []rpcMsg
		if json.Unmarshal(body, &msgs) != nil {
			b, _ := json.Marshal(rpcError(nil, -32700, "parse error"))
			return b, true
		}
		out := []any{}
		for _, m := range msgs {
			if r := mcpDispatch(m, ctx); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		b, _ := json.Marshal(out)
		return b, true
	}
	var m rpcMsg
	if json.Unmarshal(body, &m) != nil {
		b, _ := json.Marshal(rpcError(nil, -32700, "parse error"))
		return b, true
	}
	r := mcpDispatch(m, ctx)
	if r == nil {
		return nil, false
	}
	b, _ := json.Marshal(r)
	return b, true
}

// ---------- calling the app's own API ----------

func callAPI(method, path string, body any) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	innerMux.ServeHTTP(rec, req)
	out := map[string]any{}
	dec := json.NewDecoder(rec.Body)
	dec.UseNumber()
	dec.Decode(&out)
	if rec.Code >= 300 {
		msg, _ := out["error"].(string)
		if msg == "" {
			msg = fmt.Sprintf("request failed (%d)", rec.Code)
		}
		return out, errors.New(msg)
	}
	return out, nil
}

// ---------- argument helpers ----------

func argStr(a map[string]any, k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	}
	return ""
}

func argNum(a map[string]any, k string) (float64, bool) {
	switch v := a[k].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimLeft(v, "$€£")), 64)
		return f, err == nil
	}
	return 0, false
}

func toCents(f float64) int64 { return int64(math.Round(f * 100)) }

func fromCents(v any) any {
	if c, ok := toInt64(v); ok {
		return float64(c) / 100
	}
	return nil
}

func toInt(v any) int64 {
	n, _ := toInt64(v)
	return n
}

// resolveItem finds an item id from "id" or "code" (strict code match, like scanning).
func resolveItem(a map[string]any) (int64, error) {
	if n, ok := argNum(a, "id"); ok && n > 0 {
		return int64(n), nil
	}
	code := argStr(a, "code")
	if code == "" {
		return 0, errors.New("give the item's id or code")
	}
	r, _ := callAPI("GET", "/api/lookup?code="+url.QueryEscape(code), nil)
	if id, ok := toInt64(r["item_id"]); ok {
		return id, nil
	}
	return 0, fmt.Errorf("no item has the code %q", code)
}

type catInfo struct {
	ID   int64
	Name string
	Path string
}

func categoriesList() []catInfo {
	cats, _ := loadCategories()
	byID := map[int64]Category{}
	for _, c := range cats {
		byID[c.ID] = c
	}
	out := []catInfo{}
	for _, c := range cats {
		p := c.Name
		cur := c
		for i := 0; cur.ParentID != nil && i < 20; i++ {
			cur = byID[*cur.ParentID]
			p = cur.Name + " > " + p
		}
		out = append(out, catInfo{c.ID, c.Name, p})
	}
	return out
}

func resolveCategory(name string) (*int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	if n, err := strconv.ParseInt(name, 10, 64); err == nil {
		return &n, nil
	}
	list := categoriesList()
	for _, c := range list {
		if strings.EqualFold(c.Path, name) || strings.EqualFold(c.Name, name) {
			id := c.ID
			return &id, nil
		}
	}
	names := []string{}
	for _, c := range list {
		names = append(names, c.Path)
	}
	return nil, fmt.Errorf("no category %q. Existing: %s. (Use create_category to add one.)", name, strings.Join(names, ", "))
}

// mapFields turns {"Storage": "128 GB", "ram": 8} into custom values keyed by field key,
// matching field keys or labels case-insensitively. Money fields take plain amounts.
func mapFields(catID *int64, in map[string]any) (map[string]any, error) {
	all, _ := loadAllFields()
	fields := fieldsFor(catID, all)
	out := map[string]any{}
	for k, v := range in {
		var f *Field
		for i := range fields {
			if strings.EqualFold(fields[i].Key, k) || strings.EqualFold(fields[i].Label, k) || makeKey(fields[i].Label) == makeKey(k) {
				f = &fields[i]
				break
			}
		}
		if f == nil {
			labels := []string{}
			for _, x := range fields {
				labels = append(labels, x.Label)
			}
			return nil, fmt.Errorf("this category has no field %q. Its fields: %s (add one with add_field)", k, strings.Join(labels, ", "))
		}
		if f.Type == "money" {
			if n, ok := argNum(map[string]any{"v": v}, "v"); ok {
				v = toCents(n)
			}
		}
		out[f.Key] = v
	}
	return out, nil
}

// applyItemArgs copies agent-friendly arguments onto an item map (as used by the API).
func applyItemArgs(it map[string]any, a map[string]any, isNew bool) error {
	var catID *int64
	if c, ok := toInt64(it["category_id"]); ok {
		catID = &c
	}
	if s, ok := a["category"]; ok && s != nil {
		id, err := resolveCategory(fmt.Sprint(s))
		if err != nil {
			return err
		}
		catID = id
		it["category_id"] = id
	}
	for _, k := range []string{"name", "description", "location", "supplier", "notes", "status", "code", "barcode_format"} {
		if v, ok := a[k]; ok && v != nil {
			it[k] = fmt.Sprint(v)
		}
	}
	for _, k := range []string{"price", "cost"} {
		if v, ok := a[k]; ok {
			if v == nil || v == "" {
				it[k] = nil
			} else if n, ok := argNum(a, k); ok {
				it[k] = toCents(n)
			} else {
				return fmt.Errorf("%s must be a number like 19.99", k)
			}
		}
	}
	for _, k := range []string{"quantity", "low_stock_at"} {
		if _, ok := a[k]; ok {
			if n, ok := argNum(a, k); ok {
				it[k] = int64(n)
			} else if k == "low_stock_at" {
				it[k] = nil
			}
		}
	}
	if v, ok := a["tags"].([]any); ok {
		it["tags"] = v
	}
	if v, ok := a["fields"].(map[string]any); ok && len(v) > 0 {
		mapped, err := mapFields(catID, v)
		if err != nil {
			return err
		}
		cur, _ := it["custom"].(map[string]any)
		if cur == nil {
			cur = map[string]any{}
		}
		for k, x := range mapped {
			if x == nil || x == "" {
				delete(cur, k)
			} else {
				cur[k] = x
			}
		}
		it["custom"] = cur
	}
	if isNew {
		if _, ok := it["quantity"]; !ok {
			it["quantity"] = settingInt("default_quantity")
		}
	}
	return nil
}

// briefItem: what an agent needs to see about an item.
func briefItem(it map[string]any) map[string]any {
	out := map[string]any{
		"id": it["id"], "code": it["code"], "name": it["name"], "category": it["category_name"],
		"price": fromCents(it["price"]), "quantity": it["quantity"], "status": it["status"],
	}
	if v, _ := it["location"].(string); v != "" {
		out["location"] = v
	}
	if c, ok := it["custom"].(map[string]any); ok && len(c) > 0 {
		out["fields"] = c
	}
	if t, ok := it["tags"].([]any); ok && len(t) > 0 {
		out["tags"] = t
	}
	return out
}

func appLink(ctx mcpCtx, hash string) string {
	if ctx.base == "" {
		return hash
	}
	return ctx.base + "/" + hash
}

// showOnScreen moves the SHELFCODE page the owner used last to hash.
func showOnScreen(ctx mcpCtx, hash string) bool {
	if ctx.inProc {
		return false
	}
	return hub.sendBest("navigate", map[string]any{"hash": hash})
}

// ---------- tools ----------

type mcpTool struct {
	desc   string
	schema map[string]any
	read   bool
	danger bool
	run    func(map[string]any, mcpCtx) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func numb(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

var itemRef = map[string]any{
	"id":   map[string]any{"type": "integer", "description": "Item id"},
	"code": str("Item code (exact; any of its codes, e.g. IMEI)"),
}

func itemProps(extra map[string]any) map[string]any {
	p := map[string]any{
		"name":         str("Item name"),
		"category":     str("Category name (e.g. Phones, or Clothing > Shirts)"),
		"code":         str("Barcode value. Leave out to generate one."),
		"price":        numb("Selling price, e.g. 19.99"),
		"cost":         numb("What the store paid"),
		"quantity":     map[string]any{"type": "integer"},
		"low_stock_at": map[string]any{"type": "integer", "description": "Warn when quantity is at or below this"},
		"status":       str("in_stock, reserved, sold, archived (or a custom status)"),
		"location":     str("Shelf/bin/room"),
		"supplier":     str(""),
		"description":  str(""),
		"notes":        str("Private notes"),
		"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"fields":       map[string]any{"type": "object", "description": "Category fields by key or label, e.g. {\"brand\": \"Apple\", \"Storage\": \"256 GB\"}. See get_store_overview.", "additionalProperties": true},
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

var mcpTools map[string]mcpTool

func init() {
	mcpTools = map[string]mcpTool{
		"get_store_overview": {
			desc: "Call this first. Store name, currency, the owner's store notes (preferences to follow), categories with all their fields (key, label, type, choices), statuses, label designs and totals.",
			read: true, schema: obj(map[string]any{}), run: toolOverview,
		},
		"search_items": {
			desc:   "Find items. query matches name, code, brand, any field (contains). Optional filters by field value, category, status, low stock. Returns up to 'limit' items (default 25) and the total.",
			read:   true,
			schema: obj(map[string]any{"query": str("Words to look for"), "category": str("Category name"), "status": str(""), "low_stock": map[string]any{"type": "boolean"}, "filters": map[string]any{"type": "object", "description": "Exact field values, e.g. {\"brand\": \"Apple\", \"storage\": \"128 GB\"}", "additionalProperties": true}, "sort": str("name, price, quantity, created_at, updated_at, or a field key; add ' desc' for descending"), "limit": map[string]any{"type": "integer"}}),
			run:    toolSearch,
		},
		"get_item": {
			desc: "Everything about one item (all fields, codes, stock, variants), by id or exact code.", read: true,
			schema: obj(itemRef), run: toolGetItem,
		},
		"create_items": {
			desc:   "Add one or many items in one go. Codes are generated unless given. Returns each new item's id and code.",
			schema: obj(map[string]any{"items": map[string]any{"type": "array", "items": obj(itemProps(nil), "name")}}, "items"),
			run:    toolCreate,
		},
		"update_item": {
			desc:   "Change an item. Give id or code to find it, and only the properties to change (fields are merged; set a field to null to clear it). Changing 'code' replaces its barcode value.",
			schema: obj(map[string]any{"id": itemRef["id"], "code": itemRef["code"], "changes": obj(itemProps(nil))}, "changes"),
			run:    toolUpdate,
		},
		"delete_items": {
			desc: "Move items to the Trash (restorable for 30 days with restore_items).", danger: true,
			schema: obj(map[string]any{"items": map[string]any{"type": "array", "description": "ids (numbers) or codes (strings)", "items": map[string]any{}}}, "items"),
			run:    toolDelete,
		},
		"restore_items": {
			desc:   "Bring items back from the Trash.",
			schema: obj(map[string]any{"ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "ids"),
			run:    toolRestore,
		},
		"adjust_stock": {
			desc:   "Record a stock change: sold (negative), received (positive), returned, damaged, lost, or set the counted quantity. Unique items (phones) become 'sold' when sold.",
			schema: obj(map[string]any{"id": itemRef["id"], "code": itemRef["code"], "change": map[string]any{"type": "integer", "description": "e.g. -1 sold, +12 received"}, "set_to": map[string]any{"type": "integer", "description": "Counted quantity (instead of change)"}, "reason": map[string]any{"type": "string", "enum": []string{"sold", "received", "returned", "damaged", "lost", "adjustment", "count"}}, "price_each": numb("Sale price or cost per unit"), "note": str("")}, "reason"),
			run:    toolStock,
		},
		"make_variants": {
			desc:   "Create one item per combination, e.g. sizes × colours, each with its own code, linked to the original. Example options: {\"size\": [\"S\",\"M\",\"L\"], \"color\": [\"Black\",\"White\"]}.",
			schema: obj(map[string]any{"id": itemRef["id"], "code": itemRef["code"], "options": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "quantity": map[string]any{"type": "integer", "description": "Quantity of each variant"}}, "options"),
			run:    toolVariants,
		},
		"create_category": {
			desc:   "Add a category, from a template (clothing, phones, electronics, books, shoes, jewelry, tools, general) or with your own fields. Field types: text, long_text, number, money, select, size, multi_select, boolean, date, color, url, identifier. scannable=true makes a field (IMEI, serial) open the item when scanned.",
			schema: obj(map[string]any{"name": str("Category name"), "template": str("Optional template id"), "parent": str("Optional parent category"), "code_prefix": str("Optional, e.g. PH-"), "one_of_a_kind": map[string]any{"type": "boolean", "description": "Every item is unique (quantity 1), like phones"}, "fields": map[string]any{"type": "array", "items": obj(map[string]any{"label": str(""), "type": str(""), "choices": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "unit": str(""), "required": map[string]any{"type": "boolean"}, "scannable": map[string]any{"type": "boolean"}, "group": str("Form section, e.g. Specs")}, "label", "type")}}),
			run:    toolCreateCategory,
		},
		"add_field": {
			desc:   "Add a custom field to a category (or to every item if category is left out).",
			schema: obj(map[string]any{"category": str(""), "label": str(""), "type": str("text, long_text, number, money, select, size, multi_select, boolean, date, color, url, identifier"), "choices": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "unit": str(""), "required": map[string]any{"type": "boolean"}, "scannable": map[string]any{"type": "boolean"}, "group": str("")}, "label", "type"),
			run:    toolAddField,
		},
		"prepare_labels": {
			desc:   "Make a print sheet of barcode labels: any mix of items and copies, plus optional blank codes (new unique codes to stick on things and assign later). It opens on the owner's SHELFCODE screen, ready to print.",
			schema: obj(map[string]any{"items": map[string]any{"type": "array", "items": obj(map[string]any{"id": itemRef["id"], "code": itemRef["code"], "copies": map[string]any{"type": "integer"}})}, "blank_codes": map[string]any{"type": "integer", "description": "How many blank codes to add"}, "design": str("Label design name, e.g. 'Small 38×25', 'Hang tag 70×40' (default: each category's own)"), "sheet": str("Optional sticker sheet: avery5160, avery5167, avery5163, avery5164, l7160, l7651"), "label_printer": map[string]any{"type": "boolean", "description": "One label per page (Dymo/Zebra/Brother)"}}),
			run:    toolLabels,
		},
		"item_history": {
			desc: "An item's change history and stock movements (newest first). Each change has an id usable with revert_change.", read: true,
			schema: obj(itemRef), run: toolHistory,
		},
		"revert_change": {
			desc:   "Undo one change from item_history: puts the changed values back as they were before it.",
			schema: obj(map[string]any{"change_id": map[string]any{"type": "integer"}}, "change_id"),
			run:    toolRevert,
		},
		"inventory_report": {
			desc: "Totals, stock value, low stock, sales over the last N days (default 30), top sellers, stock by category, oldest stock.", read: true,
			schema: obj(map[string]any{"days": map[string]any{"type": "integer"}}), run: toolReport,
		},
		"remember_preference": {
			desc:   "Save a lasting store preference to the owner's store notes, so every future session follows it (e.g. 'Price used phones ending in .99', 'Tag new arrivals with new').",
			schema: obj(map[string]any{"note": str("The preference, in one sentence")}, "note"),
			run:    toolRemember,
		},
		"open_in_app": {
			desc:   "Show something on the owner's SHELFCODE screen: an item (id/code), a search, or a page (home, items, print, stock, reports, settings).",
			read:   true,
			schema: obj(map[string]any{"id": itemRef["id"], "code": itemRef["code"], "search": str(""), "page": str("")}),
			run:    toolOpen,
		},
	}
}

func mcpToolList() []any {
	names := make([]string, 0, len(mcpTools))
	for n := range mcpTools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []any{}
	for _, n := range names {
		t := mcpTools[n]
		out = append(out, map[string]any{
			"name": n, "description": t.desc, "inputSchema": t.schema,
			"annotations": map[string]any{"readOnlyHint": t.read, "destructiveHint": t.danger, "openWorldHint": false},
		})
	}
	return out
}

func toolOverview(a map[string]any, ctx mcpCtx) (any, error) {
	all, _ := loadAllFields()
	cats := []any{}
	for _, c := range categoriesList() {
		id := c.ID
		fs := []any{}
		for _, f := range fieldsFor(&id, all) {
			fd := map[string]any{"key": f.Key, "label": f.Label, "type": f.Type}
			if ch := choiceList(f); len(ch) > 0 {
				fd["choices"] = ch
			}
			if u, _ := f.Options["unit"].(string); u != "" {
				fd["unit"] = u
			}
			if f.Required {
				fd["required"] = true
			}
			if f.AsCode {
				fd["scannable"] = true
			}
			fs = append(fs, fd)
		}
		var n int64
		db.QueryRow(`SELECT COUNT(*) FROM items WHERE category_id=? AND deleted_at IS NULL`, c.ID).Scan(&n)
		cats = append(cats, map[string]any{"name": c.Path, "id": c.ID, "items": n, "fields": fs})
	}
	labels := []string{}
	if r, err := callAPI("GET", "/api/docs/label", nil); err == nil {
		if docs, ok := r["docs"].([]any); ok {
			for _, d := range docs {
				if m, ok := d.(map[string]any); ok {
					labels = append(labels, fmt.Sprint(m["name"]))
				}
			}
		}
	}
	statuses := []string{}
	if list, ok := getSetting("statuses").([]any); ok {
		for _, s := range list {
			if m, ok := s.(map[string]any); ok {
				statuses = append(statuses, fmt.Sprint(m["key"]))
			}
		}
	}
	var items, units int64
	db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN quantity>0 THEN quantity END),0) FROM items WHERE deleted_at IS NULL`).Scan(&items, &units)
	notes := settingString("agent_notes")
	if notes == "" {
		notes = "(none yet)"
	}
	return map[string]any{
		"store": settingString("store_name"), "currency": settingString("currency_symbol"),
		"store_notes": notes, "items": items, "units_in_stock": units,
		"categories": cats, "statuses": statuses, "label_designs": labels,
		"app": appLink(ctx, "#/home"),
	}, nil
}

func toolSearch(a map[string]any, ctx mcpCtx) (any, error) {
	q := url.Values{}
	q.Set("q", argStr(a, "query"))
	limit := 25
	if n, ok := argNum(a, "limit"); ok && n > 0 {
		limit = int(math.Min(n, 200))
	}
	q.Set("limit", strconv.Itoa(limit))
	var catID *int64
	if c := argStr(a, "category"); c != "" {
		id, err := resolveCategory(c)
		if err != nil {
			return nil, err
		}
		catID = id
		q.Set("category", strconv.FormatInt(*id, 10))
	}
	filters := []any{}
	if s := argStr(a, "status"); s != "" {
		filters = append(filters, map[string]any{"k": "status", "op": "in", "v": []any{s}})
	}
	if f, ok := a["filters"].(map[string]any); ok && len(f) > 0 {
		mapped, err := mapFields(catID, f)
		if err != nil {
			return nil, err
		}
		for k, v := range mapped {
			filters = append(filters, map[string]any{"k": "custom." + k, "op": "in", "v": []any{fmt.Sprint(v)}})
		}
	}
	if len(filters) > 0 {
		b, _ := json.Marshal(filters)
		q.Set("f", string(b))
	}
	if b, _ := a["low_stock"].(bool); b {
		q.Set("low", "1")
	}
	if s := argStr(a, "sort"); s != "" {
		k, dir, _ := strings.Cut(s, " ")
		if _, builtin := builtinCols[k]; !builtin {
			k = "custom." + k
		}
		if dir == "" {
			dir = "asc"
		}
		b, _ := json.Marshal([]any{map[string]any{"k": k, "dir": dir}})
		q.Set("sort", string(b))
	}
	r, err := callAPI("GET", "/api/items?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	list := []any{}
	if items, ok := r["items"].([]any); ok {
		for _, x := range items {
			if m, ok := x.(map[string]any); ok {
				list = append(list, briefItem(m))
			}
		}
	}
	return map[string]any{"total": r["total"], "shown": len(list), "items": list}, nil
}

func toolGetItem(a map[string]any, ctx mcpCtx) (any, error) {
	id, err := resolveItem(a)
	if err != nil {
		return nil, err
	}
	r, err := callAPI("GET", fmt.Sprintf("/api/items/%d", id), nil)
	if err != nil {
		return nil, err
	}
	it, _ := r["item"].(map[string]any)
	out := briefItem(it)
	for _, k := range []string{"description", "supplier", "notes", "barcode_format", "extra_codes", "low_stock_at", "created_at", "updated_at"} {
		if v := it[k]; v != nil && v != "" {
			out[k] = v
		}
	}
	out["cost"] = fromCents(it["cost"])
	if v, ok := r["variants"].([]any); ok && len(v) > 0 {
		out["variants"] = v
	}
	if p := r["parent"]; p != nil {
		out["variant_of"] = p
	}
	if it["deleted_at"] != nil {
		out["in_trash"] = true
	}
	out["link"] = appLink(ctx, fmt.Sprintf("#/item/%d", id))
	return out, nil
}

func toolCreate(a map[string]any, ctx mcpCtx) (any, error) {
	list, _ := a["items"].([]any)
	if len(list) == 0 {
		return nil, errors.New("items is empty")
	}
	created := []any{}
	failed := []any{}
	for i, x := range list {
		ia, _ := x.(map[string]any)
		it := map[string]any{"custom": map[string]any{}, "tags": []any{}, "extra_codes": []any{}}
		if err := applyItemArgs(it, ia, true); err != nil {
			failed = append(failed, map[string]any{"index": i, "name": ia["name"], "error": err.Error()})
			continue
		}
		if s, _ := it["code"].(string); s == "" {
			var cat int64
			if c, ok := toInt64(it["category_id"]); ok {
				cat = c
			}
			code, seq, format, err := nextCode(cat)
			if err != nil {
				return nil, err
			}
			it["code"], it["gen_seq"] = code, seq
			if it["barcode_format"] == nil {
				it["barcode_format"] = format
			}
		}
		r, err := callAPI("POST", "/api/items", it)
		if err != nil {
			failed = append(failed, map[string]any{"index": i, "name": ia["name"], "error": err.Error()})
			continue
		}
		m, _ := r["item"].(map[string]any)
		created = append(created, map[string]any{"id": m["id"], "code": m["code"], "name": m["name"], "link": appLink(ctx, fmt.Sprintf("#/item/%v", m["id"]))})
	}
	if len(created) == 1 {
		if m, ok := created[0].(map[string]any); ok {
			showOnScreen(ctx, fmt.Sprintf("#/item/%v", m["id"]))
		}
	} else if len(created) > 1 {
		showOnScreen(ctx, "#/items?sort="+url.QueryEscape(`[{"k":"created_at","dir":"desc"}]`))
	}
	out := map[string]any{"created": created}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	return out, nil
}

func toolUpdate(a map[string]any, ctx mcpCtx) (any, error) {
	id, err := resolveItem(a)
	if err != nil {
		return nil, err
	}
	changes, _ := a["changes"].(map[string]any)
	if len(changes) == 0 {
		return nil, errors.New("changes is empty")
	}
	r, err := callAPI("GET", fmt.Sprintf("/api/items/%d", id), nil)
	if err != nil {
		return nil, err
	}
	it, _ := r["item"].(map[string]any)
	if err := applyItemArgs(it, changes, false); err != nil {
		return nil, err
	}
	res, err := callAPI("PUT", fmt.Sprintf("/api/items/%d", id), it)
	if err != nil {
		return nil, err
	}
	m, _ := res["item"].(map[string]any)
	return map[string]any{"updated": briefItem(m), "link": appLink(ctx, fmt.Sprintf("#/item/%d", id))}, nil
}

func idsFromRefs(list []any) ([]int64, error) {
	ids := []int64{}
	for _, x := range list {
		var a map[string]any
		switch v := x.(type) {
		case map[string]any:
			a = v
		case string:
			a = map[string]any{"code": v}
		default:
			a = map[string]any{"id": v}
		}
		id, err := resolveItem(a)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func toolDelete(a map[string]any, ctx mcpCtx) (any, error) {
	list, _ := a["items"].([]any)
	ids, err := idsFromRefs(list)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("no items given")
	}
	if _, err := callAPI("POST", "/api/items/bulk", map[string]any{"ids": ids, "action": "trash"}); err != nil {
		return nil, err
	}
	return map[string]any{"moved_to_trash": ids, "undo": "restore_items with these ids"}, nil
}

func toolRestore(a map[string]any, ctx mcpCtx) (any, error) {
	list, _ := a["ids"].([]any)
	ids, err := idsFromRefs(list)
	if err != nil {
		return nil, err
	}
	if _, err := callAPI("POST", "/api/items/bulk", map[string]any{"ids": ids, "action": "restore"}); err != nil {
		return nil, err
	}
	return map[string]any{"restored": ids}, nil
}

func toolStock(a map[string]any, ctx mcpCtx) (any, error) {
	id, err := resolveItem(a)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"item_id": id, "reason": argStr(a, "reason"), "note": argStr(a, "note")}
	if n, ok := argNum(a, "set_to"); ok {
		body["set_to"] = int64(n)
		if body["reason"] == "" {
			body["reason"] = "count"
		}
	} else if n, ok := argNum(a, "change"); ok {
		body["change"] = int64(n)
	} else {
		return nil, errors.New("give change (e.g. -1) or set_to")
	}
	if n, ok := argNum(a, "price_each"); ok {
		body["price_each"] = toCents(n)
	}
	return callAPI("POST", "/api/stock", body)
}

func toolVariants(a map[string]any, ctx mcpCtx) (any, error) {
	id, err := resolveItem(a)
	if err != nil {
		return nil, err
	}
	r, err := callAPI("GET", fmt.Sprintf("/api/items/%d", id), nil)
	if err != nil {
		return nil, err
	}
	it, _ := r["item"].(map[string]any)
	var catID *int64
	if c, ok := toInt64(it["category_id"]); ok {
		catID = &c
	}
	opts, _ := a["options"].(map[string]any)
	axes := []any{}
	for k, v := range opts {
		m, err := mapFields(catID, map[string]any{k: ""})
		if err != nil {
			return nil, err
		}
		for key := range m {
			axes = append(axes, map[string]any{"key": key, "values": v})
		}
	}
	body := map[string]any{"axes": axes, "copy_images": true}
	if n, ok := argNum(a, "quantity"); ok {
		body["quantity"] = int64(n)
	}
	res, err := callAPI("POST", fmt.Sprintf("/api/items/%d/variants", id), body)
	if err != nil {
		return nil, err
	}
	showOnScreen(ctx, fmt.Sprintf("#/items?parent=%d", id))
	ids, _ := res["created"].([]any)
	codes := []any{}
	for _, x := range ids {
		if v, err := callAPI("GET", fmt.Sprintf("/api/items/%v", x), nil); err == nil {
			if m, ok := v["item"].(map[string]any); ok {
				codes = append(codes, map[string]any{"id": m["id"], "code": m["code"], "name": m["name"]})
			}
		}
	}
	return map[string]any{"created": codes}, nil
}

func fieldBody(catID *int64, a map[string]any) map[string]any {
	opts := map[string]any{}
	if ch, ok := a["choices"].([]any); ok && len(ch) > 0 {
		opts["choices"] = ch
		opts["allow_new"] = true
	}
	if u := argStr(a, "unit"); u != "" {
		opts["unit"] = u
	}
	t := argStr(a, "type")
	if t == "dropdown" {
		t = "select"
	}
	scannable, _ := a["scannable"].(bool)
	if scannable && t != "identifier" && t != "text" {
		t = "identifier"
	}
	req, _ := a["required"].(bool)
	return map[string]any{"category_id": catID, "label": argStr(a, "label"), "type": t, "options": opts, "required": req,
		"as_code": scannable, "unique": scannable, "show_in_list": true, "group": argStr(a, "group")}
}

func toolCreateCategory(a map[string]any, ctx mcpCtx) (any, error) {
	if t := strings.ToLower(argStr(a, "template")); t != "" {
		r, err := callAPI("POST", "/api/templates/apply", map[string]any{"ids": []string{t}})
		if err != nil {
			return nil, err
		}
		ids, _ := r["created"].([]any)
		if len(ids) == 0 {
			return nil, fmt.Errorf("no template %q (use clothing, phones, electronics, books, shoes, jewelry, tools, general)", t)
		}
		id := toInt(ids[0])
		if name := argStr(a, "name"); name != "" {
			if cats, err := loadCategories(); err == nil {
				for _, c := range cats {
					if c.ID == id {
						c.Name = name
						callAPI("PUT", fmt.Sprintf("/api/categories/%d", id), c)
					}
				}
			}
		}
		return map[string]any{"category_id": id, "from_template": t}, nil
	}
	name := argStr(a, "name")
	if name == "" {
		return nil, errors.New("give a name or a template")
	}
	body := map[string]any{"name": name, "code_prefix": argStr(a, "code_prefix"), "hidden_builtins": []any{}, "variant_fields": []any{}}
	if p := argStr(a, "parent"); p != "" {
		pid, err := resolveCategory(p)
		if err != nil {
			return nil, err
		}
		body["parent_id"] = pid
	}
	if b, _ := a["one_of_a_kind"].(bool); b {
		body["unique_items"] = true
		body["hidden_builtins"] = []any{"quantity", "low_stock_at"}
	}
	r, err := callAPI("POST", "/api/categories", body)
	if err != nil {
		return nil, err
	}
	id := toInt(r["id"])
	added := []any{}
	if fs, ok := a["fields"].([]any); ok {
		for _, f := range fs {
			fa, _ := f.(map[string]any)
			if _, err := callAPI("POST", "/api/fields", fieldBody(&id, fa)); err != nil {
				return nil, fmt.Errorf("category created, but field %q failed: %v", fa["label"], err)
			}
			added = append(added, fa["label"])
		}
	}
	showOnScreen(ctx, fmt.Sprintf("#/categories/%d", id))
	return map[string]any{"category_id": id, "name": name, "fields": added}, nil
}

func toolAddField(a map[string]any, ctx mcpCtx) (any, error) {
	catID, err := resolveCategory(argStr(a, "category"))
	if err != nil {
		return nil, err
	}
	return callAPI("POST", "/api/fields", fieldBody(catID, a))
}

func toolLabels(a map[string]any, ctx mcpCtx) (any, error) {
	labelID := func(name string) any {
		if name == "" {
			return nil
		}
		r, _ := callAPI("GET", "/api/docs/label", nil)
		docs, _ := r["docs"].([]any)
		for _, d := range docs {
			m, _ := d.(map[string]any)
			if strings.Contains(strings.ToLower(fmt.Sprint(m["name"])), strings.ToLower(name)) {
				return m["id"]
			}
		}
		return nil
	}
	design := labelID(argStr(a, "design"))
	lines := []any{}
	n := time.Now().UnixMilli()
	count := 0
	list, _ := a["items"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		id, err := resolveItem(m)
		if err != nil {
			return nil, err
		}
		copies := int64(1)
		if c, ok := argNum(m, "copies"); ok && c > 0 {
			copies = int64(c)
		}
		lid := design
		if lid == nil {
			if r, err := callAPI("GET", fmt.Sprintf("/api/items/%d", id), nil); err == nil {
				it, _ := r["item"].(map[string]any)
				if c, ok := toInt64(it["category_id"]); ok {
					var l sql.NullInt64
					db.QueryRow(`SELECT label_template_id FROM categories WHERE id=?`, c).Scan(&l)
					if l.Valid {
						lid = l.Int64
					}
				}
			}
		}
		n++
		lines = append(lines, map[string]any{"id": n, "item_id": id, "label_id": lid, "qty": copies, "format": ""})
		count += int(copies)
	}
	var blanks []any
	if b, ok := argNum(a, "blank_codes"); ok && b > 0 {
		r, err := callAPI("POST", "/api/codes/reserve", map[string]any{"count": int(b)})
		if err != nil {
			return nil, err
		}
		blanks, _ = r["codes"].([]any)
		lid := design
		if lid == nil {
			lid = labelID("Small")
		}
		n++
		lines = append(lines, map[string]any{"id": n, "batch": blanks, "format": r["format"], "label_id": lid, "qty": 1})
		count += len(blanks)
	}
	if len(lines) == 0 {
		return nil, errors.New("give items and/or blank_codes")
	}
	page := map[string]any{"mode": "free"}
	if s := argStr(a, "sheet"); s != "" {
		page = map[string]any{"mode": "sheet", "sheet": strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "avery", "avery")), "start": 0}
	}
	if b, _ := a["label_printer"].(bool); b {
		page = map[string]any{"mode": "roll", "roll": "label"}
	}
	name := fmt.Sprintf("From AI agent %s", time.Now().Format("2006-01-02 15:04"))
	r, err := callAPI("POST", "/api/docs/printjob", map[string]any{"name": name, "data": map[string]any{"lines": lines, "page": page}})
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("#/print?job=%v", r["id"])
	shown := showOnScreen(ctx, hash)
	out := map[string]any{"labels": count, "print_sheet": appLink(ctx, hash)}
	if len(blanks) > 0 {
		out["blank_codes"] = blanks
	}
	if shown {
		out["on_screen"] = "The print sheet is open on the owner's SHELFCODE screen. They press Print."
	} else {
		out["on_screen"] = "No SHELFCODE page is open. Ask the owner to open the print_sheet link and press Print."
	}
	return out, nil
}

func toolHistory(a map[string]any, ctx mcpCtx) (any, error) {
	id, err := resolveItem(a)
	if err != nil {
		return nil, err
	}
	act, err := callAPI("GET", fmt.Sprintf("/api/activity?item_id=%d&limit=50", id), nil)
	if err != nil {
		return nil, err
	}
	st, _ := callAPI("GET", fmt.Sprintf("/api/stock?item_id=%d&limit=50", id), nil)
	changes := []any{}
	if list, ok := act["activity"].([]any); ok {
		for _, x := range list {
			m, _ := x.(map[string]any)
			changes = append(changes, map[string]any{"change_id": m["id"], "when": m["created_at"], "action": m["action"], "details": m["details"]})
		}
	}
	return map[string]any{"changes": changes, "stock_movements": st["moves"], "note": "Money values in details are in cents."}, nil
}

func toolRevert(a map[string]any, ctx mcpCtx) (any, error) {
	cid, ok := argNum(a, "change_id")
	if !ok {
		return nil, errors.New("give change_id from item_history")
	}
	var itemID sql.NullInt64
	var action, details string
	if err := db.QueryRow(`SELECT item_id, action, details FROM activity WHERE id=?`, int64(cid)).Scan(&itemID, &action, &details); err != nil {
		return nil, errors.New("no such change")
	}
	if !itemID.Valid {
		return nil, errors.New("that change is not about one item")
	}
	switch action {
	case "deleted":
		callAPI("POST", fmt.Sprintf("/api/items/%d/restore", itemID.Int64), map[string]any{})
		return map[string]any{"restored_from_trash": itemID.Int64}, nil
	case "updated":
	default:
		return nil, fmt.Errorf("only 'updated' and 'deleted' changes can be undone (this one is %q)", action)
	}
	var diff map[string][]any
	json.Unmarshal([]byte(details), &diff)
	r, err := callAPI("GET", fmt.Sprintf("/api/items/%d", itemID.Int64), nil)
	if err != nil {
		return nil, err
	}
	it, _ := r["item"].(map[string]any)
	custom, _ := it["custom"].(map[string]any)
	if custom == nil {
		custom = map[string]any{}
	}
	undone := []string{}
	for k, pair := range diff {
		if len(pair) != 2 {
			continue
		}
		old := pair[0]
		if strings.HasPrefix(k, "custom.") {
			if old == nil {
				delete(custom, strings.TrimPrefix(k, "custom."))
			} else {
				custom[strings.TrimPrefix(k, "custom.")] = old
			}
		} else {
			it[k] = old
		}
		undone = append(undone, k)
	}
	it["custom"] = custom
	if _, err := callAPI("PUT", fmt.Sprintf("/api/items/%d", itemID.Int64), it); err != nil {
		return nil, err
	}
	sort.Strings(undone)
	return map[string]any{"item_id": itemID.Int64, "restored_fields": undone}, nil
}

func toolReport(a map[string]any, ctx mcpCtx) (any, error) {
	days := 30
	if n, ok := argNum(a, "days"); ok && n > 0 {
		days = int(n)
	}
	d, err := callAPI("GET", "/api/dashboard", nil)
	if err != nil {
		return nil, err
	}
	r, err := callAPI("GET", fmt.Sprintf("/api/reports?days=%d", days), nil)
	if err != nil {
		return nil, err
	}
	delete(d, "scanner")
	delete(d, "recent")
	return map[string]any{"currency": settingString("currency_symbol"), "money_in_cents": true, "summary": d, "report": r}, nil
}

func toolRemember(a map[string]any, ctx mcpCtx) (any, error) {
	note := argStr(a, "note")
	if note == "" {
		return nil, errors.New("note is empty")
	}
	cur := strings.TrimSpace(settingString("agent_notes"))
	if cur != "" {
		cur += "\n"
	}
	cur += "- " + note
	if err := setSetting(db, "agent_notes", cur); err != nil {
		return nil, err
	}
	return map[string]any{"store_notes": cur}, nil
}

func toolOpen(a map[string]any, ctx mcpCtx) (any, error) {
	hash := "#/home"
	switch {
	case argStr(a, "code") != "" || argStr(a, "id") != "":
		id, err := resolveItem(a)
		if err != nil {
			return nil, err
		}
		hash = fmt.Sprintf("#/item/%d", id)
	case argStr(a, "search") != "":
		hash = "#/items?q=" + url.QueryEscape(argStr(a, "search"))
	case argStr(a, "page") != "":
		hash = "#/" + strings.Trim(strings.ToLower(argStr(a, "page")), "/# ")
	}
	if showOnScreen(ctx, hash) {
		return map[string]any{"shown": true, "link": appLink(ctx, hash)}, nil
	}
	return map[string]any{"shown": false, "link": appLink(ctx, hash), "note": "No SHELFCODE page is open; give the owner the link."}, nil
}

// ---------- "shelfcode mcp" (stdio) ----------

func mcpCommand(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	remote := fs.String("url", "", "SHELFCODE address on another computer, e.g. http://192.168.1.20:8765")
	token := fs.String("token", "", "agent token for --url (Settings → AI agents)")
	data := fs.String("data", envOr("SHELFCODE_DATA", ""), "data folder (default: next to the program)")
	fs.Parse(args)
	serverMode, plainLog = true, true
	noColors()
	logOut = os.Stderr

	var forward func([]byte) ([]byte, bool, error)
	if *remote != "" {
		forward = httpForwarder(strings.TrimRight(*remote, "/")+"/mcp", *token)
	} else {
		dataDir = resolveDataDir(*data)
		if url, ok := runningInstance(); ok {
			if t := readTokenRO(); t != "" {
				forward = httpForwarder(strings.Replace(url, "localhost", "127.0.0.1", 1)+"/mcp", t)
			}
		}
		if forward == nil {
			// SHELFCODE isn't running: work on the database directly.
			if err := ensureDirs(); err != nil {
				fmt.Fprintln(os.Stderr, "SHELFCODE:", err)
				return 1
			}
			if err := openDB(); err != nil {
				fmt.Fprintln(os.Stderr, "SHELFCODE: can't open the database:", err)
				return 1
			}
			routes()
			ctx := mcpCtx{inProc: true}
			forward = func(b []byte) ([]byte, bool, error) {
				out, ok := mcpHandleRaw(b, ctx)
				return out, ok, nil
			}
		}
	}
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	var outMu sync.Mutex
	for {
		line, err := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			resp, ok, ferr := forward(line)
			if ferr != nil {
				var m rpcMsg
				json.Unmarshal(line, &m)
				if len(m.ID) > 0 {
					resp, _ = json.Marshal(rpcError(m.ID, -32603, "SHELFCODE is not reachable: "+ferr.Error()))
					ok = true
				}
			}
			if ok {
				var compact bytes.Buffer
				if json.Compact(&compact, resp) == nil {
					resp = compact.Bytes()
				}
				outMu.Lock()
				os.Stdout.Write(append(resp, '\n'))
				outMu.Unlock()
			}
		}
		if err != nil {
			return 0
		}
	}
}

func httpForwarder(endpoint, token string) func([]byte) ([]byte, bool, error) {
	c := &http.Client{Timeout: 2 * time.Minute}
	return func(b []byte) ([]byte, bool, error) {
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := c.Do(req)
		if err != nil {
			return nil, false, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusAccepted {
			return nil, false, nil
		}
		if resp.StatusCode != 200 {
			return nil, false, fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return body, true, nil
	}
}

func readTokenRO() string {
	d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath())+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return ""
	}
	defer d.Close()
	var v string
	if d.QueryRow(`SELECT value FROM settings WHERE key='agent_token'`).Scan(&v) != nil {
		return ""
	}
	var t string
	json.Unmarshal([]byte(v), &t)
	return t
}

// resolveDataDir: same rules as the main program.
func resolveDataDir(flagVal string) string {
	d := flagVal
	if d == "" {
		exe, err := os.Executable()
		if err == nil {
			exe, _ = filepath.EvalSymlinks(exe)
		}
		d = filepath.Join(filepath.Dir(exe), "SHELFCODE-data")
	}
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	return d
}
