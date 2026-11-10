// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"github.com/mattn/go-isatty"
	"golang.org/x/sys/windows"
)

// enableColor turns on color for a Windows console, which needs it asked for. Git Bash's
// and Cygwin's terminals aren't consoles, and show color already.
func enableColor(f *os.File) bool {
	if isatty.IsCygwinTerminal(f.Fd()) {
		return true
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
