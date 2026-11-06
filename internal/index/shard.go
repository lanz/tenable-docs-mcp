// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package index stores the documentation index in SQLite, with full-text search
// tuned to each language.
//
// The index is split into shards: one per site (language), and within each, one for
// the current versions of the guides and one for older versions. Each shard is a
// single SQLite file that can be built on its own and swapped in without touching the
// others.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver, as "sqlite"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Key identifies a shard.
type Key struct {
	// Site is the site code ("en", "jp", …; see site.Sites).
	Site string
	// Older is true for the shard of older guide versions.
	Older bool
}

func (k Key) String() string {
	if k.Older {
		return k.Site + "-older"
	}
	return k.Site + "-current"
}

// FileName returns the name of the shard's file.
func (k Key) FileName() string { return k.String() + ".db" }

// Keys returns every shard's key, in the order of site.Sites, current before older.
func Keys() []Key {
	var keys []Key
	for _, s := range site.Sites {
		keys = append(keys, Key{Site: s.Code}, Key{Site: s.Code, Older: true})
	}
	return keys
}

// Tokenizer returns the full-text tokenizer for a site's language: Porter stemming for
// English, words with diacritics folded for French, German, and Spanish, and trigrams
// for Japanese, Chinese, and Korean, which don't separate words with spaces.
func Tokenizer(siteCode string) (string, error) {
	s, ok := site.ByCode(siteCode)
	if !ok {
		return "", fmt.Errorf("unknown site %q", siteCode)
	}
	switch strings.SplitN(s.Lang, "-", 2)[0] {
	case "en":
		return "porter unicode61 remove_diacritics 2", nil
	case "ja", "zh", "ko":
		return "trigram remove_diacritics 1", nil
	}
	return "unicode61 remove_diacritics 2", nil
}

// ErrSchemaTooNew is returned for a shard written by a newer build.
var ErrSchemaTooNew = errors.New("the index was written by a newer version of tenable-docs-mcp")

// ErrWrongShard is returned when a file holds a different shard than expected, or one
// built with a different tokenizer.
var ErrWrongShard = errors.New("the index file holds a different shard")

// Shard is one open shard.
type Shard struct {
	Key  Key
	Path string
	db   *sql.DB
}

// OpenShard opens or creates a shard's file, and brings its schema up to date.
func OpenShard(path string, key Key) (*Shard, error) { return openShard(path, key, migrations) }

func openShard(path string, key Key, migs []migration) (*Shard, error) {
	tok, err := Tokenizer(key.Site)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	creating := errors.Is(statErr, os.ErrNotExist)
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(10000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Shard{Key: key, Path: path, db: db}
	if err := s.migrate(context.Background(), tok, migs); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if creating {
		restrict(path)
	}
	return s, nil
}

func (s *Shard) migrate(ctx context.Context, tok string, migs []migration) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migs) {
		return fmt.Errorf("%w (schema %d; this build reads up to %d)", ErrSchemaTooNew, version, len(migs))
	}
	if version > 0 {
		var gotKey, gotTok string
		_ = s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'shard'").Scan(&gotKey)
		_ = s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'tokenizer'").Scan(&gotTok)
		if gotKey != s.Key.String() || gotTok != tok {
			return fmt.Errorf("%w: %s with %q, want %s with %q", ErrWrongShard, gotKey, gotTok, s.Key, tok)
		}
	}
	for v := version; v < len(migs); v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := migs[v](tx, tok); err != nil {
			tx.Rollback()
			return fmt.Errorf("schema %d: %w", v+1, err)
		}
		if v == 0 {
			for k, val := range map[string]string{"shard": s.Key.String(), "site": s.Key.Site, "older": strconv.FormatBool(s.Key.Older), "tokenizer": tok} {
				if _, err := tx.ExecContext(ctx, "INSERT INTO meta(key, value) VALUES (?, ?)", k, val); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		// PRAGMA doesn't take parameters; v+1 is an integer.
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion returns the shard's schema version.
func (s *Shard) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// Meta returns a metadata value, or "" if it isn't set.
func (s *Shard) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Close checkpoints the write-ahead log into the main file and closes the shard.
func (s *Shard) Close() error {
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// Update runs fn in one write transaction, committing if it returns nil. Batch many
// writes into one Update; each transaction costs a sync to disk.
func (s *Shard) Update(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{ctx: ctx, tx: sqlTx, shard: s}
	if err := fn(tx); err != nil {
		tx.close()
		sqlTx.Rollback()
		return err
	}
	tx.close()
	return sqlTx.Commit()
}

// Optimize merges the full-text indexes' segments and updates the query planner's
// statistics, after a build.
func (s *Shard) Optimize(ctx context.Context) error {
	for _, q := range []string{
		"INSERT INTO sections_fts(sections_fts) VALUES ('optimize')",
		"INSERT INTO settings_fts(settings_fts) VALUES ('optimize')",
		"PRAGMA optimize",
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Counts returns the number of rows in each of the shard's main tables.
func (s *Shard) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, t := range []string{"pages", "sections", "settings", "requirements", "releases", "glossary", "links", "tocs", "changes"} {
		var n int
		// The table names are constants.
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}
