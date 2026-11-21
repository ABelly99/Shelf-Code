//go:build windows

package main

import (
	"os"
	"os/exec"
	"os/signal"
)

// Windows has no exec: start the new program in this same console window, hand it the
// window, and leave when it does.
func execProgram(path string) error { return execProgramEnv(path, "SHELFCODE_JUST_UPDATED=1") }

func execProgramEnv(path, extra string) error {
	cmd := exec.Command(path, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), extra)
	if err := cmd.Start(); err != nil {
		return err
	}
	signal.Ignore(os.Interrupt) // Ctrl+C / closing the window is handled by the new program
	cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	os.Exit(code)
	return nil
}

func relaunch() error { return execProgram(selfPath()) }
