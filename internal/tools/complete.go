// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// maxCompletions is the most values one completion returns, as the protocol allows.
const maxCompletions = 100

// Complete suggests values for prompt arguments and resource template variables:
// products, guides, guide types, versions, and languages. Other arguments get none.
func (e *Env) Complete(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	p := req.Params
	var given map[string]string
	if p.Context != nil {
		given = p.Context.Arguments
	}
	values, err := e.completions(ctx, p.Argument.Name, given)
	if err != nil {
		return nil, err
	}
	matched := matching(values, p.Argument.Value)
	res := &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: matched, Total: len(matched)}}
	if len(matched) > maxCompletions {
		res.Completion.Values, res.Completion.HasMore = matched[:maxCompletions], true
	}
	if res.Completion.Values == nil {
		res.Completion.Values = []string{}
	}
	return res, nil
}

// matching returns the values that start with typed (ignoring case), then the others
// that contain it, keeping their order.
func matching(values []string, typed string) []string {
	typed = strings.ToLower(strings.TrimSpace(typed))
	var starts, contains []string
	for _, v := range values {
		l := strings.ToLower(v)
		switch {
		case strings.HasPrefix(l, typed):
			starts = append(starts, v)
		case strings.Contains(l, typed):
			contains = append(contains, v)
		}
	}
	return append(starts, contains...)
}

func (e *Env) completions(ctx context.Context, name string, given map[string]string) ([]string, error) {
	switch name {
	case "product":
		var out []string
		for _, p := range search.Products {
			out = append(out, p.Name)
		}
		return out, nil
	case "language":
		var out []string
		for _, s := range site.Sites {
			out = append(out, s.Code)
		}
		return out, nil
	case "guide":
		return e.guidePaths(ctx, given["language"])
	case "guide_type":
		guides, err := e.guides(ctx, index.Key{Site: "en"})
		if err != nil {
			return nil, err
		}
		var out []string
		for _, g := range guides {
			if g.GuideType != "" && !slices.Contains(out, g.GuideType) {
				out = append(out, g.GuideType)
			}
		}
		slices.Sort(out)
		return out, nil
	case "version", "from_version", "to_version":
		return e.versions(ctx, given)
	}
	return nil, nil
}

// guides lists a shard's guides, or none when the index doesn't have the shard.
func (e *Env) guides(ctx context.Context, k index.Key) ([]index.GuideInfo, error) {
	if e.ready() != nil || e.Index.Shard(k) == nil {
		return nil, nil
	}
	var out []index.GuideInfo
	err := e.Index.With(k, func(s *index.Shard) error {
		var err error
		out, err = s.Guides(ctx)
		return err
	})
	return out, err
}

// guidePaths lists the guides in a language: current ones, then older versions.
func (e *Env) guidePaths(ctx context.Context, language string) ([]string, error) {
	code := "en"
	if c, ok := search.SiteFor(language); ok {
		code = c
	}
	var out []string
	for _, older := range []bool{false, true} {
		guides, err := e.guides(ctx, index.Key{Site: code, Older: older})
		if err != nil {
			return nil, err
		}
		for _, g := range guides {
			if !slices.Contains(out, g.Path) {
				out = append(out, g.Path)
			}
		}
	}
	return out, nil
}

// versions lists the older versions the index has, newest first: of the guide given, or
// of the product's guides, or of every guide.
func (e *Env) versions(ctx context.Context, given map[string]string) ([]string, error) {
	older, err := e.guides(ctx, index.Key{Site: "en", Older: true})
	if err != nil {
		return nil, err
	}
	var current []string
	switch {
	case given["guide"] != "":
		current = []string{strings.Trim(given["guide"], "/ ")}
	case given["product"] != "":
		product, ok := search.ProductNamed(given["product"])
		if !ok {
			return nil, nil
		}
		guides, err := e.guides(ctx, index.Key{Site: "en"})
		if err != nil {
			return nil, err
		}
		for _, g := range guides {
			if g.Product == product {
				current = append(current, g.Path)
			}
		}
	}
	filtered := given["guide"] != "" || given["product"] != ""
	var out []string
	for _, g := range older {
		if g.Version == "" || slices.Contains(out, g.Version) {
			continue
		}
		if filtered && !slices.ContainsFunc(current, func(c string) bool { return strings.HasPrefix(g.Path, c+"/") }) {
			continue
		}
		out = append(out, g.Version)
	}
	slices.SortFunc(out, func(a, b string) int { return index.CompareVersions(b, a) })
	return out, nil
}
