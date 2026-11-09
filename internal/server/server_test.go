// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// connect starts the server on an in-memory transport and returns a connected client.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := New(Options{}).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestInitialize(t *testing.T) {
	init := connect(t).InitializeResult()
	if init.ServerInfo.Name != Name {
		t.Errorf("server name = %q, want %q", init.ServerInfo.Name, Name)
	}
	if init.ServerInfo.Version == "" {
		t.Error("server version is empty")
	}
	if init.ServerInfo.WebsiteURL != ProjectURL {
		t.Errorf("website = %q, want %q", init.ServerInfo.WebsiteURL, ProjectURL)
	}
	if !strings.Contains(init.Instructions, "developer.tenable.com/mcp") {
		t.Error("instructions should send API questions to the Developer Portal MCP")
	}
	if c := init.Capabilities; c == nil || c.Tools == nil || c.Resources == nil || c.Prompts == nil || c.Completions == nil {
		t.Errorf("capabilities = %+v", c)
	}
}

func TestToolAnnotations(t *testing.T) {
	res, err := connect(t).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	// Tools that aren't plain queries of the local index.
	type want struct{ readOnly, openWorld bool }
	special := map[string]want{
		"verify_citations":   {readOnly: true, openWorld: true},  // can check the live sites
		"report_gap":         {readOnly: false, openWorld: true}, // writes a local file, and can send to Tenable
		"refresh_index":      {readOnly: false, openWorld: true}, // fetches from the live sites
		"open_feedback_form": {readOnly: false, openWorld: true}, // sends to Tenable
		// Can fetch a page the index doesn't have from its live site.
		"read_doc":      {readOnly: true, openWorld: true},
		"outline":       {readOnly: true, openWorld: true},
		"related_pages": {readOnly: true, openWorld: true},
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		w, ok := special[tool.Name]
		if !ok {
			w = want{readOnly: true}
		}
		a := tool.Annotations
		if a == nil || a.ReadOnlyHint != w.readOnly || a.OpenWorldHint == nil || *a.OpenWorldHint != w.openWorld {
			t.Errorf("%s: annotations %+v, want read-only %v and open-world %v", tool.Name, a, w.readOnly, w.openWorld)
		}
		if !a.ReadOnlyHint && (a.DestructiveHint == nil || *a.DestructiveHint) {
			t.Errorf("%s: tools that write must be marked not destructive", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: every tool needs an output schema", tool.Name)
		}
	}
	if got, want := strings.Join(names, " "), "browse_guide compare_versions define_term find_setting index_status list_products open_feedback_form outline read_doc refresh_index related_pages release_notes report_gap required_role requirements search_docs verify_citations whats_changed"; got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
}

func TestIndexStatus(t *testing.T) {
	res, err := connect(t).CallTool(context.Background(), &mcp.CallToolParams{Name: "index_status"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("index_status returned an error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Server  string `json:"server"`
		Version string `json:"version"`
		Index   struct {
			State string `json:"state"`
		} `json:"index"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("structured content: %v (%s)", err, raw)
	}
	if out.Server != Name || out.Version == "" || out.Index.State != "not built" {
		t.Errorf("index_status = %s", raw)
	}
}

// A server older than the release the last update check found says so in its
// instructions and in its first tool result.
func TestUpdateInInstructions(t *testing.T) {
	old := version.Version
	version.Version = "v1.0.0"
	t.Cleanup(func() { version.Version = old })
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	if err := published.WriteState(ix.Dir, published.State{LatestRelease: "v1.1.0"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := New(Options{Index: ix}).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if in := cs.InitializeResult().Instructions; !strings.Contains(in, "Tell the user, once: Tenable Docs MCP v1.1.0 is available; this is v1.0.0") {
		t.Errorf("instructions lack the update:\n%s", in)
	}
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "index_status"})
	if err != nil || r.IsError {
		t.Fatalf("index_status = %v, %v", r, err)
	}
	var notes int
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.HasPrefix(tc.Text, "Note for the user (once):") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("first result has %d update notes", notes)
	}

	// Without a newer release, the instructions say nothing about one.
	if in := connect(t).InitializeResult().Instructions; strings.Contains(in, "is available") {
		t.Errorf("instructions mention an update:\n%s", in)
	}
}
