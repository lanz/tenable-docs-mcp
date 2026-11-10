// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// HostedOptions configure a hosted server's HTTP layer: the server that runs for
// everyone, behind a load balancer, rather than on one user's computer.
type HostedOptions struct {
	// AllowHosts are the host names clients reach the server by, such as
	// docs.tenable.com. Requests to /mcp for any other host are refused. Required.
	AllowHosts []string
	// ClientIPHeader names the header in which a trusted proxy in front of the server
	// gives the client's address, such as CF-Connecting-IP. Empty means the
	// connection's own address, which is the proxy's if there is one.
	ClientIPHeader string
	// RatePerMinute is how many requests each client may make a minute (default 120),
	// in bursts of up to Burst (default a quarter of that).
	RatePerMinute int
	Burst         int
	// MaxInFlight is how many requests are handled at once (default 64); more get 503.
	MaxInFlight int
	// MaxBody is the largest request body taken (default 1 MiB).
	MaxBody int64
	// RequestTimeout is how long a request may take (default one minute).
	RequestTimeout time.Duration
	// Ready reports whether the server can answer: its index is loaded. Until then,
	// /readyz says not ready, so a load balancer sends it nothing.
	Ready func() bool
	// Log, when set, gets a line for each request: its MCP method and tool, status,
	// and duration. Never what was asked, nor who asked.
	Log *slog.Logger
}

func (o *HostedOptions) defaults() {
	if o.RatePerMinute <= 0 {
		o.RatePerMinute = 120
	}
	if o.Burst <= 0 {
		o.Burst = max(1, o.RatePerMinute/4)
	}
	if o.MaxInFlight <= 0 {
		o.MaxInFlight = 64
	}
	if o.MaxBody <= 0 {
		o.MaxBody = 1 << 20
	}
	if o.RequestTimeout <= 0 {
		o.RequestTimeout = time.Minute
	}
	if o.Ready == nil {
		o.Ready = func() bool { return true }
	}
}

// HostedHandler serves MCP for a hosted server at /mcp, with /healthz (the process is
// up), /readyz (it can answer), and /metrics (Prometheus text). Only /mcp is for
// clients; route only it from the internet.
//
// MCP runs stateless, so any instance behind a load balancer can answer any request,
// with plain JSON responses, which proxies and CDNs pass through as they are. Each
// request is limited in size and time, each client in rate, and the server in how many
// requests it handles at once.
func HostedHandler(srv *mcp.Server, o HostedOptions) (http.Handler, error) {
	h, err := newHosted(srv, o)
	if err != nil {
		return nil, err
	}
	return h.mux, nil
}

func newHosted(srv *mcp.Server, o HostedOptions) (*hosted, error) {
	if len(o.AllowHosts) == 0 {
		return nil, fmt.Errorf("a hosted server needs the host names clients reach it by")
	}
	o.defaults()
	h := &hosted{opts: o, sem: make(chan struct{}, o.MaxInFlight), clients: map[string]*client{}, metrics: newMetrics()}
	for _, a := range o.AllowHosts {
		h.hosts = append(h.hosts, strings.ToLower(strings.TrimSpace(a)))
	}
	// The SDK's DNS-rebinding check is for servers on localhost; a hosted server checks
	// the Host header against AllowHosts instead, which also covers a proxy on the same
	// machine.
	h.mcp = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: true,
	})
	h.cop = http.NewCrossOriginProtection()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.serveMCP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { text(w, http.StatusOK, "ok\n") })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if o.Ready() {
			text(w, http.StatusOK, "ready\n")
			return
		}
		text(w, http.StatusServiceUnavailable, "not ready: the index is still loading\n")
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		h.metrics.write(w, len(h.sem), o.Ready())
	})
	h.mux = mux
	return h, nil
}

type hosted struct {
	mux   *http.ServeMux
	opts  HostedOptions
	hosts []string
	mcp   http.Handler
	cop   *http.CrossOriginProtection
	sem   chan struct{}

	mu      sync.Mutex
	clients map[string]*client
	swept   time.Time

	metrics *metrics
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

func text(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func (h *hosted) serveMCP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	call := mcpCall{Method: "-", Tool: "-"}
	code := h.handle(w, r, &call)
	h.metrics.observe(call.Method, call.Tool, code, time.Since(start))
	if h.opts.Log != nil {
		h.opts.Log.Info("request", "method", call.Method, "tool", call.Tool, "status", code,
			"duration_ms", time.Since(start).Milliseconds())
	}
}

// handle serves one request to /mcp, or refuses it, and returns its status.
func (h *hosted) handle(w http.ResponseWriter, r *http.Request, call *mcpCall) int {
	if !h.allowedHost(r.Host) {
		text(w, http.StatusMisdirectedRequest, "this server doesn't answer for that host\n")
		return http.StatusMisdirectedRequest
	}
	if err := h.cop.Check(r); err != nil {
		text(w, http.StatusForbidden, "cross-origin requests from browsers aren't allowed\n")
		return http.StatusForbidden
	}
	if wait, ok := h.allow(h.clientKey(r), time.Now()); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(wait.Seconds()))))
		text(w, http.StatusTooManyRequests, "too many requests; try again shortly\n")
		return http.StatusTooManyRequests
	}
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		w.Header().Set("Retry-After", "1")
		text(w, http.StatusServiceUnavailable, "the server is busy; try again shortly\n")
		return http.StatusServiceUnavailable
	}
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, h.opts.MaxBody+1))
		if err != nil {
			text(w, http.StatusBadRequest, "couldn't read the request\n")
			return http.StatusBadRequest
		}
		if int64(len(body)) > h.opts.MaxBody {
			text(w, http.StatusRequestEntityTooLarge, "the request is too large\n")
			return http.StatusRequestEntityTooLarge
		}
		*call = parseCall(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.opts.RequestTimeout)
	defer cancel()
	sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	h.mcp.ServeHTTP(sw, r.WithContext(ctx))
	return sw.code
}

