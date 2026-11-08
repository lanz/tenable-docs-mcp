// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSource(t *testing.T) {
	root := t.TempDir()
	en, jp := filepath.Join(root, "english"), filepath.Join(root, "japanese")
	writeFile(t, filepath.Join(en, "Sitemap.xml"), sitemapFixture)
	writeFile(t, filepath.Join(en, "widget", "Content", "Page.htm"), "english page")
	writeFile(t, filepath.Join(en, "widget", "index.htm"), "guide home")
	writeFile(t, filepath.Join(jp, "widget", "Content", "Page.htm"), "japanese page")
	writeFile(t, filepath.Join(root, "secret.txt"), "outside the docs")

	ls, err := NewLocal(map[string]string{"en": en, "JP": jp})
	if err != nil {
		t.Fatal(err)
	}
	if got := ls.Sites(); len(got) != 2 || got[0].Code != "en" || got[1].Code != "jp" {
		t.Errorf("sites = %v", got)
	}
	ctx := context.Background()
	for raw, want := range map[string]string{
		"https://docs.tenable.com/widget/Content/Page.htm":    "english page",
		"https://jp.docs.tenable.com/widget/Content/Page.htm": "japanese page",
		"https://docs.tenable.com/widget/":                    "guide home",
		"https://docs.tenable.com/widget/Content/Page%2Ehtm":  "english page",
	} {
		resp, err := ls.Fetch(ctx, Request{URL: raw})
		if err != nil || string(resp.Body) != want || resp.Status != 200 || resp.LastModified == "" {
			t.Errorf("%s = %v, %+v", raw, err, resp)
		}
	}
	page, _ := ls.Fetch(ctx, Request{URL: "https://docs.tenable.com/widget/Content/Page.htm"})
	same, err := ls.Fetch(ctx, Request{URL: page.URL, LastModified: page.LastModified})
	if err != nil || !same.NotModified || same.Body != nil {
		t.Errorf("unchanged file = %v, %+v", err, same)
	}
	for _, raw := range []string{
		"https://docs.tenable.com/missing.htm",
		"https://docs.tenable.com/widget/Content",  // a folder
		"https://fr.docs.tenable.com/widget/A.htm", // a site with no folder here
	} {
		if _, err := ls.Fetch(ctx, Request{URL: raw}); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s = %v, want ErrNotFound", raw, err)
		}
	}
	for _, raw := range []string{
		"https://docs.tenable.com/../secret.txt",
		"https://docs.tenable.com/%2e%2e/secret.txt",
		"https://docs.tenable.com/widget/..%5c..%5csecret.txt",
	} {
		resp, err := ls.Fetch(ctx, Request{URL: raw})
		if err == nil && string(resp.Body) == "outside the docs" {
			t.Errorf("%s escaped the documentation folder", raw)
		}
	}
	if _, err := ls.Fetch(ctx, Request{URL: "https://evil.example/x"}); !errors.Is(err, ErrHostNotAllowed) {
		t.Errorf("off-site = %v", err)
	}
	entries, err := FetchSitemap(ctx, ls, Sites[0])
	if err != nil || len(entries) != 2 {
		t.Errorf("local sitemap = %v, %d entries", err, len(entries))
	}
}

func TestNewLocalErrors(t *testing.T) {
	dir := t.TempDir()
	for name, dirs := range map[string]map[string]string{
		"no folders":     nil,
		"missing folder": {"en": filepath.Join(dir, "missing")},
		"unknown site":   {"xx": dir},
	} {
		if _, err := NewLocal(dirs); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseSiteDirs(t *testing.T) {
	sep := string(filepath.ListSeparator)
	got, err := ParseSiteDirs("en=" + filepath.Join("a", "en") + sep + " JP = " + filepath.Join("b", "jp") + sep)
	if err != nil || len(got) != 2 || got["en"] != filepath.Join("a", "en") || got["jp"] != filepath.Join("b", "jp") {
		t.Errorf("ParseSiteDirs = %v, %v", got, err)
	}
	// Semicolons work on every platform, so scripts and docs can use one form.
	got, err = ParseSiteDirs("en=" + filepath.Join("a", "en") + ";jp=" + filepath.Join("b", "jp"))
	if err != nil || len(got) != 2 || got["jp"] != filepath.Join("b", "jp") {
		t.Errorf("with semicolons = %v, %v", got, err)
	}
	for _, bad := range []string{"en", "=dir", "en=", "en=a" + sep + "en=b", "en=a;en=b"} {
		if _, err := ParseSiteDirs(bad); err == nil {
			t.Errorf("ParseSiteDirs(%q): no error", bad)
		}
	}
}
