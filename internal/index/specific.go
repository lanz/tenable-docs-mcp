// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/extract"
)

// SettingRow is a stored setting with its page's details.
type SettingRow struct {
	Name        string
	Default     string
	Description string
	Values      string
	Required    string
	Other       []extract.Field
	Group       string
	URL         string
	Context     string
	Title       string
	Product     string
	Version     string
	GuideType   string
	// Score is the full-text rank; lower is better.
	Score float64
}

// Settings finds settings matching query (an FTS5 query; see MatchQuery), with a match
// in the setting's name counting most.
func (s *Shard) Settings(ctx context.Context, query string, f Filter, limit int) ([]SettingRow, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	where, args := pageFilter(f)
	rows, err := s.db.QueryContext(ctx, `
		SELECT st.name, st.default_value, st.description, st.valid_values, st.required, st.other, st.group_name,
			st.url, st.context, p.title, p.product, p.version, p.guide_type, bm25(settings_fts, 10.0, 2.0, 1.0) AS score
		FROM settings_fts JOIN settings st ON st.id = settings_fts.rowid JOIN pages p ON p.id = st.page_id
		WHERE settings_fts MATCH ?`+where+`
		ORDER BY score LIMIT ?`, append(append([]any{query}, args...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettingRow
	for rows.Next() {
		var r SettingRow
		var other string
		if err := rows.Scan(&r.Name, &r.Default, &r.Description, &r.Values, &r.Required, &other, &r.Group,
			&r.URL, &r.Context, &r.Title, &r.Product, &r.Version, &r.GuideType, &r.Score); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(other), &r.Other)
		out = append(out, r)
	}
	return out, rows.Err()
}

// pageFilter turns a Filter's page fields into SQL conditions on pages p.
func pageFilter(f Filter) (string, []any) {
	var b strings.Builder
	var args []any
	if f.Product != "" {
		b.WriteString(" AND p.product = ?")
		args = append(args, f.Product)
	}
	if f.GuideType != "" {
		b.WriteString(" AND p.guide_type = ?")
		args = append(args, f.GuideType)
	}
	if f.Guide != "" {
		b.WriteString(" AND (p.guide = ? OR p.guide LIKE ? ESCAPE '\\')")
		args = append(args, f.Guide, likePrefix(f.Guide)+"/%")
	}
	if f.Version != "" {
		b.WriteString(" AND p.version = ?")
		args = append(args, f.Version)
	}
	if f.Related != "" {
		b.WriteString(" AND EXISTS (SELECT 1 FROM json_each(p.related) WHERE value = ?)")
		args = append(args, f.Related)
	}
	return b.String(), args
}

// textFilter adds a condition per word: the text must contain it, ignoring case.
func textFilter(expr string, words []string) (string, []any) {
	var b strings.Builder
	var args []any
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			b.WriteString(" AND instr(lower(" + expr + "), lower(?)) > 0")
			args = append(args, w)
		}
	}
	return b.String(), args
}

// RequirementRow is a stored requirement with its page's details.
type RequirementRow struct {
	Kind    string
	Fields  []extract.Field
	Group   string
	URL     string
	Context string
	Title   string
	Product string
	Version string
}

// RequirementQuery selects requirements.
type RequirementQuery struct {
	Filter
	// Kind is one of extract's kinds (ports, hardware, …), or empty for all.
	Kind string
	// Words must all appear in the row or its context.
	Words []string
	Limit int
}

