// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
)

type changesIn struct {
	Since    string `json:"since" jsonschema:"the start of the period: a date (2026-10-01 or 2026-10), or a number of days back (30d)"`
	Product  string `json:"product,omitempty" jsonschema:"only this product, by any of its names"`
	Guide    string `json:"guide,omitempty" jsonschema:"only this guide, by its path, such as nessus or security-center/6_8"`
	Language string `json:"language,omitempty" jsonschema:"the docs' language, as a site code (default en)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many pages to list, from 1 to 100 (default 20)"`
}

type changedPage struct {
	Change   string          `json:"change" jsonschema:"added, updated, or removed"`
	At       string          `json:"at" jsonschema:"when the index found the change"`
	Title    string          `json:"title"`
	Product  string          `json:"product,omitempty"`
	Guide    string          `json:"guide,omitempty"`
	URL      string          `json:"url"`
	Sections []sectionChange `json:"sections,omitempty" jsonschema:"for an update: the sections that changed, were added, or were removed, with the lines that differ"`
}

type datedPage struct {
	LastMod string `json:"lastmod" jsonschema:"the page's last-modified date in the sitemap"`
	Title   string `json:"title"`
	Product string `json:"product,omitempty"`
	URL     string `json:"url"`
}

type changesOut struct {
	Since        string        `json:"since"`
	HistorySince string        `json:"history_since,omitempty" jsonschema:"when this index's change history begins; it only knows changes from then on"`
	Changes      []changedPage `json:"changes" jsonschema:"newest first"`
	Dated        []datedPage   `json:"dated,omitempty" jsonschema:"for the part of the period before the history begins: pages the sitemap dates in it, without what changed"`
	More         bool          `json:"more,omitempty" jsonschema:"there are more; narrow the period, product, or guide, or raise limit"`
	Message      string        `json:"message,omitempty"`
}

func registerChanges(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "whats_changed",
		Description: "List the docs pages that were added, updated, or removed since a date, newest first, with the " +
			"sections each update changed and the lines that differ. Narrow it by product or guide. The index keeps " +
			"a year of changes from the time it was first built; for earlier dates, it lists the pages the sitemap " +
			"dates in the period, without what changed. For a product's release history, use release_notes.",
		Annotations: readOnly("What's changed"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in changesIn) (*mcp.CallToolResult, changesOut, error) {
		if err := env.ready(); err != nil {
			return nil, changesOut{}, err
		}
		since, err := parseSince(in.Since, env.now())
		if err != nil {
			return nil, changesOut{}, err
		}
		site := "en"
		if in.Language != "" {
			l, ok := search.SiteFor(in.Language)
			if !ok {
				return nil, changesOut{}, fmt.Errorf("no documentation in %q", in.Language)
			}
			site = l
		}
		product := ""
		if in.Product != "" {
			p, ok := search.ProductNamed(in.Product)
			if !ok {
				return nil, changesOut{}, fmt.Errorf("no product named %q; list_products lists them", in.Product)
			}
			product = p
		}
		limit := clamp(in.Limit, 20, 100)
		guide := strings.Trim(strings.TrimSpace(in.Guide), "/")
		out := changesOut{Since: since.Format("2006-01-02"), Changes: []changedPage{}}
		q := index.ChangeQuery{Since: since.Format(time.RFC3339), Product: product, Guide: guide, Limit: limit + 1, Sections: true}
		for _, k := range []index.Key{{Site: site}, {Site: site, Older: true}} {
			_, err := env.onShard(k, func(sh *index.Shard) error {
				hs, err := sh.HistorySince(ctx)
				if err != nil {
					return err
				}
				if hs != "" && (out.HistorySince == "" || hs < out.HistorySince) {
					out.HistorySince = hs
				}
				// A shard without a history start was built before history was kept: its
				// records list every page as added when it was built.
				var cs []index.Change
				if hs != "" {
					if cs, err = sh.Changes(ctx, q); err != nil {
						return err
					}
				}
				for _, c := range cs {
					cp := changedPage{Change: c.Change, At: c.At, Title: c.Title, Product: c.Product, Guide: c.Guide, URL: c.URL}
					for _, s := range c.Sections {
						cp.Sections = append(cp.Sections, sectionChange(s))
					}
					out.Changes = append(out.Changes, cp)
				}
				// Before the history begins, the sitemap's dates are all there is.
				if hs == "" || since.Format(time.RFC3339) < hs {
					before := "9999"
					if hs != "" {
						before = hs[:10]
					}
					dated, err := sh.PagesDatedSince(ctx, out.Since, before, index.ChangeQuery{Product: product, Guide: guide, Limit: limit + 1})
					if err != nil {
						return err
					}
					for _, p := range dated {
						out.Dated = append(out.Dated, datedPage{LastMod: p.LastMod, Title: p.Title, Product: p.Product, URL: p.URL})
					}
				}
				return nil
			})
			if err != nil {
				return nil, changesOut{}, err
			}
		}
		sortChanges(out.Changes)
		// A page the history lists doesn't need its sitemap date too.
		listed := map[string]bool{}
		for _, c := range out.Changes {
			listed[c.URL] = true
		}
		out.Dated = slices.DeleteFunc(out.Dated, func(p datedPage) bool { return listed[p.URL] })
		if len(out.Changes) > limit {
			out.Changes, out.More = out.Changes[:limit], true
		}
		if len(out.Dated) > limit {
			out.Dated, out.More = out.Dated[:limit], true
		}
		if len(out.Changes) == 0 && len(out.Dated) == 0 {
			out.Message = fmt.Sprintf("No pages changed since %s.", out.Since)
			if out.HistorySince != "" && out.HistorySince > since.Format(time.RFC3339) {
				out.Message = fmt.Sprintf("No changes found since %s; this index's history begins %s.", out.Since, out.HistorySince[:10])
			}
		}
		return summary("%s", changesSummary(out)), out, nil
	})
}

