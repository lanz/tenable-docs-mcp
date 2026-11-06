// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DefaultDir returns the folder the index is kept in: tenable-docs-mcp/index under the
// user's cache folder.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tenable-docs-mcp", "index"), nil
}

// EnsureDir creates a folder only its owner can read, and tightens an existing one.
// (On Windows, the user's cache folder is already private to them; permission bits
// don't apply there.)
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// restrict makes a new file readable only by its owner.
func restrict(path string) { _ = os.Chmod(path, 0o600) }

// Index is the set of shards in one folder. Searches read the shards that are open;
// a shard can be rebuilt beside the live one and then swapped in.
type Index struct {
	Dir    string
	mu     sync.RWMutex
	shards map[Key]*Shard
}

// Open opens the shards found in dir, creating dir if needed. Shards that are missing
// are skipped; build them with Build.
func Open(dir string) (*Index, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	ix := &Index{Dir: dir, shards: map[Key]*Shard{}}
	for _, k := range Keys() {
		path := filepath.Join(dir, k.FileName())
		if _, err := os.Stat(path); err != nil {
			continue
		}
		s, err := OpenShard(path, k)
		if err != nil {
			ix.Close()
			return nil, err
		}
		ix.shards[k] = s
	}
	return ix, nil
}

// Shard returns an open shard, or nil.
func (ix *Index) Shard(k Key) *Shard {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.shards[k]
}

// Keys returns the keys of the open shards, in the order of Keys.
func (ix *Index) Keys() []Key {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out []Key
	for _, k := range Keys() {
		if ix.shards[k] != nil {
			out = append(out, k)
		}
	}
	return out
}

// With runs fn with the shard open for reading, so a swap waits until it's done.
func (ix *Index) With(k Key, fn func(*Shard) error) error {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	s := ix.shards[k]
	if s == nil {
		return fmt.Errorf("no %s shard", k)
	}
	return fn(s)
}

// Close closes every shard.
func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var errs []error
	for k, s := range ix.shards {
		errs = append(errs, s.Close())
		delete(ix.shards, k)
	}
	return errors.Join(errs...)
}

// Build opens a new, empty shard beside the live one, to fill and then pass to
// Install. Any earlier unfinished build of the same shard is discarded.
func (ix *Index) Build(k Key) (*Shard, error) {
	path := filepath.Join(ix.Dir, k.String()+".building.db")
	if err := removeDB(path); err != nil {
		return nil, err
	}
	return OpenShard(path, k)
}

// Install swaps a shard made by Build in for the live one: it closes the new shard,
// waits for searches on the old one to finish, and replaces the file.
func (ix *Index) Install(built *Shard) error {
	k := built.Key
	if err := built.Close(); err != nil {
		return err
	}
	final := filepath.Join(ix.Dir, k.FileName())
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old := ix.shards[k]; old != nil {
		if err := old.Close(); err != nil {
			return err
		}
		delete(ix.shards, k)
	}
	if err := removeDB(final); err != nil {
		return err
	}
	if err := os.Rename(built.Path, final); err != nil {
		return err
	}
	s, err := OpenShard(final, k)
	if err != nil {
		return err
	}
	ix.shards[k] = s
	return nil
}

// Discard closes and deletes a shard made by Build without installing it.
func (ix *Index) Discard(built *Shard) error {
	built.Close()
	return removeDB(built.Path)
}

// removeDB deletes a database file and its write-ahead log files.
func removeDB(path string) error {
	var errs []error
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
