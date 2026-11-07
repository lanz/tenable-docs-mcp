// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// testIndex builds an index with one made-up page.
func testIndex(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "index")
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	b, err := ix.Build(index.Key{Site: "en"})
	if err != nil {
		t.Fatal(err)
	}
	en, _ := site.ByCode("en")
	u := "https://docs.tenable.com/nessus/Content/Install.htm"
	p, err := page.Parse([]byte(`<div id="mc-main-content"><h1>Install Nessus</h1><h2 id="Steps">Steps</h2><p>Run it.</p></div>`), u, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Update(context.Background(), func(tx *index.Tx) error {
		_, err := tx.Put(index.NewDoc(p, en, site.Entry{URL: u, Products: []string{"Nessus"}}, nil))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Install(b); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyLinks(t *testing.T) {
	dir := testIndex(t)
	good := writeFile(t, "good.md", "See [install](https://docs.tenable.com/nessus/Content/Install.htm#Steps).")
	bad := writeFile(t, "Draft.htm", `<a href="Install.htm#Steps">ok</a> <a href="Missing.htm">missing</a>`)

	code, out, errOut := runCmd("verify-links", "-index", dir, good)
	if code != 0 || !strings.Contains(out, "1 docs links: 0 broken") {
		t.Errorf("good: %d %q %q", code, out, errOut)
	}
	code, out, _ = runCmd("verify-links", "-index", dir, "-base", "https://docs.tenable.com/nessus/Content/Draft.htm", bad)
	if code != 1 || !strings.Contains(out, "1 broken") || !strings.Contains(out, "Missing.htm (line 1): not found") {
		t.Errorf("bad: %d %q", code, out)
	}
	code, out, _ = runCmd("verify-links", "-index", dir, "-json", good, bad)
	var multi map[string]struct {
		Problems int `json:"problems"`
		Skipped  struct {
			Relative int `json:"relative_without_base"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(out), &multi); err != nil || code != 0 || multi[bad].Skipped.Relative != 2 {
		t.Errorf("json: %d %v %+v", code, err, multi)
	}
	stdin = strings.NewReader("https://docs.tenable.com/nessus/Content/Nope.htm")
	defer func() { stdin = os.Stdin }()
	if code, out, _ := runCmd("verify-links", "-index", dir, "-"); code != 1 || !strings.Contains(out, "not found") {
		t.Errorf("stdin: %d %q", code, out)
	}
	for _, args := range [][]string{
		{"verify-links"},
		{"verify-links", "-index", dir, filepath.Join(t.TempDir(), "missing.md")},
		{"verify-links", "-index", dir, "-base", "https://docs.tenable.com/x.htm", good, bad},
		{"verify-links", "-bogus"},
	} {
		if code, _, _ := runCmd(args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func TestGapsCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gaps.jsonl")
	if code, out, _ := runCmd("gaps", "export", "-file", file); code != 0 || !strings.Contains(out, "None recorded") {
		t.Errorf("empty export: %d %q", code, out)
	}
	if _, err := gaps.Add(file, gaps.Record{Question: "Scan printers?", Product: "Nessus"}); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runCmd("gaps", "export", "-file", file); code != 0 || !strings.Contains(out, "## Nessus (1)") {
		t.Errorf("export: %d %q", code, out)
	}
	if code, out, _ := runCmd("gaps", "export", "-file", file, "-json", "-since", "2000-01-01"); code != 0 || !strings.Contains(out, `"total": 1`) {
		t.Errorf("json export: %d %q", code, out)
	}
	if code, out, _ := runCmd("gaps", "export", "-file", file, "-since", "2999-01-01"); code != 0 || !strings.Contains(out, "None recorded") {
		t.Errorf("future since: %d %q", code, out)
	}
	if code, _, errOut := runCmd("gaps", "clear", "-file", file); code != 2 || !strings.Contains(errOut, "-yes") {
		t.Errorf("clear without -yes: %d %q", code, errOut)
	}
	if code, _, _ := runCmd("gaps", "clear", "-file", file, "-yes"); code != 0 {
		t.Errorf("clear: %d", code)
	}
	if recs, _ := gaps.Read(file); len(recs) != 0 {
		t.Errorf("after clear: %d", len(recs))
	}
	if code, out, _ := runCmd("gaps", "path", "-file", file); code != 0 || strings.TrimSpace(out) != file {
		t.Errorf("path: %d %q", code, out)
	}
	if code, out, _ := runCmd("gaps", "path"); code != 0 || !strings.HasSuffix(strings.TrimSpace(filepath.ToSlash(out)), "tenable-docs-mcp/gaps.jsonl") {
		t.Errorf("default path: %d %q", code, out)
	}
	for _, args := range [][]string{{"gaps"}, {"gaps", "list"}, {"gaps", "export", "-since", "last week"}, {"gaps", "export", "-bogus"}} {
		if code, _, _ := runCmd(args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}
