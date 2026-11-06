// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-documentation-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools defines the server's MCP tools.
package tools

import (
	"context"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Info describes the running server, for index_status.
type Info struct {
	Name    string
	Version string
}

// RegisterStatus adds index_status, which reports the server's version and what the
// documentation index holds.
func RegisterStatus(s *mcp.Server, info Info) {
	closed := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "index_status",
		Description: "Show this server's version and what its documentation index holds: products, versions, and " +
			"languages, where the index came from, and when it was last updated. Use it first when a search returns " +
			"nothing, to check whether the index is built.",
		Annotations: &mcp.ToolAnnotations{Title: "Index status", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
		return nil, statusOut{
			Server:    info.Name,
			Version:   info.Version,
			GoVersion: runtime.Version(),
			Index:     indexState{State: "not built"},
			Note:      "The documentation index isn't available in this build yet.",
		}, nil
	})
}

type statusIn struct{}

type indexState struct {
	// State is "not built", "building", or "ready".
	State string `json:"state" jsonschema:"not built, building, or ready"`
}

type statusOut struct {
	Server    string     `json:"server"`
	Version   string     `json:"version"`
	GoVersion string     `json:"go_version"`
	Index     indexState `json:"index"`
	Note      string     `json:"note,omitempty"`
}
