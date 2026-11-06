// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"
)

// Filter narrows a search to pages that match every field that's set.
type Filter struct {
	Product   string
	GuideType string
	// Guide is a guide's path ("nessus", "nessus/10_8"); pages in that guide match.
	Guide string
	// Version is a guide version ("10.8").
	Version string
	// Contains lists text a section must contain, in its title, heading, or body, ignoring
	// case. It's for words too short for the trigram index (two-character Japanese,
	// Chinese, and Korean words), and is slow without a full-text query beside it.
	Contains []string
}

// Candidate is a section that matches a search, with what ranking needs to know about
// it and its page.
type Candidate struct {
	SectionID int64
	PageID    int64
	URL       string
	PageURL   string
	Title     string
	Heading   string
	Level     int
	Location  string
	Snippet   string
	// Score is the full-text rank; lower is better.
	Score     float64
	Product   string
	Related   []string
	GuideType string
	Guide     string
	Version   string
	LastMod   string
	// Requirements is how many requirement rows the page has.
	Requirements int
}

// Candidates returns up to limit sections matching query (an FTS5 query; see
// MatchQuery) and the filter, best first, with their pages' details. With no query but
// f.Contains set, it scans the sections for that text instead, ranking those with it
// in their titles and headings first.
func (s *Shard) Candidates(ctx context.Context, query string, f Filter, limit int) ([]Candidate, error) {
	scan := strings.TrimSpace(query) == ""
	if scan && len(f.Contains) == 0 {
		return nil, nil
	}
	var where []string
	var args []any
	from := `FROM sections_fts JOIN sections s ON s.id = sections_fts.rowid JOIN pages p ON p.id = s.page_id`
	score := `bm25(sections_fts, ?, ?, ?, ?)`
	snippet := `snippet(sections_fts, 3, '«', '»', '…', 24)`
	var scoreArgs []any
	if scan {
		from = `FROM sections s JOIN pages p ON p.id = s.page_id`
		// Each piece of text found in the title or heading counts more than one found in
		// the body; scores are negative, like bm25's, so lower is better.
		var parts []string
		for _, c := range f.Contains {
			parts = append(parts, `(CASE WHEN instr(lower(s.title || ' ' || s.heading), lower(?)) > 0 THEN 3 ELSE 1 END)`)
			scoreArgs = append(scoreArgs, c)
		}
		score = "-(" + strings.Join(parts, " + ") + ")"
		snippet = `substr(s.body, 1, 200)`
	} else {
		scoreArgs = []any{weightTitle, weightHeading, weightLocation, weightBody}
		where = append(where, "sections_fts MATCH ?")
		args = append(args, query)
	}
	for _, c := range f.Contains {
		where = append(where, "instr(lower(s.title || ' ' || s.heading || ' ' || s.body), lower(?)) > 0")
		args = append(args, c)
	}
	if f.Product != "" {
		where = append(where, "p.product = ?")
		args = append(args, f.Product)
	}
	if f.GuideType != "" {
		where = append(where, "p.guide_type = ?")
		args = append(args, f.GuideType)
	}
	if f.Guide != "" {
		where = append(where, "(p.guide = ? OR p.guide LIKE ? ESCAPE '\\')")
		args = append(args, f.Guide, likePrefix(f.Guide)+"/%")
	}
	if f.Version != "" {
		where = append(where, "p.version = ?")
		args = append(args, f.Version)
	}
	args = append(append(scoreArgs, args...), limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, p.id, s.url, p.url, s.title, s.heading, s.level, s.location,
			`+snippet+`, `+score+` AS score,
			p.product, p.related, p.guide_type, p.guide, p.version, p.lastmod,
			(SELECT count(*) FROM requirements r WHERE r.page_id = p.id)
		`+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY score, s.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var related string
		if err := rows.Scan(&c.SectionID, &c.PageID, &c.URL, &c.PageURL, &c.Title, &c.Heading, &c.Level, &c.Location,
			&c.Snippet, &c.Score, &c.Product, &related, &c.GuideType, &c.Guide, &c.Version, &c.LastMod, &c.Requirements); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(related), &c.Related)
		out = append(out, c)
	}
	return out, rows.Err()
}

func likePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ProductCounts returns how many pages each product has.
func (s *Shard) ProductCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT product, count(*) FROM pages GROUP BY product")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// Words splits text into the words the vocabulary holds: runs of letters and digits,
// lowercased, with at least one letter.
func Words(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.ContainsFunc(w, unicode.IsLetter) {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

// RebuildVocabulary recounts the words in every section's heading and text. Words
// seen only once are left out, as likely typos.
func (s *Shard) RebuildVocabulary(ctx context.Context) error {
	counts := map[string]int{}
	rows, err := s.db.QueryContext(ctx, "SELECT title, heading, body FROM sections")
	if err != nil {
		return err
	}
	for rows.Next() {
		var title, heading, body string
		if err := rows.Scan(&title, &heading, &body); err != nil {
			rows.Close()
			return err
		}
		for _, w := range Words(title + " " + heading + " " + body) {
			if len([]rune(w)) <= 40 {
				counts[w]++
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return s.Update(ctx, func(tx *Tx) error {
		if _, err := tx.exec("DELETE FROM vocabulary"); err != nil {
			return err
		}
		for w, n := range counts {
			if n < 2 {
				continue
			}
			if _, err := tx.exec("INSERT INTO vocabulary (word, freq) VALUES (?, ?)", w, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// Vocabulary returns every word in the vocabulary with its frequency.
func (s *Shard) Vocabulary(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT word, freq FROM vocabulary")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var w string
		var n int
		if err := rows.Scan(&w, &n); err != nil {
			return nil, err
		}
		out[w] = n
	}
	return out, rows.Err()
}
