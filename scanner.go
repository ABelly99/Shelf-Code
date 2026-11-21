package main

// Connector for the Barcode Scanner relay (Cloudflare Worker).
// SHELFCODE joins a room as role=pc, exactly like Barcode Typer does:
//   GET wss://<site>/ws?role=pc&code=<connecting code>
//   server -> pc: {t:'scan', id, v, enter}  |  {t:'phones', n}
//   pc -> server: {t:'typed', id, ok}
//   plain "ping" -> "pong" keep-alive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// normalizeConnectCode accepts "k7p2-qx9m-4tzr", spaces, dashes, and the usual look-alikes (O->0, I/L->1).
func normalizeConnectCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == ' ' || r == '-' || r == '_' || r == '\t':
			continue
		case r == 'O':
			r = '0'
		case r == 'I' || r == 'L':
			r = '1'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	c := b.String()
	return c, len(c) == 12
}

func prettyConnectCode(c string) string {
	if len(c) != 12 {
		return c
	}
	return c[0:4] + "-" + c[4:8] + "-" + c[8:12]
}

// relayWSURL turns "https://barcode-scanner.x.workers.dev" (or a bare host) into the wss:// endpoint.
func relayWSURL(site, code string) (string, error) {
	site = strings.TrimSpace(site)
	if site == "" {
		return "", errors.New("no scanner website address set")
	}
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("not a valid website address: %q", site)
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("address must start with https://")
	}
	u.Path = "/ws"
	u.RawQuery = "role=pc&code=" + url.QueryEscape(code)
	u.Fragment = ""
	return u.String(), nil
}

type scannerState struct {
	mu        sync.Mutex
	status    string // off | connecting | connected | error
	message   string
	phones    int
	code      string
	site      string
	lastScan  string
	lastAt    string
	cancel    context.CancelFunc
	conn      *websocket.Conn
	generation int
}

var scanner = &scannerState{status: "off"}

func (s *scannerState) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{
		"status":    s.status,
		"message":   s.message,
		"phones":    s.phones,
		"code":      prettyConnectCode(s.code),
		"site":      s.site,
		"last_scan": s.lastScan,
		"last_at":   s.lastAt,
	}
}

func (s *scannerState) set(gen int, status, msg string) {
	s.mu.Lock()
	if gen != s.generation {
		s.mu.Unlock()
		return
	}
	changed := s.status != status || s.message != msg
	s.status, s.message = status, msg
	s.mu.Unlock()
	if changed {
		hub.broadcast("scanner", scanner.snapshot())
	}
}

// Connect starts (or restarts) the connection loop. It keeps retrying until Disconnect is called.
func (s *scannerState) Connect(site, rawCode string) error {
	code, ok := normalizeConnectCode(rawCode)
	if !ok {
		return errors.New("that isn't a valid connecting code: it has 12 letters and numbers, like K7P2-QX9M-4TZR")
	}
	if _, err := relayWSURL(site, code); err != nil {
		return err
	}
	s.Disconnect()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.generation++
	gen := s.generation
	s.cancel = cancel
	s.code, s.site = code, site
	s.status, s.message, s.phones = "connecting", "Connecting…", 0
	s.mu.Unlock()
	hub.broadcast("scanner", s.snapshot())
	go s.loop(ctx, gen, site, code)
	return nil
}

func (s *scannerState) Disconnect() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.conn != nil {
		s.conn.Close(websocket.StatusNormalClosure, "bye")
		s.conn = nil
	}
	s.generation++
	wasOn := s.status != "off"
	s.status, s.message, s.phones = "off", "", 0
	s.mu.Unlock()
	if wasOn {
		hub.broadcast("scanner", s.snapshot())
	}
}

