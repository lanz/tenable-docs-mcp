// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// serveSnapshot builds the made-up sites into a snapshot and serves it over HTTPS,
// as docs.tenable.com serves the official one, for index pull to download.
func serveSnapshot(t *testing.T) string {
	t.Helper()
	snap := filepath.Join(t.TempDir(), "snapshot")
	if code, _, errOut := runCmd("index", "build", "-source", testSources(t), "-out", snap); code != 0 {
		t.Fatalf("index build: %d %s", code, errOut)
	}
	srv := httptest.NewTLSServer(http.StripPrefix("/mcp/index/v1/", http.FileServer(http.Dir(snap))))
	t.Cleanup(srv.Close)
	pullClient = srv.Client()
	t.Cleanup(func() { pullClient = nil })
	return srv.URL + "/mcp/index/v1/"
}

func TestIndexPull(t *testing.T) {
	t.Setenv("TDM_INDEX_URL", serveSnapshot(t))
	dir := filepath.Join(t.TempDir(), "index")
	code, out, errOut := runCmd("index", "pull", "-index", dir)
	if code != 0 || !strings.Contains(out, "rebuilt en-current, en-older, jp-current; 0 unchanged") {
		t.Fatalf("first pull: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(errOut, "Downloading ") || !strings.Contains(errOut, "Indexing ") {
		t.Errorf("progress = %q", errOut)
	}
	if got := shardsIn(t, dir); got != "en-current en-older jp-current" {
		t.Errorf("shards = %q", got)
	}
	code, out, _ = runCmd("index", "pull", "-index", dir)
	if code != 0 || !strings.Contains(out, "is up to date with the snapshot built") {
		t.Errorf("second pull: %d %q", code, out)
	}
}

func TestIndexPullSites(t *testing.T) {
	t.Setenv("TDM_INDEX_URL", serveSnapshot(t))
	dir := filepath.Join(t.TempDir(), "index")
	if code, out, errOut := runCmd("index", "pull", "-sites", "jp", "-index", dir); code != 0 {
		t.Fatalf("pull -sites jp: %d %q %q", code, out, errOut)
	}
	if got := shardsIn(t, dir); got != "jp-current" {
		t.Errorf("shards = %q", got)
	}
	// TDM_SITES works the same way when -sites isn't given.
	dir2 := filepath.Join(t.TempDir(), "index")
	t.Setenv("TDM_SITES", "en")
	if code, out, errOut := runCmd("index", "pull", "-index", dir2); code != 0 {
		t.Fatalf("pull with TDM_SITES: %d %q %q", code, out, errOut)
	}
	if got := shardsIn(t, dir2); got != "en-current en-older" {
		t.Errorf("shards = %q", got)
	}
}

func TestIndexPullErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	for _, c := range []struct {
		env, sites, want string
		code             int
	}{
		{"off", "", "TDM_INDEX_URL is off", 2},
		{"http://docs.tenable.com/mcp/index/v1/", "", "must be an https:// address", 2},
		{"https://example.invalid/", "xx", `no site "xx"`, 2},
	} {
		t.Setenv("TDM_INDEX_URL", c.env)
		args := []string{"index", "pull", "-index", dir}
		if c.sites != "" {
			args = append(args, "-sites", c.sites)
		}
		code, _, errOut := runCmd(args...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Errorf("TDM_INDEX_URL=%s -sites %q: %d %q", c.env, c.sites, code, errOut)
		}
	}
	if code, _, _ := runCmd("index", "pull", "extra"); code != 2 {
		t.Errorf("extra argument: %d", code)
	}
}

func TestServeRejectsBadIndexURL(t *testing.T) {
	t.Setenv("TDM_INDEX_DIR", t.TempDir())
	t.Setenv("TDM_INDEX_URL", "ftp://example.com/")
	if code, _, errOut := runCmd("serve"); code != 2 || !strings.Contains(errOut, "TDM_INDEX_URL") {
		t.Errorf("serve: %d %q", code, errOut)
	}
}
