// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Env is what the tools work with.
type Env struct {
	Info Info
	// Index is the documentation index; it may be nil, or have no shards yet.
	Index  *index.Index
	Engine *search.Engine
}

// NewEnv returns an Env for an index, which may be nil.
func NewEnv(info Info, ix *index.Index) *Env {
	e := &Env{Info: info, Index: ix}
	if ix != nil {
		e.Engine = search.NewEngine(ix)
	}
	return e
}

// Register adds every tool to the server.
func Register(s *mcp.Server, env *Env) {
	registerStatus(s, env)
	registerSearch(s, env)
	registerRead(s, env)
	registerBrowse(s, env)
}

var closedWorld = false

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}
}

var errNoIndex = errors.New("the documentation index isn't built yet, so there's nothing to search; index_status shows its state")

func (e *Env) ready() error {
	if e.Index == nil || len(e.Index.Keys()) == 0 {
		return errNoIndex
	}
	return nil
}

// summary makes a tool result whose text is a short summary; the SDK adds the
// structured output.
func summary(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// normalizeURL accepts a docs URL as users paste it: without the scheme, with http,
// or with the host in capitals. It returns an error for anything not on the docs sites.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("give the URL of a page on docs.tenable.com or one of its translated sites")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || !site.IsDocsHost(u.Host) {
		return "", fmt.Errorf("%q isn't a page on docs.tenable.com or one of its translated sites", raw)
	}
	u.Scheme, u.Host = "https", strings.ToLower(u.Hostname())
	return u.String(), nil
}

// withPage runs fn with the shard that holds a page, failing clearly when the index
// doesn't have it.
func (e *Env) withPage(ctx context.Context, pageURL string, fn func(*index.Shard, *index.PageInfo) error) error {
	if err := e.ready(); err != nil {
		return err
	}
	k, ok := index.KeyFor(pageURL)
	if !ok {
		return fmt.Errorf("%s isn't on the docs sites", pageURL)
	}
	if e.Index.Shard(k) == nil {
		return fmt.Errorf("the index has no %s documentation, so it can't show %s", k, pageURL)
	}
	return e.Index.With(k, func(s *index.Shard) error {
		info, ok, err := s.PageInfo(ctx, pageURL)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s isn't in the index: it may have moved or been removed, or not be a page; find it with search_docs", pageURL)
		}
		return fn(s, info)
	})
}

// Links between versions and languages, as the tools show them.
type languageLink struct {
	Language string `json:"language" jsonschema:"the site code: en, jp, zh-cn, zh-tw, kr, fr, de, or es"`
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
}

type versionLink struct {
	Version string `json:"version" jsonschema:"the guide version, or current"`
	URL     string `json:"url"`
}

type variantsOut struct {
	// English is the current English page, for citing a translation or an older version.
	English   string         `json:"english_url,omitempty" jsonschema:"the current English page, to cite for a translation or an older version"`
	Current   string         `json:"current_url,omitempty" jsonschema:"the current version of this page, when this one is older"`
	Languages []languageLink `json:"other_languages,omitempty"`
	Versions  []versionLink  `json:"other_versions,omitempty"`
}

func toVariants(v *search.Variants) variantsOut {
	if v == nil {
		return variantsOut{}
	}
	out := variantsOut{English: v.English, Current: v.Current}
	for _, l := range v.Languages {
		out.Languages = append(out.Languages, languageLink{Language: l.Site, URL: l.URL, Title: l.Title})
	}
	for _, x := range v.Versions {
		ver := x.Version
		if ver == "" {
			ver = "current"
		}
		out.Versions = append(out.Versions, versionLink{Version: ver, URL: x.URL})
	}
	return out
}

func roleTexts(reqs []page.Requirement) []string {
	var out []string
	for _, r := range reqs {
		if r.Label != "" {
			out = append(out, r.Label+": "+r.Text)
		} else {
			out = append(out, r.Text)
		}
	}
	return out
}