func (s *scannerState) loop(ctx context.Context, gen int, site, code string) {
	wsURL, _ := relayWSURL(site, code)
	delay := time.Second
	for ctx.Err() == nil {
		s.set(gen, "connecting", "Connecting…")
		start := time.Now()
		err := s.session(ctx, gen, wsURL)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			delay = time.Second
		}
		msg := "Connection lost."
		if err != nil {
			msg = "Can't connect: " + shortErr(err)
		}
		s.set(gen, "error", fmt.Sprintf("%s Trying again in %ds.", msg, int(delay.Seconds())))
		logLine(cRed, "SCANNER", fmt.Sprintf("%s Retrying in %ds.", msg, int(delay.Seconds())))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 140 {
		s = s[:140] + "…"
	}
	return s
}

type relayMsg struct {
	T     string `json:"t"`
	ID    string `json:"id"`
	V     string `json:"v"`
	Enter bool   `json:"enter"`
	N     int    `json:"n"`
}

func (s *scannerState) session(ctx context.Context, gen int, wsURL string) error {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, wsURL, nil)
	cancel()
	if err != nil {
		return err
	}
	conn.SetReadLimit(64 * 1024)
	s.mu.Lock()
	if gen != s.generation {
		s.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
		return nil
	}
	s.conn = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	s.set(gen, "connected", "Connected. Waiting for your phone.")
	logLine(cGreen, "SCANNER", "Connected to the relay with code "+prettyConnectCode(s.code)+". Open the scanner site on your phone and tap Scan.")

	// Keep-alive: the relay answers a plain "ping" with "pong" without waking the room.
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	var lastPong time.Time
	var pongMu sync.Mutex
	lastPong = time.Now()
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				pongMu.Lock()
				stale := time.Since(lastPong) > 80*time.Second
				pongMu.Unlock()
				if stale {
					conn.Close(websocket.StatusGoingAway, "no pong")
					return
				}
				wctx, c := context.WithTimeout(pingCtx, 10*time.Second)
				conn.Write(wctx, websocket.MessageText, []byte("ping"))
				c()
			}
		}
	}()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		pongMu.Lock()
		lastPong = time.Now()
		pongMu.Unlock()
		if typ != websocket.MessageText || string(data) == "pong" {
			continue
		}
		var m relayMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "phones":
			s.mu.Lock()
			prev := s.phones
			s.phones = m.N
			s.mu.Unlock()
			switch {
			case m.N > 0:
				s.set(gen, "connected", fmt.Sprintf("Ready. %d phone%s connected.", m.N, plural(m.N)))
				if m.N > prev {
					logLine(cGreen, "SCANNER", "Phone connected. Ready to scan.")
				}
			default:
				s.set(gen, "connected", "Connected. Waiting for your phone.")
				if prev > 0 {
					logLine(cYellow, "SCANNER", "Phone disconnected.")
				}
			}
			hub.broadcast("scanner", s.snapshot())
		case "scan":
			value := strings.TrimRight(m.V, "\r\n")
			value = strings.TrimSpace(value)
			delivered := deliverScan(value, "phone")
			s.mu.Lock()
			s.lastScan, s.lastAt = value, time.Now().Format("15:04:05")
			s.mu.Unlock()
			reply, _ := json.Marshal(map[string]any{"t": "typed", "id": m.ID, "ok": delivered})
			wctx, c := context.WithTimeout(ctx, 10*time.Second)
			conn.Write(wctx, websocket.MessageText, reply)
			c()
			hub.broadcast("scanner", s.snapshot())
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// deliverScan sends a scan to the browser tab that was used most recently.
// It returns false when no SHELFCODE page is open (the phone then shows "Could not type").
func deliverScan(code, source string) bool {
	if code == "" {
		return false
	}
	ok := hub.sendScan(code, source)
	if ok {
		logLine(cCyan, "SCAN", code)
	} else {
		logLine(cYellow, "SCAN", code+"  (no SHELFCODE page is open — open it in a browser to receive scans)")
	}
	return ok
}

func startScannerFromSettings() {
	site := settingString("scanner_relay_url")
	code := settingString("scanner_code")
	if site == "" || code == "" || !settingBool("scanner_autoconnect") {
		return
	}
	if err := scanner.Connect(site, code); err != nil {
		logLine(cRed, "SCANNER", err.Error())
	}
}
