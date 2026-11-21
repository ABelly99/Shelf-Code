package main

// One-click "Connect" for AI agents on this computer: SHELFCODE writes the agent's MCP
// settings itself (no copying config snippets). Each agent gets the same local entry:
//   command: <this program>   args: ["mcp"]
// For agents on other computers, the settings page shows install links and commands for
// the HTTP endpoint (/mcp + token).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type agentDef struct {
	ID, Name string
	Restart  string // what the person has to do afterwards
	detect   func() bool
	status   func() bool
	connect  func(exe string) error
	remove   func() error
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// appConfigDir: %APPDATA% on Windows, ~/Library/Application Support on macOS, ~/.config on Linux.
func appConfigDir() string {
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return a
		}
		return filepath.Join(home(), "AppData", "Roaming")
	case "darwin":
		return filepath.Join(home(), "Library", "Application Support")
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(home(), ".config")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ---------- JSON config files (mcpServers / servers maps) ----------

func readJSONFile(p string) (map[string]any, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) || len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s has comments or isn't plain JSON, so it was not changed. Add SHELFCODE there by hand (see below)", p)
	}
	return m, nil
}

func writeJSONFile(p string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(p); err == nil && !exists(p+".shelfcode-backup") {
		os.WriteFile(p+".shelfcode-backup", b, 0o600) // one-time safety copy
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func jsonAgent(id, name, restart string, path func() string, key string, entry func(exe string) map[string]any, detect func() bool) agentDef {
	getMap := func(m map[string]any) map[string]any {
		s, _ := m[key].(map[string]any)
		if s == nil {
			s = map[string]any{}
			m[key] = s
		}
		return s
	}
	return agentDef{
		ID: id, Name: name, Restart: restart, detect: detect,
		status: func() bool {
			m, err := readJSONFile(path())
			if err != nil {
				return false
			}
			s, _ := m[key].(map[string]any)
			_, ok := s["shelfcode"]
			return ok
		},
		connect: func(exe string) error {
			m, err := readJSONFile(path())
			if err != nil {
				return err
			}
			getMap(m)["shelfcode"] = entry(exe)
			return writeJSONFile(path(), m)
		},
		remove: func() error {
			m, err := readJSONFile(path())
			if err != nil {
				return err
			}
			delete(getMap(m), "shelfcode")
			return writeJSONFile(path(), m)
		},
	}
}

func stdioEntry(exe string) map[string]any {
	return map[string]any{"command": exe, "args": []string{"mcp"}}
}

// ---------- Codex (TOML) ----------

var codexBlock = regexp.MustCompile(`(?ms)^\[mcp_servers\.shelfcode\]\n.*?(?:\n\[|\z)`)

func codexPath() string { return filepath.Join(home(), ".codex", "config.toml") }

func codexRemove(s string) string {
	return codexBlock.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasSuffix(m, "\n[") {
			return "["
		}
		return ""
	})
}

// ---------- the list ----------

