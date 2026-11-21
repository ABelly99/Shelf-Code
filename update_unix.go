//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execProgram replaces this process with another program (same arguments and settings).
func execProgram(path string) error { return execProgramEnv(path, "SHELFCODE_JUST_UPDATED=1") }

func execProgramEnv(path, extra string) error {
	env := append(os.Environ(), extra)
	return syscall.Exec(path, append([]string{path}, os.Args[1:]...), env)
}

func relaunch() error { return execProgram(selfPath()) }
