// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package published downloads the index snapshot published on docs.tenable.com and
// builds the local index from it, then keeps it up to date.
//
// An update reads the snapshot's manifest, downloads only the shard files whose
// checksums differ from the ones the local shards were built from, verifies each
// file's size and SHA-256, and rebuilds and swaps in only those shards. Downloaded
// files are kept in the index folder's snapshot subfolder, so a shard can be rebuilt
// without downloading it again.
//
// docs.tenable.com's CDN can serve a cached manifest that's a day old, so the
// manifest is requested with a query string the CDN hasn't cached. Publishing keeps
// old shard files for days, so even an old manifest's files can be found; if one is
// missing anyway, the update reads the manifest again once.
package published

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/snapshot"
)

// DefaultURL is where the snapshot is published.
const DefaultURL = "https://docs.tenable.com/mcp/index/v1/"

// SnapshotDir is the index folder's subfolder that holds the downloaded snapshot.
const SnapshotDir = "snapshot"

// shaMeta is the shard metadata key for the checksum of the snapshot file the shard
// was built from.
const shaMeta = "snapshot_sha256"

// maxManifestBytes and maxShardBytes guard against a broken or hostile server.
const (
	maxManifestBytes = 1 << 20
	maxShardBytes    = 1 << 30
)

// ErrBusy is returned when another process is updating the same index.
var ErrBusy = errors.New("another tenable-docs-mcp is updating this index")

// Updater updates an index from the published snapshot.
type Updater struct {
	// URL is the snapshot's folder, ending in a slash (default DefaultURL).
	URL string
	// Client makes the requests (default: one with a 5-minute timeout, using
	// HTTPS_PROXY and the operating system's certificates).
	Client *http.Client
	// UserAgent identifies the client.
	UserAgent string
	// Index is the index to update.
	Index *index.Index
	// Sites limits the update to these site codes; empty means every site in the
	// snapshot.
	Sites []string
	// Progress, when set, reports what the update is doing.
	Progress func(msg string)
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Result says what an update did.
type Result struct {
	// BuiltAt is when the snapshot was built.
	BuiltAt string `json:"built_at"`
	// Downloaded lists the shard files downloaded.
	Downloaded []string `json:"downloaded,omitempty"`
	// Imported lists the shards rebuilt from the snapshot.
	Imported []string `json:"imported,omitempty"`
	// Unchanged counts shards that were already up to date.
	Unchanged int `json:"unchanged"`
	// Bytes is how much was downloaded, manifest included.
	Bytes int64 `json:"bytes"`
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) progress(format string, args ...any) {
	if u.Progress != nil {
		u.Progress(fmt.Sprintf(format, args...))
	}
}

func (u *Updater) baseURL() string {
	if u.URL == "" {
		return DefaultURL
	}
	if !strings.HasSuffix(u.URL, "/") {
		return u.URL + "/"
	}
	return u.URL
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// errMissing is a shard file the server doesn't have.
type errMissing struct{ file string }

func (e errMissing) Error() string { return e.file + " isn't on the server" }

// Update brings the index up to date with the published snapshot, and records the
// check in the index folder's state file, whether it succeeds or not.
func (u *Updater) Update(ctx context.Context) (Result, error) {
	unlock, err := lock(u.Index.Dir, u.now())
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	res, err := u.update(ctx)
	var me errMissing
	if errors.As(err, &me) {
		// The manifest was out of date; a fresh one lists files that exist.
		u.progress("%v; reading the manifest again", err)
		res, err = u.update(ctx)
	}
	st, _ := ReadState(u.Index.Dir)
	st.URL = u.baseURL()
	if err != nil {
		if ctx.Err() == nil {
			st.LastError, st.ErrorAt = err.Error(), u.now().UTC().Format(time.RFC3339)
			_ = WriteState(u.Index.Dir, st)
		}
		return res, err
	}
	st.CheckedAt, st.BuiltAt, st.LastError, st.ErrorAt = u.now().UTC().Format(time.RFC3339), res.BuiltAt, "", ""
	return res, WriteState(u.Index.Dir, st)
}

func (u *Updater) update(ctx context.Context) (Result, error) {
	var res Result
	snapDir := filepath.Join(u.Index.Dir, SnapshotDir)
	if err := index.EnsureDir(snapDir); err != nil {
		return res, err
	}
	u.progress("Checking %s for an updated index", u.baseURL())
	data, err := u.get(ctx, ManifestURL(u.baseURL(), u.now()), maxManifestBytes)
	if err != nil {
		return res, fmt.Errorf("manifest: %w", err)
	}
	res.Bytes += int64(len(data))
	m, err := snapshot.ParseManifest(data)
	if err != nil {
		return res, err
	}
	res.BuiltAt = m.BuiltAt

	var todo []snapshot.Shard
	for _, sh := range m.Shards {
		if len(u.Sites) > 0 && !slices.Contains(u.Sites, sh.Site) {
			continue
		}
		if u.builtFrom(ctx, sh.IndexKey()) == strings.ToLower(sh.SHA256) {
			res.Unchanged++
			continue
		}
		todo = append(todo, sh)
	}
	if len(todo) == 0 && len(u.Sites) > 0 && res.Unchanged == 0 {
		return res, fmt.Errorf("the snapshot has no shards for %s", strings.Join(u.Sites, ", "))
	}

	// Download everything first, so a failed download leaves the index as it was.
	for _, sh := range todo {
		if snapshot.Verify(snapDir, sh) == nil {
			continue // downloaded earlier
		}
		u.progress("Downloading %s (%.1f MB)", sh.File, float64(sh.Size)/(1<<20))
		if err := u.download(ctx, snapDir, sh); err != nil {
			return res, err
		}
		res.Downloaded = append(res.Downloaded, sh.File)
		res.Bytes += sh.Size
	}
	for _, sh := range todo {
		u.progress("Indexing %s: %d pages", sh.Key, sh.Pages)
		sink := build.NewIndexSink(u.Index, m.BuiltAt)
		sink.SetMeta(sh.IndexKey(), shaMeta, strings.ToLower(sh.SHA256))
		if err := snapshot.Read(ctx, snapDir, sh, sink); err != nil {
			sink.Discard()
			return res, fmt.Errorf("%s: %w", sh.Key, err)
		}
		if err := sink.Finish(ctx); err != nil {
			sink.Discard()
			return res, fmt.Errorf("%s: %w", sh.Key, err)
		}
		res.Imported = append(res.Imported, sh.Key)
	}
	if err := writeFileAtomic(filepath.Join(snapDir, snapshot.ManifestFile), data); err != nil {
		return res, err
	}
	prune(snapDir, m)
	return res, nil
}

// ManifestURL is the manifest's address under base, with a query string that changes
// every second, so the CDN fetches it fresh.
func ManifestURL(base string, now time.Time) string {
	return base + snapshot.ManifestFile + "?t=" + strconv.FormatInt(now.Unix(), 10)
}

// builtFrom returns the checksum of the snapshot file a shard was built from, or ""
// for a shard that's missing or was built some other way.
func (u *Updater) builtFrom(ctx context.Context, k index.Key) string {
	var sum string
	_ = u.Index.With(k, func(sh *index.Shard) error {
		sum, _ = sh.Meta(ctx, shaMeta)
		return nil
	})
	return sum
}

// download fetches a shard's file into dir, checking its size and checksum as it
// arrives, and moves it into place only when both match.
func (u *Updater) download(ctx context.Context, dir string, sh snapshot.Shard) error {
	if sh.Size <= 0 || sh.Size > maxShardBytes {
		return fmt.Errorf("%s: unlikely size %d in the manifest", sh.File, sh.Size)
	}
	part := filepath.Join(dir, sh.File+".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := u.fetch(ctx, u.baseURL()+sh.File, sh.Size, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n != sh.Size:
		err = fmt.Errorf("%s: got %d bytes; the manifest says %d", sh.File, n, sh.Size)
	case !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sh.SHA256):
		err = fmt.Errorf("%s doesn't match its checksum in the manifest", sh.File)
	}
	if err != nil {
		os.Remove(part)
		var se statusError
		if errors.As(err, &se) && se.code == http.StatusNotFound {
			return errMissing{file: sh.File}
		}
		return err
	}
	return os.Rename(part, filepath.Join(dir, sh.File))
}

// get fetches a small document.
func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	var b strings.Builder
	if _, err := u.fetch(ctx, url, limit, &b); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// retryDelay is the wait before the first retry; the second waits twice as long.
var retryDelay = 2 * time.Second

type statusError struct {
	url  string
	code int
}

func (e statusError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.url, e.code) }

