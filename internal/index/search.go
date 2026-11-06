// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"strings"
	"unicode"
)

// Column weights for ranking sections: a match in the page's title counts most, then
// the section's heading, its location, and its text.
const (
	weightTitle    = 10.0
	weightHeading  = 6.0
	weightLocation = 3.0
	weightBody     = 1.0
)

// Hit is a section that matches a search.
type Hit struct {
	URL      string
	Title    string
	Heading  string
	Location string
	Snippet  string
	// Score is the match's rank; lower is better.
	Score float64
}

// MatchQuery turns words into an FTS5 query that matches sections containing all of
// them, with each word quoted so FTS5 syntax in the input is read as text.
func MatchQuery(words string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(words, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"'
	}) {
		terms = append(terms, `"`+w+`"`)
	}
	return strings.Join(terms, " ")
}

// SearchSections returns the sections that best match query, an FTS5 query (see
// MatchQuery).
func (s *Shard) SearchSections(ctx context.Context, query string, limit int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.url, s.title, s.heading, s.location,
			snippet(sections_fts, 3, '«', '»', '…', 16),
			bm25(sections_fts, ?, ?, ?, ?) AS score
		FROM sections_fts JOIN sections s ON s.id = sections_fts.rowid
		WHERE sections_fts MATCH ?
		ORDER BY score LIMIT ?`,
		weightTitle, weightHeading, weightLocation, weightBody, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.URL, &h.Title, &h.Heading, &h.Location, &h.Snippet, &h.Score); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// LinksTo returns the pages that link to a page.
func (s *Shard) LinksTo(ctx context.Context, pageURL string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.url FROM links l JOIN pages p ON p.id = l.page_id
		WHERE l.to_url = ? ORDER BY p.url`, pageURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
