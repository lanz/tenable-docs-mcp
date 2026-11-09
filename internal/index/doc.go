// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/extract"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/textdiff"
)

// Doc is one page ready to store: the parsed page, what the sitemap says about it, and
// what was extracted from it.
type Doc struct {
	Page  *page.Page
	Site  site.Site
	Entry site.Entry
	Guide site.Guide
	// Product and Related come from page.Products.
	Product string
	Related []string
	// TocTrail is the page's place in its guide's table of contents.
	TocTrail []string
	// ETag and LastModified are the page's validators, for conditional requests.
	ETag, LastModified string

	Settings     []extract.Setting
	Requirements []extract.Requirement
	Releases     []extract.ReleaseEntry
	Terms        []extract.Term
	Links        []string
}

// NewDoc makes a Doc from a parsed page, running the extractors.
func NewDoc(p *page.Page, s site.Site, e site.Entry, trail []string) Doc {
	g, _ := site.GuideOf(p.URL)
	product, related := page.Products(e.Products, g.Path)
	d := Doc{
		Page: p, Site: s, Entry: e, Guide: g, Product: product, Related: related, TocTrail: trail,
		Settings: extract.Settings(p), Requirements: extract.Requirements(p),
		Terms: extract.Glossary(p), Links: extract.LinksOut(p),
	}
	if extract.IsReleaseNotes(p.URL) {
		d.Releases = extract.ReleaseNotes(p)
	}
	return d
}

// hash identifies a Doc's content, so an unchanged page isn't rewritten.
func (d Doc) hash() string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode([]any{d.Page, d.Product, d.Related, d.TocTrail, d.Entry.GuideType, d.Entry.LastMod})
	return hex.EncodeToString(h.Sum(nil))
}

// location describes where a section is, for searching and for showing: the product,
// the guide type, the page's place in the table of contents, and the headings above
// the section on its page.
func (d Doc) location(s page.Section) string {
	var parts []string
	add := func(v ...string) {
		for _, x := range v {
			if x = strings.TrimSpace(x); x != "" && (len(parts) == 0 || parts[len(parts)-1] != x) {
				parts = append(parts, x)
			}
		}
	}
	add(d.Product, d.Entry.GuideType)
	add(d.TocTrail...)
	add(s.Trail...)
	return strings.Join(parts, " › ")
}

// Tx is a write transaction on a shard.
type Tx struct {
	ctx   context.Context
	tx    *sql.Tx
	shard *Shard
	stmts map[string]*sql.Stmt
	// recorded is set when the transaction adds to the change history.
	recorded bool
}

func (t *Tx) close() {
	for _, s := range t.stmts {
		s.Close()
	}
}