func agentDefs() []agentDef {
	claudeDesktop := func() string { return filepath.Join(appConfigDir(), "Claude", "claude_desktop_config.json") }
	vscode := func() string { return filepath.Join(appConfigDir(), "Code", "User", "mcp.json") }
	look := func(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }

	claudeCode := agentDef{
		ID: "claude-code", Name: "Claude Code", Restart: "Start a new Claude Code session.",
		detect: func() bool {
			return look("claude") || exists(filepath.Join(home(), ".claude.json")) || exists(filepath.Join(home(), ".claude"))
		},
		status: func() bool {
			m, err := readJSONFile(filepath.Join(home(), ".claude.json"))
			if err != nil {
				return false
			}
			s, _ := m["mcpServers"].(map[string]any)
			_, ok := s["shelfcode"]
			return ok
		},
		connect: func(exe string) error {
			// Prefer the official command; fall back to editing ~/.claude.json.
			if look("claude") {
				exec.Command("claude", "mcp", "remove", "--scope", "user", "shelfcode").Run()
				out, err := exec.Command("claude", "mcp", "add", "--scope", "user", "shelfcode", "--", exe, "mcp").CombinedOutput()
				if err == nil {
					return nil
				}
				logLine(cYellow, "AGENTS", "claude mcp add failed ("+strings.TrimSpace(string(out))+"); editing ~/.claude.json instead")
			}
			p := filepath.Join(home(), ".claude.json")
			m, err := readJSONFile(p)
			if err != nil {
				return err
			}
			s, _ := m["mcpServers"].(map[string]any)
			if s == nil {
				s = map[string]any{}
				m["mcpServers"] = s
			}
			e := stdioEntry(exe)
			e["type"] = "stdio"
			s["shelfcode"] = e
			return writeJSONFile(p, m)
		},
		remove: func() error {
			if look("claude") && exec.Command("claude", "mcp", "remove", "--scope", "user", "shelfcode").Run() == nil {
				return nil
			}
			p := filepath.Join(home(), ".claude.json")
			m, err := readJSONFile(p)
			if err != nil {
				return err
			}
			if s, ok := m["mcpServers"].(map[string]any); ok {
				delete(s, "shelfcode")
			}
			return writeJSONFile(p, m)
		},
	}

	codex := agentDef{
		ID: "codex", Name: "OpenAI Codex", Restart: "Start a new Codex session.",
		detect: func() bool { return look("codex") || exists(filepath.Join(home(), ".codex")) },
		status: func() bool {
			b, _ := os.ReadFile(codexPath())
			return bytes.Contains(b, []byte("[mcp_servers.shelfcode]"))
		},
		connect: func(exe string) error {
			b, _ := os.ReadFile(codexPath())
			s := strings.TrimRight(codexRemove(string(b)), "\n")
			if s != "" {
				s += "\n\n"
			}
			// TOML literal strings ('…') keep Windows backslashes as they are.
			s += "[mcp_servers.shelfcode]\ncommand = '" + exe + "'\nargs = ['mcp']\n"
			if err := os.MkdirAll(filepath.Dir(codexPath()), 0o755); err != nil {
				return err
			}
			if len(b) > 0 && !exists(codexPath()+".shelfcode-backup") {
				os.WriteFile(codexPath()+".shelfcode-backup", b, 0o600)
			}
			return os.WriteFile(codexPath(), []byte(s), 0o600)
		},
		remove: func() error {
			b, err := os.ReadFile(codexPath())
			if err != nil {
				return nil
			}
			return os.WriteFile(codexPath(), []byte(codexRemove(string(b))), 0o600)
		},
	}

	return []agentDef{
		claudeCode,
		jsonAgent("claude-desktop", "Claude Desktop", "Quit Claude Desktop completely and open it again.", claudeDesktop, "mcpServers", stdioEntry,
			func() bool { return exists(filepath.Dir(claudeDesktop())) }),
		jsonAgent("cursor", "Cursor", "Cursor picks it up by itself (Settings → MCP shows SHELFCODE).", func() string { return filepath.Join(home(), ".cursor", "mcp.json") }, "mcpServers", stdioEntry,
			func() bool { return exists(filepath.Join(home(), ".cursor")) }),
		jsonAgent("vscode", "VS Code (Copilot)", "In VS Code, open Copilot Chat in Agent mode; SHELFCODE is in the tools list.", vscode, "servers",
			func(exe string) map[string]any { e := stdioEntry(exe); e["type"] = "stdio"; return e },
			func() bool { return exists(filepath.Dir(vscode())) }),
		jsonAgent("windsurf", "Windsurf", "Open Windsurf's MCP panel and press Refresh.", func() string { return filepath.Join(home(), ".codeium", "windsurf", "mcp_config.json") }, "mcpServers", stdioEntry,
			func() bool { return exists(filepath.Join(home(), ".codeium", "windsurf")) }),
		jsonAgent("gemini", "Gemini CLI", "Start a new Gemini CLI session (/mcp lists SHELFCODE).", func() string { return filepath.Join(home(), ".gemini", "settings.json") }, "mcpServers", stdioEntry,
			func() bool { return look("gemini") || exists(filepath.Join(home(), ".gemini")) }),
		codex,
	}
}

