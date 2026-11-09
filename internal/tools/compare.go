// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/textdiff"
)

type compareIn struct {
	URL         string `json:"url,omitempty" jsonschema:"a docs page, in any version: it's compared with the same page in the other version"`
	Setting     string `json:"setting,omitempty" jsonschema:"a setting's name, to compare instead of a page"`
	Product     string `json:"product,omitempty" jsonschema:"the setting's product, by any of its names (with setting)"`
	FromVersion string `json:"from_version" jsonschema:"the earlier version, such as 6.5"`
	ToVersion   string `json:"to_version,omitempty" jsonschema:"the later version (default: the current one)"`
}

type versionedPage struct {
	Version string `json:"version" jsonschema:"the guide version; current for the current one"`
	URL     string `json:"url"`
	Title   string `json:"title"`
}

type sectionChange struct {
	Heading string `json:"heading"`
	// Status is changed, added (only in the later version), or removed.
	Status  string   `json:"status" jsonschema:"changed, added (only in the later version), or removed (only in the earlier one)"`
	FromURL string   `json:"from_url,omitempty"`
	ToURL   string   `json:"to_url,omitempty"`
	Added   []string `json:"added,omitempty" jsonschema:"lines the later version has and the earlier one doesn't"`
	Removed []string `json:"removed,omitempty" jsonschema:"lines the earlier version has and the later one doesn't"`
	More    int      `json:"more_lines,omitempty" jsonschema:"changed lines left out, to keep the answer short; read both versions for all of them"`
}

type settingChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type compareOut struct {
	From      versionedPage   `json:"from"`
	To        versionedPage   `json:"to"`
	Unchanged int             `json:"unchanged_sections,omitempty"`
	Sections  []sectionChange `json:"sections,omitempty" jsonschema:"the sections that differ, in the later version's order, then the removed ones"`
	Setting   string          `json:"setting,omitempty"`
	Fields    []settingChange `json:"setting_changes,omitempty" jsonschema:"how the setting's row differs"`
	Versions  []string        `json:"versions_available,omitempty" jsonschema:"the versions the docs have, for choosing others"`
	Message   string          `json:"message,omitempty"`
}

func registerCompare(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "compare_versions",
		Description: "Compare a page, or a setting, between two versions of an on-premises product (Security Center, " +
			"Nessus, OT Security, Identity Exposure on-premises, and others with versioned guides). For a page, it " +
			"lists the sections that changed, were added, or were removed, with the lines that differ; for a " +
			"setting, how its default, values, and description changed. Give url (a page in any version) or " +
			"setting, and from_version; to_version defaults to the current version. Cite both urls.",
		Annotations: readOnly("Compare versions"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in compareIn) (*mcp.CallToolResult, compareOut, error) {
		if err := env.ready(); err != nil {
			return nil, compareOut{}, err
		}
		if (in.URL == "") == (in.Setting == "") {
			return nil, compareOut{}, fmt.Errorf("give url (a page) or setting (a setting's name), not both")
		}
		from, to := normVersion(in.FromVersion), normVersion(in.ToVersion)
		if from == "" {
			return nil, compareOut{}, fmt.Errorf("give from_version, the earlier version, such as 6.5")
		}
		if from == to {
			return nil, compareOut{}, fmt.Errorf("from_version and to_version are the same")
		}
		var out compareOut
		var err error
		if in.URL != "" {
			out, err = env.comparePage(ctx, in.URL, from, to)
		} else {
			out, err = env.compareSetting(ctx, in.Setting, in.Product, from, to)
		}
		if err != nil {
			return nil, compareOut{}, err
		}
		return summary("%s", compareSummary(out)), out, nil
	})
}

// normVersion writes a version as the docs' guides do: 6.5, not v6.5 or 6_5; "" and
// "current" mean the current version.
func normVersion(v string) string {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	v = strings.ReplaceAll(v, "_", ".")
	if v == "current" || v == "latest" {
		return ""
	}
	return v
}

func compareLabel(v string) string {
	if v == "" {
		return "current"
	}
	return v
}

