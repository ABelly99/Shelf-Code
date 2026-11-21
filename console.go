package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cGray   = "\x1b[90m"
	cRed    = "\x1b[91m"
	cGreen  = "\x1b[92m"
	cYellow = "\x1b[93m"
	cBlue   = "\x1b[94m"
	cCyan   = "\x1b[96m"
)

func noColors() {
	cReset, cBold, cGray, cRed, cGreen, cYellow, cBlue, cCyan = "", "", "", "", "", "", "", ""
}

var outMu sync.Mutex

// Log lines go through a buffered channel so a slow or paused console never blocks a request.
var logCh = make(chan string, 256)

func init() {
	go func() {
		for line := range logCh {
			outMu.Lock()
			fmt.Print(line)
			outMu.Unlock()
		}
	}()
}

func logLine(color, tag, msg string) {
	line := fmt.Sprintf("\r%s%s%s  %s%-8s%s %s\n%s> %s", cGray, time.Now().Format("15:04:05"), cReset, color, tag, cReset, msg, cBold, cReset)
	select {
	case logCh <- line:
	default: // console is stuck: drop the line rather than freeze
	}
}

func say(lines ...string) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Print("\r")
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Print(cBold + "> " + cReset)
}

func printBanner(wantedPort int, localOnly bool) {
	outMu.Lock()
	fmt.Println()
	fmt.Println(cBold + "  ███ SHELFCODE " + cReset + cGray + " barcode inventory · v" + appVersion + cReset)
	fmt.Println()
	fmt.Printf("  %sOn this PC:%s        %shttp://localhost:%d%s\n", cGray, cReset, cCyan, listenPort, cReset)
	ips := lanIPs()
	if localOnly || !settingBool("lan_enabled") {
		fmt.Printf("  %sOther devices:%s     %soff%s (turn on in Settings → Network)\n", cGray, cReset, cYellow, cReset)
	} else if len(ips) == 0 {
		fmt.Printf("  %sOther devices:%s     %sno network found%s\n", cGray, cReset, cYellow, cReset)
	} else {
		for i, a := range ips {
			label := "Other devices:"
			if i > 0 {
				label = ""
			}
			fmt.Printf("  %s%-18s%s %shttp://%s:%d%s  %s(%s)%s\n", cGray, label, cReset, cCyan, a.IP, listenPort, cReset, cGray, a.Iface, cReset)
		}
		fmt.Printf("  %s%-18s%s Your PC's local IP is %s%s%s. Phones/tablets on the same Wi-Fi can use the address above.\n", cGray, "", cReset, cBold, ips[0].IP, cReset)
		if settingString("lan_pin") != "" {
			fmt.Printf("  %s%-18s%s Other devices must enter the PIN set in Settings → Network.\n", cGray, "", cReset)
		}
	}
	if listenPort != wantedPort {
		fmt.Printf("  %sPort %d was busy, so %d is used.%s\n", cYellow, wantedPort, listenPort, cReset)
	}
	fmt.Printf("  %sData folder:%s       %s\n", cGray, cReset, dataDir)
	fmt.Println()
	fmt.Println(cGray + "  Keep this window open while you use SHELFCODE. Closing it stops the app." + cReset)
	fmt.Println(cGray + "  Type " + cReset + "help" + cGray + " for commands." + cReset)
	fmt.Println()
	if settingString("scanner_code") == "" {
		fmt.Println(cYellow + "  Phone scanner:" + cReset + " not connected yet. Type  " + cBold + "connect XXXX-XXXX-XXXX" + cReset + "  with the code from your phone's Drawer,")
		fmt.Println("                 or use Settings → Scanner in the app. (Close Barcode Typer first: both would get every scan.)")
		fmt.Println()
	}
	fmt.Print(cBold + "> " + cReset)
	outMu.Unlock()
}

