// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/snapshot"
)

// testSources returns -source for the build package's made-up sites.
func testSources(t *testing.T) string {
	t.Helper()
	enDir, _ := filepath.Abs(filepath.Join("..", "..", "internal", "build", "testdata", "en"))
	jpDir, _ := filepath.Abs(filepath.Join("..", "..", "internal", "build", "testdata", "jp"))
	return "en=" + enDir + ";jp=" + jpDir
}

func shardsIn(t *testing.T, dir string) string {
	t.Helper()
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var keys []string
	for _, k := range ix.Keys() {
		keys = append(keys, k.String())
	}
	return strings.Join(keys, " ")
}

func TestIndexBuildAndImport(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
	snap := filepath.Join(t.TempDir(), "snapshot")

	code, out, errOut := runCmd("index", "build", "-source", testSources(t), "-out", snap)
	if code != 0 || !strings.Contains(out, "Built 4 pages into the snapshot in "+snap+": en-current 2, en-older 1, jp-current 1; 1 tables of contents; 1 left out by robots.txt; 2 skipped.") ||
		!strings.Contains(errOut, "en: 6 of 6 sitemap entries") {
		t.Fatalf("build = %d\n%s\n%s", code, out, errOut)
	}
	m, err := snapshot.ReadManifest(snap)
	if err != nil || m.BuiltAt != "2026-11-07T12:00:00Z" || !strings.HasPrefix(m.Generator, "tenable-docs-mcp ") || len(m.Shards) != 3 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}

	code, out, _ = runCmd("index", "verify", snap)
	if code != 0 || strings.Count(out, "✓") != 3 || !strings.Contains(out, "Snapshot format 1, built 2026-11-07T12:00:00Z") {
		t.Errorf("verify = %d\n%s", code, out)
	}

	// Import only English, into a new index.
	ixDir := filepath.Join(t.TempDir(), "index")
	code, out, errOut = runCmd("index", "import", "-sites", "en", "-index", ixDir, snap)
	if code != 0 || !strings.Contains(out, "en-current 2, en-older 1.") {
		t.Fatalf("import = %d\n%s\n%s", code, out, errOut)
	}
	if got := shardsIn(t, ixDir); got != "en-current en-older" {
		t.Errorf("imported shards = %s", got)
	}

	// A damaged snapshot isn't imported, and the index is left as it was.
	jp := m.Shards[2].File
	file := filepath.Join(snap, jp)
	if err := os.WriteFile(file, []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runCmd("index", "verify", snap); code != 1 || !strings.Contains(out, "✗ "+jp) {
		t.Errorf("verify damaged = %d\n%s", code, out)
	}
	if code, _, errOut := runCmd("index", "import", "-index", ixDir, snap); code != 1 || !strings.Contains(errOut, jp) {
		t.Errorf("import damaged = %d\n%s", code, errOut)
	}
	if got := shardsIn(t, ixDir); got != "en-current en-older" {
		t.Errorf("after a failed import, shards = %s", got)
	}
}

func TestIndexBuildIntoIndex(t *testing.T) {
	ixDir := filepath.Join(t.TempDir(), "index")
	code, out, errOut := runCmd("index", "build", "-source", testSources(t), "-sites", "jp", "-index", ixDir, "-json")
	if code != 0 {
		t.Fatalf("build = %d\n%s\n%s", code, out, errOut)
	}
	var rep build.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || rep.Total() != 1 || rep.Pages["jp-current"] != 1 {
		t.Errorf("report = %s, %v", out, err)
	}
	if got := shardsIn(t, ixDir); got != "jp-current" {
		t.Errorf("shards = %s", got)
	}
	// TDM_INDEX_DIR is the default folder.
	t.Setenv("TDM_INDEX_DIR", filepath.Join(t.TempDir(), "env-index"))
	if code, _, errOut := runCmd("index", "build", "-source", testSources(t), "-sites", "en"); code != 0 {
		t.Errorf("build into TDM_INDEX_DIR = %d\n%s", code, errOut)
	}
	if got := shardsIn(t, os.Getenv("TDM_INDEX_DIR")); got != "en-current en-older" {
		t.Errorf("shards = %s", got)
	}
}

func TestIndexUsage(t *testing.T) {
	src := testSources(t)
	for _, args := range [][]string{
		{"index"},
		{"index", "nope"},
		{"index", "build"},
		{"index", "build", "-source", "pt=/x"},
		{"index", "build", "-source", src, "-sites", "fr"},
		{"index", "build", "-source", src, "-out", "a", "-index", "b"},
		{"index", "import"},
		{"index", "verify"},
	} {
		if code, _, _ := runCmd(args...); code != 2 {
			t.Errorf("%v = %d, want 2", args, code)
		}
	}
	if code, _, _ := runCmd("index", "import", t.TempDir()); code != 1 {
		t.Error("a folder without a manifest should fail")
	}
	if code, out, _ := runCmd("help"); code != 0 || !strings.Contains(out, "index build") {
		t.Errorf("help = %s", out)
	}
}
