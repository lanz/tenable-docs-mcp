// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
//
// Several processes can use one folder, as when more than one MCP client runs the
// server. So that none of them replaces a file another has open (which Windows
// doesn't allow), each installed build of a shard gets a new, numbered file
// (en-current.3.db), and older files are deleted once nothing has them open. Reload
// switches a process to builds that another process installed.
type Index struct {
	Dir    string
	mu     sync.RWMutex
	shards map[Key]*Shard
	gens   map[Key]int
}

// Open opens the newest build of each shard found in dir, creating dir if needed.
// Shards that are missing are skipped; build them with Build.
func Open(dir string) (*Index, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	ix := &Index{Dir: dir, shards: map[Key]*Shard{}, gens: map[Key]int{}}
	if _, err := ix.Reload(); err != nil {
		ix.Close()
		return nil, err
	}
	return ix, nil
}

// genPath returns the file of a shard's numbered build. Build 0 is the unnumbered
// en-current.db that earlier versions wrote.
func genPath(dir string, k Key, gen int) string {
	if gen == 0 {
		return filepath.Join(dir, k.FileName())
	}
	return filepath.Join(dir, k.String()+"."+strconv.Itoa(gen)+".db")
}

// builds returns the numbers of a shard's installed builds in dir, newest first.
func builds(dir string, k Key) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var gens []int
	for _, e := range entries {
		if gen, ok := parseGen(e.Name(), k); ok && e.Type().IsRegular() {
			gens = append(gens, gen)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(gens)))
	return gens, nil
}

// parseGen returns the build number of a shard's installed file name.
func parseGen(name string, k Key) (int, bool) {
	if name == k.FileName() {
		return 0, true
	}
	rest, ok := strings.CutPrefix(name, k.String()+".")
	if !ok {
		return 0, false
	}
	num, ok := strings.CutSuffix(rest, ".db")
	if !ok || num == "" || num[0] == '0' {
		return 0, false
	}
	gen, err := strconv.Atoi(num)
	if err != nil || gen <= 0 {
		return 0, false
	}
	return gen, true
}

// Reload opens any shard build newer than the one this Index has open, as one that
// another process installed, and swaps it in. It reports whether anything changed.
// Searches in progress finish on the build they started with.
func (ix *Index) Reload() (bool, error) {
	changed := false
	var errs []error
	for _, k := range Keys() {
		gens, err := builds(ix.Dir, k)
		if err != nil {
			return changed, err
		}
		if len(gens) == 0 {
			continue
		}
		ix.mu.RLock()
		cur, open := ix.gens[k], ix.shards[k] != nil
		ix.mu.RUnlock()
		if open && gens[0] <= cur {
			continue
		}
		s, err := OpenShard(genPath(ix.Dir, k, gens[0]), k)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ix.swap(k, s, gens[0])
		changed = true
	}
	return changed, errors.Join(errs...)
}

// swap makes s the open build of its shard, closes the build it replaces, and deletes
// older builds' files that nothing has open.
func (ix *Index) swap(k Key, s *Shard, gen int) {
	ix.mu.Lock()
	old := ix.shards[k]
	ix.shards[k], ix.gens[k] = s, gen
	ix.mu.Unlock()
	if old != nil {
		old.Close()
	}
	ix.removeOld(k, gen)
}

// removeOld deletes a shard's builds older than gen. A file that another process has
// open can't be deleted on Windows; it stays until a later install or reload.
func (ix *Index) removeOld(k Key, gen int) {
	gens, err := builds(ix.Dir, k)
	if err != nil {
		return
	}
	for _, g := range gens {
		if g < gen {
			_ = removeDB(genPath(ix.Dir, k, g))
		}
	}
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
		delete(ix.gens, k)
	}
	return errors.Join(errs...)
}

// buildPrefix starts the name of a shard's file while it's being built.
func buildPrefix(k Key) string { return k.String() + ".building." }

// Build opens a new, empty shard beside the live one, to fill and then pass to
// Install. Its file has a name of its own, so builds in two processes don't collide.
// Unfinished builds of the same shard left by a process that stopped are deleted.
func (ix *Index) Build(k Key) (*Shard, error) {
	if entries, err := os.ReadDir(ix.Dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), buildPrefix(k)) && strings.HasSuffix(e.Name(), ".db") {
				// A build in progress in another process has its file open; on
				// Windows, that keeps it from being deleted.
				_ = removeDB(filepath.Join(ix.Dir, e.Name()))
			}
		}
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return OpenShard(filepath.Join(ix.Dir, buildPrefix(k)+hex.EncodeToString(b)+".db"), k)
}

// Install swaps a shard made by Build into the index as the shard's newest build.
func (ix *Index) Install(built *Shard) error {
	k := built.Key
	if err := built.Close(); err != nil {
		return err
	}
	gens, err := builds(ix.Dir, k)
	if err != nil {
		return err
	}
	gen := 1
	if len(gens) > 0 {
		gen = gens[0] + 1
	}
	ix.mu.RLock()
	if cur := ix.gens[k]; ix.shards[k] != nil && cur >= gen {
		gen = cur + 1
	}
	ix.mu.RUnlock()
	final := genPath(ix.Dir, k, gen)
	// The write-ahead log was checkpointed into the file when it closed.
	_ = os.Remove(built.Path + "-wal")
	if err := os.Rename(built.Path, final); err != nil {
		_ = removeDB(built.Path)
		return err
	}
	_ = os.Remove(built.Path + "-shm")
	s, err := OpenShard(final, k)
	if err != nil {
		return err
	}
	ix.swap(k, s, gen)
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