// Requirements returns requirement rows, those on pages titled as requirements pages
// first, in page order.
func (s *Shard) Requirements(ctx context.Context, q RequirementQuery) ([]RequirementRow, error) {
	where, args := pageFilter(q.Filter)
	if q.Kind != "" {
		where += " AND r.kind = ?"
		args = append(args, q.Kind)
	}
	tw, targs := textFilter("r.fields || ' ' || r.context || ' ' || r.group_name", q.Words)
	where += tw
	args = append(args, targs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.kind, r.fields, r.group_name, r.url, r.context, p.title, p.product, p.version
		FROM requirements r JOIN pages p ON p.id = r.page_id
		WHERE 1 = 1`+where+`
		ORDER BY (instr(lower(p.title), 'requirement') = 0), p.url, r.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequirementRow
	for rows.Next() {
		var r RequirementRow
		var fields string
		if err := rows.Scan(&r.Kind, &fields, &r.Group, &r.URL, &r.Context, &r.Title, &r.Product, &r.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fields), &r.Fields)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReleaseRow is a stored release-note entry.
type ReleaseRow struct {
	Product string
	Title   string
	Version string
	Date    string
	URL     string
	Body    string
}

// ReleaseQuery selects release-note entries.
type ReleaseQuery struct {
	Product string
	// Since and Until bound the entries' dates, as YYYY-MM-DD (or a prefix of one);
	// entries with only a month compare by their month.
	Since, Until string
	// Version matches an entry's version exactly or as a prefix (10.8 matches 10.8.1).
	Version string
	Words   []string
	Limit   int
}

// Releases returns release-note entries, newest first.
func (s *Shard) Releases(ctx context.Context, q ReleaseQuery) ([]ReleaseRow, error) {
	where := ""
	var args []any
	if q.Product != "" {
		where += " AND r.product = ?"
		args = append(args, q.Product)
	}
	if q.Since != "" {
		where += " AND r.date <> '' AND r.date >= substr(?, 1, length(r.date))"
		args = append(args, q.Since)
	}
	if q.Until != "" {
		where += " AND r.date <> '' AND r.date <= substr(? || '-99-99', 1, length(r.date))"
		args = append(args, q.Until)
	}
	if q.Version != "" {
		where += " AND (r.version = ? OR r.version LIKE ? ESCAPE '\\')"
		args = append(args, q.Version, likePrefix(q.Version)+".%")
	}
	tw, targs := textFilter("r.title || ' ' || r.body", q.Words)
	where += tw
	args = append(args, targs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.product, r.title, r.version, r.date, r.url, r.body FROM releases r
		WHERE 1 = 1`+where+` ORDER BY r.date DESC, r.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReleaseRow
	for rows.Next() {
		var r ReleaseRow
		if err := rows.Scan(&r.Product, &r.Title, &r.Version, &r.Date, &r.URL, &r.Body); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TermRow is a stored glossary term.
type TermRow struct {
	Term       string
	Definition string
	URL        string
}

// Terms finds glossary terms for a word or phrase: an exact match first, then an
// abbreviation in parentheses (VPR in "Vulnerability Priority Rating (VPR)"), then terms
// starting with it, then terms containing it. An abbreviation (two to six capital
// letters) also matches terms by their initials (NVD finds National Vulnerability
// Database).
func (s *Shard) Terms(ctx context.Context, term string, limit int) ([]TermRow, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.termsContaining(ctx, term, limit)
	if err != nil || !isAbbreviation(term) {
		return rows, err
	}
	all, err := s.termsContaining(ctx, "", 100000)
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if len(rows) >= limit {
			break
		}
		if strings.EqualFold(initials(r.Term), term) && !slices.ContainsFunc(rows, func(x TermRow) bool { return x.URL == r.URL && x.Term == r.Term }) {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

func isAbbreviation(s string) bool {
	if len(s) < 2 || len(s) > 6 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// initials are the first letters of a term's words, leaving out small words and any
// part in parentheses.
func initials(term string) string {
	if i := strings.IndexByte(term, '('); i >= 0 {
		term = term[:i]
	}
	var b strings.Builder
	for _, w := range strings.FieldsFunc(term, func(r rune) bool { return r == ' ' || r == '-' || r == '/' }) {
		switch strings.ToLower(w) {
		case "of", "the", "and", "for", "a", "an", "in", "on", "to":
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
	}
	return b.String()
}

func (s *Shard) termsContaining(ctx context.Context, term string, limit int) ([]TermRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT term, definition, url FROM glossary
		WHERE instr(lower(term), lower(?1)) > 0
		ORDER BY CASE
			WHEN lower(term) = lower(?1) THEN 0
			WHEN instr(lower(term), '(' || lower(?1) || ')') > 0 THEN 1
			WHEN lower(term) LIKE lower(?2) || '%' ESCAPE '\' THEN 2
			ELSE 3 END, (instr(url, '/glossary/') = 0), length(term), term
		LIMIT ?3`, term, likePrefix(term), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TermRow
	for rows.Next() {
		var r TermRow
		if err := rows.Scan(&r.Term, &r.Definition, &r.URL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SynonymsOf returns the words recorded as meaning the same as a term, either way.
func (s *Shard) SynonymsOf(ctx context.Context, term string) ([]string, error) {
	return s.strings(ctx, `SELECT synonym FROM synonyms WHERE term = ?1 COLLATE NOCASE
		UNION SELECT term FROM synonyms WHERE synonym = ?1 COLLATE NOCASE ORDER BY 1`, term)
}
