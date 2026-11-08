// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package gaps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAddReadSummarize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "gaps.jsonl")
	if recs, err := Read(path); err != nil || recs != nil {
		t.Fatalf("missing file = %v, %v", recs, err)
	}
	day := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }
	for _, r := range []Record{
		{Time: day(1), Question: "How do I scan IoT devices?", Product: "Nessus", Note: "no IoT page"},
		{Time: day(2), Question: "how do I  scan IoT devices?", Product: "Nessus", Note: "no IoT page"},
		{Time: day(3), Question: "Archive old scans?", Product: "Nessus"},
		{Time: day(4), Question: "SC 7.1 upgrade path?", Product: "Security Center", Language: "en"},
		{Time: day(5), Question: "サポート期間は？", Language: "jp"},
	} {
		if _, err := Add(path, r); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Add(path, Record{Question: strings.Repeat("x", MaxQuestion+10), Note: strings.Repeat("n", MaxNote+5)})
	if err != nil || n != 6 {
		t.Fatalf("add = %d, %v", n, err)
	}
	if _, err := Add(path, Record{Question: "  "}); err == nil {
		t.Error("an empty question should be an error")
	}
	// A line that doesn't parse is skipped.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n")
	f.Close()
	recs, err := Read(path)
	if err != nil || len(recs) != 6 {
		t.Fatalf("read = %d, %v", len(recs), err)
	}
	if len([]rune(recs[5].Question)) != MaxQuestion || len([]rune(recs[5].Note)) != MaxNote || recs[5].Time.IsZero() {
		t.Errorf("clipped = %d %d %v", len(recs[5].Question), len(recs[5].Note), recs[5].Time)
	}

	s := Summarize(recs[:5], time.Time{})
	if s.Total != 5 || s.First != day(1) || s.Last != day(5) || s.Language["en"] != 4 || s.Language["jp"] != 1 {
		t.Errorf("summary = %+v", s)
	}
	if s.Products[0].Product != "Nessus" || s.Products[0].Count != 3 || len(s.Products[0].Questions) != 2 ||
		s.Products[0].Questions[0].Count != 2 || len(s.Products[0].Questions[0].Notes) != 1 {
		t.Errorf("nessus = %+v", s.Products[0])
	}
	if s := Summarize(recs[:5], day(4)); s.Total != 2 {
		t.Errorf("since = %d", s.Total)
	}
	md := Summarize(recs[:5], time.Time{}).Markdown()
	for _, want := range []string{"5 questions recorded", "## Nessus (3)", "- How do I scan IoT devices? (asked 2 times)", "  - Note: no IoT page", "## No product named (1)", "Languages: en 4, jp 1."} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if md := Summarize(nil, time.Time{}).Markdown(); !strings.Contains(md, "None recorded") {
		t.Errorf("empty = %q", md)
	}

	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 {
			t.Errorf("file is %v", info.Mode().Perm())
		}
	}
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Errorf("clearing again = %v", err)
	}
	if recs, _ := Read(path); len(recs) != 0 {
		t.Errorf("after clear = %d", len(recs))
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil || !strings.HasSuffix(filepath.ToSlash(p), "tenable-docs-mcp/gaps.jsonl") {
		t.Errorf("DefaultPath = %q, %v", p, err)
	}
}

func TestMarkSent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	t1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range []Record{{Time: t1, Question: "First"}, {Time: t1.Add(time.Hour), Question: "Again"}, {Time: t1.Add(2 * time.Hour), Question: "Again"}} {
		if _, err := Add(path, r); err != nil {
			t.Fatal(err)
		}
	}
	// A line that doesn't parse is kept as it is.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n")
	f.Close()

	sent := t1.Add(24 * time.Hour)
	if ok, err := MarkSent(path, t1, "First", sent); !ok || err != nil {
		t.Fatalf("MarkSent = %v, %v", ok, err)
	}
	if ok, _ := MarkSent(path, t1, "First", sent); ok {
		t.Error("marked the same record twice")
	}
	if ok, _ := MarkSent(path, t1, "Missing", sent); ok {
		t.Error("marked a record that isn't there")
	}
	// The latest unsent one of a question.
	if ok, err := MarkLatestSent(path, "Again", sent); !ok || err != nil {
		t.Fatalf("MarkLatestSent = %v, %v", ok, err)
	}
	recs, _ := Read(path)
	if len(recs) != 3 || recs[0].SentAt.IsZero() || !recs[1].SentAt.IsZero() || recs[2].SentAt.IsZero() {
		t.Errorf("records = %+v", recs)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(data), "not json\n") {
		t.Errorf("the unparsed line was lost: %q", data)
	}
}
