//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// enableConsoleColors turns on ANSI colours in the Windows console (Windows 10+).
func enableConsoleColors() {
	h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		noColors()
		return
	}
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		noColors()
		return
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		noColors()
	}
	windows.SetConsoleOutputCP(65001) // UTF-8 for box characters and names with accents

	// Turn off QuickEdit: clicking in the window would otherwise pause all output, which can
	// freeze the app until Esc is pressed.
	if in, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE); err == nil {
		var m uint32
		if windows.GetConsoleMode(in, &m) == nil {
			const quickEdit, extended = 0x0040, 0x0080
			windows.SetConsoleMode(in, (m&^quickEdit)|extended)
		}
	}
}

func setConsoleTitle(t string) {
	p, err := windows.UTF16PtrFromString(t)
	if err == nil {
		setTitle(p)
	}
}
