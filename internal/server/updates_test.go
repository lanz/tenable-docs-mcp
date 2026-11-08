// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"path/filepath"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/official"
)

func TestNewConnectsUpdates(t *testing.T) {
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	w := &official.Watcher{Updater: &official.Updater{Index: ix}}
	New(Options{Index: ix, Updates: w})
	if w.OnChange == nil {
		t.Fatal("New didn't connect the watcher's OnChange")
	}
	// Re-registering the guides of an index that has none is harmless.
	w.OnChange()
}
