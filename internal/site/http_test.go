// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureSite serves a made-up documentation site with the behaviors the client must
// handle.
type fixtureSite struct {
	srv       *httptest.Server
	limited   atomic.Int32 // requests to /limited so far
	flaky     atomic.Int32
	inFlight  atomic.Int32
	maxFlight atomic.Int32
	agents    sync.Map
}

func newFixtureSite(t *testing.T) *fixtureSite {
	t.Helper()
	f := &fixtureSite{}
	mux := http.NewServeMux()
	const etag, lastMod = `"v1"`, "Fri, 06 Nov 2026 16:53:11 GMT"
	mux.HandleFunc("/guide/Content/Page.htm", func(w http.ResponseWriter, r *http.Request) {
		f.agents.Store(r.Header.Get("User-Agent"), true)
		if r.Header.Get("If-None-Match") == etag || r.Header.Get("If-Modified-Since") == lastMod {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastMod)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>page</body></html>"))
	})
	mux.HandleFunc("/gone.htm", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) })
	mux.HandleFunc("/limited.htm", func(w http.ResponseWriter, _ *http.Request) {
		if f.limited.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("after the limit"))
	})
	mux.HandleFunc("/very-limited.htm", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	mux.HandleFunc("/flaky.htm", func(w http.ResponseWriter, _ *http.Request) {
		if f.flaky.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	})
	mux.HandleFunc("/broken.htm", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	mux.HandleFunc("/forbidden.htm", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	mux.HandleFunc("/moved.htm", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/guide/Content/Page.htm", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/offsite.htm", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/steal", http.StatusFound)
	})
	mux.HandleFunc("/big.htm", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	})
	mux.HandleFunc("/slow.htm", func(w http.ResponseWriter, _ *http.Request) {
		n := f.inFlight.Add(1)
		for {
			m := f.maxFlight.Load()
			if n <= m || f.maxFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		f.inFlight.Add(-1)
		_, _ = w.Write([]byte("ok"))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixtureSite) url(path string) string { return f.srv.URL + path }

func (f *fixtureSite) host() string {
	u, _ := url.Parse(f.srv.URL)
	return u.Hostname()
}

// newTestSource returns a fast HTTPSource that may fetch only the fixture site, and records
// the waits between retries.
func newTestSource(f *fixtureSite, waits *[]time.Duration, mod func(*HTTPConfig)) *HTTPSource {
	var mu sync.Mutex
	cfg := HTTPConfig{
		UserAgent:     "tenable-docs-mcp/test (+https://github.com/lanz/tenable-docs-mcp)",
		RatePerSecond: 1000,
		MaxBodyBytes:  1024,
		AllowHost:     func(h string) bool { return h == f.host() },
		AllowHTTP:     true,
		Sleep: func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			if waits != nil {
				*waits = append(*waits, d)
			}
			return nil
		},
	}
	if mod != nil {
		mod(&cfg)
	}
	return NewHTTP(cfg)
}

func TestFetchAndConditionalRequests(t *testing.T) {
	f := newFixtureSite(t)
	src := newTestSource(f, nil, nil)
	ctx := context.Background()
	resp, err := src.Fetch(ctx, Request{URL: f.url("/guide/Content/Page.htm")})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || string(resp.Body) != "<html><body>page</body></html>" || resp.ETag != `"v1"` || resp.LastModified == "" {
		t.Fatalf("first fetch = %+v", resp)
	}
	again, err := src.Fetch(ctx, Request{URL: resp.URL, ETag: resp.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified || again.Status != http.StatusNotModified || len(again.Body) != 0 || again.ETag != `"v1"` {
		t.Errorf("conditional fetch = %+v", again)
	}
	byDate, _ := src.Fetch(ctx, Request{URL: resp.URL, LastModified: resp.LastModified})
	if !byDate.NotModified || byDate.LastModified != resp.LastModified {
		t.Errorf("If-Modified-Since fetch = %+v", byDate)
	}
	if _, ok := f.agents.Load("tenable-docs-mcp/test (+https://github.com/lanz/tenable-docs-mcp)"); !ok {
		t.Error("the User-Agent wasn't sent")
	}
}

func TestRemovedPages(t *testing.T) {
	f := newFixtureSite(t)
	src := newTestSource(f, nil, nil)
	for _, p := range []string{"/gone.htm", "/missing.htm"} {
		resp, err := src.Fetch(context.Background(), Request{URL: f.url(p)})
		if !errors.Is(err, ErrNotFound) || resp == nil || resp.Status < 404 {
			t.Errorf("%s: %v, %+v", p, err, resp)
		}
	}
}

func TestRateLimitHonorsRetryAfter(t *testing.T) {
	f := newFixtureSite(t)
	var waits []time.Duration
	src := newTestSource(f, &waits, nil)
	resp, err := src.Fetch(context.Background(), Request{URL: f.url("/limited.htm")})
	if err != nil || string(resp.Body) != "after the limit" {
		t.Fatalf("fetch = %v, %+v", err, resp)
	}
	if len(waits) != 1 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want [2s] from Retry-After", waits)
	}
	if _, err := src.Fetch(context.Background(), Request{URL: f.url("/very-limited.htm")}); err == nil ||
		!strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "1h0m0s") {
		t.Errorf("a Retry-After beyond the limit should fail with the wait: %v", err)
	}
}

func TestServerErrorsBackOffThenSucceed(t *testing.T) {
	f := newFixtureSite(t)
	var waits []time.Duration
	src := newTestSource(f, &waits, nil)
	resp, err := src.Fetch(context.Background(), Request{URL: f.url("/flaky.htm")})
	if err != nil || string(resp.Body) != "recovered" {
		t.Fatalf("fetch = %v, %+v", err, resp)
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != 2*time.Second {
		t.Errorf("waits = %v, want [1s 2s]", waits)
	}
	var none []time.Duration
	quick := newTestSource(f, &none, func(c *HTTPConfig) { c.Retries = -1 })
	if _, err := quick.Fetch(context.Background(), Request{URL: f.url("/broken.htm")}); err == nil || len(none) != 0 {
		t.Errorf("with retries off: %v, waits %v", err, none)
	}
	if _, err := src.Fetch(context.Background(), Request{URL: f.url("/forbidden.htm")}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("403 should fail without retrying: %v", err)
	}
}

func TestRedirects(t *testing.T) {
	f := newFixtureSite(t)
	src := newTestSource(f, nil, nil)
	resp, err := src.Fetch(context.Background(), Request{URL: f.url("/moved.htm")})
	if err != nil || !strings.HasSuffix(resp.URL, "/guide/Content/Page.htm") {
		t.Errorf("on-site redirect = %v, %+v", err, resp)
	}
	if _, err := src.Fetch(context.Background(), Request{URL: f.url("/offsite.htm")}); !errors.Is(err, ErrHostNotAllowed) {
		t.Errorf("off-site redirect = %v, want ErrHostNotAllowed", err)
	}
}

func TestOffSiteHostsAreRefusedWithoutARequest(t *testing.T) {
	src := NewHTTP(HTTPConfig{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request should be sent")
		return nil, nil
	})})
	for _, raw := range []string{"https://evil.example/x", "https://docs.tenable.com.evil.example/x", "ftp://docs.tenable.com/x", "not a url"} {
		if _, err := src.Fetch(context.Background(), Request{URL: raw}); err == nil {
			t.Errorf("%s: fetched", raw)
		}
	}
	if _, err := src.Fetch(context.Background(), Request{URL: "https://evil.example/x"}); !errors.Is(err, ErrHostNotAllowed) {
		t.Errorf("off-site = %v", err)
	}
}

func TestBodyLimit(t *testing.T) {
	f := newFixtureSite(t)
	if _, err := newTestSource(f, nil, nil).Fetch(context.Background(), Request{URL: f.url("/big.htm")}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big body = %v, want ErrTooLarge", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	f := newFixtureSite(t)
	src := newTestSource(f, nil, func(c *HTTPConfig) { c.Concurrency = 2 })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := src.Fetch(context.Background(), Request{URL: f.url("/slow.htm")}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := f.maxFlight.Load(); got > 2 || got < 1 {
		t.Errorf("at most %d requests were in flight, want 1 or 2", got)
	}
}

func TestCanceledContext(t *testing.T) {
	f := newFixtureSite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newTestSource(f, nil, nil).Fetch(ctx, Request{URL: f.url("/guide/Content/Page.htm")}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled = %v", err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	// A huge value is capped, not overflowed into a short wait.
	for _, v := range []string{"9300000000", "18446744074", "99999999999999999999"} {
		if d := retryAfter(v); d != 0 && d < time.Hour {
			t.Errorf("retryAfter(%s) = %v", v, d)
		}
	}
	if retryAfter("") != 0 || retryAfter("abc") != 0 || retryAfter("5") != 5*time.Second {
		t.Error("seconds form")
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if d := retryAfter(future); d < 80*time.Second || d > 91*time.Second {
		t.Errorf("date form = %v", d)
	}
	if backoff(0) != time.Second || backoff(3) != 8*time.Second || backoff(10) != 30*time.Second {
		t.Error("backoff")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The documentation sites are fetched over HTTPS only.
func TestFetchRefusesPlainHTTP(t *testing.T) {
	src := NewHTTP(HTTPConfig{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("request to %s", r.URL)
		return nil, errors.New("no requests")
	})})
	if _, err := src.Fetch(context.Background(), Request{URL: "http://docs.tenable.com/nessus/Content/Install.htm"}); err == nil || !strings.Contains(err.Error(), "not an https URL") {
		t.Errorf("err = %v", err)
	}
}
