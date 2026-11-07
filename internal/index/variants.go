// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"database/sql"
	"errors"
)

// PageRef identifies a stored page.
type PageRef struct {
	URL     string
	Title   string
	Version string // the guide version, or "" for the current one
}

// Page returns a stored page's reference, or false if the shard doesn't have it.
func (s *Shard) Page(ctx context.Context, pageURL string) (PageRef, bool, error) {
	r := PageRef{URL: pageURL}
	err := s.db.QueryRowContext(ctx, "SELECT title, version FROM pages WHERE url = ?", pageURL).Scan(&r.Title, &r.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return PageRef{}, false, nil
	}
	return r, err == nil, err
}

// Versions returns the stored pages whose current version is currentURL: in the
// shard of older versions, a page's older versions, newest first.
func (s *Shard) Versions(ctx context.Context, currentURL string) ([]PageRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url, title, version FROM pages WHERE current_url = ? AND url <> ?`, currentURL, currentURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageRef
	for rows.Next() {
		var r PageRef
		if err := rows.Scan(&r.URL, &r.Title, &r.Version); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortVersions(out)
	return out, nil
}

// GuideVersions returns the versions the shard has of a guide, by the guide's current
// path ("nessus"), newest first.
func (s *Shard) GuideVersions(ctx context.Context, currentGuide string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT version FROM pages
		WHERE version <> '' AND (guide LIKE ? ESCAPE '\')`, likePrefix(currentGuide)+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs := make([]PageRef, len(out))
	for i, v := range out {
		refs[i].Version = v
	}
	sortVersions(refs)
	for i := range refs {
		out[i] = refs[i].Version
	}
	return out, nil
}

// sortVersions orders pages newest version first, comparing versions number by number
// (10.12 is newer than 10.9).
func sortVersions(refs []PageRef) {
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && CompareVersions(refs[j].Version, refs[j-1].Version) > 0; j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

// CompareVersions compares two dotted version numbers, number by number: it returns 1
// if a is newer, -1 if b is, and 0 if they're the same.
func CompareVersions(a, b string) int {
	for a != "" || b != "" {
		var x, y int
		x, a = leadingNumber(a)
		y, b = leadingNumber(b)
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func leadingNumber(s string) (int, string) {
	n, i := 0, 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		n = n*10 + int(s[i]-'0')
		i++
	}
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	return n, s[i:]
}
