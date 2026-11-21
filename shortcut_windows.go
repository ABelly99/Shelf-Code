//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// createDesktopShortcut puts "SHELFCODE" on the desktop (works with OneDrive-redirected desktops).
func createDesktopShortcut() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	if strings.Contains(strings.ToLower(exe), `\appdata\local\temp`) {
		return "", errors.New("running from a temporary folder")
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := `$d=[Environment]::GetFolderPath('Desktop'); $p=Join-Path $d 'SHELFCODE.lnk';` +
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut($p);` +
		`$s.TargetPath=` + q(exe) + `;$s.WorkingDirectory=` + q(filepath.Dir(exe)) + `;` +
		`$s.Description='SHELFCODE barcode inventory';$s.IconLocation=` + q(exe+",0") + `;$s.Save(); Write-Output $p`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
