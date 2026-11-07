// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/extract"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
)

// Settings and requirements are extracted from English column headings, so those tools
// read the English docs.
const extractedNote = "Settings and requirements are read from the English documentation."

// resolveProduct reads a product filter, by any of its names.
func resolveProduct(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	p, ok := search.ProductNamed(name)
	if !ok {
		return "", fmt.Errorf("unknown product %q: list_products lists the products", name)
	}
	return p, nil
}

// productFor is the product a tool should filter on: the one given, or the only one
// the question names. For Tenable Core with one other product ("Tenable Core ports for
// Network Monitor"), it's Tenable Core, with the other product as related: the Tenable
// Core pages for that product.
func productFor(given string, parsed search.Parsed) (product, related string, err error) {
	if given != "" {
		if product, err = resolveProduct(given); err != nil {
			return "", "", err
		}
		if product == "Tenable Core" {
			for _, p := range parsed.Products {
				if p != "Tenable Core" {
					if related != "" {
						return product, "", nil // several: no one of them
					}
					related = p
				}
			}
		}
		return product, related, nil
	}
	if other := search.CoreProduct(parsed.Products); other != "" {
		return "Tenable Core", other, nil
	}
	if len(parsed.Products) == 1 {
		return parsed.Products[0], "", nil
	}
	return "", "", nil
}

// onShard runs fn on the shard for a language's current or older versions, reporting
// false when the index doesn't have it.
func (e *Env) onShard(k index.Key, fn func(*index.Shard) error) (bool, error) {
	if e.Index.Shard(k) == nil {
		return false, nil
	}
	return true, e.Index.With(k, fn)
}

