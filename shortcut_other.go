//go:build !windows

package main

func createDesktopShortcut() (string, error) { return "", nil }
