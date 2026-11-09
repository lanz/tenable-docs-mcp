// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package gaps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every field is clipped, so no record can grow too long to read back.
func TestAddClipsEveryField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	huge := strings.Repeat("x", 2<<20)
	if _, err := Add(path, Record{Question: "First?", Product: huge, Language: huge, Version: huge, URL: "https://docs.tenable.com/" + huge}); err != nil {
		t.Fatal(err)
	}
	if n, err := Add(path, Record{Question: "Second?"}); err != nil || n != 2 {
		t.Fatalf("second Add = %d, %v", n, err)
	}
	recs, err := Read(path)
	if err != nil || len(recs) != 2 {
		t.Fatalf("Read = %d, %v", len(recs), err)
	}
	r := recs[0]
	if len(r.Product) > maxProduct || len(r.Language) > maxLanguage || len(r.Version) > maxVersion || r.URL != "" {
		t.Errorf("record = %d %d %d %d", len(r.Product), len(r.Language), len(r.Version), len(r.URL))
	}
}

// A line too long to read, written by an older version or by hand, is skipped, and
// the records after it are still read and can still be marked sent.
func TestReadSkipsALongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	if _, err := Add(path, Record{Question: "Before?"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"question":"Long?","product":"` + strings.Repeat("p", maxLine) + "\"}\r\n")
	f.Close()
	if n, err := Add(path, Record{Question: "After?"}); err != nil || n != 2 {
		t.Fatalf("Add = %d, %v", n, err)
	}
	recs, err := Read(path)
	if err != nil || len(recs) != 2 || recs[0].Question != "Before?" || recs[1].Question != "After?" {
		t.Fatalf("Read = %+v, %v", recs, err)
	}
	if ok, err := MarkLatestSent(path, "After?", recs[1].Time); !ok || err != nil {
		t.Errorf("MarkLatestSent = %v, %v", ok, err)
	}
	// A last line without a newline is read too.
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"question":"Unfinished?"}`)
	f.Close()
	if recs, _ := Read(path); len(recs) != 3 || recs[2].Question != "Unfinished?" {
		t.Errorf("Read = %d records", len(recs))
	}
}
