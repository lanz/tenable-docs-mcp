// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps the tests off the network and away from the user's settings: the
// server doesn't update its index from docs.tenable.com, crawl the live sites, or fetch
// pages on demand, and it doesn't read the user's synonyms. Tests of updates set their
// own TDM_INDEX_URL.
func TestMain(m *testing.M) {
	os.Setenv("TDM_INDEX_URL", "off")
	os.Setenv("TDM_SYNONYMS", filepath.Join(os.TempDir(), "tdm-test-no-synonyms.txt"))
	os.Setenv("TDM_CRAWL", "off")
	os.Unsetenv("TDM_SITES")
	os.Exit(m.Run())
}
