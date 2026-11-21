package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// appVersion is set at build time: -ldflags "-X main.appVersion=1.2.3"
var appVersion = "dev"

var (
	dataDir    string
	listenPort int
	startedAt  = time.Now()
	// serverMode: running as a service (Linux server, Docker, ZimaOS). No console, no browser,
	// fixed port, and every visitor is treated the same (the PIN applies to all of them).
	serverMode bool
)

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("SHELFCODE " + appVersion)
			return
		case "healthcheck":
			os.Exit(healthcheck())
		case "self-update":
			os.Exit(selfUpdateCommand())
		}
	}
	defPort, _ := strconv.Atoi(envOr("SHELFCODE_PORT", "8765"))
	port := flag.Int("port", defPort, "port to listen on (env SHELFCODE_PORT)")
	host := flag.String("host", envOr("SHELFCODE_HOST", ""), "address to listen on, e.g. 127.0.0.1 or 0.0.0.0 (env SHELFCODE_HOST)")
	data := flag.String("data", envOr("SHELFCODE_DATA", ""), "data folder (env SHELFCODE_DATA; default: 'SHELFCODE-data' next to the program)")
	headless := flag.Bool("headless", envBool("SHELFCODE_HEADLESS"), "run as a server: no console prompt, no browser (env SHELFCODE_HEADLESS=1)")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser on start")
	localOnly := flag.Bool("local-only", false, "only allow this PC (ignore other devices on the network)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "SHELFCODE %s — barcode inventory database\n\nUsage:\n  shelfcode [options]\n  shelfcode version\n  shelfcode healthcheck\n\nOptions:\n", appVersion)
		flag.PrintDefaults()
	}
	flag.Parse()

	// No terminal on Linux/macOS (systemd, Docker): behave as a server automatically.
	serverMode = *headless || (runtime.GOOS != "windows" && !stdinIsTerminal())
	if serverMode {
		noColors()
		plainLog = true
	} else {
		enableConsoleColors()
		setConsoleTitle("SHELFCODE")
	}

	if *data != "" {
		dataDir = *data
	} else {
		exe, err := os.Executable()
		if err == nil {
			exe, _ = filepath.EvalSymlinks(exe)
		}
		dir := filepath.Dir(exe)
		if err != nil || strings.Contains(strings.ToLower(dir), strings.ToLower(os.TempDir())) && strings.Contains(dir, "go-build") {
			dir, _ = os.Getwd() // `go run`: keep data next to the source instead of a temp folder
		}
		dataDir = filepath.Join(dir, "SHELFCODE-data")
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	_, statErr := os.Stat(dbPath())
	firstRun := os.IsNotExist(statErr)

	// Already running with this data folder? Then just open it instead of starting a second copy.
	if url, ok := runningInstance(); ok {
		if serverMode {
			fatal("SHELFCODE is already running with this data folder at " + url)
		}
		fmt.Println()
		fmt.Println("  SHELFCODE is already running: " + url)
		fmt.Println("  Opening it in your browser…")
		openBrowser(url)
		time.Sleep(2500 * time.Millisecond)
		return
	}

	if err := ensureDirs(); err != nil {
		fatal("Can't create the data folder " + dataDir + ": " + err.Error())
	}
	if !firstRun {
		startupUpdate() // may hand over to a newer version and not return
	}
	if err := openDB(); err != nil {
		fatal("Can't open the database: " + err.Error())
	}
	if pin := os.Getenv("SHELFCODE_PIN"); pin != "" {
		setSetting(db, "lan_pin", strings.TrimSpace(pin))
	}

	bindHost := *host
	if bindHost == "" {
		bindHost = "0.0.0.0"
		if *localOnly {
			bindHost = "127.0.0.1"
		}
	}
	var ln net.Listener
	var err error
	if serverMode {
		// Services need a fixed, predictable port.
		ln, err = net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(*port)))
		if err == nil {
			listenPort = *port
		}
	} else {
		ln, listenPort, err = listen(bindHost, *port)
	}
	if err != nil {
		fatal(err.Error())
	}
	writeLock()
	srv := &http.Server{Handler: routes(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("Server stopped: " + err.Error())
		}
	}()

	if serverMode {
		printServerStart(bindHost)
	} else {
		if firstRun {
			firstRunSetup()
		}
		printBanner(*port, *localOnly)
		if lower := strings.ToLower(dataDir); strings.Contains(lower, `\appdata\local\temp`) || strings.Contains(lower, `.zip\`) {
			fmt.Println(cRed + cBold + "  WARNING: SHELFCODE is running from inside a zip file or a temporary folder." + cReset)
			fmt.Println(cRed + "  Your data would be lost. Close this window, extract (unzip) the SHELFCODE folder first," + cReset)
			fmt.Println(cRed + "  for example to Documents, and start SHELFCODE.exe from there." + cReset)
			fmt.Println()
			fmt.Print(cBold + "> " + cReset)
		}
	}
	startScannerFromSettings()
	go func() {
		dailyBackup()
		autoEmptyTrash()
		t := time.NewTicker(time.Hour)
		for range t.C {
			dbLock.RLock()
			if db != nil {
				dailyBackup()
				autoEmptyTrash()
			}
			dbLock.RUnlock()
		}
	}()
	if !*noBrowser && !serverMode {
		openBrowser(fmt.Sprintf("http://localhost:%d", listenPort))
	}
	sig := make(chan os.Signal, 1)
	// Ctrl+C, systemctl stop, docker stop, and closing the console window (Windows sends SIGTERM).
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	if serverMode {
		<-sig
		shutdown(srv)
	}
	go func() {
		<-sig
		shutdown(srv)
	}()
	consoleLoop()
	shutdown(srv)
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func printServerStart(host string) {
	fmt.Printf("SHELFCODE %s started (server mode)\n", appVersion)
	fmt.Printf("  listening on %s:%d\n", host, listenPort)
	fmt.Printf("  data folder  %s\n", dataDir)
	if _, err := os.Stat("/.dockerenv"); err == nil || os.Getenv("container") != "" {
		fmt.Printf("  open         http://<this machine's IP>:<the port you published, usually %d>\n", listenPort)
	} else {
		for _, a := range lanIPs() {
			fmt.Printf("  open         http://%s:%d   (%s)\n", a.IP, listenPort, a.Iface)
		}
	}
	if settingString("lan_pin") == "" {
		fmt.Println("  NOTE: no PIN is set, so anyone who can reach this port can use SHELFCODE.")
		fmt.Println("        Set one in Settings → Network, or with SHELFCODE_PIN.")
	}
}

// ---------- single instance ----------

func lockPath() string { return filepath.Join(dataDir, "shelfcode.lock") }

func writeLock() {
	b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "port": listenPort, "started": startedAt.Format(time.RFC3339)})
	os.WriteFile(lockPath(), b, 0o644)
}

// runningInstance checks whether another SHELFCODE is serving this same data folder.
func runningInstance() (string, bool) {
	b, err := os.ReadFile(lockPath())
	if err != nil {
		return "", false
	}
	var l struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(b, &l) != nil || l.Port == 0 {
		return "", false
	}
	c := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/info", l.Port))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var info struct {
		DataDir string `json:"data_dir"`
	}
	if json.NewDecoder(resp.Body).Decode(&info) != nil || !samePath(info.DataDir, dataDir) {
		return "", false
	}
	return fmt.Sprintf("http://localhost:%d", l.Port), true
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// healthcheck is used by Docker: exit 0 when the server answers.
func healthcheck() int {
	port := envOr("SHELFCODE_PORT", "8765")
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/api/pin-required")
	if err != nil {
		fmt.Println("unhealthy:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Println("unhealthy: status", resp.StatusCode)
		return 1
	}
	return 0
}

// ---------- first start on a PC ----------

func firstRunSetup() {
	fmt.Println()
	fmt.Println(cBold + "  Setting up SHELFCODE for the first time…" + cReset)
	fmt.Println("  " + cGreen + "✓" + cReset + " Data folder     " + dataDir)
	fmt.Println("  " + cGreen + "✓" + cReset + " Database        " + dbPath())
	fmt.Println("  " + cGreen + "✓" + cReset + " Photos folder   " + imagesDir())
	fmt.Println("  " + cGreen + "✓" + cReset + " Backups folder  " + backupsDir() + "  (a backup is made every day)")
	readme := `SHELFCODE data folder
=====================

This folder holds everything SHELFCODE stores:

  shelfcode.db   the database (items, categories, settings, history)
  images\        item photos
  backups\       automatic daily backups (.zip: database + photos)

Keep this folder next to SHELFCODE.exe. To move SHELFCODE to another PC,
copy the whole SHELFCODE folder (the .exe and this folder together).
Don't edit or delete files here by hand.
`
	if runtime.GOOS != "windows" {
		readme = strings.ReplaceAll(readme, `\`, "/")
		readme = strings.ReplaceAll(readme, "SHELFCODE.exe", "the shelfcode program")
	}
	os.WriteFile(filepath.Join(dataDir, "READ-ME.txt"), []byte(strings.ReplaceAll(readme, "\n", newline())), 0o644)
	if name, err := createDesktopShortcut(); err == nil && name != "" {
		fmt.Println("  " + cGreen + "✓" + cReset + " Desktop shortcut " + name)
	}
	fmt.Println()
}

func newline() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

func listen(host string, port int) (net.Listener, int, error) {
	var lastErr error
	for p := port; p < port+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p))
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("no free port between %d and %d: %v", port, port+19, lastErr)
}

var shutdownOnce sync.Once

func shutdown(srv *http.Server) {
	shutdownOnce.Do(func() {
		if serverMode {
			fmt.Println("SHELFCODE stopping")
		} else {
			fmt.Println()
			fmt.Println(cGray + "  Stopping SHELFCODE…" + cReset)
		}
		scanner.Disconnect()
		srv.Close()
		dbLock.Lock()
		closeDB()
		dbLock.Unlock()
		os.Remove(lockPath())
		os.Exit(0)
	})
}

func fatal(msg string) {
	if serverMode {
		fmt.Fprintln(os.Stderr, "ERROR: "+msg)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println(cRed + "  ERROR: " + msg + cReset)
	fmt.Println()
	fmt.Println("  Press Enter to close.")
	fmt.Scanln()
	os.Exit(1)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

type lanAddr struct {
	IP    string `json:"ip"`
	Iface string `json:"iface"`
}

// lanIPs lists this PC's IPv4 addresses that other devices on the network can use.
func lanIPs() []lanAddr {
	out := []lanAddr{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifc.Name)
		if strings.Contains(name, "vethernet") || strings.Contains(name, "virtualbox") || strings.Contains(name, "vmware") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			out = append(out, lanAddr{IP: ip.String(), Iface: ifc.Name})
		}
	}
	// Home/shop networks first (192.168.x, 10.x, 172.16-31.x).
	sort.SliceStable(out, func(a, b int) bool {
		return net.ParseIP(out[a].IP).IsPrivate() && !net.ParseIP(out[b].IP).IsPrivate()
	})
	return out
}

func serverURLs() map[string]any {
	lan := []map[string]string{}
	for _, a := range lanIPs() {
		lan = append(lan, map[string]string{"url": fmt.Sprintf("http://%s:%d", a.IP, listenPort), "ip": a.IP, "iface": a.Iface})
	}
	return map[string]any{"local": fmt.Sprintf("http://localhost:%d", listenPort), "lan": lan, "port": listenPort}
}
