// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/search"
)

// MaxQuery is the most characters a search argument takes: a question is far shorter,
// and a long one costs a search time for every word.
const MaxQuery = 1000

// checkLength refuses a search argument longer than MaxQuery.
func checkLength(name, v string) error {
	if n := utf8.RuneCountInString(v); n > MaxQuery {
		return fmt.Errorf("%s is %d characters; the limit is %d. Ask a shorter question", name, n, MaxQuery)
	}
	return nil
}

type searchIn struct {
	Query     string `json:"query" jsonschema:"what to look for, in the user's words and language: a task, setting, message, or feature, with the product and version if known"`
	Product   string `json:"product,omitempty" jsonschema:"only this product, by any of its names (retired ones such as Tenable.io and Tenable.sc work too)"`
	GuideType string `json:"guide_type,omitempty" jsonschema:"only this guide type: User Guide, API Guide, Reference Guide, Integration Guide, Release Notes, Best Practices Guide, Migration Guide, Cyber Exposure Study, or Whitepaper"`
	Guide     string `json:"guide,omitempty" jsonschema:"only this guide, by its path on the site, such as nessus or security-center/director (see browse_guide and list_products)"`
	Version   string `json:"version,omitempty" jsonschema:"only this product version, such as 6.3 (default: the version the query names, or the current one)"`
	Language  string `json:"language,omitempty" jsonschema:"the docs' language, as a site code (en, jp, zh-cn, zh-tw, kr, fr, de, es) or language tag (ja, ko); default: the query's language"`
	// ReleaseNotes limits the search to the release notes.
	ReleaseNotes bool   `json:"release_notes,omitempty" jsonschema:"only release notes"`
	Role         string `json:"required_role,omitempty" jsonschema:"only pages that say they need this role or permission, such as Administrator"`
	Limit        int    `json:"limit,omitempty" jsonschema:"how many results to return, from 1 to 25 (default 8)"`
}

type alternate struct {
	Product string `json:"product"`
	Guide   string `json:"guide,omitempty"`
	URL     string `json:"url"`
}

type searchResult struct {
	Title         string      `json:"title"`
	Heading       string      `json:"heading"`
	URL           string      `json:"url" jsonschema:"the section's link, to cite"`
	Snippet       string      `json:"snippet" jsonschema:"text around the matched words, which are marked «like this»"`
	Product       string      `json:"product,omitempty"`
	GuideType     string      `json:"guide_type,omitempty"`
	Version       string      `json:"version,omitempty" jsonschema:"the guide version; empty for the current one"`
	Language      string      `json:"language"`
	Location      string      `json:"location" jsonschema:"where the section is: product, guide, table of contents, and headings"`
	LastModified  string      `json:"last_modified,omitempty"`
	RequiredRoles []string    `json:"required_roles,omitempty"`
	AlsoIn        []alternate `json:"also_in,omitempty" jsonschema:"pages with the same title in other products or guides, which may be what the user means"`
	variantsOut
}

type searchOut struct {
	Results           []searchResult `json:"results"`
	Language          string         `json:"language" jsonschema:"the docs' language searched"`
	RequestedLanguage string         `json:"requested_language" jsonschema:"the language asked for or detected; it differs from language when the results fell back to English"`
	Version           string         `json:"version,omitempty" jsonschema:"the product version searched; empty for current versions"`
	Notes             []string       `json:"notes,omitempty"`
	Relaxed           bool           `json:"relaxed,omitempty" jsonschema:"set when no section had every word of the query"`
	Suggestions       []string       `json:"suggestions,omitempty" jsonschema:"other spellings of the query to try, when nothing matched"`
	OtherProducts     []string       `json:"other_products,omitempty" jsonschema:"products that do have matches, when the product filter excluded them"`
	Message           string         `json:"message,omitempty"`
}

