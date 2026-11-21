package main

import (
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
	"strings"
	"syscall"
	"time"
)

const appVersion = "1.0.0"

var (
	dataDir    string
	listenPort int
	startedAt  = time.Now()
)

func main() {
	port := flag.Int("port", 8765, "port to listen on (the next free port is used if this one is busy)")
	data := flag.String("data", "", "data folder (default: 'SHELFCODE-data' next to the program)")
	noBrowser := flag.Bool("no-browser", false, "don't open the browser on start")
	localOnly := flag.Bool("local-only", false, "only allow this PC (ignore other devices on the network)")
	flag.Parse()

	enableConsoleColors()
	setConsoleTitle("SHELFCODE")

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
	if err := ensureDirs(); err != nil {
		fatal("Can't create the data folder " + dataDir + ": " + err.Error())
	}
	if err := openDB(); err != nil {
		fatal("Can't open the database: " + err.Error())
	}

	host := "0.0.0.0"
	if *localOnly {
		host = "127.0.0.1"
	}
	ln, p, err := listen(host, *port)
	if err != nil {
		fatal(err.Error())
	}
	listenPort = p
	srv := &http.Server{Handler: routes(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal("Server stopped: " + err.Error())
		}
	}()

	printBanner(*port, *localOnly)
	if lower := strings.ToLower(dataDir); strings.Contains(lower, `\appdata\local\temp`) || strings.Contains(lower, `.zip\`) {
		fmt.Println(cRed + cBold + "  WARNING: SHELFCODE is running from inside a zip file or a temporary folder." + cReset)
		fmt.Println(cRed + "  Your data would be lost. Close this window, extract (unzip) the SHELFCODE folder first," + cReset)
		fmt.Println(cRed + "  for example to Documents, and start SHELFCODE.exe from there." + cReset)
		fmt.Println()
		fmt.Print(cBold + "> " + cReset)
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
	if !*noBrowser {
		openBrowser(fmt.Sprintf("http://localhost:%d", listenPort))
	}
	sig := make(chan os.Signal, 1)
	// Ctrl+C, and closing the console window (Windows sends SIGTERM), stop cleanly.
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		shutdown(srv)
	}()
	consoleLoop()
	shutdown(srv)
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

func shutdown(srv *http.Server) {
	fmt.Println()
	logLine(cGray, "STOP", "Stopping SHELFCODE…")
	scanner.Disconnect()
	srv.Close()
	dbLock.Lock()
	closeDB()
	dbLock.Unlock()
	os.Exit(0)
}

func fatal(msg string) {
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
