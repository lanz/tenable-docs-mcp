// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// HTTPConfig configures an HTTPSource. The zero value is usable.
type HTTPConfig struct {
	// UserAgent identifies the client to the sites.
	UserAgent string
	// Concurrency is the most requests in flight at once (default 4).
	Concurrency int
	// RatePerSecond is the most requests started per second (default 5).
	RatePerSecond float64
	// Retries is how many times a request is retried after a rate limit, a server
	// error, or a network error (default 3; negative for none).
	Retries int
	// MaxRetryAfter is the longest Retry-After the source waits for (default 60s);
	// longer ones fail the request.
	MaxRetryAfter time.Duration
	// MaxBodyBytes limits a document's size (default 32 MiB).
	MaxBodyBytes int64
	// Timeout limits each attempt (default 60s).
	Timeout time.Duration
	// AllowHost decides which hosts may be fetched (default: the documentation sites).
	AllowHost func(host string) bool
	// Transport carries the requests (default: http.DefaultTransport, which uses
	// HTTPS_PROXY and the operating system's certificates).
	Transport http.RoundTripper
	// Sleep waits between retries; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// HTTPSource fetches documents over HTTPS, politely: requests are limited in number and
// rate, conditional requests avoid downloading unchanged pages, and rate limits are
// honored.
type HTTPSource struct {
	cfg     HTTPConfig
	client  *http.Client
	limiter *rate.Limiter
	slots   chan struct{}
}

// NewHTTP returns an HTTPSource with the config's defaults filled in.
func NewHTTP(cfg HTTPConfig) *HTTPSource {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.RatePerSecond <= 0 {
		cfg.RatePerSecond = 5
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	} else if cfg.Retries == 0 {
		cfg.Retries = 3
	}
	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = time.Minute
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 32 << 20
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Minute
	}
	if cfg.AllowHost == nil {
		cfg.AllowHost = IsDocsHost
	}
	if cfg.Transport == nil {
		cfg.Transport = http.DefaultTransport
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	s := &HTTPSource{
		cfg:     cfg,
		limiter: rate.NewLimiter(rate.Limit(cfg.RatePerSecond), 1),
		slots:   make(chan struct{}, cfg.Concurrency),
	}
	s.client = &http.Client{
		Transport: cfg.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != via[0].URL.Scheme {
				return fmt.Errorf("redirect to %s: %w", req.URL.Redacted(), ErrHostNotAllowed)
			}
			if !cfg.AllowHost(req.URL.Hostname()) {
				return fmt.Errorf("redirect to %s: %w", req.URL.Redacted(), ErrHostNotAllowed)
			}
			return nil
		},
	}
	return s
}

// Fetch gets one document. A 404 or 410 returns the response with ErrNotFound.
func (s *HTTPSource) Fetch(ctx context.Context, req Request) (*Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("fetch %q: not an http(s) URL", req.URL)
	}
	if !s.cfg.AllowHost(u.Hostname()) {
		return nil, fmt.Errorf("fetch %s: %w", u.Redacted(), ErrHostNotAllowed)
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := s.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		resp, wait, err := s.attempt(ctx, u, req)
		if err == nil || !retryable(err) || attempt >= s.cfg.Retries {
			return resp, err
		}
		lastErr = err
		if wait == 0 {
			wait = backoff(attempt)
		}
		if wait > s.cfg.MaxRetryAfter {
			return nil, fmt.Errorf("%w (asked to wait %s)", lastErr, wait.Round(time.Second))
		}
		if err := s.cfg.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

type retryError struct{ err error }

func (e retryError) Error() string { return e.err.Error() }
func (e retryError) Unwrap() error { return e.err }

func retryable(err error) bool {
	var r retryError
	return errors.As(err, &r)
}

// attempt makes one request. A retryable failure comes back wrapped in retryError, with
// the server's Retry-After when it sent one.
func (s *HTTPSource) attempt(ctx context.Context, u *url.URL, req Request) (*Response, time.Duration, error) {
	actx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(actx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	if s.cfg.UserAgent != "" {
		hreq.Header.Set("User-Agent", s.cfg.UserAgent)
	}
	if req.ETag != "" {
		hreq.Header.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		hreq.Header.Set("If-Modified-Since", req.LastModified)
	}
	hresp, err := s.client.Do(hreq)
	if err != nil {
		if errors.Is(err, ErrHostNotAllowed) || ctx.Err() != nil {
			return nil, 0, err
		}
		return nil, 0, retryError{fmt.Errorf("fetch %s: %w", u.Redacted(), err)}
	}
	defer hresp.Body.Close()
	resp := &Response{
		URL:          hresp.Request.URL.String(),
		Status:       hresp.StatusCode,
		ContentType:  hresp.Header.Get("Content-Type"),
		ETag:         hresp.Header.Get("ETag"),
		LastModified: hresp.Header.Get("Last-Modified"),
	}
	switch code := hresp.StatusCode; {
	case code == http.StatusNotModified:
		resp.NotModified = true
		resp.ETag = firstNonEmpty(resp.ETag, req.ETag)
		resp.LastModified = firstNonEmpty(resp.LastModified, req.LastModified)
		return resp, 0, nil
	case code >= 200 && code < 300:
		body, err := io.ReadAll(io.LimitReader(hresp.Body, s.cfg.MaxBodyBytes+1))
		if err != nil {
			return nil, 0, retryError{fmt.Errorf("fetch %s: reading the body: %w", u.Redacted(), err)}
		}
		if int64(len(body)) > s.cfg.MaxBodyBytes {
			return nil, 0, fmt.Errorf("fetch %s: %w (over %d bytes)", u.Redacted(), ErrTooLarge, s.cfg.MaxBodyBytes)
		}
		resp.Body = body
		return resp, 0, nil
	case code == http.StatusNotFound || code == http.StatusGone:
		return resp, 0, fmt.Errorf("fetch %s: HTTP %d: %w", u.Redacted(), code, ErrNotFound)
	case code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable ||
		code == http.StatusBadGateway || code == http.StatusGatewayTimeout || code == http.StatusInternalServerError:
		_, _ = io.Copy(io.Discard, io.LimitReader(hresp.Body, 64<<10))
		return nil, retryAfter(hresp.Header.Get("Retry-After")), retryError{fmt.Errorf("fetch %s: HTTP %d", u.Redacted(), code)}
	default:
		return nil, 0, fmt.Errorf("fetch %s: HTTP %d", u.Redacted(), code)
	}
}

// retryAfter parses a Retry-After header: seconds, or an HTTP date. Zero means none.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// backoff is 1s, 2s, 4s, … capped at 30s.
func backoff(attempt int) time.Duration {
	d := time.Second << attempt
	if d > 30*time.Second || d <= 0 {
		return 30 * time.Second
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
