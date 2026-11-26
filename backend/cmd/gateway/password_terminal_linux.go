//go:build linux

package main

import "golang.org/x/sys/unix"

func getPasswordTerminal(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func setPasswordTerminal(fd int, state *unix.Termios) error {
	// Flush unread input before restoring echo, including cancelled/pasted secrets.
	return unix.IoctlSetTermios(fd, unix.TCSETSF, state)
}