// agentExe: the program path agents should start ("SHELFCODE.exe mcp").
func agentExe() (string, error) {
	exe := selfPath()
	lower := strings.ToLower(exe)
	if strings.Contains(lower, `\appdata\local\temp`) || strings.Contains(lower, "go-build") {
		return "", errors.New("SHELFCODE is running from a temporary folder. Put SHELFCODE.exe in its own folder first")
	}
	return exe, nil
}

// ---------- HTTP API ----------

func agentsAllowedLocally(r *http.Request) bool { return !serverMode && isLoopback(r) }

func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	// On the main PC "localhost" means nothing to other computers: offer its network address.
	if h, _, _ := strings.Cut(host, ":"); (h == "localhost" || h == "127.0.0.1") && !serverMode {
		if ips := lanIPs(); len(ips) > 0 {
			host = fmt.Sprintf("%s:%d", ips[0].IP, listenPort)
		}
	}
	return scheme + "://" + host
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	if !trusted(r) {
		writeErr(w, 403, "AI agent settings can only be opened on the main PC")
		return
	}
	local := agentsAllowedLocally(r)
	list := []any{}
	if local {
		for _, a := range agentDefs() {
			list = append(list, map[string]any{"id": a.ID, "name": a.Name, "detected": a.detect(), "connected": a.status(), "after": a.Restart})
		}
	}
	exe, exeErr := agentExe()
	writeJSON(w, map[string]any{
		"local":     local,
		"agents":    list,
		"exe":       exe,
		"exe_error": errString(exeErr),
		"url":       requestBase(r) + "/mcp",
		"token":     agentToken(),
		"notes":     settingString("agent_notes"),
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func handleAgentConnect(w http.ResponseWriter, r *http.Request) {
	if !agentsAllowedLocally(r) {
		writeErr(w, 403, "Only possible on the main PC")
		return
	}
	var b struct {
		ID     string `json:"id"`
		Remove bool   `json:"remove"`
	}
	readJSON(r, &b)
	exe, err := agentExe()
	if err != nil && !b.Remove {
		writeErr(w, 400, err.Error())
		return
	}
	for _, a := range agentDefs() {
		if a.ID != b.ID {
			continue
		}
		if b.Remove {
			err = a.remove()
		} else {
			err = a.connect(exe)
		}
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		verb := "Connected"
		if b.Remove {
			verb = "Disconnected"
		}
		logLine(cGreen, "AGENTS", verb+" "+a.Name)
		writeJSON(w, map[string]any{"ok": true, "connected": a.status(), "after": a.Restart})
		return
	}
	writeErr(w, 404, "unknown agent")
}

func handleAgentToken(w http.ResponseWriter, r *http.Request) {
	if !trusted(r) {
		writeErr(w, 403, "Only possible on the main PC")
		return
	}
	setSetting(db, "agent_token", "")
	writeJSON(w, map[string]any{"token": agentToken()})
}

// connectAllAgents: console command "agents connect".
func connectAllAgents() []string {
	out := []string{}
	exe, err := agentExe()
	if err != nil {
		return []string{err.Error()}
	}
	for _, a := range agentDefs() {
		if !a.detect() {
			continue
		}
		if err := a.connect(exe); err != nil {
			out = append(out, fmt.Sprintf("%-18s could not connect: %v", a.Name, err))
		} else {
			out = append(out, fmt.Sprintf("%-18s connected. %s", a.Name, a.Restart))
		}
	}
	if len(out) == 0 {
		out = append(out, "No AI agents found on this PC (Claude Code, Claude Desktop, Cursor, VS Code, Windsurf, Gemini CLI, Codex).")
	}
	return out
}