func (h *hosted) allowedHost(host string) bool {
	if hh, _, err := net.SplitHostPort(host); err == nil {
		host = hh
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range h.hosts {
		if host == a {
			return true
		}
	}
	return false
}

// clientKey is who a request is from, for its rate limit: the address the trusted
// proxy gives, or the connection's.
func (h *hosted) clientKey(r *http.Request) string {
	if name := h.opts.ClientIPHeader; name != "" {
		if v := strings.TrimSpace(strings.Split(r.Header.Get(name), ",")[0]); v != "" {
			return v
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// allow reports whether a client may make a request now, and if not, how long until
// it may. Clients not seen for ten minutes are forgotten.
func (h *hosted) allow(key string, now time.Time) (time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if now.Sub(h.swept) > time.Minute {
		for k, c := range h.clients {
			if now.Sub(c.seen) > 10*time.Minute {
				delete(h.clients, k)
			}
		}
		h.swept = now
	}
	c := h.clients[key]
	if c == nil {
		c = &client{lim: rate.NewLimiter(rate.Limit(float64(h.opts.RatePerMinute)/60), h.opts.Burst)}
		h.clients[key] = c
	}
	c.seen = now
	res := c.lim.ReserveN(now, 1)
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return d, false
	}
	return 0, true
}

// mcpCall is what a request asks for: its JSON-RPC method and, for a tool call, the
// tool.
type mcpCall struct{ Method, Tool string }

func parseCall(body []byte) mcpCall {
	var m struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	c := mcpCall{Method: "-", Tool: "-"}
	if json.Unmarshal(body, &m) != nil {
		return c
	}
	if m.Method != "" {
		c.Method = label(m.Method)
	}
	if m.Method == "tools/call" && m.Params.Name != "" {
		c.Tool = label(m.Params.Name)
	}
	return c
}

// label makes a value from a request safe for a metric label and a log line: letters,
// digits, and _ / . - only, and at most 64 bytes.
func label(s string) string {
	b := []byte(s)
	if len(b) > 64 {
		b = b[:64]
	}
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '/' || c == '.' || c == '-') {
			b[i] = '_'
		}
	}
	return string(b)
}

// statusWriter records a response's status, and passes Flush through.
type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.code, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// metrics counts requests by method, tool, and status, and their durations, for
// /metrics. Labels come from requests, so their number is capped.
type metrics struct {
	mu       sync.Mutex
	requests map[[3]string]int64
	seconds  map[[2]string]float64
	counts   map[[2]string]int64
}

// maxSeries caps the method and tool pairs metrics keeps; others count as "other".
const maxSeries = 100

func newMetrics() *metrics {
	return &metrics{requests: map[[3]string]int64{}, seconds: map[[2]string]float64{}, counts: map[[2]string]int64{}}
}

func (m *metrics) observe(method, tool string, code int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := [2]string{method, tool}
	if _, ok := m.counts[key]; !ok && len(m.counts) >= maxSeries {
		key = [2]string{"other", "other"}
	}
	m.requests[[3]string{key[0], key[1], fmt.Sprint(code)}]++
	m.seconds[key] += d.Seconds()
	m.counts[key]++
}

func (m *metrics) write(w io.Writer, inFlight int, ready bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintln(w, "# HELP tdm_requests_total MCP requests, by JSON-RPC method, tool, and HTTP status.")
	fmt.Fprintln(w, "# TYPE tdm_requests_total counter")
	keys := make([][3]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], "\x00") < strings.Join(keys[j][:], "\x00") })
	for _, k := range keys {
		fmt.Fprintf(w, "tdm_requests_total{method=%q,tool=%q,status=%q} %d\n", k[0], k[1], k[2], m.requests[k])
	}
	fmt.Fprintln(w, "# HELP tdm_request_duration_seconds Time spent answering MCP requests.")
	fmt.Fprintln(w, "# TYPE tdm_request_duration_seconds summary")
	pairs := make([][2]string, 0, len(m.counts))
	for k := range m.counts {
		pairs = append(pairs, k)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0]+"\x00"+pairs[i][1] < pairs[j][0]+"\x00"+pairs[j][1] })
	for _, k := range pairs {
		fmt.Fprintf(w, "tdm_request_duration_seconds_sum{method=%q,tool=%q} %g\n", k[0], k[1], m.seconds[k])
		fmt.Fprintf(w, "tdm_request_duration_seconds_count{method=%q,tool=%q} %d\n", k[0], k[1], m.counts[k])
	}
	fmt.Fprintln(w, "# HELP tdm_in_flight MCP requests being answered now.")
	fmt.Fprintln(w, "# TYPE tdm_in_flight gauge")
	fmt.Fprintf(w, "tdm_in_flight %d\n", inFlight)
	fmt.Fprintln(w, "# HELP tdm_ready Whether the server can answer: 1 once its index is loaded.")
	fmt.Fprintln(w, "# TYPE tdm_ready gauge")
	r := 0
	if ready {
		r = 1
	}
	fmt.Fprintf(w, "tdm_ready %d\n", r)
}
