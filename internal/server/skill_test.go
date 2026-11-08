// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The companion skill names tools and output fields in backticks. These are the
// fields and statuses it names that aren't tools.
var skillNonTools = map[string]bool{
	"current_url": true, "english_url": true, "not_found": true, "anchor_missing": true, "older_version": true,
}

// TestSkillMatchesTheTools keeps skills/tenable-docs/SKILL.md in step with the server:
// its frontmatter names the skill, says when to use it, and gives its version and
// maintainer, and every tool it names exists, so renaming a tool fails here rather
// than quietly breaking the skill.
func TestSkillMatchesTheTools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "tenable-docs", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	front, body, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !strings.HasPrefix(text, "---\n") || !ok {
		t.Fatal("SKILL.md doesn't start with YAML frontmatter")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(front, "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			fields[k] = v
		}
	}
	if fields["name"] != "tenable-docs" {
		t.Errorf("name = %q", fields["name"])
	}
	if !strings.Contains(front, "\nmetadata:\n") || !strings.Contains(front, "\n  maintainer: ") || !strings.Contains(front, "\n  version: ") {
		t.Error("the frontmatter should give metadata.version and metadata.maintainer, as the docs team's skills do")
	}
	if d := fields["description"]; !strings.HasPrefix(d, "Use ") || len(d) > 1024 {
		t.Errorf("description should say when to use the skill, in at most 1,024 characters: %d characters, %.40q", len(d), d)
	}

	res, err := connect(t).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = true
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile("`([a-z]+(?:_[a-z]+)+)`").FindAllStringSubmatch(body, -1) {
		named[m[1]] = true
	}
	for name := range named {
		if !tools[name] && !skillNonTools[name] {
			t.Errorf("SKILL.md names %q, which isn't one of the server's tools", name)
		}
	}
	for _, want := range []string{"search_docs", "read_doc", "verify_citations", "report_gap", "open_feedback_form", "index_status"} {
		if !named[want] {
			t.Errorf("SKILL.md doesn't mention %s", want)
		}
	}
}
