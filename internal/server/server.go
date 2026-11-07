// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package server builds the MCP server, registers its tools, and serves it over HTTP.
package server

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
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
English page. State the product and version an answer is for, and any required role the docs give. index_status shows
what the documentation index holds.`

// New returns the MCP server with every tool registered, answering from an index, which
// may be nil (the tools then say the index isn't built).
func New(ix *index.Index) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: Name, Title: "Tenable Docs MCP", Version: version.Get(), WebsiteURL: ProjectURL},
		&mcp.ServerOptions{Instructions: instructions},
	)
	tools.Register(s, tools.NewEnv(tools.Info{Name: Name, Version: version.Get()}, ix))
	return s
}
