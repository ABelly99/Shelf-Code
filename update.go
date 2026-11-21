package main

// Automatic updates. Every start checks GitHub for a newer release and, if there is one,
// downloads it, verifies its SHA-256 against checksums.txt, swaps it in and restarts.
//
//   Windows .exe        replaces itself (rename trick) and relaunches in the same window
//   Linux installer     "shelfcode self-update" runs as root before the service starts
//                       (ExecStartPre=+…), so "systemctl restart shelfcode" updates
//   Docker / ZimaOS     the new program is kept in <data>/.app and started from there
//   .deb / .rpm         skipped: the package manager owns /usr/bin/shelfcode
//
// Turn it off with Settings → About, or SHELFCODE_AUTO_UPDATE=0. Any failure (offline,
// GitHub down, bad checksum) is logged and the current version keeps running.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const releaseAPI = "https://api.github.com/repos/ABelly99/Shelf-Code/releases/latest"

var (
	updateMu     sync.Mutex
	updateStatus = "not checked yet"
)

func setUpdateStatus(s string) {
	updateMu.Lock()
	updateStatus = s
	updateMu.Unlock()
}

func getUpdateStatus() string {
	updateMu.Lock()
	defer updateMu.Unlock()
	return updateStatus
}

// ---------- versions ----------

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// isNewer reports whether version a is newer than b.
func isNewer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// ---------- settings (read before the database is opened) ----------

func autoUpdateEnabled() bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("SHELFCODE_AUTO_UPDATE"))); v == "0" || v == "false" || v == "no" || v == "off" {
		return false
	}
	if _, ok := parseVersion(appVersion); !ok {
		return false // "dev" builds never update themselves
	}
	if _, err := os.Stat(dbPath()); err != nil {
		return true
	}
	d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath())+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return true
	}
	defer d.Close()
	var v string
	if d.QueryRow(`SELECT value FROM settings WHERE key='auto_update'`).Scan(&v) == nil && v == "false" {
		return false
	}
	return true
}

// ---------- install type ----------

func installMode() string {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if runtime.GOOS == "linux" && exe == "/usr/bin/shelfcode" {
		return "package"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil || os.Getenv("container") != "" {
		return "docker"
	}
	return "replace"
}

func assetName() (name string, tarball bool) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "SHELFCODE-arm64.exe", false
		}
		return "SHELFCODE.exe", false
	case "linux":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return "shelfcode-linux-" + runtime.GOARCH + ".tar.gz", true
		case "arm":
			return "shelfcode-linux-armv7.tar.gz", true
		}
	}
	return "", false
}

// ---------- GitHub ----------

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *ghRelease) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

