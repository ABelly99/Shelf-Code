package main

// Server-Sent Events: pushes scans and scanner status to open SHELFCODE pages.
// Each page has a client id. Pages report focus/activity, and a scan goes to the page
// that was used most recently (so two open tabs never both react to one scan).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type sseClient struct {
	id       string
	ch       chan []byte
	lastUsed time.Time
	visible  bool
	remote   string
}

type eventHub struct {
	mu      sync.Mutex
	clients map[string]*sseClient
}

var hub = &eventHub{clients: map[string]*sseClient{}}

func encodeEvent(kind string, data any) []byte {
	b, _ := json.Marshal(data)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", kind, b))
}

func (h *eventHub) broadcast(kind string, data any) {
	msg := encodeEvent(kind, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		select {
		case c.ch <- msg:
		default:
		}
	}
}

func (h *eventHub) sendScan(code, source string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	var best *sseClient
	for _, c := range h.clients {
		if best == nil ||
			(c.visible && !best.visible) ||
			(c.visible == best.visible && c.lastUsed.After(best.lastUsed)) {
			best = c
		}
	}
	if best == nil {
		return false
	}
	msg := encodeEvent("scan", map[string]any{"code": code, "source": source, "at": time.Now().UnixMilli()})
	select {
	case best.ch <- msg:
		return true
	default:
		return false
	}
}

func (h *eventHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("client")
	if id == "" || len(id) > 64 {
		http.Error(w, "missing client id", 400)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: don't buffer the stream
	c := &sseClient{id: id, ch: make(chan []byte, 32), lastUsed: time.Now(), visible: true, remote: r.RemoteAddr}
	hub.mu.Lock()
	if old, ok := hub.clients[id]; ok {
		close(old.ch)
	}
	hub.clients[id] = c
	hub.mu.Unlock()
	defer func() {
		hub.mu.Lock()
		if hub.clients[id] == c {
			delete(hub.clients, id)
		}
		hub.mu.Unlock()
	}()

	w.Write(encodeEvent("scanner", scanner.snapshot()))
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-c.ch:
			if !ok {
				return
			}
			w.Write(msg)
			fl.Flush()
		case <-ping.C:
			w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

// POST /api/focus {client, visible}
func handleFocus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Client  string `json:"client"`
		Visible bool   `json:"visible"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	hub.mu.Lock()
	if c, ok := hub.clients[body.Client]; ok {
		c.visible = body.Visible
		if body.Visible {
			c.lastUsed = time.Now()
		}
	}
	hub.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}
