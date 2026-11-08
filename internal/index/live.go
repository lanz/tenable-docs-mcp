// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"database/sql"
	"errors"
)

// What a crawl of the live sites needs from a shard: each page's dates, to compare
// with the sitemap, and the pages that redirect elsewhere.

// Alias kinds a crawl records: a page's former address, and a sitemap entry that
// isn't a page (it's missing, or isn't HTML), with its sitemap date.
const (
	redirectKind = "redirect"
	skippedKind  = "skipped"
)

// PageDate is a page's dates and validators, as last fetched.
type PageDate struct {
	// LastMod is the page's last-modified date in the sitemap when it was indexed.
	LastMod string
	// ETag and LastModified are the validators from the last fetch.
	ETag, LastModified string
}

// PageDates returns every page's dates, by URL.
func (s *Shard) PageDates(ctx context.Context) (map[string]PageDate, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT url, lastmod, etag, last_modified FROM pages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PageDate{}
	for rows.Next() {
		var u string
		var d PageDate
		if err := rows.Scan(&u, &d.LastMod, &d.ETag, &d.LastModified); err != nil {
			return nil, err
		}
		out[u] = d
	}
	return out, rows.Err()
}

// SetPageDates records a page's sitemap date and validators without rewriting it, as
// after a conditional request found it unchanged. It returns false if the page isn't
// stored.
func (t *Tx) SetPageDates(pageURL string, d PageDate) (bool, error) {
	res, err := t.exec("UPDATE pages SET lastmod = ?, etag = ?, last_modified = ? WHERE url = ?",
		d.LastMod, d.ETag, d.LastModified, pageURL)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PutRedirect records that a page at from has moved to to.
func (t *Tx) PutRedirect(from, to string) error { return t.PutAlias(from, to, redirectKind) }

// PutSkipped records a sitemap entry that isn't a page, with its sitemap date, so a
// crawl doesn't fetch it again until the date changes.
func (t *Tx) PutSkipped(entryURL, lastmod string) error {
	return t.PutAlias(entryURL, lastmod, skippedKind)
}

// ForgetSkipped removes an entry PutSkipped recorded.
func (t *Tx) ForgetSkipped(entryURL string) error {
	_, err := t.exec("DELETE FROM aliases WHERE alias = ? AND kind = ?", entryURL, skippedKind)
	return err
}

// Skipped returns the sitemap entries recorded as not pages, with their sitemap dates.
func (s *Shard) Skipped(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT alias, canonical FROM aliases WHERE kind = ?", skippedKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var u, lastmod string
		if err := rows.Scan(&u, &lastmod); err != nil {
			return nil, err
		}
		out[u] = lastmod
	}
	return out, rows.Err()
}

// Redirect returns the address a page at pageURL moved to, if a crawl found that it
// redirects.
func (s *Shard) Redirect(ctx context.Context, pageURL string) (string, bool, error) {
	var to string
	err := s.db.QueryRowContext(ctx, "SELECT canonical FROM aliases WHERE alias = ? AND kind = ?", pageURL, redirectKind).Scan(&to)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return to, err == nil, err
}