// pageVersions returns a page's versions in its language: the current one ("") and
// the older ones the index has, by version.
func (e *Env) pageVersions(ctx context.Context, pageURL string) (map[string]index.PageRef, error) {
	k, ok := index.KeyFor(pageURL)
	if !ok {
		return nil, fmt.Errorf("%s isn't on the docs sites", pageURL)
	}
	current := site.CurrentURL(pageURL)
	out := map[string]index.PageRef{}
	if _, err := e.onShard(index.Key{Site: k.Site}, func(sh *index.Shard) error {
		ref, found, err := sh.Page(ctx, current)
		if found {
			out[""] = ref
		}
		return err
	}); err != nil {
		return nil, err
	}
	if _, err := e.onShard(index.Key{Site: k.Site, Older: true}, func(sh *index.Shard) error {
		refs, err := sh.Versions(ctx, current)
		for _, r := range refs {
			out[r.Version] = r
		}
		return err
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// pickVersion finds a version: exactly, or the newest one it's the start of (10.8 for
// 10.8.1 isn't a match, but 10 finds 10.9).
func pickVersion(available map[string]index.PageRef, want string) (index.PageRef, bool) {
	if r, ok := available[want]; ok {
		return r, true
	}
	var best string
	for v := range available {
		if v != "" && strings.HasPrefix(v, want+".") && (best == "" || versionLess(best, v)) {
			best = v
		}
	}
	if best == "" {
		return index.PageRef{}, false
	}
	return available[best], true
}

// versionLess compares dotted versions numerically.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var x, y int
		fmt.Sscan(as[i], &x)
		fmt.Sscan(bs[i], &y)
		if x != y {
			return x < y
		}
	}
	return len(as) < len(bs)
}

func sortedVersions(available map[string]index.PageRef) []string {
	var vs []string
	for v := range available {
		if v != "" {
			vs = append(vs, v)
		}
	}
	slices.SortFunc(vs, func(a, b string) int {
		if versionLess(a, b) {
			return -1
		}
		return 1
	})
	if _, ok := available[""]; ok {
		vs = append(vs, "current")
	}
	return vs
}

func (e *Env) comparePage(ctx context.Context, raw, from, to string) (compareOut, error) {
	u, err := normalizeURL(raw)
	if err != nil {
		return compareOut{}, err
	}
	pageURL, _, _ := strings.Cut(u, "#")
	available, err := e.pageVersions(ctx, pageURL)
	if err != nil {
		return compareOut{}, err
	}
	out := compareOut{Versions: sortedVersions(available)}
	fromRef, ok := pickVersion(available, from)
	if !ok {
		return compareOut{}, fmt.Errorf("the docs have no version %s of this page; they have: %s", from, strings.Join(out.Versions, ", "))
	}
	toRef, ok := pickVersion(available, to)
	if !ok {
		return compareOut{}, fmt.Errorf("the docs have no %s version of this page; they have: %s", compareLabel(to), strings.Join(out.Versions, ", "))
	}
	out.From = versionedPage{Version: compareLabel(fromRef.Version), URL: fromRef.URL, Title: fromRef.Title}
	out.To = versionedPage{Version: compareLabel(toRef.Version), URL: toRef.URL, Title: toRef.Title}
	before, err := e.sectionsOf(ctx, fromRef.URL)
	if err != nil {
		return compareOut{}, err
	}
	after, err := e.sectionsOf(ctx, toRef.URL)
	if err != nil {
		return compareOut{}, err
	}
	var changes []textdiff.Change
	changes, out.Unchanged = textdiff.Sections(diffable(before), diffable(after))
	for _, c := range changes {
		out.Sections = append(out.Sections, sectionChange(c))
	}
	if len(out.Sections) == 0 {
		out.Message = fmt.Sprintf("The page is the same in %s and %s.", out.From.Version, out.To.Version)
	}
	return out, nil
}

// diffable is a page's sections, as textdiff compares them.
func diffable(secs []index.Section) []textdiff.Section {
	out := make([]textdiff.Section, len(secs))
	for i, s := range secs {
		out[i] = textdiff.Section{Heading: s.Heading, URL: s.URL, Body: s.Body}
	}
	return out
}

func (e *Env) sectionsOf(ctx context.Context, pageURL string) ([]index.Section, error) {
	var secs []index.Section
	err := e.withPage(ctx, pageURL, func(sh *index.Shard, _ *index.PageInfo) error {
		var err error
		secs, err = sh.Sections(ctx, pageURL)
		return err
	})
	return secs, err
}

func (e *Env) compareSetting(ctx context.Context, setting, productName, from, to string) (compareOut, error) {
	parsed := search.Parse(setting)
	if len(parsed.Terms) == 0 {
		return compareOut{}, fmt.Errorf("setting has no words to look for")
	}
	product, related, err := productFor(productName, parsed)
	if err != nil {
		return compareOut{}, err
	}
	want := strings.ToLower(strings.TrimSpace(setting))
	named := func(r index.SettingRow) bool { return strings.ToLower(strings.TrimSpace(r.Name)) == want }
	pageOf := func(r index.SettingRow) string { p, _, _ := strings.Cut(r.URL, "#"); return p }
	// find returns the version's best row: the one named as asked on page, if page is
	// given and has it; otherwise the one named as asked; otherwise the best match.
	find := func(version, page string) (index.SettingRow, bool, error) {
		k := index.Key{Site: "en", Older: version != ""}
		var rows []index.SettingRow
		_, err := e.onShard(k, func(sh *index.Shard) error {
			f := index.Filter{Product: product, Related: related, Version: version}
			var err error
			rows, err = sh.Settings(ctx, index.MatchQuery(strings.Join(parsed.Terms, " ")), f, 20)
			return err
		})
		if err != nil || len(rows) == 0 {
			return index.SettingRow{}, false, err
		}
		if page != "" {
			for _, r := range rows {
				if pageOf(r) == page && named(r) {
					return r, true, nil
				}
			}
		}
		for _, r := range rows {
			if named(r) {
				return r, true, nil
			}
		}
		return rows[0], true, nil
	}
	before, ok, err := find(from, "")
	if err != nil {
		return compareOut{}, err
	}
	if !ok {
		return compareOut{}, fmt.Errorf("no setting matching %q in version %s of the docs; try find_setting with that version", setting, from)
	}
	// The same page in the later version, so the rows compared describe the same thing.
	samePage := ""
	if versions, err := e.pageVersions(ctx, pageOf(before)); err == nil {
		if ref, ok := pickVersion(versions, to); ok {
			samePage = ref.URL
		}
	}
	after, ok, err := find(to, samePage)
	if err != nil {
		return compareOut{}, err
	}
	if !ok {
		return compareOut{}, fmt.Errorf("no setting matching %q in the %s version of the docs; it may have been removed or renamed", setting, compareLabel(to))
	}
	out := compareOut{Setting: after.Name,
		From: versionedPage{Version: compareLabel(from), URL: before.URL, Title: before.Title},
		To:   versionedPage{Version: compareLabel(to), URL: after.URL, Title: after.Title}}
	for _, f := range []struct{ name, a, b string }{
		{"name", before.Name, after.Name},
		{"default", before.Default, after.Default},
		{"values", before.Values, after.Values},
		{"required", before.Required, after.Required},
		{"description", before.Description, after.Description},
	} {
		if textdiff.Comparable(f.a) != textdiff.Comparable(f.b) {
			out.Fields = append(out.Fields, settingChange{Field: f.name, From: f.a, To: f.b})
		}
	}
	if len(out.Fields) == 0 {
		out.Message = fmt.Sprintf("%s is documented the same way in %s and %s.", after.Name, out.From.Version, out.To.Version)
	}
	return out, nil
}

func compareSummary(out compareOut) string {
	var b strings.Builder
	if out.Setting != "" {
		fmt.Fprintf(&b, "%s: %s → %s\n%s\n%s\n", out.Setting, out.From.Version, out.To.Version, out.From.URL, out.To.URL)
		for _, f := range out.Fields {
			fmt.Fprintf(&b, "\n%s:\n- %s: %s\n- %s: %s\n", f.Field, out.From.Version, orNone(f.From), out.To.Version, orNone(f.To))
		}
	} else {
		counts := map[string]int{}
		for _, s := range out.Sections {
			counts[s.Status]++
		}
		sections := "sections"
		if counts["changed"] == 1 {
			sections = "section"
		}
		fmt.Fprintf(&b, "%s: %s → %s: %d %s changed, %d added, %d removed, %d unchanged.\n%s\n%s\n",
			out.To.Title, out.From.Version, out.To.Version, counts["changed"], sections, counts["added"], counts["removed"], out.Unchanged,
			out.From.URL, out.To.URL)
		for _, s := range out.Sections {
			link := s.ToURL
			if link == "" {
				link = s.FromURL
			}
			fmt.Fprintf(&b, "\n%s: %s (%s)\n", upperFirstWord(s.Status), s.Heading, link)
			for _, l := range s.Added {
				b.WriteString("+ " + l + "\n")
			}
			for _, l := range s.Removed {
				b.WriteString("- " + l + "\n")
			}
			if s.More > 0 {
				fmt.Fprintf(&b, "  (%d more changed lines)\n", s.More)
			}
		}
	}
	if out.Message != "" {
		b.WriteString("\n" + out.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func upperFirstWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