func registerSearch(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "search_docs",
		Description: "Search Tenable's product documentation (docs.tenable.com and its translated sites) for sections that " +
			"answer a question. Returns ranked sections with links to cite, snippets, product, version, language, and the " +
			"roles a task needs. Pass the user's question in their words and language; retired product names such as " +
			"Tenable.io and Tenable.sc work. Read a result in full with read_doc before relying on it. Use the Tenable " +
			"Developer Portal MCP for API questions.",
		Annotations: readOnly("Search the docs"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
		if err := checkLength("query", in.Query); err != nil {
			return nil, searchOut{}, err
		}
		if err := env.ready(); err != nil {
			return nil, searchOut{}, err
		}
		if strings.TrimSpace(in.Query) == "" {
			return nil, searchOut{}, fmt.Errorf("query is empty: give a task, setting, or question to look for")
		}
		limit := in.Limit
		switch {
		case limit <= 0:
			limit = 8
		case limit > 25:
			limit = 25
		}
		r := search.Request{Query: in.Query, Product: in.Product, GuideType: in.GuideType, Guide: strings.Trim(in.Guide, "/ "),
			Version: in.Version, Language: in.Language, Role: in.Role, Limit: limit}
		if in.ReleaseNotes {
			r.Guide = "release-notes"
		}
		res, err := env.Engine.Search(ctx, r)
		if err != nil {
			return nil, searchOut{}, err
		}
		out := searchOut{Results: []searchResult{}, Language: res.Language, RequestedLanguage: res.Requested, Version: res.Version,
			Notes: res.Notes, Relaxed: res.Relaxed, Suggestions: res.Suggestions, OtherProducts: res.OtherProducts, Message: res.NoResultsShown}
		for _, h := range res.Hits {
			sr := searchResult{Title: h.Title, Heading: h.Heading, URL: h.URL, Snippet: h.Snippet, Product: h.Product,
				GuideType: h.GuideType, Version: h.Version, Language: h.Language, Location: h.Location,
				LastModified: h.LastMod, RequiredRoles: h.Roles, variantsOut: toVariants(h.Variants)}
			for _, a := range h.AlsoIn {
				sr.AlsoIn = append(sr.AlsoIn, alternate{Product: a.Product, Guide: a.Guide, URL: a.URL})
			}
			out.Results = append(out.Results, sr)
		}
		return summary("%s", searchSummary(in.Query, out)), out, nil
	})
}

func searchSummary(q string, out searchOut) string {
	var b strings.Builder
	if len(out.Results) == 0 {
		b.WriteString(out.Message)
		if b.Len() == 0 {
			fmt.Fprintf(&b, "No documentation found for %q.", q)
		}
		for _, n := range out.Notes {
			b.WriteString("\n" + n)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "%d results for %q", len(out.Results), q)
	if out.Version != "" {
		fmt.Fprintf(&b, ", version %s", out.Version)
	}
	if out.Language != "en" {
		fmt.Fprintf(&b, " (%s docs)", out.Language)
	}
	b.WriteString(":\n")
	for _, n := range out.Notes {
		b.WriteString(n + "\n")
	}
	if out.Relaxed {
		b.WriteString("No section has every word; these have most of them.\n")
	}
	for i, r := range out.Results {
		head := r.Title
		if r.Heading != "" && r.Heading != r.Title {
			head += " › " + r.Heading
		}
		var tags []string
		for _, t := range []string{r.Product, r.GuideType, r.Version} {
			if t != "" {
				tags = append(tags, t)
			}
		}
		fmt.Fprintf(&b, "%d. %s (%s)\n   %s\n", i+1, head, strings.Join(tags, ", "), r.URL)
		if s := oneLine(r.Snippet, 220); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
		if len(r.AlsoIn) > 0 {
			var ps []string
			for _, a := range r.AlsoIn {
				ps = append(ps, a.Product)
			}
			fmt.Fprintf(&b, "   Same title in: %s\n", strings.Join(ps, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine shortens text to one line of at most n characters.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