// exec runs a statement, preparing it once per transaction.
func (t *Tx) exec(query string, args ...any) (sql.Result, error) {
	if t.stmts == nil {
		t.stmts = map[string]*sql.Stmt{}
	}
	st, ok := t.stmts[query]
	if !ok {
		var err error
		if st, err = t.tx.PrepareContext(t.ctx, query); err != nil {
			return nil, err
		}
		t.stmts[query] = st
	}
	return st.ExecContext(t.ctx, args...)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func toJSON(v any) string {
	if v == nil {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}

// Put stores a page, replacing what was stored for its URL. It returns false, and
// writes nothing, when the page hasn't changed.
func (t *Tx) Put(d Doc) (changed bool, err error) {
	p := d.Page
	if p == nil || p.URL == "" {
		return false, errors.New("put: no page")
	}
	newHash := d.hash()
	var oldID int64
	var oldHash string
	var oldSections []textdiff.Section
	err = t.tx.QueryRowContext(t.ctx, "SELECT id, content_hash FROM pages WHERE url = ?", p.URL).Scan(&oldID, &oldHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	case oldHash == newHash:
		// Only the validators may have changed.
		_, err = t.exec("UPDATE pages SET etag = ?, last_modified = ? WHERE id = ?", d.ETag, d.LastModified, oldID)
		return false, err
	default:
		// The page's sections as they were, to record what the update changes.
		if oldSections, err = t.sectionsOf(oldID); err != nil {
			return false, err
		}
		if _, err := t.exec("DELETE FROM pages WHERE id = ?", oldID); err != nil {
			return false, err
		}
	}
	change := "added"
	if oldHash != "" {
		change = "updated"
	}

	res, err := t.exec(`INSERT INTO pages (url, site, lang, title, description, product, related, guide,
		guide_type, version, product_version, current_url, toc_trail, roles, lastmod, etag, last_modified,
		content_hash, indexed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.URL, d.Site.Code, d.Site.Lang, p.Title, p.Description, d.Product, toJSON(d.Related), d.Guide.Path,
		d.Entry.GuideType, d.Guide.Version, p.ProductVersion, site.CurrentURL(p.URL), toJSON(d.TocTrail),
		toJSON(p.Requirements), d.Entry.LastMod, d.ETag, d.LastModified, newHash, now())
	if err != nil {
		return false, fmt.Errorf("put %s: %w", p.URL, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, err
	}
	sectionIDs := make([]int64, len(p.Sections))
	for i, s := range p.Sections {
		r, err := t.exec(`INSERT INTO sections (page_id, ord, level, anchor, url, title, heading, location, body)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, i, s.Level, s.Anchor, s.URL, p.Title, s.Heading, d.location(s), s.Markdown)
		if err != nil {
			return false, fmt.Errorf("put %s: section %d: %w", p.URL, i, err)
		}
		sectionIDs[i], _ = r.LastInsertId()
	}
	for _, s := range d.Settings {
		if _, err := t.exec(`INSERT INTO settings (page_id, section, name, default_value, description, valid_values,
			required, other, group_name, url, context) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, s.Section, s.Name, s.Default, s.Description, s.Values, s.Required, toJSON(s.Other), s.Group, s.URL, s.Context); err != nil {
			return false, fmt.Errorf("put %s: setting: %w", p.URL, err)
		}
	}
	for _, r := range d.Requirements {
		if _, err := t.exec(`INSERT INTO requirements (page_id, section, kind, fields, group_name, url, context)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, r.Section, r.Kind, toJSON(r.Fields), r.Group, r.URL, r.Context); err != nil {
			return false, fmt.Errorf("put %s: requirement: %w", p.URL, err)
		}
	}
	for _, e := range d.Releases {
		if _, err := t.exec(`INSERT INTO releases (page_id, product, title, version, date, url, body)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, d.Product, e.Title, e.Version, e.Date, e.URL, e.Markdown); err != nil {
			return false, fmt.Errorf("put %s: release: %w", p.URL, err)
		}
	}
	for _, g := range d.Terms {
		if _, err := t.exec(`INSERT INTO glossary (page_id, term, definition, url) VALUES (?, ?, ?, ?)`,
			id, g.Term, g.Definition, g.URL); err != nil {
			return false, fmt.Errorf("put %s: term: %w", p.URL, err)
		}
	}
	for _, l := range d.Links {
		if _, err := t.exec(`INSERT OR IGNORE INTO links (page_id, to_url) VALUES (?, ?)`, id, l); err != nil {
			return false, fmt.Errorf("put %s: link: %w", p.URL, err)
		}
	}
	var sectionChanges []textdiff.Change
	if change == "updated" {
		newSections := make([]textdiff.Section, len(p.Sections))
		for i, s := range p.Sections {
			newSections[i] = textdiff.Section{Heading: s.Heading, URL: s.URL, Body: s.Markdown}
		}
		sectionChanges, _ = textdiff.Sections(oldSections, newSections)
	}
	if err := t.recordChange(Change{URL: p.URL, Change: change, At: now(), OldHash: oldHash, NewHash: newHash,
		Title: p.Title, Product: d.Product, Guide: d.Guide.Path, Sections: sectionChanges}); err != nil {
		return false, err
	}
	return true, nil
}

// sectionsOf returns a stored page's sections, for comparing.
func (t *Tx) sectionsOf(pageID int64) ([]textdiff.Section, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT heading, url, body FROM sections WHERE page_id = ? ORDER BY ord", pageID)
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

// recordChange adds a change, with its sections, to the change history.
func (t *Tx) recordChange(c Change) error {
	t.recorded = true
	res, err := t.exec(`INSERT INTO changes (url, change, at, old_hash, new_hash, title, product, guide) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.URL, c.Change, c.At, c.OldHash, c.NewHash, c.Title, c.Product, c.Guide)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	for i, s := range c.Sections {
		url := s.ToURL
		if url == "" {
			url = s.FromURL
		}
		if _, err := t.exec(`INSERT INTO section_changes (change_id, ord, heading, status, url, added, removed, more)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, i, s.Heading, s.Status, url, toJSON(s.Added), toJSON(s.Removed), s.More); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes a page and everything extracted from it. It returns false if the page
// wasn't stored.
func (t *Tx) Remove(pageURL string) (bool, error) {
	c := Change{URL: pageURL, Change: "removed", At: now()}
	err := t.tx.QueryRowContext(t.ctx, "SELECT content_hash, title, product, guide FROM pages WHERE url = ?", pageURL).
		Scan(&c.OldHash, &c.Title, &c.Product, &c.Guide)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if _, err := t.exec("DELETE FROM pages WHERE url = ?", pageURL); err != nil {
		return false, err
	}
	err = t.recordChange(c)
	return err == nil, err
}

// PutToc stores a guide's table of contents, and its name. A guide without a name keeps
// the one it has: a crawl of the live sites has no names, and shouldn't lose a
// published index's.
func (t *Tx) PutToc(g site.Guide, toc []*site.TocEntry) error {
	b, err := json.Marshal(toc)
	if err != nil {
		return err
	}
	_, err = t.exec(`INSERT INTO tocs (guide, root_url, entries, fetched_at, name) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (guide) DO UPDATE SET root_url = excluded.root_url, entries = excluded.entries, fetched_at = excluded.fetched_at,
			name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE tocs.name END`,
		g.Path, g.RootURL, string(b), now(), g.Name)
	return err
}

// SetMeta sets a metadata value.
func (t *Tx) SetMeta(key, value string) error {
	_, err := t.exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// PutSynonyms records words that mean the same as term in searches.
func (t *Tx) PutSynonyms(term string, synonyms ...string) error {
	for _, s := range synonyms {
		if _, err := t.exec(`INSERT OR IGNORE INTO synonyms (term, synonym) VALUES (?, ?)`, term, s); err != nil {
			return err
		}
	}
	return nil
}

// PutAlias records another name for something (a former product name, say).
func (t *Tx) PutAlias(alias, canonical, kind string) error {
	_, err := t.exec(`INSERT INTO aliases (alias, canonical, kind) VALUES (?, ?, ?)
		ON CONFLICT (alias) DO UPDATE SET canonical = excluded.canonical, kind = excluded.kind`, alias, canonical, kind)
	return err
}
