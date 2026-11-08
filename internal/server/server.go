// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package server builds the MCP server, registers its tools, and serves it over HTTP.
package server

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/tools"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// Name is the server's MCP implementation name and its command name.
const Name = "tenable-docs-mcp"

// ProjectURL is reported to MCP clients as the server's website.
const ProjectURL = "https://github.com/lanz/tenable-docs-mcp"

const instructions = `Cited answers from Tenable's product documentation (docs.tenable.com and its translated sites).
Search the docs before answering a question about a Tenable product, cite the link you used, and say when the docs
don't cover a question rather than answering from memory. For Tenable API questions, use the Tenable Developer
Portal MCP (https://developer.tenable.com/mcp).

Start with search_docs, passing the user's question in their words and language; read the best result with read_doc
before answering, and cite the url it returns. For a translation or an older version, english_url is the current
English page. State the product and version an answer is for, and any required role the docs give. Before sending an
answer with docs links, check them with verify_citations. When the docs don't answer a question, say so, and offer to
record it with report_gap, which also lets the user send it to Tenable if they choose. When the user asks to give Tenable feedback on a page, open_feedback_form shows them the
docs feedback form; the feedback is theirs, and they write it. index_status shows what the documentation index holds.`

// Options configure the server.
type Options struct {
	// Index is the documentation index; nil means it isn't built (the tools say so).
	Index *index.Index
	// Live fetches from the live docs sites; nil turns off live link checks and
	// refresh_index.
	Live site.Source
	// GapsFile is where report_gap records questions; empty turns it off.
	GapsFile string
	// Feedback sends open_feedback_form's feedback; nil turns it off.
	Feedback *feedback.Sender
	// Hosted turns off what a shared server mustn't do: refresh_index, report_gap, and
	// open_feedback_form.
	Hosted bool
	// Updates keeps the index up to date from the published snapshot; nil means
	// automatic updates are off. New connects it to the tools; run it afterward.
	Updates *published.Watcher
}

// New returns the MCP server with every tool, resource, and prompt registered.
func New(opts Options) *mcp.Server {
	env := tools.NewEnv(tools.Info{Name: Name, Version: version.Get()}, opts.Index)
	env.Live = opts.Live
	if !opts.Hosted {
		env.GapsFile, env.AllowRefresh, env.Feedback = opts.GapsFile, true, opts.Feedback
	}
	s := mcp.NewServer(
		&mcp.Implementation{Name: Name, Title: "Tenable Docs MCP", Version: version.Get(), WebsiteURL: ProjectURL},
		&mcp.ServerOptions{Instructions: instructions, CompletionHandler: env.Complete},
	)
	tools.Register(s, env)
	if w := opts.Updates; w != nil {
		env.Updates = w.Status
		w.OnChange = func() { tools.RegisterGuides(s, env) }
	}
	return s
}

// UserAgent identifies the server to the docs sites.
func UserAgent() string { return Name + "/" + version.Get() + " (+" + ProjectURL + ")" }
