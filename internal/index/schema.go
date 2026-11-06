// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"database/sql"
	"fmt"
	"strings"
)

// A migration moves a shard's schema up one version. Migrations run in order, each in
// its own transaction, and the shard records the version it reached in user_version.
type migration func(tx *sql.Tx, tokenizer string) error

// migrations lists every schema version. Append to it; never change an entry that has
// shipped.
var migrations = []migration{schemaV1, schemaV2}

// schemaV2 adds the vocabulary that spelling suggestions come from: each word in the
// sections' text, as written (not stemmed), with how often it occurs.
func schemaV2(tx *sql.Tx, _ string) error {
	_, err := tx.Exec(`CREATE TABLE vocabulary (
		word TEXT PRIMARY KEY,
		freq INTEGER NOT NULL
	) WITHOUT ROWID`)
	return err
}

// SchemaVersion is the schema version this build writes.
func SchemaVersion() int { return len(migrations) }

func schemaV1(tx *sql.Tx, tokenizer string) error {
	tok := "'" + strings.ReplaceAll(tokenizer, "'", "''") + "'"
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,

		// One row per page. Lists are JSON arrays.
		`CREATE TABLE pages (
			id              INTEGER PRIMARY KEY,
			url             TEXT NOT NULL UNIQUE,
			site            TEXT NOT NULL,
			lang            TEXT NOT NULL,
			title           TEXT NOT NULL,
			description     TEXT NOT NULL DEFAULT '',
			product         TEXT NOT NULL DEFAULT '',
			related         TEXT NOT NULL DEFAULT '[]',
			guide           TEXT NOT NULL DEFAULT '',
			guide_type      TEXT NOT NULL DEFAULT '',
			version         TEXT NOT NULL DEFAULT '',
			product_version TEXT NOT NULL DEFAULT '',
			current_url     TEXT NOT NULL DEFAULT '',
			toc_trail       TEXT NOT NULL DEFAULT '[]',
			roles           TEXT NOT NULL DEFAULT '[]',
			lastmod         TEXT NOT NULL DEFAULT '',
			etag            TEXT NOT NULL DEFAULT '',
			last_modified   TEXT NOT NULL DEFAULT '',
			content_hash    TEXT NOT NULL,
			indexed_at      TEXT NOT NULL
		)`,
		`CREATE INDEX pages_product ON pages(product)`,
		`CREATE INDEX pages_guide ON pages(guide)`,
		`CREATE INDEX pages_current ON pages(current_url)`,

		// Sections, with the page's title and the section's location copied in so the
		// full-text index can weight them.
		`CREATE TABLE sections (
			id       INTEGER PRIMARY KEY,
			page_id  INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			ord      INTEGER NOT NULL,
			level    INTEGER NOT NULL,
			anchor   TEXT NOT NULL DEFAULT '',
			url      TEXT NOT NULL,
			title    TEXT NOT NULL,
			heading  TEXT NOT NULL,
			location TEXT NOT NULL,
			body     TEXT NOT NULL
		)`,
		`CREATE INDEX sections_page ON sections(page_id, ord)`,
		`CREATE VIRTUAL TABLE sections_fts USING fts5(title, heading, location, body,
			content='sections', content_rowid='id', tokenize=` + tok + `)`,
		`CREATE TRIGGER sections_ai AFTER INSERT ON sections BEGIN
			INSERT INTO sections_fts(rowid, title, heading, location, body)
			VALUES (new.id, new.title, new.heading, new.location, new.body);
		END`,
		`CREATE TRIGGER sections_ad AFTER DELETE ON sections BEGIN
			INSERT INTO sections_fts(sections_fts, rowid, title, heading, location, body)
			VALUES ('delete', old.id, old.title, old.heading, old.location, old.body);
		END`,
		`CREATE TRIGGER sections_au AFTER UPDATE ON sections BEGIN
			INSERT INTO sections_fts(sections_fts, rowid, title, heading, location, body)
			VALUES ('delete', old.id, old.title, old.heading, old.location, old.body);
			INSERT INTO sections_fts(rowid, title, heading, location, body)
			VALUES (new.id, new.title, new.heading, new.location, new.body);
		END`,

		// Table rows: settings and requirements.
		`CREATE TABLE settings (
			id            INTEGER PRIMARY KEY,
			page_id       INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			section       INTEGER NOT NULL,
			name          TEXT NOT NULL,
			default_value TEXT NOT NULL DEFAULT '',
			description   TEXT NOT NULL DEFAULT '',
			valid_values  TEXT NOT NULL DEFAULT '',
			required      TEXT NOT NULL DEFAULT '',
			other         TEXT NOT NULL DEFAULT '[]',
			group_name    TEXT NOT NULL DEFAULT '',
			url           TEXT NOT NULL,
			context       TEXT NOT NULL
		)`,
		`CREATE INDEX settings_page ON settings(page_id)`,
		`CREATE VIRTUAL TABLE settings_fts USING fts5(name, description, context,
			content='settings', content_rowid='id', tokenize=` + tok + `)`,
		`CREATE TRIGGER settings_ai AFTER INSERT ON settings BEGIN
			INSERT INTO settings_fts(rowid, name, description, context)
			VALUES (new.id, new.name, new.description, new.context);
		END`,
		`CREATE TRIGGER settings_ad AFTER DELETE ON settings BEGIN
			INSERT INTO settings_fts(settings_fts, rowid, name, description, context)
			VALUES ('delete', old.id, old.name, old.description, old.context);
		END`,
		`CREATE TABLE requirements (
			id         INTEGER PRIMARY KEY,
			page_id    INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			section    INTEGER NOT NULL,
			kind       TEXT NOT NULL,
			fields     TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			url        TEXT NOT NULL,
			context    TEXT NOT NULL
		)`,
		`CREATE INDEX requirements_page ON requirements(page_id)`,
		`CREATE INDEX requirements_kind ON requirements(kind)`,

		// Release notes and glossary.
		`CREATE TABLE releases (
			id      INTEGER PRIMARY KEY,
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			product TEXT NOT NULL DEFAULT '',
			title   TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			date    TEXT NOT NULL DEFAULT '',
			url     TEXT NOT NULL,
			body    TEXT NOT NULL
		)`,
		`CREATE INDEX releases_page ON releases(page_id)`,
		`CREATE INDEX releases_product_date ON releases(product, date)`,
		`CREATE TABLE glossary (
			id         INTEGER PRIMARY KEY,
			page_id    INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			term       TEXT NOT NULL,
			definition TEXT NOT NULL,
			url        TEXT NOT NULL
		)`,
		`CREATE INDEX glossary_page ON glossary(page_id)`,
		`CREATE INDEX glossary_term ON glossary(term COLLATE NOCASE)`,

		// Links between pages, by URL without a fragment.
		`CREATE TABLE links (
			page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			to_url  TEXT NOT NULL,
			PRIMARY KEY (page_id, to_url)
		) WITHOUT ROWID`,
		`CREATE INDEX links_to ON links(to_url)`,

		// Tables of contents, one row per guide, as JSON.
		`CREATE TABLE tocs (
			guide      TEXT PRIMARY KEY,
			root_url   TEXT NOT NULL,
			entries    TEXT NOT NULL,
			fetched_at TEXT NOT NULL
		)`,

		// Words that mean the same in searches, and other names for products and
		// features (former names, abbreviations).
		`CREATE TABLE synonyms (
			term    TEXT NOT NULL COLLATE NOCASE,
			synonym TEXT NOT NULL COLLATE NOCASE,
			PRIMARY KEY (term, synonym)
		) WITHOUT ROWID`,
		`CREATE TABLE aliases (
			alias     TEXT NOT NULL PRIMARY KEY COLLATE NOCASE,
			canonical TEXT NOT NULL,
			kind      TEXT NOT NULL DEFAULT ''
		) WITHOUT ROWID`,

		// What changed in each build.
		`CREATE TABLE changes (
			id       INTEGER PRIMARY KEY,
			url      TEXT NOT NULL,
			change   TEXT NOT NULL CHECK (change IN ('added', 'updated', 'removed')),
			at       TEXT NOT NULL,
			old_hash TEXT NOT NULL DEFAULT '',
			new_hash TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX changes_at ON changes(at)`,
		`CREATE INDEX changes_url ON changes(url)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("schema v1: %w\n%s", err, s)
		}
	}
	return nil
}
