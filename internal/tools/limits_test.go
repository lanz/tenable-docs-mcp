// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"strings"
	"testing"
)

// A search argument is limited, so one huge question can't cost minutes of work.
func TestLongQueriesAreRefused(t *testing.T) {
	cs := connect(t, buildIndex(t))
	long := strings.Repeat("scan ", MaxQuery/5+1)
	for tool, args := range map[string]map[string]any{
		"search_docs":   {"query": long},
		"find_setting":  {"query": long},
		"required_role": {"task": long},
		"requirements":  {"query": long},
		"release_notes": {"query": long},
		"define_term":   {"term": long},
	} {
		if text, isErr := callRaw(t, cs, tool, args, nil); !isErr || !strings.Contains(text, "the limit is") {
			t.Errorf("%s: %v %q", tool, isErr, text)
		}
	}
	if _, isErr := callRaw(t, cs, "search_docs", map[string]any{"query": strings.Repeat("é", MaxQuery)}, nil); isErr {
		t.Error("a query at the limit was refused")
	}
}
