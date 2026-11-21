package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type stockReq struct {
	ItemID    int64  `json:"item_id"`
	Change    int64  `json:"change"`
	Reason    string `json:"reason"`
	PriceEach *int64 `json:"price_each"`
	Note      string `json:"note"`
	SetTo     *int64 `json:"set_to"` // for stock counts: set quantity to this value
}

var stockReasons = map[string]bool{"received": true, "sold": true, "returned": true, "damaged": true, "adjustment": true, "count": true, "lost": true}

func applyStock(tx *sql.Tx, s stockReq) (map[string]any, error) {
	if !stockReasons[s.Reason] {
		return nil, badReq("unknown reason")
	}
	var qty int64
	var status string
	var catID sql.NullInt64
	var name, code string
	if err := tx.QueryRow(`SELECT quantity, status, category_id, name, code FROM items WHERE id=? AND deleted_at IS NULL`, s.ItemID).Scan(&qty, &status, &catID, &name, &code); err != nil {
		return nil, &apiError{404, map[string]any{"error": "Item not found"}}
	}
	change := s.Change
	if s.SetTo != nil {
		change = *s.SetTo - qty
	}
	if change == 0 && s.SetTo == nil {
		return nil, badReq("change can't be 0")
	}
	unique := false
	if catID.Valid {
		tx.QueryRow(`SELECT unique_items FROM categories WHERE id=?`, catID.Int64).Scan(&unique)
	}
	newQty := qty + change
	newStatus := status
	switch {
	case s.Reason == "sold" && (unique || newQty <= 0) && newQty <= 0:
		newStatus = "sold"
	case change > 0 && status == "sold":
		newStatus = "in_stock"
	}
	if unique && newQty > 1 {
		newQty = 1
		change = newQty - qty
	}
	ts := now()
	if _, err := tx.Exec(`UPDATE items SET quantity=?, status=?, updated_at=? WHERE id=?`, newQty, newStatus, ts, s.ItemID); err != nil {
		return nil, err
	}
	if change != 0 {
		if _, err := tx.Exec(`INSERT INTO stock_movements(item_id, change, reason, price_each, note, created_at) VALUES(?,?,?,?,?,?)`, s.ItemID, change, s.Reason, s.PriceEach, s.Note, ts); err != nil {
			return nil, err
		}
	}
	return map[string]any{"item_id": s.ItemID, "quantity": newQty, "status": newStatus, "change": change, "name": name, "code": code}, nil
}

func handleStock(w http.ResponseWriter, r *http.Request) {
	var s stockReq
	if err := readJSON(r, &s); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	res, err := applyStock(tx, s)
	if err != nil {
		sendErr(w, err)
		return
	}
	tx.Commit()
	writeJSON(w, res)
}

func handleStockBatch(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Moves []stockReq `json:"moves"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, err := db.Begin()
	if err != nil {
		sendErr(w, err)
		return
	}
	defer tx.Rollback()
	out := []map[string]any{}
	for _, m := range b.Moves {
		if m.SetTo == nil && m.Change == 0 {
			continue
		}
		res, err := applyStock(tx, m)
		if err != nil {
			sendErr(w, err)
			return
		}
		out = append(out, res)
	}
	if err := tx.Commit(); err != nil {
		sendErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"results": out})
}

func handleListStock(w http.ResponseWriter, r *http.Request) {
	q := `SELECT m.id, m.item_id, m.change, m.reason, m.price_each, m.note, m.created_at, i.name, i.code FROM stock_movements m JOIN items i ON i.id=m.item_id`
	args := []any{}
	where := ""
	if id, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64); err == nil {
		where = " WHERE m.item_id=?"
		args = append(args, id)
	} else if reason := r.URL.Query().Get("reason"); reason != "" {
		where = " WHERE m.reason=?"
		args = append(args, reason)
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := db.Query(q+where+fmt.Sprintf(" ORDER BY m.id DESC LIMIT %d", limit), args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, item, change int64
		var reason, note, created, name, code string
		var price sql.NullInt64
		rows.Scan(&id, &item, &change, &reason, &price, &note, &created, &name, &code)
		out = append(out, map[string]any{"id": id, "item_id": item, "change": change, "reason": reason, "price_each": nullPtr(price), "note": note, "created_at": created, "name": name, "code": code})
	}
	writeJSON(w, map[string]any{"moves": out})
}

func handleListActivity(w http.ResponseWriter, r *http.Request) {
	q := `SELECT a.id, a.item_id, a.action, a.details, a.created_at, COALESCE(i.name,''), COALESCE(i.code,'') FROM activity a LEFT JOIN items i ON i.id=a.item_id`
	args := []any{}
	where := ""
	if id, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64); err == nil {
		where = " WHERE a.item_id=?"
		args = append(args, id)
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := db.Query(q+where+fmt.Sprintf(" ORDER BY a.id DESC LIMIT %d", limit), args...)
	if err != nil {
		sendErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var item sql.NullInt64
		var action, details, created, name, code string
		rows.Scan(&id, &item, &action, &details, &created, &name, &code)
		var d any
		json.Unmarshal([]byte(details), &d)
		out = append(out, map[string]any{"id": id, "item_id": nullPtr(item), "action": action, "details": d, "created_at": created, "name": name, "code": code})
	}
	writeJSON(w, map[string]any{"activity": out})
}

// POST /api/printed {items: {id: copies}}
func handlePrinted(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Items map[string]int64 `json:"items"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	for k, n := range b.Items {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			logActivity(tx, id, "printed", map[string]any{"labels": n})
		}
	}
	tx.Commit()
	writeJSON(w, map[string]any{"ok": true})
}

