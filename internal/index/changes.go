// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/textdiff"
)

// The change history: what each update added, updated, or removed, and for an update,
// the sections that differ. A shard updated in place (by a crawl, or a page fetched on
// demand) records its own changes as it goes. A shard rebuilt from a snapshot or a
// full read takes the history of the shard it replaces, and records the differences
// between the two as one more update.

// HistoryDays is how long changes are kept.
const HistoryDays = 365

// historyMeta is the meta key for when the shard's history begins.
const historyMeta = "history_since"

// Change is one entry of the change history.
type Change struct {
	ID int64
	// URL is the page's address; Change is added, updated, or removed; At is when
	// (RFC 3339).
	URL, Change, At string
	// OldHash and NewHash identify the page's content before and after.
	OldHash, NewHash string
	// Title, Product, and Guide describe the page, as it was before a removal.
	Title, Product, Guide string
	// Sections are the sections an update changed.
	Sections []textdiff.Change
}

// StartHistory records that the shard's change history begins now, for a shard
// created empty and filled in place, as a crawl does. Records from before, which
// aren't history, are dropped.
func (t *Tx) StartHistory() error {
	if _, err := t.exec("DELETE FROM section_changes"); err != nil {
		return err
	}
	if _, err := t.exec("DELETE FROM changes"); err != nil {
		return err
	}
	return t.SetMeta(historyMeta, now())
}

// HistorySince returns when the shard's change history begins (RFC 3339), or "" when
// it has none.
func (s *Shard) HistorySince(ctx context.Context) (string, error) { return s.Meta(ctx, historyMeta) }

// TakeHistory gives a rebuilt shard, before it's installed, the history of the shard it
// replaces (old, which may be nil), and records the differences between them as
// changes made now. The rebuilt shard's own records, which list every page as added,
// are dropped. Changes older than HistoryDays are left behind.
func (s *Shard) TakeHistory(ctx context.Context, old *Shard) error {
	at := now()
	return s.Update(ctx, func(tx *Tx) error {
		if _, err := tx.exec("DELETE FROM section_changes"); err != nil {
			return err
		}
		if _, err := tx.exec("DELETE FROM changes"); err != nil {
			return err
		}
		since := ""
		if old != nil {
			var err error
			if since, err = old.HistorySince(ctx); err != nil {
				return err
			}
		}
		if old == nil || since == "" {
			// No history to take: it begins now. (A shard written before history was
			// kept lists its whole build as added, which isn't history.)
			return tx.SetMeta(historyMeta, at)
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -HistoryDays).Format(time.RFC3339)
		past, err := old.Changes(ctx, ChangeQuery{Since: cutoff, Sections: true})
		if err != nil {
			return err
		}
		// Oldest first, so the history keeps its order.
		for i := len(past) - 1; i >= 0; i-- {
			if err := tx.recordChange(past[i]); err != nil {
				return err
			}
		}
		if since < cutoff {
			since = cutoff
		}
		if err := tx.SetMeta(historyMeta, since); err != nil {
			return err
		}
		return s.recordDifferences(ctx, tx, old, at)
	})
}

// pageState is what comparing two builds needs of a page.
type pageState struct {
	id                    int64
	hash                  string
	title, product, guide string
}

// recordDifferences records the pages old and the shard tx writes to differ in.
func (s *Shard) recordDifferences(ctx context.Context, tx *Tx, old *Shard, at string) error {
	const query = "SELECT id, url, content_hash, title, product, guide FROM pages"
	before, err := statesOf(ctx, old, nil, query)
	if err != nil {
		return err
	}
	after, err := statesOf(ctx, nil, tx, query)
	if err != nil {
		return err
	}
	for url, b := range before {
		if _, ok := after[url]; !ok {
			if err := tx.recordChange(Change{URL: url, Change: "removed", At: at, OldHash: b.hash,
				Title: b.title, Product: b.product, Guide: b.guide}); err != nil {
				return err
			}
		}
	}
	for url, a := range after {
		b, ok := before[url]
		switch {
		case !ok:
			if err := tx.recordChange(Change{URL: url, Change: "added", At: at, NewHash: a.hash,
				Title: a.title, Product: a.product, Guide: a.guide}); err != nil {
				return err
			}
		case b.hash != a.hash:
			oldSecs, err := sectionsIn(ctx, old, nil, b.id)
			if err != nil {
				return err
			}
			newSecs, err := sectionsIn(ctx, nil, tx, a.id)
			if err != nil {
				return err
			}
			secs, _ := textdiff.Sections(oldSecs, newSecs)
			if err := tx.recordChange(Change{URL: url, Change: "updated", At: at, OldHash: b.hash, NewHash: a.hash,
				Title: a.title, Product: a.product, Guide: a.guide, Sections: secs}); err != nil {
				return err
			}
		}
	}
	return nil
}

