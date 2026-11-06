// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-documentation-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect starts the server on an in-memory transport and returns a connected client.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := New().Connect(ctx, serverT, nil)
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
}

func TestToolsAreReadOnlyAndClosedWorld(t *testing.T) {
	res, err := connect(t).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: query tools must be marked read-only and closed-world", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: every tool needs an output schema", tool.Name)
		}
	}
	if got, want := strings.Join(names, " "), "index_status"; got != want {
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
