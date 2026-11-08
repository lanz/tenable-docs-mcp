// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yosida95/uritemplate/v3"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

// Resource URIs.
const (
	statusURI   = "tenable-docs://status"
	pageTmpl    = "tenable-docs://page/{+doc_id}"
	tocTmpl     = "tenable-docs://guide/{+guide}/toc{?language}"
	resourceMax = 1 << 20 // a page resource is the whole page, up to this many characters
)

var (
	pageTemplate = uritemplate.MustNew(pageTmpl)
	tocTemplate  = uritemplate.MustNew(tocTmpl)
)

// PageURI returns the resource URI of a docs page.
func PageURI(pageURL string) string {
	return "tenable-docs://page/" + strings.TrimPrefix(pageURL, "https://")
}

// TocURI returns the resource URI of a guide's table of contents.
func TocURI(guide, language string) string {
	u := "tenable-docs://guide/" + strings.Trim(guide, "/") + "/toc"
	if language != "" && language != "en" {
		u += "?language=" + language
	}
	return u
}

func registerResources(s *mcp.Server, env *Env) {
	s.AddResource(&mcp.Resource{
		URI: statusURI, Name: "status", Title: "Index status", MIMEType: "application/json",
		Description: "This server's version and what its documentation index holds, as index_status shows it.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		out, _, err := env.status(ctx)
		if err != nil {
			return nil, err
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		return contents(statusURI, "application/json", string(data)), nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: pageTmpl, Name: "page", Title: "Docs page", MIMEType: "text/markdown",
		Description: "A docs page as Markdown, with a header saying where it is. doc_id is the page's address " +
			"without https://, such as docs.tenable.com/nessus/Content/GettingStarted.htm.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		id := pageTemplate.Match(uri).Get("doc_id").String()
		u, err := normalizeURL(id)
		if err != nil || strings.Contains(u, "#") {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		out, err := env.read(ctx, u, 1, resourceMax)
		if err != nil {
			return nil, resourceErr(uri, err)
		}
		return contents(uri, "text/markdown", out.Markdown), nil
	})

	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: tocTmpl, Name: "guide-toc", Title: "Guide table of contents", MIMEType: "text/markdown",
		Description: "A guide's table of contents, with links to its pages. guide is the guide's path on the site, " +
			"such as nessus or security-center/6_8; language is a site code (default en).",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		vals := tocTemplate.Match(uri)
		out, err := env.guideOutline(ctx, vals.Get("guide").String(), vals.Get("language").String())
		if err != nil {
			return nil, resourceErr(uri, err)
		}
		return contents(uri, "text/markdown", tocMarkdown(out)), nil
	})

	RegisterGuides(s, env)
}

// RegisterGuides lists each current English guide's table of contents as a resource,
// so clients can offer them. After the index changes, calling it again adds new
// guides and removes ones that are gone; the server tells clients the list changed.
func RegisterGuides(s *mcp.Server, env *Env) {
	env.guidesMu.Lock()
	defer env.guidesMu.Unlock()
	var guides []index.GuideInfo
	if k := (index.Key{Site: "en"}); env.Index != nil && env.Index.Shard(k) != nil {
		_ = env.Index.With(k, func(sh *index.Shard) error {
			var err error
			guides, err = sh.Guides(context.Background())
			return err
		})
	}
	now := map[string]bool{}
	for _, g := range guides {
		now[TocURI(g.Path, "")] = true
	}
	var gone []string
	for uri := range env.guideURIs {
		if !now[uri] {
			gone = append(gone, uri)
		}
	}
	if len(gone) > 0 {
		s.RemoveResources(gone...)
	}
	env.guideURIs = now
	for _, g := range guides {
		title := strings.TrimSpace(g.Product + " " + g.GuideType)
		if title == "" {
			title = g.Path
		}
		s.AddResource(&mcp.Resource{
			URI: TocURI(g.Path, ""), Name: "toc/" + g.Path, Title: title + ": table of contents", MIMEType: "text/markdown",
			Description: fmt.Sprintf("The table of contents of %s (%s), %d pages.", title, g.Path, g.Pages),
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			out, err := env.guideOutline(ctx, g.Path, "")
			if err != nil {
				return nil, resourceErr(req.Params.URI, err)
			}
			return contents(req.Params.URI, "text/markdown", tocMarkdown(out)), nil
		})
	}
}

func contents(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

// resourceErr reports a page or guide the index doesn't have as not found.
func resourceErr(uri string, err error) error {
	msg := err.Error()
	if strings.Contains(msg, "isn't in the index") || strings.Contains(msg, "no guide") || strings.Contains(msg, "isn't on the docs sites") {
		return mcp.ResourceNotFoundError(uri)
	}
	return err
}

// tocMarkdown renders a guide's table of contents as a nested list of links.
func tocMarkdown(o outlineOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", o.Title)
	if o.URL != "" {
		fmt.Fprintf(&b, "%s\n\n", o.URL)
	}
	for _, it := range o.Items {
		indent := strings.Repeat("  ", max(it.Level-1, 0))
		if it.URL != "" {
			fmt.Fprintf(&b, "%s- [%s](%s)\n", indent, it.Title, it.URL)
		} else {
			fmt.Fprintf(&b, "%s- %s\n", indent, it.Title)
		}
	}
	return b.String()
}