func queryRows(q string, args ...any) []map[string]any {
	rows, err := db.Query(q, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out
}

func scalar(q string, args ...any) any {
	var v any
	db.QueryRow(q, args...).Scan(&v)
	return v
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")
	weekAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	writeJSON(w, map[string]any{
		"items":       scalar(`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`),
		"units":       scalar(`SELECT COALESCE(SUM(quantity),0) FROM items WHERE deleted_at IS NULL AND quantity>0`),
		"retail":      scalar(`SELECT COALESCE(SUM(price*quantity),0) FROM items WHERE deleted_at IS NULL AND quantity>0 AND price IS NOT NULL`),
		"cost":        scalar(`SELECT COALESCE(SUM(cost*quantity),0) FROM items WHERE deleted_at IS NULL AND quantity>0 AND cost IS NOT NULL`),
		"low":         scalar(`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL AND low_stock_at IS NOT NULL AND quantity<=low_stock_at`),
		"trash":       scalar(`SELECT COUNT(*) FROM items WHERE deleted_at IS NOT NULL`),
		"sold_today":  scalar(`SELECT COALESCE(-SUM(change),0) FROM stock_movements WHERE reason='sold' AND created_at>=?`, today),
		"sold_week":   scalar(`SELECT COALESCE(-SUM(change),0) FROM stock_movements WHERE reason='sold' AND created_at>=?`, weekAgo),
		"sales_week":  scalar(`SELECT COALESCE(SUM(-change*price_each),0) FROM stock_movements WHERE reason='sold' AND created_at>=? AND price_each IS NOT NULL`, weekAgo),
		"low_items":   queryRows(`SELECT id, code, name, quantity, low_stock_at FROM items WHERE deleted_at IS NULL AND low_stock_at IS NOT NULL AND quantity<=low_stock_at ORDER BY quantity ASC LIMIT 10`),
		"recent":      queryRows(`SELECT i.id, i.code, i.name, i.price, i.created_at, COALESCE((SELECT thumb FROM images im WHERE im.item_id=i.id ORDER BY sort_order, id LIMIT 1),'') AS thumb FROM items i WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 8`),
		"recent_sold": queryRows(`SELECT m.item_id AS id, i.code, i.name, -m.change AS qty, m.price_each, m.created_at FROM stock_movements m JOIN items i ON i.id=m.item_id WHERE m.reason='sold' ORDER BY m.id DESC LIMIT 8`),
		"scanner":     scanner.snapshot(),
	})
}

func handleReports(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	writeJSON(w, map[string]any{
		"by_category": queryRows(`SELECT COALESCE(c.name,'(no category)') AS category, COUNT(*) AS items, COALESCE(SUM(CASE WHEN i.quantity>0 THEN i.quantity END),0) AS units,
			COALESCE(SUM(CASE WHEN i.quantity>0 THEN i.price*i.quantity END),0) AS retail, COALESCE(SUM(CASE WHEN i.quantity>0 THEN i.cost*i.quantity END),0) AS cost
			FROM items i LEFT JOIN categories c ON c.id=i.category_id WHERE i.deleted_at IS NULL GROUP BY i.category_id ORDER BY items DESC`),
		"by_status": queryRows(`SELECT status, COUNT(*) AS items FROM items WHERE deleted_at IS NULL GROUP BY status ORDER BY items DESC`),
		"top_sellers": queryRows(`SELECT i.id, i.code, i.name, -SUM(m.change) AS sold, COALESCE(SUM(-m.change*m.price_each),0) AS revenue
			FROM stock_movements m JOIN items i ON i.id=m.item_id WHERE m.reason='sold' AND m.created_at>=? GROUP BY m.item_id ORDER BY sold DESC LIMIT 20`, since),
		"sales_by_day": queryRows(`SELECT substr(created_at,1,10) AS day, -SUM(change) AS units, COALESCE(SUM(-change*price_each),0) AS revenue
			FROM stock_movements WHERE reason='sold' AND created_at>=? GROUP BY day ORDER BY day`, since),
		"aging": queryRows(`SELECT id, code, name, quantity, price, created_at FROM items WHERE deleted_at IS NULL AND quantity>0 AND status='in_stock' ORDER BY created_at ASC LIMIT 20`),
		"days":  days,
	})
}
