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
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/version"
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
	// State is "not built", "downloading", or "ready".
	State  string       `json:"state" jsonschema:"not built, downloading (the first download is in progress), or ready"`
	Folder string       `json:"folder,omitempty"`
	Shards []shardState `json:"shards,omitempty"`
}

// updatesState describes the automatic updates from the published snapshot.
type updatesState struct {
	// On is false when automatic updates are turned off.
	On              bool   `json:"on"`
	Source          string `json:"source,omitempty" jsonschema:"where the published snapshot is downloaded from"`
	Updating        bool   `json:"updating,omitempty" jsonschema:"an update is in progress"`
	Step            string `json:"step,omitempty" jsonschema:"what the update in progress is doing"`
	CheckedAt       string `json:"checked_at,omitempty" jsonschema:"when the last successful check for an update finished"`
	SnapshotBuiltAt string `json:"snapshot_built_at,omitempty" jsonschema:"when the published snapshot that check found was built"`
	LastError       string `json:"last_error,omitempty" jsonschema:"why the last check failed, if it did"`
	ErrorAt         string `json:"error_at,omitempty"`
	// From is where the index was last updated from.
	From       string `json:"from,omitempty" jsonschema:"where the index was last updated from: published (the snapshot on docs.tenable.com) or live (the live sites, crawled because the snapshot couldn't be reached)"`
	CrawledAt  string `json:"crawled_at,omitempty" jsonschema:"when the last crawl of the live sites finished"`
	CrawlError string `json:"crawl_error,omitempty" jsonschema:"why the last crawl of the live sites failed, if it did"`
}

type statusOut struct {
	Server    string       `json:"server"`
	Version   string       `json:"version"`
	GoVersion string       `json:"go_version"`
	Index     indexState   `json:"index"`
	Updates   updatesState `json:"updates"`
	// Update is set when a newer release of the server is available.
	Update *version.Update `json:"update,omitempty" jsonschema:"set when a newer release of this server is available: tell the user, with its link"`
	Note   string          `json:"note,omitempty"`
}

func registerStatus(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "index_status",
		Description: "Show this server's version and what its documentation index holds: each language's current and " +
			"older-version pages, and when they were indexed. Use it first when a search returns nothing, to check " +
			"whether the index is built.",
		Annotations: readOnly("Index status"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, statusOut, error) {
		out, text, err := env.status(ctx)
		if err != nil {
			return nil, statusOut{}, err
		}
		return summary("%s", text), out, nil
	})
}

// status reports the server's version and what the index holds, with a summary.
func (e *Env) status(ctx context.Context) (statusOut, string, error) {
	out := statusOut{Server: e.Info.Name, Version: e.Info.Version, GoVersion: runtime.Version(), Index: indexState{State: "not built"}}
	if e.Index != nil {
		out.Index.Folder = e.Index.Dir
		for _, k := range e.Index.Keys() {
			err := e.Index.With(k, func(sh *index.Shard) error {
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
				return statusOut{}, "", err
			}
		}
	}
	if e.Hosted {
		return e.hostedStatus(out), hostedStatusText(e.hostedStatus(out)), nil
	}
	if e.Updates != nil {
		st := e.Updates()
		out.Updates = updatesState{On: true, Source: st.URL, Updating: st.Updating, Step: st.Step, CheckedAt: st.CheckedAt,
			SnapshotBuiltAt: st.BuiltAt, LastError: st.LastError, ErrorAt: st.ErrorAt,
			From: st.Source, CrawledAt: st.CrawledAt, CrawlError: st.CrawlError}
	}
	switch {
	case len(out.Index.Shards) > 0:
		out.Index.State = "ready"
	case out.Updates.Updating:
		out.Index.State = "downloading"
		out.Note = "The documentation index is being built for the first time; the search and reading tools work once it's done. " +
			"Downloading it takes a minute or two; reading the live sites instead, when the download fails, takes about an hour."
	case out.Updates.LastError != "":
		out.Note = "The documentation index isn't built yet: downloading it failed, and the download is tried again every few minutes."
	case out.Updates.On:
		out.Note = "The documentation index isn't built yet; it downloads automatically shortly after the server starts."
	default:
		out.Note = "The documentation index isn't built yet, and automatic updates are off; run tenable-docs-mcp index pull to download it."
	}
	if u, ok := e.UpdateAvailable(); ok {
		out.Update = &u
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
	u := out.Updates
	switch {
	case !u.On:
		b.WriteString("\nAutomatic updates are off.")
	case u.Updating:
		fmt.Fprintf(&b, "\nUpdating from %s: %s.", u.Source, u.Step)
	default:
		if u.CheckedAt != "" {
			fmt.Fprintf(&b, "\nUpdates from %s: last checked %s; snapshot built %s.", u.Source, u.CheckedAt, u.SnapshotBuiltAt)
		} else {
			b.WriteString("\nUpdates from the published snapshot: not checked yet.")
		}
		if u.LastError != "" {
			fmt.Fprintf(&b, "\nThe last check failed at %s: %s", u.ErrorAt, u.LastError)
		}
		if u.From == published.SourceLive {
			fmt.Fprintf(&b, "\nSince the published snapshot couldn't be reached, the index is kept up to date from the live sites; last read %s.", u.CrawledAt)
		}
		if u.CrawlError != "" {
			fmt.Fprintf(&b, "\nThe last read of the live sites failed: %s", u.CrawlError)
		}
	}
	if out.Note != "" {
		b.WriteString("\n" + out.Note)
	}
	if out.Update != nil {
		b.WriteString("\n" + out.Update.Message())
	}
	return out, b.String(), nil
}

// hostedStatus is the status a hosted server reports: what the index holds and when it
// was last updated, for anyone who connects. It leaves out the server's own details (its
// folder, where it downloads from, and errors), and anything about updating the server,
// which its users don't run.
func (e *Env) hostedStatus(out statusOut) statusOut {
	out.Index.Folder = ""
	if e.Updates != nil {
		st := e.Updates()
		out.Updates = updatesState{On: true, Updating: st.Updating, CheckedAt: st.CheckedAt, SnapshotBuiltAt: st.BuiltAt}
	}
	if len(out.Index.Shards) > 0 {
		out.Index.State = "ready"
	} else {
		out.Index.State = "loading"
		out.Note = "The documentation index is still loading; try again in a minute."
	}
	return out
}

func hostedStatusText(out statusOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (hosted); index %s.", out.Server, out.Version, out.Index.State)
	for _, sh := range out.Index.Shards {
		fmt.Fprintf(&b, "\n- %s: %d pages, %d sections, last indexed %s", sh.Shard, sh.Pages, sh.Sections, sh.LastIndexed)
	}
	if u := out.Updates; u.CheckedAt != "" {
		fmt.Fprintf(&b, "\nThe index is updated from the published docs every hour; last checked %s, built %s.", u.CheckedAt, u.SnapshotBuiltAt)
	}
	if out.Note != "" {
		b.WriteString("\n" + out.Note)
	}
	return b.String()
}
