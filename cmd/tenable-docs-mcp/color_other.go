// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package main

import "os"

// enableColor reports that a terminal shows color; outside Windows, they all do.
func enableColor(*os.File) bool { return true }