// versioned runs a query for a version, in the shard of older versions, and falls back
// to the current versions when that version has nothing. It returns a note when it
// falls back.
func (e *Env) versioned(site, version string, run func(sh *index.Shard, version string) (int, error)) (string, error) {
	if version != "" {
		found := 0
		ok, err := e.onShard(index.Key{Site: site, Older: true}, func(sh *index.Shard) error {
			n, err := run(sh, version)
			found = n
			return err
		})
		if err != nil {
			return "", err
		}
		if ok && found > 0 {
			return "", nil
		}
	}
	ok, err := e.onShard(index.Key{Site: site}, func(sh *index.Shard) error {
		_, err := run(sh, "")
		return err
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("the index has no %s documentation", site)
	}
	if version != "" {
		return fmt.Sprintf("The documentation for older versions has nothing for version %s, so these are for the current version.", version), nil
	}
	return "", nil
}

type field struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

func fields(fs []extract.Field) []field {
	out := make([]field, 0, len(fs))
	for _, f := range fs {
		out = append(out, field{Label: f.Label, Text: f.Text})
	}
	return out
}

// find_setting

type settingIn struct {
	Query   string `json:"query" jsonschema:"the setting's name or what it does, such as max simultaneous hosts or session timeout, with the product if known"`
	Product string `json:"product,omitempty" jsonschema:"only this product, by any of its names"`
	Version string `json:"version,omitempty" jsonschema:"the product version, such as 6.3 (default: the one the query names, or the current one)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"how many settings to return, from 1 to 20 (default 5)"`
}

type settingOut struct {
	Name        string  `json:"name"`
	Default     string  `json:"default,omitempty"`
	Description string  `json:"description,omitempty"`
	Values      string  `json:"valid_values,omitempty"`
	Required    string  `json:"required,omitempty"`
	Other       []field `json:"other,omitempty" jsonschema:"the table's other columns"`
	Group       string  `json:"group,omitempty" jsonschema:"the subheading the setting comes under in its table"`
	Product     string  `json:"product,omitempty"`
	Version     string  `json:"version,omitempty"`
	Page        string  `json:"page"`
	URL         string  `json:"url" jsonschema:"the section with the setting's table, to cite"`
	Location    string  `json:"location"`
}

type settingsOut struct {
	Settings []settingOut `json:"settings"`
	Product  string       `json:"product,omitempty" jsonschema:"the product filter applied"`
	For      string       `json:"for_product,omitempty" jsonschema:"for Tenable Core, the product it runs that the filter applied"`
	Notes    []string     `json:"notes,omitempty"`
	Message  string       `json:"message,omitempty"`
}

// required_role

type roleIn struct {
	Task     string `json:"task" jsonschema:"what the user wants to do, such as create a scan or add users"`
	Product  string `json:"product,omitempty"`
	Version  string `json:"version,omitempty"`
	Language string `json:"language,omitempty"`
}

type roleStatement struct {
	Label string   `json:"label,omitempty" jsonschema:"what the statement covers, such as Required User Role, sometimes with a product"`
	Text  string   `json:"text"`
	Roles []string `json:"roles,omitempty" jsonschema:"the roles named, when the docs list them"`
}

type roleAnswer struct {
	Page       string          `json:"page"`
	URL        string          `json:"url"`
	Product    string          `json:"product,omitempty"`
	Version    string          `json:"version,omitempty"`
	Language   string          `json:"language"`
	Required   []roleStatement `json:"required"`
	EnglishURL string          `json:"english_url,omitempty"`
}

type rolesOut struct {
	Answers []roleAnswer `json:"answers" jsonschema:"pages about the task that state the roles it needs, best first"`
	Without []link       `json:"closest_pages_without_roles,omitempty" jsonschema:"when no page states a role: the pages that best match the task"`
	Notes   []string     `json:"notes,omitempty"`
	Message string       `json:"message,omitempty"`
}

// requirements

var kinds = []string{extract.Ports, extract.Hardware, extract.OS, extract.Browsers, extract.Dependencies, extract.Other}

type requirementsIn struct {
	Product string `json:"product,omitempty" jsonschema:"the product, by any of its names"`
	Kind    string `json:"kind,omitempty" jsonschema:"ports, hardware, operating-systems, browsers, dependencies, or other (default: all, or what the query asks about)"`
	Query   string `json:"query,omitempty" jsonschema:"words to look for, such as 443, RHEL, or 10,000 hosts; the product and kind can be named here too"`
	Version string `json:"version,omitempty"`
	Limit   int    `json:"limit,omitempty" jsonschema:"how many rows to return, from 1 to 100 (default 30)"`
}

type requirementOut struct {
	Kind     string  `json:"kind"`
	Fields   []field `json:"fields"`
	Group    string  `json:"group,omitempty"`
	Product  string  `json:"product,omitempty"`
	Version  string  `json:"version,omitempty"`
	Page     string  `json:"page"`
	URL      string  `json:"url"`
	Location string  `json:"location"`
}

type requirementsResultOut struct {
	Requirements []requirementOut `json:"requirements"`
	Product      string           `json:"product,omitempty"`
	For          string           `json:"for_product,omitempty" jsonschema:"for Tenable Core, the product it runs that the filter applied"`
	Kind         string           `json:"kind,omitempty"`
	Notes        []string         `json:"notes,omitempty"`
	Message      string           `json:"message,omitempty"`
}

// Words in a requirements question that pick a kind of requirement.
var kindWords = []struct {
	re   *regexp.Regexp
	kind string
}{
	{regexp.MustCompile(`(?i)\b(ports?|firewall|protocols?)\b`), extract.Ports},
	{regexp.MustCompile(`(?i)\b(hardware|cpus?|cores?|memory|ram|disk|storage|sizing)\b`), extract.Hardware},
	{regexp.MustCompile(`(?i)\b(operating systems?|os|platforms?|distributions?|linux|windows|macos)\b`), extract.OS},
	{regexp.MustCompile(`(?i)\bbrowsers?\b`), extract.Browsers},
	{regexp.MustCompile(`(?i)\b(dependency|dependencies|packages?|prerequisites?)\b`), extract.Dependencies},
}

// Words that only say a question is about requirements.
var requirementFiller = map[string]bool{"requirement": true, "requirements": true, "system": true, "minimum": true,
	"supported": true, "support": true, "hardware": true, "ports": true, "port": true, "firewall": true, "browsers": true,
	"browser": true, "dependencies": true, "dependency": true, "prerequisites": true, "os": true, "operating": true,
	"systems": true, "platforms": true, "platform": true, "sizing": true, "specs": true, "need": true, "open": true}

// release_notes

type releaseIn struct {
	Product  string `json:"product,omitempty"`
	Since    string `json:"since,omitempty" jsonschema:"only entries on or after this date, as YYYY-MM-DD, YYYY-MM, or YYYY"`
	Until    string `json:"until,omitempty" jsonschema:"only entries on or before this date, as YYYY-MM-DD, YYYY-MM, or YYYY"`
	Version  string `json:"version,omitempty" jsonschema:"only entries for this version or its patch releases (10.8 matches 10.8.1)"`
	Query    string `json:"query,omitempty" jsonschema:"words the entries must contain, such as a feature or a fixed issue"`
	Language string `json:"language,omitempty" jsonschema:"the release notes' language (default en)"`
	Full     bool   `json:"full,omitempty" jsonschema:"return each entry's full text instead of a summary"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many entries to return, from 1 to 50 (default 10)"`
}

type releaseOut struct {
	Product  string `json:"product,omitempty"`
	Title    string `json:"title"`
	Version  string `json:"version,omitempty"`
	Date     string `json:"date,omitempty"`
	URL      string `json:"url"`
	Summary  string `json:"summary"`
	Markdown string `json:"markdown,omitempty"`
}

type releasesOut struct {
	Entries  []releaseOut `json:"entries" jsonschema:"newest first"`
	Product  string       `json:"product,omitempty"`
	Language string       `json:"language"`
	Message  string       `json:"message,omitempty"`
}

var dateRe = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

// define_term

type termIn struct {
	Term     string `json:"term" jsonschema:"a term, abbreviation, or product name, such as VPR, plugin family, or Tenable.io"`
	Language string `json:"language,omitempty" jsonschema:"also look in this language's glossary (default: English only)"`
}

type definitionOut struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
	URL        string `json:"url"`
}

