//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetConsoleTitleW = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleTitleW")

func setTitle(p *uint16) { procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(p))) }
