// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A build deleted after it was listed isn't created again, empty.
func TestOpenExistingDoesntCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "en-current.7.db")
	k := Key{Site: "en"}
	if _, err := openExisting(path, k); !errors.Is(err, errNoBuild) {
		t.Errorf("openExisting = %v", err)
	}
	// Even past the first check, as when the file goes between the check and the open.
	if s, err := openShardMode(path, k, migrations, "rw"); err == nil {
		s.Close()
		t.Error("mode=rw opened a missing file")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file was created: %v", err)
	}
}
