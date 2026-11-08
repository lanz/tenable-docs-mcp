// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

// TestMain keeps the tests off the network: the server doesn't update its index from
// docs.tenable.com, crawl the live sites, or fetch pages on demand. Tests of updates
// set their own TDM_INDEX_URL.
func TestMain(m *testing.M) {
	os.Setenv("TDM_INDEX_URL", "off")
	os.Setenv("TDM_CRAWL", "off")
	os.Unsetenv("TDM_SITES")
	os.Exit(m.Run())
}
