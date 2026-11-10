// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package published

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A redirect on the snapshot's own host is followed.
func TestSameHostRedirect(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		if !strings.HasPrefix(r.URL.Path, "/mcp/") {
			return false
		}
		http.Redirect(w, r, "/moved/"+filepath.Base(r.URL.Path), http.StatusFound)
		return true
	}
	if _, err := updater(s, openIndex(t)).Update(ctx); err != nil {
		t.Fatal(err)
	}
}

// The redirect policy allows only HTTPS on the snapshot's own host, ten times at most.
func TestRedirectPolicy(t *testing.T) {
	check := sameHostRedirect("https://Docs.Tenable.com/mcp/index/v1/")
	req := func(u string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for u, ok := range map[string]bool{
		"https://docs.tenable.com/mcp/index/v2/manifest.json": true,
		"https://DOCS.tenable.com:443/x":                      true,
		"http://docs.tenable.com/mcp/index/v1/manifest.json":  false,
		"https://example.com/mcp/index/v1/manifest.json":      false,
		"https://docs.tenable.com.example.com/x":              false,
		"ftp://docs.tenable.com/x":                            false,
	} {
		if err := check(req(u), []*http.Request{req("https://docs.tenable.com/")}); (err == nil) != ok {
			t.Errorf("%s: %v", u, err)
		}
	}
	via := make([]*http.Request, maxRedirects)
	if err := check(req("https://docs.tenable.com/x"), via); err == nil {
		t.Error("an eleventh redirect was allowed")
	}
	// The updater uses it, unless the caller's client has its own policy.
	u := &Updater{Client: &http.Client{}}
	if err := u.client().CheckRedirect(req("http://example.com/"), nil); err == nil {
		t.Error("the updater's client follows any redirect")
	}
	called := false
	u.Client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { called = true; return nil }}
	if u.client().CheckRedirect(nil, nil); !called {
		t.Error("the caller's CheckRedirect was replaced")
	}
}

// A process whose stale lock was taken over doesn't release the new holder's.
func TestReleaseKeepsAnotherHoldersLock(t *testing.T) {
	dir := t.TempDir()
	first, err := Lock(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lockFile)
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	second, err := Lock(dir, clock)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	first.Release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the first holder removed the second's lock: %v", err)
	}
	if _, err := Lock(dir, clock); err != ErrBusy {
		t.Errorf("a third = %v", err)
	}
	second.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the lock is still there: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.stale")); len(matches) > 0 {
		t.Errorf("left %v", matches)
	}
}

// An error quoting the server at length is shortened in the state file.
func TestLastErrorIsClipped(t *testing.T) {
	dir, _ := makeSnapshot(t, "")
	s := newServer(t, dir)
	s.fault = func(w http.ResponseWriter, r *http.Request, n int) bool {
		w.Write([]byte(`{"format": 1, "shards": [{"key": "` + strings.Repeat("k", 5000) + `"}]}`))
		return true
	}
	ix := openIndex(t)
	if _, err := updater(s, ix).Update(ctx); err == nil {
		t.Fatal("no error")
	}
	st, _ := ReadState(ix.Dir)
	if st.LastError == "" || len([]rune(st.LastError)) > 501 {
		t.Errorf("LastError is %d characters", len([]rune(st.LastError)))
	}
	if long := clipError(strings.Repeat("é", 600)); len([]rune(long)) != 501 || !strings.HasSuffix(long, "…") {
		t.Errorf("clipError = %d runes", len([]rune(long)))
	}
}

// A hosted server checks more often than daily.
func TestDueEvery(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	st := State{CheckedAt: now.Add(-90 * time.Minute).Format(time.RFC3339)}
	if Due(st, false, now) || DueEvery(st, false, now, 0) {
		t.Error("due daily after 90 minutes")
	}
	if !DueEvery(st, false, now, time.Hour) {
		t.Error("not due hourly after 90 minutes")
	}
	if DueEvery(State{CheckedAt: now.Add(-30 * time.Minute).Format(time.RFC3339)}, false, now, time.Hour) {
		t.Error("due hourly after 30 minutes")
	}
}