// fetch copies a document into w, at most limit bytes (one more fails it). A network
// or server error before any of the body arrives is retried twice; one partway
// through fails the fetch, since w already holds part of the body.
func (u *Updater) fetch(ctx context.Context, url string, limit int64, w io.Writer) (int64, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * retryDelay):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		n, retry, err := u.fetchOnce(ctx, url, limit, w)
		if err == nil || !retry || n > 0 {
			return n, err
		}
		lastErr = err
	}
	return 0, lastErr
}

// fetchOnce makes one attempt, and reports whether a failed one is worth retrying.
func (u *Updater) fetchOnce(ctx context.Context, url string, limit int64, w io.Writer) (int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	if u.UserAgent != "" {
		req.Header.Set("User-Agent", u.UserAgent)
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return 0, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests, statusError{url: url, code: resp.StatusCode}
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("%s is larger than expected", url)
	}
	return n, false, err
}

// prune deletes downloaded files the manifest no longer lists, and unfinished
// downloads.
func prune(dir string, m snapshot.Manifest) {
	keep := map[string]bool{snapshot.ManifestFile: true}
	for _, sh := range m.Shards {
		keep[sh.File] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !keep[e.Name()] && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// StateFile is the index folder's record of the last update check.
const StateFile = "updates.json"

// State records the last update check, so every process using the index folder
// knows when the next one is due.
type State struct {
	// URL is the snapshot's address.
	URL string `json:"url,omitempty"`
	// CheckedAt is when the last successful check finished (RFC 3339).
	CheckedAt string `json:"checked_at,omitempty"`
	// BuiltAt is when the snapshot that check found was built.
	BuiltAt string `json:"built_at,omitempty"`
	// LastError is why the last check failed, if it did, and ErrorAt is when.
	LastError string `json:"last_error,omitempty"`
	ErrorAt   string `json:"error_at,omitempty"`
}

// ReadState reads the index folder's state file; a missing one is the zero State.
func ReadState(dir string) (State, error) {
	var st State
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

// WriteState writes the index folder's state file.
func WriteState(dir string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, StateFile), append(data, '\n'))
}

// lockFile is held by the process updating the index folder.
const lockFile = "update.lock"

// staleLock is how old a lock file must be before it's taken to be left by a process
// that stopped; an update takes a minute or two.
const staleLock = time.Hour

// lock takes the index folder's update lock, so only one process updates it at a
// time, and returns the function that releases it.
func lock(dir string, now time.Time) (func(), error) {
	path := filepath.Join(dir, lockFile)
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d %s\n", os.Getpid(), now.UTC().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, serr := os.Stat(path)
		// The file's time is the real clock's, whatever now is in a test.
		if serr != nil || time.Since(info.ModTime()) < staleLock {
			return nil, ErrBusy
		}
		_ = os.Remove(path)
	}
	return nil, ErrBusy
}