// statesOf reads pages from a shard, or from a transaction's shard.
func statesOf(ctx context.Context, sh *Shard, tx *Tx, query string) (map[string]pageState, error) {
	var rows interface {
		Next() bool
		Scan(...any) error
		Err() error
		Close() error
	}
	var err error
	if sh != nil {
		rows, err = sh.db.QueryContext(ctx, query)
	} else {
		rows, err = tx.tx.QueryContext(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]pageState{}
	for rows.Next() {
		var url string
		var p pageState
		if err := rows.Scan(&p.id, &url, &p.hash, &p.title, &p.product, &p.guide); err != nil {
			return nil, err
		}
		out[url] = p
	}
	return out, rows.Err()
}

func sectionsIn(ctx context.Context, sh *Shard, tx *Tx, pageID int64) ([]textdiff.Section, error) {
	if tx != nil {
		return tx.sectionsOf(pageID)
	}
	rows, err := sh.db.QueryContext(ctx, "SELECT heading, url, body FROM sections WHERE page_id = ? ORDER BY ord", pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []textdiff.Section
	for rows.Next() {
		var s textdiff.Section
		if err := rows.Scan(&s.Heading, &s.URL, &s.Body); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ChangeQuery picks changes to list.
type ChangeQuery struct {
	// Since is the earliest change to list (RFC 3339, or a date).
	Since string
	// Product and Guide narrow the list: a product's pages, and a guide's (a path such
	// as nessus or security-center/6_5; its subguides match too).
	Product, Guide string
	// Limit caps how many are listed; 0 means no limit.
	Limit int
	// Sections includes each update's changed sections.
	Sections bool
}

// Changes lists changes, newest first.
func (s *Shard) Changes(ctx context.Context, q ChangeQuery) ([]Change, error) {
	query := "SELECT id, url, change, at, old_hash, new_hash, title, product, guide FROM changes WHERE at >= ?"
	args := []any{q.Since}
	if q.Product != "" {
		query += " AND product = ?"
		args = append(args, q.Product)
	}
	if q.Guide != "" {
		query += ` AND (guide = ? OR guide LIKE ? ESCAPE '\')`
		args = append(args, q.Guide, likePrefix(q.Guide)+"/%")
	}
	query += " ORDER BY at DESC, id DESC"
	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.URL, &c.Change, &c.At, &c.OldHash, &c.NewHash, &c.Title, &c.Product, &c.Guide); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil || !q.Sections {
		return out, err
	}
	for i := range out {
		if out[i].Change != "updated" {
			continue
		}
		secs, err := s.sectionChanges(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Sections = secs
	}
	return out, nil
}

func (s *Shard) sectionChanges(ctx context.Context, changeID int64) ([]textdiff.Change, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT heading, status, url, added, removed, more FROM section_changes
		WHERE change_id = ? ORDER BY ord`, changeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []textdiff.Change
	for rows.Next() {
		var c textdiff.Change
		var url, added, removed string
		if err := rows.Scan(&c.Heading, &c.Status, &url, &added, &removed, &c.More); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(added), &c.Added)
		_ = json.Unmarshal([]byte(removed), &c.Removed)
		if c.Status == "removed" {
			c.FromURL = url
		} else {
			c.ToURL = url
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DatedPage is a page whose sitemap date is on or after a date.
type DatedPage struct {
	URL, Title, Product, Guide, LastMod string
}

// PagesDatedSince lists pages whose sitemap date is on or after since (YYYY-MM-DD),
// newest first, for the time before the change history begins.
func (s *Shard) PagesDatedSince(ctx context.Context, since, before string, q ChangeQuery) ([]DatedPage, error) {
	query := "SELECT url, title, product, guide, lastmod FROM pages WHERE lastmod >= ? AND lastmod < ?"
	args := []any{since, before}
	if q.Product != "" {
		query += " AND product = ?"
		args = append(args, q.Product)
	}
	if q.Guide != "" {
		query += ` AND (guide = ? OR guide LIKE ? ESCAPE '\')`
		args = append(args, q.Guide, likePrefix(q.Guide)+"/%")
	}
	query += " ORDER BY lastmod DESC, url"
	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatedPage
	for rows.Next() {
		var p DatedPage
		if err := rows.Scan(&p.URL, &p.Title, &p.Product, &p.Guide, &p.LastMod); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