var daysBack = regexp.MustCompile(`^(\d+)\s*(d|days?)$`)

// parseSince reads the start of a period: a date, a month, or a number of days back.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if m := daysBack.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		d := now.UTC().AddDate(0, 0, -n)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("since takes a date (2026-10-01), a month (2026-10), or a number of days back (30d), not %q", s)
}

// sortChanges puts the current and older versions' changes together, newest first.
func sortChanges(cs []changedPage) {
	slices.SortStableFunc(cs, func(a, b changedPage) int { return strings.Compare(b.At, a.At) })
}

func changesSummary(out changesOut) string {
	var b strings.Builder
	if out.HistorySince == "" {
		b.WriteString("This index has no change history yet; it begins with the next update of the index.")
	} else {
		fmt.Fprintf(&b, "%d pages changed since %s (the index's history begins %s).", len(out.Changes), out.Since, out.HistorySince[:10])
	}
	for _, c := range out.Changes {
		fmt.Fprintf(&b, "\n- %s %s: %s", c.At[:10], c.Change, c.Title)
		if c.Product != "" {
			fmt.Fprintf(&b, " (%s)", c.Product)
		}
		fmt.Fprintf(&b, "\n  %s", c.URL)
		for _, s := range c.Sections {
			fmt.Fprintf(&b, "\n  %s: %s", upperFirstWord(s.Status), s.Heading)
			for _, l := range s.Added[:min(2, len(s.Added))] {
				b.WriteString("\n    + " + l)
			}
			for _, l := range s.Removed[:min(2, len(s.Removed))] {
				b.WriteString("\n    - " + l)
			}
		}
	}
	if len(out.Dated) > 0 {
		fmt.Fprintf(&b, "\n\nBefore the history begins, the sitemap dates %d pages in the period:", len(out.Dated))
		for _, p := range out.Dated {
			fmt.Fprintf(&b, "\n- %s %s\n  %s", p.LastMod, p.Title, p.URL)
		}
	}
	if out.More {
		b.WriteString("\n\nThere are more; narrow the period, product, or guide, or raise limit.")
	}
	if out.Message != "" {
		b.WriteString("\n" + out.Message)
	}
	return b.String()
}