func httpGet(url string, timeout time.Duration, limit int64) ([]byte, error) {
	c := http.Client{Timeout: timeout}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "shelfcode-updater/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func latestRelease() (*ghRelease, error) {
	api := releaseAPI
	if v := os.Getenv("SHELFCODE_UPDATE_URL"); v != "" { // for testing against a local server
		api = v
	}
	b, err := httpGet(api, 6*time.Second, 1<<20)
	if err != nil {
		return nil, err
	}
	var r ghRelease
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// downloadProgram fetches the release file for this computer, checks its SHA-256
// against checksums.txt, and returns the program bytes.
func downloadProgram(r *ghRelease) ([]byte, error) {
	name, tarball := assetName()
	if name == "" {
		return nil, errors.New("no release file for " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	url, sumsURL := r.assetURL(name), r.assetURL("checksums.txt")
	if url == "" || sumsURL == "" {
		return nil, errors.New("the release has no " + name + " or checksums.txt yet")
	}
	sums, err := httpGet(sumsURL, 20*time.Second, 1<<20)
	if err != nil {
		return nil, err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = f[0]
		}
	}
	if want == "" {
		return nil, errors.New("checksums.txt has no entry for " + name)
	}
	data, err := httpGet(url, 3*time.Minute, 200<<20)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, errors.New("checksum mismatch for " + name)
	}
	if !tarball {
		return data, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, errors.New("program not found in " + name)
		}
		if filepath.Base(h.Name) == "shelfcode" && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// ---------- startup ----------

func updLog(msg string) {
	if serverMode {
		fmt.Println("UPDATE   " + msg)
	} else {
		fmt.Println("  " + cGray + msg + cReset)
	}
}

// startupUpdate runs before the database is opened. It either returns (keep running this
// version) or hands over to a newer program and never returns.
func startupUpdate() {
	if os.Getenv("SHELFCODE_JUST_UPDATED") != "" {
		updLog("Updated to SHELFCODE " + appVersion + ".")
		setUpdateStatus("Updated to " + appVersion + " at the last start.")
		os.Unsetenv("SHELFCODE_JUST_UPDATED")
		cleanupOld()
		return
	}
	cleanupOld()
	mode := installMode()
	handoff := os.Getenv("SHELFCODE_HANDOFF") != ""
	os.Unsetenv("SHELFCODE_HANDOFF")
	if mode == "docker" && !handoff {
		// A newer program downloaded earlier lives in the data folder: start that one
		// (it then checks for even newer releases itself).
		if ver := readOverrideVersion(); isNewer(ver, appVersion) {
			if err := execProgramEnv(overridePath(), "SHELFCODE_HANDOFF=1"); err != nil {
				updLog("Could not start the downloaded update: " + err.Error())
			}
		}
	}
	if !autoUpdateEnabled() {
		setUpdateStatus("Automatic updates are off.")
		return
	}
	if mode == "package" {
		setUpdateStatus("Installed as a .deb/.rpm package: update with apt or dnf.")
		return
	}
	if mode == "replace" && !exeDirWritable() {
		// e.g. the Linux service user: the root "self-update" step before start does it.
		setUpdateStatus("Updates are installed by the service when it restarts.")
		return
	}
	updLog("Checking for updates…")
	rel, err := latestRelease()
	if err != nil {
		setUpdateStatus("Could not check for updates: " + shortErr(err))
		updLog("Could not check for updates (" + shortErr(err) + "). Starting " + appVersion + ".")
		return
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if !isNewer(latest, appVersion) {
		setUpdateStatus("Up to date (" + appVersion + "), checked " + time.Now().Format("2006-01-02 15:04") + ".")
		updLog("Up to date (" + appVersion + ").")
		return
	}
	updLog("Downloading SHELFCODE " + latest + "…")
	prog, err := downloadProgram(rel)
	if err != nil {
		setUpdateStatus("Update to " + latest + " failed: " + shortErr(err))
		updLog("Update failed (" + shortErr(err) + "). Starting " + appVersion + ".")
		return
	}
	if mode == "docker" {
		if err := writeOverride(prog, latest); err != nil {
			updLog("Could not save the update: " + err.Error())
			return
		}
		updLog("Starting SHELFCODE " + latest + "…")
		if err := execProgram(overridePath()); err != nil {
			updLog("Could not start the update: " + err.Error())
		}
		return
	}
	if err := replaceSelf(prog); err != nil {
		setUpdateStatus("Update to " + latest + " failed: " + shortErr(err))
		updLog("Could not install the update (" + shortErr(err) + "). Starting " + appVersion + ".")
		return
	}
	updLog("Installed SHELFCODE " + latest + ". Restarting…")
	if err := relaunch(); err != nil {
		updLog("Please start SHELFCODE again: " + err.Error())
		os.Exit(0)
	}
}

// selfUpdateCommand is "shelfcode self-update": used by the Linux service (as root, before
// start). It only replaces the program file; the service then starts the new one.
func selfUpdateCommand() int {
	serverMode, plainLog = true, true
	noColors()
	if d := os.Getenv("SHELFCODE_DATA"); d != "" {
		dataDir = d
	}
	if installMode() == "package" {
		fmt.Println("UPDATE   Installed as a package: update with apt or dnf.")
		return 0
	}
	if !autoUpdateEnabled() {
		fmt.Println("UPDATE   Automatic updates are off.")
		return 0
	}
	rel, err := latestRelease()
	if err != nil {
		fmt.Println("UPDATE   Could not check for updates:", shortErr(err))
		return 0 // never block the service from starting
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if !isNewer(latest, appVersion) {
		fmt.Println("UPDATE   Up to date (" + appVersion + ").")
		return 0
	}
	prog, err := downloadProgram(rel)
	if err == nil {
		err = replaceSelf(prog)
	}
	if err != nil {
		fmt.Println("UPDATE   Update to "+latest+" failed:", shortErr(err))
		return 0
	}
	fmt.Println("UPDATE   Installed SHELFCODE " + latest + " (was " + appVersion + ").")
	return 0
}

// ---------- file helpers ----------

func selfPath() string {
	exe, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		return p
	}
	return exe
}

func exeDirWritable() bool {
	f, err := os.CreateTemp(filepath.Dir(selfPath()), ".shelfcode-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

func cleanupOld() { os.Remove(selfPath() + ".old") }

func overridePath() string {
	name := "shelfcode"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dataDir, ".app", name)
}

func readOverrideVersion() string {
	b, err := os.ReadFile(filepath.Join(dataDir, ".app", "version"))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(overridePath()); err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeOverride(prog []byte, version string) error {
	dir := filepath.Join(dataDir, ".app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := overridePath() + ".new"
	if err := os.WriteFile(tmp, prog, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, overridePath()); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644)
}

// replaceSelf writes the new program next to the running one and swaps them. A running
// program can be renamed (Windows too), so the swap is safe while this one keeps running.
func replaceSelf(prog []byte) error {
	exe := selfPath()
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, prog, mode); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(exe + ".old")
		if err := os.Rename(exe, exe+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			os.Rename(exe+".old", exe) // put the old one back
			return err
		}
		return nil
	}
	return os.Rename(tmp, exe)
}
