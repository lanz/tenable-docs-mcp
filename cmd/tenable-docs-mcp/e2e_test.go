// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEndToEnd builds the server, runs it over stdio as an MCP client would, and answers
// every evaluation question the way an assistant would: search_docs, then read_doc on
// the best result. It checks that each answer comes with a citation that read_doc can
// open, and reports how often the expected page was in the top five. It's skipped
// unless TDM_INDEX_DIR names a built index.
func TestEndToEnd(t *testing.T) {
	dir := os.Getenv("TDM_INDEX_DIR")
	if dir == "" {
		t.Skip("set TDM_INDEX_DIR to a built index")
	}
	bin := filepath.Join(t.TempDir(), "tenable-docs-mcp.exe")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin, "serve", "-index", dir)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tools; instructions: %d characters", len(tools.Tools), len(cs.InitializeResult().Instructions))

	callTool := func(name string, args map[string]any, out any) error {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return err
		}
		if res.IsError {
			var text []string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text = append(text, tc.Text)
				}
			}
			t.Errorf("%s(%v): %s", name, args, strings.Join(text, " "))
			return nil
		}
		raw, _ := json.Marshal(res.StructuredContent)
		return json.Unmarshal(raw, out)
	}

	sets := []string{"eval-en", "eval-en-holdout", "eval-versions", "eval-ja", "eval-fr"}
	for _, set := range sets {
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "search", "testdata", set+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var cases []struct {
			Q      string   `json:"q"`
			Expect []string `json:"expect"`
		}
		if err := json.Unmarshal(data, &cases); err != nil {
			t.Fatal(err)
		}
		top5, cited := 0, 0
		for _, c := range cases {
			var sr struct {
				Results []struct {
					URL     string `json:"url"`
					English string `json:"english_url"`
				} `json:"results"`
			}
			if err := callTool("search_docs", map[string]any{"query": c.Q, "limit": 5}, &sr); err != nil {
				t.Fatal(err)
			}
			for _, r := range sr.Results {
				if expected(r.URL, c.Expect) {
					top5++
					break
				}
			}
			if len(sr.Results) == 0 {
				t.Logf("%s: no results for %q", set, c.Q)
				continue
			}
			var rd struct {
				URL      string `json:"url"`
				Markdown string `json:"markdown"`
			}
			if err := callTool("read_doc", map[string]any{"url": sr.Results[0].URL}, &rd); err != nil {
				t.Fatal(err)
			}
			if rd.URL != "" && strings.Contains(rd.Markdown, "> Source: "+rd.URL) {
				cited++
			} else {
				t.Errorf("%s: %q: read_doc gave no citation for %s", set, c.Q, sr.Results[0].URL)
			}
		}
		t.Logf("%s: %d questions, expected page in the top five for %d, cited answers for %d", set, len(cases), top5, cited)
	}
}

func expected(u string, patterns []string) bool {
	_, path, _ := strings.Cut(strings.TrimPrefix(u, "https://"), "/")
	path, _, _ = strings.Cut(path, "#")
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}
	return false
}
