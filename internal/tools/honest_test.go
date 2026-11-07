// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// liveSite serves made-up live pages.
type liveSite map[string]string

func (l liveSite) Fetch(_ context.Context, req site.Request) (*site.Response, error) {
	body, ok := l[req.URL]
	if !ok {
		return nil, fmt.Errorf("fetch %s: %w", req.URL, site.ErrNotFound)
	}
	return &site.Response{URL: req.URL, Status: 200, Body: []byte(body)}, nil
}

func connectEnv(t *testing.T, env *Env) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, nil)
	Register(s, env)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestVerifyCitations(t *testing.T) {
	ix := buildIndex(t)
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, ix)
	env.Live = liveSite{en + "nessus/Content/Install.htm": `<p id="Hidden">x</p>`}
	cs := connectEnv(t, env)
	answer := "Install it ([steps](https://docs.tenable.com/nessus/Content/Install.htm#Steps)); older: " +
		"https://docs.tenable.com/nessus/10_5/Content/Install.htm. Japanese: https://jp.docs.tenable.com/nessus/Content/Install.htm#Steps\n" +
		"Broken: https://docs.tenable.com/nessus/Content/Nope.htm and https://docs.tenable.com/nessus/Content/Install.htm#Hidden " +
		"and [download](https://www.tenable.com/downloads) and [rel](Ports.htm)"
	var out verifyOut
	text := call(t, cs, "verify_citations", map[string]any{"text": answer}, &out)
	if len(out.Links) != 5 || out.Problems != 1 || out.Warnings != 3 || out.Skipped.Other != 1 || out.Skipped.Relative != 1 {
		t.Fatalf("out = %+v\n%s", out, text)
	}
	byStatus := map[string]checkedLink{}
	for _, l := range out.Links {
		byStatus[l.Status] = l
	}
	if l := byStatus["older_version"]; l.Current != en+"nessus/Content/Install.htm" {
		t.Errorf("older = %+v", l)
	}
	if l := byStatus["translation"]; l.English != en+"nessus/Content/Install.htm#Steps" {
		t.Errorf("translation = %+v", l)
	}
	if l := byStatus["not_found"]; !l.Problem || l.Lines[0] != 2 {
		t.Errorf("not found = %+v", l)
	}
	for _, want := range []string{"5 docs links: 1 broken, 3 to review", "✗ https://docs.tenable.com/nessus/Content/Nope.htm (line 2): not found", "1 relative"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	// With a base, relative links resolve; with the live check, a non-heading anchor is found.
	call(t, cs, "verify_citations", map[string]any{"text": answer, "base_url": en + "nessus/Content/Draft.htm", "check_live": true}, &out)
	if out.Skipped.Relative != 0 || len(out.Links) != 6 || out.Warnings != 2 {
		t.Errorf("with base and live = %+v", out)
	}
	// Without a live source, live checks aren't offered.
	env2 := NewEnv(Info{}, ix)
	if text, isErr := callRaw(t, connectEnv(t, env2), "verify_citations", map[string]any{"text": answer, "check_live": true}, nil); !isErr {
		t.Errorf("live without a source should fail: %s", text)
	}
	if text, isErr := callRaw(t, cs, "verify_citations", map[string]any{"text": strings.Repeat("x", maxVerifyText+1)}, nil); !isErr {
		t.Errorf("an oversized text should fail: %s", text)
	}
}

func TestNoteUnpublished(t *testing.T) {
	dir := t.TempDir()
	draft := filepath.Join(dir, "Draft.htm")
	if err := os.WriteFile(filepath.Join(dir, "New Topic.htm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := verifyOut{Links: []checkedLink{
		{Status: "not_found", Raw: "New%20Topic.htm#a"},
		{Status: "not_found", Raw: "Missing.htm"},
		{Status: "not_found", Raw: "https://docs.tenable.com/x.htm"},
		{Status: "ok", Raw: "New%20Topic.htm"},
	}}
	NoteUnpublished(&out, draft)
	if !strings.Contains(out.Links[0].Note, "isn't published") || out.Links[1].Note != "" || out.Links[2].Note != "" || out.Links[3].Note != "" {
		t.Errorf("notes = %+v", out.Links)
	}
}

func TestReportGap(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gaps.jsonl")
	env := NewEnv(Info{Version: "test"}, buildIndex(t))
	env.GapsFile = file
	cs := connectEnv(t, env)
	var out gapOut
	text := call(t, cs, "report_gap", map[string]any{"question": "How do I scan printers?", "product": "Tenable.sc", "note": "only general scan pages"}, &out)
	if !out.Recorded || out.Total != 1 || out.File != file || !strings.Contains(text, "gaps export") {
		t.Errorf("out = %+v, %q", out, text)
	}
	call(t, cs, "report_gap", map[string]any{"question": "プリンターのスキャン", "product": "Custom Thing"}, &out)
	recs, err := gaps.Read(file)
	if err != nil || len(recs) != 2 || recs[0].Product != "Security Center" || recs[0].Language != "en" || recs[0].Version != "test" ||
		recs[1].Language != "jp" || recs[1].Product != "Custom Thing" {
		t.Errorf("records = %+v, %v", recs, err)
	}
	if text, isErr := callRaw(t, cs, "report_gap", map[string]any{"question": " "}, nil); !isErr {
		t.Errorf("an empty question should fail: %s", text)
	}
	// Off when there's no file (hosted mode).
	if text, isErr := callRaw(t, connect(t, nil), "report_gap", map[string]any{"question": "x"}, nil); !isErr || !strings.Contains(text, "isn't available") {
		t.Errorf("without a file = %q", text)
	}
}

func TestRefreshIndex(t *testing.T) {
	env := NewEnv(Info{}, buildIndex(t))
	env.AllowRefresh = true
	env.Live = liveSite{
		en + "robots.txt":                 "User-agent: *\nDisallow:\n",
		en + "nessus/Content/Install.htm": `<div id="mc-main-content"><h1>Install Nessus on Linux</h1><p>Rewritten instructions.</p></div>`,
		en + "nessus/Content/Ports.htm":   `<div id="mc-main-content"><h1>Port Requirements</h1><p>Ports.</p></div>`,
	}
	cs := connectEnv(t, env)
	var out refreshOut
	text := call(t, cs, "refresh_index", map[string]any{"urls": []string{"docs.tenable.com/nessus/Content/Install.htm", en + "nessus/Content/Welcome.htm"}}, &out)
	if out.Checked != 2 || out.Updated != 1 || out.Removed != 1 || !strings.Contains(text, "1 updated") {
		t.Errorf("urls = %+v, %q", out.Report, text)
	}
	var sr searchOut
	call(t, cs, "search_docs", map[string]any{"query": "rewritten instructions"}, &sr)
	if len(sr.Results) == 0 {
		t.Error("the refreshed text isn't searchable")
	}
	call(t, cs, "refresh_index", map[string]any{"guide": "nessus"}, &out)
	if out.Checked != 3 {
		t.Errorf("guide = %+v", out.Report)
	}
	for _, args := range []map[string]any{{}, {"urls": []string{en + "a.htm"}, "guide": "nessus"}, {"urls": []string{"https://example.com/x"}},
		{"guide": "nessus", "language": "pt"}, {"guide": "missing"}} {
		if text, isErr := callRaw(t, cs, "refresh_index", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
	// Off in hosted mode.
	env.AllowRefresh = false
	if text, isErr := callRaw(t, connectEnv(t, env), "refresh_index", map[string]any{"guide": "nessus"}, nil); !isErr || !strings.Contains(text, "isn't available") {
		t.Errorf("hosted = %q", text)
	}
}
