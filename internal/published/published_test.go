// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package published

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/crawl"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/snapshot"
)

var ctx = context.Background()

func init() { retryDelay = time.Millisecond }

var clock = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// copyDir copies a folder of test files.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// makeSnapshot builds the build package's made-up sites into a snapshot. With
// jpEdit, the Japanese site's start page gets that text added, so its shard differs.
func makeSnapshot(t *testing.T, jpEdit string) (string, snapshot.Manifest) {
	t.Helper()
	enDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "en"))
	jpDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "jp"))
	if jpEdit != "" {
		edited := filepath.Join(t.TempDir(), "jp")
		copyDir(t, jpDir, edited)
		start := filepath.Join(edited, "widget", "Content", "Start.htm")
		data, err := os.ReadFile(start)
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.Replace(string(data), "</div>", "<p>"+jpEdit+"</p></div>", 1))
		if err := os.WriteFile(start, data, 0o644); err != nil {
			t.Fatal(err)
		}
		jpDir = edited
	}
	src, err := site.NewLocal(map[string]string{"en": enDir, "jp": jpDir})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "snapshot")
	w, err := snapshot.NewWriter(dir, "test", clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(ctx, build.Options{Source: src, Sites: src.Sites()}, w); err != nil {
		t.Fatal(err)
	}
	m, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// server serves a snapshot folder, counting requests. Its fault hook can replace a
// response.
type server struct {
	*httptest.Server
	mu       sync.Mutex
	dir      string
	requests []string
	fault    func(w http.ResponseWriter, r *http.Request, n int) bool
}

func newServer(t *testing.T, dir string) *server {
	t.Helper()
	s := &server{dir: dir}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.URL.RequestURI())
		n, fault, dir := len(s.requests), s.fault, s.dir
		s.mu.Unlock()
		if fault != nil && fault(w, r, n) {
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) serve(dir string) {
	s.mu.Lock()
	s.dir, s.requests = dir, nil
	s.mu.Unlock()
}

func (s *server) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func openIndex(t *testing.T) *index.Index {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func updater(s *server, ix *index.Index) *Updater {
	return &Updater{URL: s.URL + "/mcp/index/v1/", Client: s.Client(), UserAgent: "test", Index: ix, Now: func() time.Time { return clock }}
}

func keys(ix *index.Index) []string {
	var out []string
	for _, k := range ix.Keys() {
		out = append(out, k.String())
	}
	return out
}

func TestFirstUpdate(t *testing.T) {
	dir, m := makeSnapshot(t, "")
	s := newServer(t, dir)
	ix := openIndex(t)
	var steps []string
	u := updater(s, ix)
	u.Progress = func(msg string) { steps = append(steps, msg) }
	res, err := u.Update(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downloaded) != len(m.Shards) || len(res.Imported) != len(m.Shards) || res.Unchanged != 0 || res.BuiltAt != m.BuiltAt {
		t.Errorf("result = %+v", res)
	}
	if got := keys(ix); len(got) != len(m.Shards) {
		t.Errorf("index shards = %v", got)
	}
	// The manifest is asked for with a query string the CDN hasn't cached.
	if reqs := s.got(); !strings.HasPrefix(reqs[0], "/mcp/index/v1/manifest.json?t=") {
		t.Errorf("first request = %s", reqs[0])
	}
	// The downloads are kept, with the manifest, and nothing unfinished.
	snap := filepath.Join(ix.Dir, SnapshotDir)
	for _, sh := range m.Shards {
		if err := snapshot.Verify(snap, sh); err != nil {
			t.Error(err)
		}
	}
	if _, err := snapshot.ReadManifest(snap); err != nil {
		t.Error(err)
	}
	entries, _ := os.ReadDir(snap)
	if len(entries) != len(m.Shards)+1 {
		t.Errorf("snapshot folder has %d files", len(entries))
	}
	st, err := ReadState(ix.Dir)
	if err != nil || st.CheckedAt != "2026-11-08T12:00:00Z" || st.BuiltAt != m.BuiltAt || st.LastError != "" || st.URL != u.URL {
		t.Errorf("state = %+v, %v", st, err)
	}
	if len(steps) == 0 || !strings.HasPrefix(steps[0], "Checking ") {
		t.Errorf("progress = %q", steps)
	}
	if _, err := os.Stat(filepath.Join(ix.Dir, lockFile)); !os.IsNotExist(err) {
		t.Errorf("lock left behind: %v", err)
	}
}

func TestUpdateOnlyChangedShards(t *testing.T) {
	dirA, mA := makeSnapshot(t, "")
	dirB, mB := makeSnapshot(t, "新しい段落")
	s := newServer(t, dirA)
	ix := openIndex(t)
	if _, err := updater(s, ix).Update(ctx); err != nil {
		t.Fatal(err)
	}
	enShard := ix.Shard(index.Key{Site: "en"})

	// Nothing changed: only the manifest is fetched.
	s.serve(dirA)
	res, err := updater(s, ix).Update(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downloaded) != 0 || len(res.Imported) != 0 || res.Unchanged != len(mA.Shards) {
		t.Errorf("no-change result = %+v", res)
	}
	if reqs := s.got(); len(reqs) != 1 {
		t.Errorf("no-change requests = %v", reqs)
	}

	// The Japanese shard changed: only it is downloaded and rebuilt.
	s.serve(dirB)
	res, err = updater(s, ix).Update(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downloaded) != 1 || !strings.HasPrefix(res.Downloaded[0], "jp-current-") ||
		!slices.Equal(res.Imported, []string{"jp-current"}) || res.Unchanged != len(mB.Shards)-1 {
		t.Errorf("changed result = %+v", res)
	}
	if ix.Shard(index.Key{Site: "en"}) != enShard {
		t.Error("the unchanged English shard was rebuilt")
	}
	// The old Japanese file is pruned from the download folder.
	entries, _ := os.ReadDir(filepath.Join(ix.Dir, SnapshotDir))
	if len(entries) != len(mB.Shards)+1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("snapshot folder = %v", names)
	}
}

func TestRebuildFromKeptFiles(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	idxDir := filepath.Join(t.TempDir(), "index")
	ix, err := index.Open(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater(s, ix).Update(ctx); err != nil {
		t.Fatal(err)
	}
	ix.Close()
	// Lose the Japanese shard's database, as after a failed disk or a schema change.
	matches, _ := filepath.Glob(filepath.Join(idxDir, "jp-current.*"))
	for _, p := range matches {
		os.Remove(p)
	}
	ix, err = index.Open(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	s.serve(dir)
	res, err := updater(s, ix).Update(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downloaded) != 0 || !slices.Equal(res.Imported, []string{"jp-current"}) {
		t.Errorf("result = %+v", res)
	}
	if reqs := s.got(); len(reqs) != 1 {
		t.Errorf("requests = %v; want only the manifest", reqs)
	}
}

func TestBadDownloadsLeaveTheIndexAlone(t *testing.T) {
	dir, m := makeSnapshot(t, "")
	jp := m.Shards[len(m.Shards)-1]
	for name, fault := range map[string]func(w http.ResponseWriter, r *http.Request, n int) bool{
		"checksum": func(w http.ResponseWriter, r *http.Request, n int) bool {
			if !strings.HasSuffix(r.URL.Path, jp.File) {
				return false
			}
			data, _ := os.ReadFile(filepath.Join(dir, jp.File))
			data[len(data)/2] ^= 0xff
			w.Write(data)
			return true
		},
		"partial": func(w http.ResponseWriter, r *http.Request, n int) bool {
			if !strings.HasSuffix(r.URL.Path, jp.File) {
				return false
			}
			data, _ := os.ReadFile(filepath.Join(dir, jp.File))
			w.Write(data[:len(data)/2])
			return true
		},
		"too large": func(w http.ResponseWriter, r *http.Request, n int) bool {
			if !strings.HasSuffix(r.URL.Path, jp.File) {
				return false
			}
			data, _ := os.ReadFile(filepath.Join(dir, jp.File))
			w.Write(append(data, data...))
			return true
		},
		"server error": func(w http.ResponseWriter, r *http.Request, n int) bool {
			if !strings.HasSuffix(r.URL.Path, jp.File) {
				return false
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return true
		},
		"bad manifest": func(w http.ResponseWriter, r *http.Request, n int) bool {
			if !strings.HasSuffix(r.URL.Path, snapshot.ManifestFile) {
				return false
			}
			w.Write([]byte(`{"format": 1, "shards": [{"key": "en-current", "site": "en", "file": "../../evil", "sha256": "x"}]}`))
			return true
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, dir)
			s.fault = fault
			ix := openIndex(t)
			_, err := updater(s, ix).Update(ctx)
			if err == nil {
				t.Fatal("update succeeded")
			}
			// Downloads all finish before any shard is rebuilt.
			if got := keys(ix); len(got) != 0 {
				t.Errorf("index shards = %v", got)
			}
			st, _ := ReadState(ix.Dir)
			if st.LastError == "" || st.ErrorAt == "" || st.CheckedAt != "" {
				t.Errorf("state = %+v", st)
			}
			matches, _ := filepath.Glob(filepath.Join(ix.Dir, SnapshotDir, "*.part"))
			if len(matches) != 0 {
				t.Errorf("unfinished downloads left: %v", matches)
			}
		})
	}
}

func TestServerErrorIsRetried(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		if n == 1 {
			http.Error(w, "busy", http.StatusBadGateway)
			return true
		}
		return false
	}
	if _, err := updater(s, openIndex(t)).Update(ctx); err != nil {
		t.Fatal(err)
	}
	if reqs := s.got(); !strings.Contains(reqs[0], "manifest") || !strings.Contains(reqs[1], "manifest") {
		t.Errorf("requests = %v", reqs)
	}
}

// A CDN can serve an old manifest whose files are gone; the update reads it again.
func TestStaleManifestIsReadAgain(t *testing.T) {
	dirA, _ := makeSnapshot(t, "")
	dirB, mB := makeSnapshot(t, "新しい段落")
	// The server has B's files, but its first manifest answer is A's, which lists a
	// Japanese file that's no longer there.
	staleManifest, _ := os.ReadFile(filepath.Join(dirA, snapshot.ManifestFile))
	s := newServer(t, dirB)
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		if n == 1 {
			w.Write(staleManifest)
			return true
		}
		return false
	}
	ix := openIndex(t)
	res, err := updater(s, ix).Update(ctx)
	if err != nil {
		t.Fatal(err)
	}
	manifests := 0
	for _, r := range s.got() {
		if strings.Contains(r, "manifest") {
			manifests++
		}
	}
	if manifests != 2 || len(res.Imported) != len(mB.Shards) {
		t.Errorf("manifests fetched %d; result %+v", manifests, res)
	}
}