type productNameOut struct {
	Name       string   `json:"name" jsonschema:"the product's current name"`
	OtherNames []string `json:"other_names" jsonschema:"its other names, including retired ones"`
}

type termOut struct {
	Term        string          `json:"term"`
	Definitions []definitionOut `json:"definitions"`
	Product     *productNameOut `json:"product,omitempty" jsonschema:"set when the term is a product's name, current or retired"`
	Synonyms    []string        `json:"synonyms,omitempty"`
	Message     string          `json:"message,omitempty"`
}

// displayName is a product's name as the docs write it in prose.
func displayName(p string) string {
	if strings.HasPrefix(p, "Tenable") {
		return p
	}
	return "Tenable " + p
}

func registerSpecific(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "find_setting",
		Description: "Look up a product setting: its default, what it does, the values it takes, and whether it's " +
			"required, from the settings tables in the docs. Search by the setting's name or what it does, with the " +
			"product if known. Cite the url it returns.",
		Annotations: readOnly("Find a setting"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in settingIn) (*mcp.CallToolResult, settingsOut, error) {
		if err := env.ready(); err != nil {
			return nil, settingsOut{}, err
		}
		parsed := search.Parse(in.Query)
		if len(parsed.Terms) == 0 {
			return nil, settingsOut{}, fmt.Errorf("query has no words to look for: give a setting's name or what it does")
		}
		product, related, err := productFor(in.Product, parsed)
		if err != nil {
			return nil, settingsOut{}, err
		}
		limit := clamp(in.Limit, 5, 20)
		version := firstNonEmpty(in.Version, parsed.Version)
		out := settingsOut{Settings: []settingOut{}, Product: product, For: related}
		find := func(product, related string) error {
			out.Settings = out.Settings[:0]
			note, err := env.versioned("en", version, func(sh *index.Shard, v string) (int, error) {
				f := index.Filter{Product: product, Related: related, Version: v}
				rows, err := sh.Settings(ctx, index.MatchQuery(strings.Join(parsed.Terms, " ")), f, limit*3)
				if err == nil && len(rows) == 0 && len(parsed.Terms) > 1 {
					// Not every word: settings that match most of them.
					rows, err = sh.Settings(ctx, anyOf(parsed.Terms), f, limit*3)
				}
				defer func() { out.Settings = out.Settings[:min(len(out.Settings), limit)] }()
				// A lookup usually wants the default: rows that give one rank as if they
				// matched better (bm25 scores are negative; lower is better).
				slices.SortStableFunc(rows, func(a, b index.SettingRow) int {
					return cmpScore(withDefault(a), withDefault(b))
				})
				out.Settings = out.Settings[:0]
				for _, r := range rows {
					out.Settings = append(out.Settings, settingOut{Name: r.Name, Default: r.Default, Description: r.Description,
						Values: r.Values, Required: r.Required, Other: fields(r.Other), Group: r.Group, Product: r.Product,
						Version: r.Version, Page: r.Title, URL: r.URL, Location: r.Context})
				}
				return len(rows), err
			})
			if note != "" {
				out.Notes = append(out.Notes, note)
			}
			return err
		}
		if err := find(product, related); err != nil {
			return nil, settingsOut{}, err
		}
		if len(out.Settings) == 0 && related != "" {
			// No Tenable Core setting for that product: any Tenable Core setting.
			if err := find(product, ""); err != nil {
				return nil, settingsOut{}, err
			}
			out.For = ""
		}
		if len(out.Settings) == 0 && product != "" && in.Product == "" {
			// The product came from the question; try without it.
			if err := find("", ""); err != nil {
				return nil, settingsOut{}, err
			}
			if len(out.Settings) > 0 {
				out.Notes = append(out.Notes, fmt.Sprintf("No %s setting matches, so these are from other products.", displayName(product)))
				out.Product = ""
			}
		}
		if search.DetectLanguage(in.Query) != "en" {
			out.Notes = append(out.Notes, extractedNote)
		}
		if len(out.Settings) == 0 {
			out.Message = fmt.Sprintf("No setting matches %q in the settings tables. Try search_docs, which also reads settings described in text.", in.Query)
		}
		var b strings.Builder
		if out.Message != "" {
			b.WriteString(out.Message)
		}
		for _, n := range out.Notes {
			b.WriteString(n + "\n")
		}
		for i, st := range out.Settings {
			fmt.Fprintf(&b, "%d. %s (%s)", i+1, st.Name, joinNonEmpty(", ", st.Product, versionLabel(st.Version)))
			if st.Default != "" {
				fmt.Fprintf(&b, ": default %s", oneLine(st.Default, 120))
			}
			b.WriteString("\n")
			if st.Description != "" {
				fmt.Fprintf(&b, "   %s\n", oneLine(st.Description, 220))
			}
			fmt.Fprintf(&b, "   %s\n", st.URL)
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "required_role",
		Description: "Find who can do a task: the user roles and permissions the docs say it needs, by product. " +
			"Describe the task in the user's words. When the docs don't state a role, it says so and lists the " +
			"closest pages.",
		Annotations: readOnly("Required role"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in roleIn) (*mcp.CallToolResult, rolesOut, error) {
		if err := env.ready(); err != nil {
			return nil, rolesOut{}, err
		}
		if strings.TrimSpace(in.Task) == "" {
			return nil, rolesOut{}, fmt.Errorf("task is empty: describe what the user wants to do")
		}
		product, _, err := productFor(in.Product, search.Parse(in.Task))
		if err != nil {
			return nil, rolesOut{}, err
		}
		res, err := env.Engine.Search(ctx, search.Request{Query: in.Task, Product: product, Version: in.Version, Language: in.Language, Limit: 20})
		if err != nil {
			return nil, rolesOut{}, err
		}
		out := rolesOut{Answers: []roleAnswer{}, Notes: res.Notes}
		seen := map[string]bool{}
		for _, h := range res.Hits {
			if len(h.Roles) == 0 || seen[h.PageURL] || len(out.Answers) == 5 {
				continue
			}
			seen[h.PageURL] = true
			a := roleAnswer{Page: h.Title, URL: h.URL, Product: h.Product, Version: h.Version, Language: h.Language, EnglishURL: h.EnglishURL}
			err := env.withPage(ctx, h.PageURL, func(_ *index.Shard, info *index.PageInfo) error {
				for _, r := range info.Roles {
					a.Required = append(a.Required, roleStatement{Label: r.Label, Text: r.Text, Roles: r.Roles})
				}
				return nil
			})
			if err != nil {
				return nil, rolesOut{}, err
			}
			out.Answers = append(out.Answers, a)
		}
		if len(out.Answers) == 0 {
			out.Message = fmt.Sprintf("The docs don't state a required role for %q on the pages that match it.", in.Task)
			for _, h := range res.Hits[:min(3, len(res.Hits))] {
				out.Without = append(out.Without, link{Title: h.Title, URL: h.URL})
			}
		}
		var b strings.Builder
		if out.Message != "" {
			b.WriteString(out.Message + "\n")
		}
		for _, a := range out.Answers {
			fmt.Fprintf(&b, "%s (%s) %s\n", a.Page, joinNonEmpty(", ", a.Product, versionLabel(a.Version)), a.URL)
			for _, r := range a.Required {
				fmt.Fprintf(&b, "   %s\n", oneLine(joinNonEmpty(": ", r.Label, r.Text), 240))
			}
		}
		for _, l := range out.Without {
			fmt.Fprintf(&b, "- %s %s\n", l.Title, l.URL)
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "requirements",
		Description: "List a product's requirements from the requirements tables in the docs: ports and protocols, " +
			"hardware, operating systems, browsers, and dependencies. Give the product and the kind, or a question " +
			"such as \"Nessus ports\" or \"Security Center hardware for 10,000 hosts\".",
		Annotations: readOnly("Requirements"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in requirementsIn) (*mcp.CallToolResult, requirementsResultOut, error) {
		if err := env.ready(); err != nil {
			return nil, requirementsResultOut{}, err
		}
		parsed := search.Parse(in.Query)
		product, related, err := productFor(in.Product, parsed)
		if err != nil {
			return nil, requirementsResultOut{}, err
		}
		kind := strings.ToLower(strings.TrimSpace(in.Kind))
		if kind != "" && !slices.Contains(kinds, kind) {
			return nil, requirementsResultOut{}, fmt.Errorf("unknown kind %q: use %s", in.Kind, strings.Join(kinds, ", "))
		}
		if kind == "" {
			for _, kw := range kindWords {
				if kw.re.MatchString(in.Query) {
					kind = kw.kind
					break
				}
			}
		}
		var words []string
		for _, t := range parsed.Terms {
			if !requirementFiller[t] && !(kind == extract.OS && (t == "linux" || t == "windows" || t == "macos")) {
				words = append(words, t)
			}
		}
		if product == "" && kind == "" && len(words) == 0 {
			return nil, requirementsResultOut{}, fmt.Errorf("name a product, a kind of requirement, or words to look for")
		}
		limit := clamp(in.Limit, 30, 100)
		version := firstNonEmpty(in.Version, parsed.Version)
		out := requirementsResultOut{Requirements: []requirementOut{}, Product: product, For: related, Kind: kind}
		note, err := env.versioned("en", version, func(sh *index.Shard, v string) (int, error) {
			f := index.Filter{Product: product, Related: related, Version: v}
			rows, err := sh.Requirements(ctx, index.RequirementQuery{Filter: f, Kind: kind, Words: words, Limit: 500})
			if err == nil && len(rows) == 0 && len(words) > 0 && (product != "" || kind != "") {
				// The words narrow the rows only when some rows have them.
				rows, err = sh.Requirements(ctx, index.RequirementQuery{Filter: f, Kind: kind, Limit: 500})
			}
			if err == nil && len(rows) == 0 && related != "" {
				// No Tenable Core rows for that product: any Tenable Core rows.
				f.Related, out.For = "", ""
				rows, err = sh.Requirements(ctx, index.RequirementQuery{Filter: f, Kind: kind, Limit: 500})
			}
			slices.SortStableFunc(rows, func(a, b index.RequirementRow) int { return pageRank(a, kind) - pageRank(b, kind) })
			rows = rows[:min(len(rows), limit)]
			out.Requirements = out.Requirements[:0]
			for _, r := range rows {
				out.Requirements = append(out.Requirements, requirementOut{Kind: r.Kind, Fields: fields(r.Fields), Group: r.Group,
					Product: r.Product, Version: r.Version, Page: r.Title, URL: r.URL, Location: r.Context})
			}
			return len(rows), err
		})
		if err != nil {
			return nil, requirementsResultOut{}, err
		}
		if note != "" {
			out.Notes = append(out.Notes, note)
		}
		if in.Query != "" && search.DetectLanguage(in.Query) != "en" {
			out.Notes = append(out.Notes, extractedNote)
		}
		if len(out.Requirements) == 0 {
			out.Message = "No requirements tables match. Try search_docs, which also reads requirements written as text."
		}
		var b strings.Builder
		if out.Message != "" {
			b.WriteString(out.Message + "\n")
		}
		for _, n := range out.Notes {
			b.WriteString(n + "\n")
		}
		lastURL := ""
		for _, r := range out.Requirements {
			if r.URL != lastURL {
				fmt.Fprintf(&b, "%s (%s, %s) %s\n", r.Location, joinNonEmpty(", ", r.Product, versionLabel(r.Version)), r.Kind, r.URL)
				lastURL = r.URL
			}
			var parts []string
			for _, f := range r.Fields {
				parts = append(parts, joinNonEmpty(": ", f.Label, oneLine(f.Text, 120)))
			}
			fmt.Fprintf(&b, "   - %s\n", strings.Join(parts, "; "))
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "release_notes",
		Description: "List release-note entries, newest first, filtered by product, dates, version, and words they " +
			"contain. Use it for what's new, when something changed, or which release fixed an issue.",
		Annotations: readOnly("Release notes"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in releaseIn) (*mcp.CallToolResult, releasesOut, error) {
		if err := env.ready(); err != nil {
			return nil, releasesOut{}, err
		}
		product, err := resolveProduct(in.Product)
		if err != nil {
			return nil, releasesOut{}, err
		}
		for _, d := range []string{in.Since, in.Until} {
			if d != "" && !dateRe.MatchString(d) {
				return nil, releasesOut{}, fmt.Errorf("date %q: use YYYY-MM-DD, YYYY-MM, or YYYY", d)
			}
		}
		lang := "en"
		if in.Language != "" {
			l, ok := search.SiteFor(in.Language)
			if !ok {
				return nil, releasesOut{}, fmt.Errorf("no documentation in %q", in.Language)
			}
			lang = l
		}
		var words []string
		if in.Query != "" {
			parsed := search.ParseLang(in.Query, lang)
			if product == "" && len(parsed.Products) == 1 {
				product = parsed.Products[0]
			}
			words = append(parsed.Terms, parsed.Short...)
		}
		out := releasesOut{Entries: []releaseOut{}, Product: product, Language: lang}
		ok, err := env.onShard(index.Key{Site: lang}, func(sh *index.Shard) error {
			rows, err := sh.Releases(ctx, index.ReleaseQuery{Product: product, Since: in.Since, Until: in.Until,
				Version: strings.TrimPrefix(strings.ToLower(in.Version), "v"), Words: words, Limit: clamp(in.Limit, 10, 50)})
			for _, r := range rows {
				e := releaseOut{Product: r.Product, Title: r.Title, Version: r.Version, Date: r.Date, URL: r.URL, Summary: releaseSummary(r.Body)}
				if in.Full {
					e.Markdown = r.Body
				}
				out.Entries = append(out.Entries, e)
			}
			return err
		})
		if err != nil {
			return nil, releasesOut{}, err
		}
		if !ok {
			return nil, releasesOut{}, fmt.Errorf("the index has no %s documentation", lang)
		}
		if len(out.Entries) == 0 {
			out.Message = "No release-note entries match."
		}
		var b strings.Builder
		if out.Message != "" {
			b.WriteString(out.Message)
		}
		for _, e := range out.Entries {
			fmt.Fprintf(&b, "- %s %s (%s) %s\n", firstNonEmpty(e.Date, "undated"), e.Title, e.Product, e.URL)
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "define_term",
		Description: "Define a Tenable term or abbreviation from the docs' glossaries, and say what a product name " +
			"refers to, including retired names (Tenable.io is now Tenable Vulnerability Management).",
		Annotations: readOnly("Define a term"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in termIn) (*mcp.CallToolResult, termOut, error) {
		if err := env.ready(); err != nil {
			return nil, termOut{}, err
		}
		term := strings.TrimSpace(in.Term)
		if term == "" {
			return nil, termOut{}, fmt.Errorf("term is empty")
		}
		out := termOut{Term: term, Definitions: []definitionOut{}}
		if p, ok := search.ProductNamed(term); ok {
			pn := &productNameOut{Name: displayName(p)}
			for _, pr := range search.Products {
				if pr.Name == p {
					pn.OtherNames = append(slices.Clone(pr.Aliases), pr.CaseSensitive...)
				}
			}
			out.Product = pn
		}
		sites := []string{"en"}
		if in.Language != "" {
			l, ok := search.SiteFor(in.Language)
			if !ok {
				return nil, termOut{}, fmt.Errorf("no documentation in %q", in.Language)
			}
			if l != "en" {
				sites = append(sites, l)
			}
		}
		for _, site := range sites {
			_, err := env.onShard(index.Key{Site: site}, func(sh *index.Shard) error {
				rows, err := sh.Terms(ctx, term, 5)
				if err != nil {
					return err
				}
				for _, r := range rows {
					out.Definitions = append(out.Definitions, definitionOut{Term: r.Term, Definition: r.Definition, URL: r.URL})
				}
				if site == "en" {
					out.Synonyms, err = sh.SynonymsOf(ctx, term)
				}
				return err
			})
			if err != nil {
				return nil, termOut{}, err
			}
		}
		if len(out.Definitions) == 0 && out.Product == nil {
			out.Message = fmt.Sprintf("The glossaries don't define %q. Try search_docs, which finds terms explained in the guides.", term)
		}
		var b strings.Builder
		if out.Product != nil {
			if strings.EqualFold(out.Product.Name, term) || strings.EqualFold(strings.TrimPrefix(out.Product.Name, "Tenable "), term) {
				fmt.Fprintf(&b, "%s is a Tenable product. Other names: %s.\n", out.Product.Name, strings.Join(out.Product.OtherNames, ", "))
			} else {
				fmt.Fprintf(&b, "%q refers to %s.\n", term, out.Product.Name)
			}
		}
		for _, d := range out.Definitions {
			fmt.Fprintf(&b, "%s: %s\n   %s\n", d.Term, oneLine(d.Definition, 300), d.URL)
		}
		if len(out.Synonyms) > 0 {
			fmt.Fprintf(&b, "Also searched as: %s\n", strings.Join(out.Synonyms, ", "))
		}
		if out.Message != "" {
			b.WriteString(out.Message)
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})
}

// withDefault is a setting's score, made better when it gives a default.
func withDefault(r index.SettingRow) float64 {
	if r.Default != "" {
		return r.Score * 1.5
	}
	return r.Score
}

func cmpScore(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Words in a page title that mark it as the main page for a kind of requirement.
var kindTitles = map[string][]string{
	extract.Ports:        {"port"},
	extract.Hardware:     {"hardware"},
	extract.OS:           {"software", "operating system", "platform"},
	extract.Browsers:     {"browser"},
	extract.Dependencies: {"dependenc", "prerequisite", "software"},
}

// pageRank orders requirement rows' pages: titles naming the kind asked about first,
// then other requirements pages, then the rest.
func pageRank(r index.RequirementRow, kind string) int {
	title := strings.ToLower(r.Title)
	for _, w := range kindTitles[kind] {
		if strings.Contains(title, w) {
			return 0
		}
	}
	if strings.Contains(title, "requirement") {
		return 1
	}
	return 2
}

// anyOf builds an FTS5 query matching any of the words.
func anyOf(words []string) string {
	var q []string
	for _, w := range words {
		q = append(q, index.MatchQuery(w))
	}
	return strings.Join(q, " OR ")
}

// releaseSummary is the start of an entry's text, after its heading.
func releaseSummary(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#") {
		lines = lines[1:]
	}
	return oneLine(strings.Join(lines, " "), 400)
}

func clamp(n, def, most int) int {
	switch {
	case n <= 0:
		return def
	case n > most:
		return most
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinNonEmpty(sep string, vals ...string) string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return strings.Join(out, sep)
}

func versionLabel(v string) string {
	if v == "" {
		return ""
	}
	return "version " + v
}
