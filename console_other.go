//go:build !windows

package main

import (
	"fmt"
	"os"
)

func enableConsoleColors() {
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		noColors()
	}
}

func setConsoleTitle(t string) {
	if cReset != "" {
		fmt.Printf("\x1b]0;%s\x07", t)
	}
}