func consoleLoop() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			say()
			continue
		}
		cmd, arg, _ := strings.Cut(line, " ")
		arg = strings.TrimSpace(arg)
		switch strings.ToLower(cmd) {
		case "help", "?", "h":
			say(
				"",
				"  "+cBold+"open"+cReset+"                     open SHELFCODE in your browser",
				"  "+cBold+"ip"+cReset+"                       show the addresses for this PC and other devices",
				"  "+cBold+"status"+cReset+"                   items, open pages, scanner status",
				"  "+cBold+"connect <code>"+cReset+"           connect the phone scanner (code from the phone's Drawer)",
				"  "+cBold+"disconnect"+cReset+"               stop receiving phone scans",
				"  "+cBold+"site <address>"+cReset+"           change the scanner website (now: "+settingString("scanner_relay_url")+")",
				"  "+cBold+"scan <code>"+cReset+"              send a test scan to the open page",
				"  "+cBold+"backup"+cReset+"                   make a backup now",
				"  "+cBold+"clear"+cReset+"                    clear this window",
				"  "+cBold+"quit"+cReset+"                     stop SHELFCODE (closing the window does the same)",
				"",
			)
		case "open", "o":
			openBrowser(fmt.Sprintf("http://localhost:%d", listenPort))
			say("  Opening http://localhost:" + fmt.Sprint(listenPort))
		case "ip", "ips", "url", "urls", "address":
			lines := []string{"", fmt.Sprintf("  This PC:        %shttp://localhost:%d%s", cCyan, listenPort, cReset)}
			for _, a := range lanIPs() {
				lines = append(lines, fmt.Sprintf("  Other devices:  %shttp://%s:%d%s   (%s)", cCyan, a.IP, listenPort, cReset, a.Iface))
			}
			if !settingBool("lan_enabled") {
				lines = append(lines, cYellow+"  Access from other devices is turned off (Settings → Network)."+cReset)
			}
			lines = append(lines, "")
			say(lines...)
		case "status":
			dbLock.RLock()
			var items, trash int64
			db.QueryRow(`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`).Scan(&items)
			db.QueryRow(`SELECT COUNT(*) FROM items WHERE deleted_at IS NOT NULL`).Scan(&trash)
			dbLock.RUnlock()
			s := scanner.snapshot()
			say("",
				fmt.Sprintf("  Items: %d  (Trash: %d)", items, trash),
				fmt.Sprintf("  Open SHELFCODE pages: %d", hub.count()),
				fmt.Sprintf("  Scanner: %v  %v  code %v", s["status"], s["message"], s["code"]),
				fmt.Sprintf("  Running since %s", startedAt.Format("15:04:05")),
				"")
		case "connect":
			if arg == "" {
				say("  Type: connect XXXX-XXXX-XXXX   (the code is in the Drawer of the scanner site on your phone)")
				continue
			}
			site := settingString("scanner_relay_url")
			if err := scanner.Connect(site, arg); err != nil {
				say("  " + cRed + err.Error() + cReset)
				continue
			}
			code, _ := normalizeConnectCode(arg)
			dbLock.RLock()
			setSetting(db, "scanner_relay_url", site)
			setSetting(db, "scanner_code", code)
			setSetting(db, "scanner_autoconnect", true)
			dbLock.RUnlock()
			say("  Connecting to " + site + " with code " + prettyConnectCode(code) + " … (it reconnects automatically next time)")
		case "disconnect":
			scanner.Disconnect()
			dbLock.RLock()
			setSetting(db, "scanner_autoconnect", false)
			dbLock.RUnlock()
			say("  Phone scanner disconnected. Type  connect <code>  to connect again.")
		case "site":
			if arg == "" {
				say("  Scanner website: " + settingString("scanner_relay_url"))
				continue
			}
			if _, err := relayWSURL(arg, "000000000000"); err != nil {
				say("  " + cRed + err.Error() + cReset)
				continue
			}
			if !strings.Contains(arg, "://") {
				arg = "https://" + arg
			}
			dbLock.RLock()
			setSetting(db, "scanner_relay_url", arg)
			dbLock.RUnlock()
			say("  Scanner website set to " + arg + ". Type  connect <code>  to connect.")
		case "scan":
			if arg == "" {
				say("  Type: scan <code>")
				continue
			}
			deliverScan(arg, "console")
		case "backup":
			dbLock.RLock()
			name, err := makeBackup("manual")
			dbLock.RUnlock()
			if err != nil {
				say("  " + cRed + "Backup failed: " + err.Error() + cReset)
			} else {
				say("  Backup saved: " + name)
			}
		case "clear", "cls":
			outMu.Lock()
			fmt.Print("\x1b[2J\x1b[H")
			outMu.Unlock()
			printBanner(listenPort, false)
		case "quit", "exit", "stop", "q":
			return
		default:
			say("  Unknown command. Type  help  for the list.")
		}
	}
	// stdin closed (e.g. started without a console): keep serving until stopped.
	select {}
}
