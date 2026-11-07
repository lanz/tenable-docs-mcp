// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

type toolCase struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	// Expect lists the pages that answer the question, as paths on any of the docs
	// sites; * matches within one path segment.
	Expect []string `json:"expect"`
}

// TestEvalTools checks that the specialized tools answer their evaluation questions
// with the right page among their first five pages. It needs a built index in
// TDM_INDEX_DIR.
func TestEvalTools(t *testing.T) {
	dir := os.Getenv("TDM_INDEX_DIR")
	if dir == "" {
		t.Skip("set TDM_INDEX_DIR to a built index")
	}
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	cs := connect(t, ix)
	// eval-tools is the set the tools were tuned on. eval-tools-holdout was written from
	// randomly chosen pages; its first run found three gaps, which were fixed, so it's
	// tuned on now too. eval-tools-holdout2 was written afterward from new random pages
	// and not tuned on.
	for _, set := range []string{"eval-tools", "eval-tools-holdout", "eval-tools-holdout2"} {
		t.Run(set, func(t *testing.T) { evalToolSet(t, cs, set) })
	}
}

func evalToolSet(t *testing.T, cs *mcp.ClientSession, set string) {
	data, err := os.ReadFile(filepath.Join("testdata", set+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []toolCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	right := 0
	for _, c := range cases {
		var out map[string]any
		text, isErr := callRaw(t, cs, c.Tool, c.Args, &out)
		if isErr {
			t.Errorf("%s %v: %s", c.Tool, c.Args, text)
			continue
		}
		pages := pagesIn(out)
		rank := 0
		for i, p := range pages[:min(5, len(pages))] {
			if matchesAny(p, c.Expect) {
				rank = i + 1
				break
			}
		}
		mark := "  "
		if rank == 0 {
			mark = "✗ "
		} else {
			right++
		}
		top := pages[:min(3, len(pages))]
		t.Logf("%s%-14s %-70v page rank %d; top: %s", mark, c.Tool, c.Args, rank, strings.Join(top, " | "))
		if os.Getenv("TDM_SHOW") != "" {
			t.Logf("%s", text)
		}
	}
	t.Logf("%d of %d questions answered with the right page", right, len(cases))
	if right < len(cases)*9/10 {
		t.Errorf("fewer than 90%% answered with the right page")
	}
}

// pagesIn lists the distinct pages a tool's output cites, in order.
func pagesIn(out map[string]any) []string {
	var pages []string
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if u, ok := x["url"].(string); ok {
				if p, err := url.Parse(u); err == nil {
					key := strings.TrimPrefix(p.Path, "/")
					if !seen[key] {
						seen[key] = true
						pages = append(pages, key)
					}
				}
			}
			for _, k := range []string{"settings", "answers", "requirements", "entries", "definitions"} {
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(out)
	return pages
}

func matchesAny(p string, patterns []string) bool {
	for _, e := range patterns {
		if ok, _ := path.Match(e, p); ok {
			return true
		}
	}
	return false
}
