// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools defines the server's MCP tools.
package tools

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

// Info describes the running server, for index_status.
type Info struct {
	Name    string
	Version string
}

type statusIn struct{}

type shardState struct {
	Shard       string `json:"shard" jsonschema:"the language and whether it holds current or older versions, such as en-current"`
	Pages       int    `json:"pages"`
	Sections    int    `json:"sections"`
	Schema      int    `json:"schema"`
	BuiltAt     string `json:"built_at,omitempty"`
	LastIndexed string `json:"last_indexed,omitempty"`
}

type indexState struct {
	// State is "not built" or "ready".
	State  string       `json:"state" jsonschema:"not built or ready"`
	Folder string       `json:"folder,omitempty"`
	Shards []shardState `json:"shards,omitempty"`
}

type statusOut struct {
	Server    string     `json:"server"`
	Version   string     `json:"version"`
	GoVersion string     `json:"go_version"`
	Index     indexState `json:"index"`
	Note      string     `json:"note,omitempty"`
}

func registerStatus(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "index_status",
		Description: "Show this server's version and what its documentation index holds: each language's current and " +
			"older-version pages, and when they were indexed. Use it first when a search returns nothing, to check " +
			"whether the index is built.",
		Annotations: readOnly("Index status"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
		out := statusOut{Server: env.Info.Name, Version: env.Info.Version, GoVersion: runtime.Version(), Index: indexState{State: "not built"}}
		if env.Index != nil {
			out.Index.Folder = env.Index.Dir
			for _, k := range env.Index.Keys() {
				err := env.Index.With(k, func(sh *index.Shard) error {
					counts, err := sh.Counts(ctx)
					if err != nil {
						return err
					}
					st := shardState{Shard: k.String(), Pages: counts["pages"], Sections: counts["sections"]}
					st.Schema, _ = sh.SchemaVersion(ctx)
					st.BuiltAt, _ = sh.Meta(ctx, "built_at")
					st.LastIndexed, _ = sh.LastIndexed(ctx)
					out.Index.Shards = append(out.Index.Shards, st)
					return nil
				})
				if err != nil {
					return nil, statusOut{}, err
				}
			}
		}
		if len(out.Index.Shards) > 0 {
			out.Index.State = "ready"
		} else {
			out.Note = "The documentation index isn't built yet, so the search and reading tools have nothing to work with."
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s; index %s", out.Server, out.Version, out.Index.State)
		if out.Index.Folder != "" {
			fmt.Fprintf(&b, " in %s", out.Index.Folder)
		}
		b.WriteString(".")
		for _, sh := range out.Index.Shards {
			fmt.Fprintf(&b, "\n- %s: %d pages, %d sections, last indexed %s", sh.Shard, sh.Pages, sh.Sections, sh.LastIndexed)
		}
		if out.Note != "" {
			b.WriteString("\n" + out.Note)
		}
		return summary("%s", b.String()), out, nil
	})
}