func TestNewerFormatIsRefused(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		w.Write([]byte(`{"format": 2, "shards": []}`))
		return true
	}
	ix := openIndex(t)
	_, err := updater(s, ix).Update(ctx)
	if !errors.Is(err, snapshot.ErrFormat) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "update it") {
		t.Errorf("the error should say to update: %v", err)
	}
}

func TestSites(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	ix := openIndex(t)
	u := updater(s, ix)
	u.Sites = []string{"jp"}
	if _, err := u.Update(ctx); err != nil {
		t.Fatal(err)
	}
	if got := keys(ix); !slices.Equal(got, []string{"jp-current"}) {
		t.Errorf("shards = %v", got)
	}
	u.Sites = []string{"de"}
	if _, err := u.Update(ctx); err == nil || !strings.Contains(err.Error(), "no shards for de") {
		t.Errorf("a site the snapshot lacks: %v", err)
	}
}

func TestOneUpdateAtATime(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	ix := openIndex(t)
	l, err := Lock(ix.Dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater(s, ix).Update(ctx); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v", err)
	}
	l.Release()
	// A lock left by a process that stopped long ago is taken over.
	path := filepath.Join(ix.Dir, lockFile)
	if err := os.WriteFile(path, []byte("1 old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := updater(s, ix).Update(ctx); err != nil {
		t.Errorf("stale lock: %v", err)
	}
}

func TestDue(t *testing.T) {
	at := func(d time.Duration) string { return clock.Add(-d).Format(time.RFC3339) }
	for _, c := range []struct {
		name  string
		st    State
		empty bool
		want  bool
	}{
		{"never checked", State{}, false, true},
		{"empty index", State{CheckedAt: at(time.Minute)}, true, true},
		{"checked today", State{CheckedAt: at(time.Hour)}, false, false},
		{"checked yesterday", State{CheckedAt: at(25 * time.Hour)}, false, true},
		{"failed recently", State{CheckedAt: at(25 * time.Hour), ErrorAt: at(10 * time.Minute)}, false, false},
		{"failed an hour ago", State{CheckedAt: at(25 * time.Hour), ErrorAt: at(61 * time.Minute)}, false, true},
		{"empty, failed just now", State{ErrorAt: at(time.Minute)}, true, false},
		{"empty, failed a while ago", State{ErrorAt: at(6 * time.Minute)}, true, true},
	} {
		if got := Due(c.st, c.empty, clock); got != c.want {
			t.Errorf("%s: Due = %v", c.name, got)
		}
	}
}

func TestWatcherTick(t *testing.T) {
	dir, m := makeSnapshot(t, "")
	s := newServer(t, dir)
	ix := openIndex(t)
	var logged []string
	w := &Watcher{Updater: updater(s, ix), Log: func(msg string) { logged = append(logged, msg) }}
	w.Tick(ctx)
	if got := keys(ix); len(got) != len(m.Shards) {
		t.Fatalf("shards = %v", got)
	}
	if st := w.Status(); st.Updating || st.CheckedAt == "" {
		t.Errorf("status = %+v", st)
	}
	if !slices.ContainsFunc(logged, func(l string) bool { return strings.HasPrefix(l, "updated the index") }) {
		t.Errorf("log = %q", logged)
	}
	// Not due again for a day: the next tick asks for nothing.
	s.serve(dir)
	w.Tick(ctx)
	if reqs := s.got(); len(reqs) != 0 {
		t.Errorf("requests = %v", reqs)
	}
}

// The watcher picks up what another process's update installed.
func TestWatcherReloads(t *testing.T) {
	dir, m := makeSnapshot(t, "")
	s := newServer(t, dir)
	idxDir := filepath.Join(t.TempDir(), "index")
	a, err := index.Open(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := index.Open(idxDir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := updater(s, a).Update(ctx); err != nil {
		t.Fatal(err)
	}
	// b's state file says a check just ran, so b only reloads.
	s.serve(dir)
	(&Watcher{Updater: updater(s, b)}).Tick(ctx)
	if got := keys(b); len(got) != len(m.Shards) {
		t.Errorf("b's shards = %v", got)
	}
	if reqs := s.got(); len(reqs) != 0 {
		t.Errorf("b made requests: %v", reqs)
	}
}

// countingSource counts what a crawl fetches.
type countingSource struct {
	site.Source
	mu sync.Mutex
	n  int
}

func (c *countingSource) Fetch(ctx context.Context, req site.Request) (*site.Response, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.Source.Fetch(ctx, req)
}

func (c *countingSource) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.n
	c.n = 0
	return n
}

// While the published snapshot can't be reached, the watcher crawls the live sites;
// once it's back, the snapshot replaces what was crawled.
func TestWatcherFallsBackToCrawling(t *testing.T) {
	dir, m := makeSnapshot(t, "")
	s := newServer(t, dir)
	down := true
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		if down {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
		return down
	}
	enDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "en"))
	jpDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "jp"))
	local, err := site.NewLocal(map[string]string{"en": enDir, "jp": jpDir})
	if err != nil {
		t.Fatal(err)
	}
	src := &countingSource{Source: local}
	ix := openIndex(t)
	now := clock
	u := updater(s, ix)
	u.Now = func() time.Time { return now }
	w := &Watcher{Updater: u, Crawler: &crawl.Crawler{Index: ix, Source: src, Sites: local.Sites(), Now: u.Now}}
	changed := 0
	w.OnChange = func() { changed++ }

	w.Tick(ctx)
	if got := keys(ix); len(got) != len(m.Shards) {
		t.Fatalf("after the crawl: shards %v", got)
	}
	st := w.Status()
	if st.Source != SourceLive || st.CrawledAt == "" || st.LastError == "" || changed != 1 {
		t.Errorf("status = %+v; changed %d", st, changed)
	}

	// An hour later the snapshot is still down; the crawl isn't due again yet.
	now = now.Add(RetryAfter + time.Minute)
	src.count()
	w.Tick(ctx)
	if n := src.count(); n != 0 {
		t.Errorf("crawled again after an hour: %d requests", n)
	}

	// The snapshot is back: it replaces the crawled shards.
	down = false
	now = now.Add(RetryAfter + time.Minute)
	w.Tick(ctx)
	st = w.Status()
	if st.Source != SourcePublished || st.LastError != "" || changed != 2 {
		t.Errorf("status after the snapshot came back = %+v; changed %d", st, changed)
	}
	if n := src.count(); n != 0 {
		t.Errorf("crawled with the snapshot back: %d requests", n)
	}
}

func TestCrawlDue(t *testing.T) {
	at := func(d time.Duration) string { return clock.Add(-d).Format(time.RFC3339) }
	for _, c := range []struct {
		name  string
		st    State
		empty bool
		want  bool
	}{
		{"never crawled", State{}, false, true},
		{"empty index", State{CrawledAt: at(time.Minute)}, true, true},
		{"crawled today", State{CrawledAt: at(time.Hour)}, false, false},
		{"crawled yesterday", State{CrawledAt: at(25 * time.Hour)}, false, true},
		{"crawl failed recently", State{CrawlErrorAt: at(10 * time.Minute)}, true, false},
		{"crawl failed an hour ago", State{CrawlErrorAt: at(61 * time.Minute)}, true, true},
	} {
		if got := CrawlDue(c.st, c.empty, clock); got != c.want {
			t.Errorf("%s: CrawlDue = %v", c.name, got)
		}
	}
}
