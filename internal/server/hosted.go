// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
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
		host, err := NormalizeHost(a)
		if err != nil {
			return nil, err
		}
		h.hosts = append(h.hosts, host)
	}
	// Metrics and logs name only the server's own tools; any other name a request makes
	// up is "other".
	names, err := toolNames(srv)
	if err != nil {
		return nil, err
	}
	h.tools = names
	// The deadline is applied inside MCP, where it reaches the tool on every protocol
	// version, and a late request gets an error rather than an empty response.
	srv.AddReceivingMiddleware(h.deadline)
	// The SDK's DNS-rebinding check is for servers on localhost; a hosted server checks
	// the Host header against AllowHosts instead, which also covers a proxy on the same
	// machine.
	h.mcp = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		DisableLocalhostProtection:   true,
		PropagateRequestCancellation: true,
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
	tools map[string]bool
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
	call.Method, call.Tool = h.known(call.Method, call.Tool)
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
		var id json.RawMessage
		*call, id = parseCall(body)
		// subscriptions/listen holds a stream open for change notifications, which mean
		// little across stateless instances, and would hold a slot all the while.
		if call.Method == "subscriptions/listen" {
			return refuse(w, id, "subscriptions/listen isn't available on this server: it's stateless, and its tools and resources don't change while it runs")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	// The deadline middleware answers first; this one only backs it up, so the slot is
	// freed even if a response can't be written.
	ctx, cancel := context.WithTimeout(r.Context(), h.opts.RequestTimeout+10*time.Second)
	defer cancel()
	sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	h.mcp.ServeHTTP(sw, r.WithContext(ctx))
	return sw.code
}

// refuse answers a JSON-RPC request with an error, as MCP clients expect.
func refuse(w http.ResponseWriter, id json.RawMessage, msg string) int {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": msg},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return http.StatusOK
}

// NormalizeHost checks and normalizes a host name a hosted server answers for: lower
// case, without a trailing dot or IPv6 brackets. A port isn't allowed: the Host
// header's port is ignored.
func NormalizeHost(h string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" {
		return "", fmt.Errorf("an empty host name")
	}
	if strings.Contains(host, ":") {
		if _, err := netip.ParseAddr(host); err != nil {
			return "", fmt.Errorf("host %q: give the name without a port", h)
		}
	}
	return host, nil
}

func (h *hosted) allowedHost(host string) bool {
	if hh, _, err := net.SplitHostPort(host); err == nil {
		host = hh
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	for _, a := range h.hosts {
		if host == a {
			return true
		}
	}
	return false
}

// clientKey is who a request is from, for its rate limit: the address the trusted
// proxy gives in ClientIPHeader, if that's one valid address, or else the connection's.
// An IPv6 client is its /64 network, which one user can rotate through freely.
func (h *hosted) clientKey(r *http.Request) string {
	if name := h.opts.ClientIPHeader; name != "" {
		if vals := r.Header.Values(name); len(vals) == 1 {
			if a, err := netip.ParseAddr(strings.TrimSpace(vals[0])); err == nil {
				return addrKey(a)
			}
		}
	}
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		return addrKey(ap.Addr())
	}
	return "unknown"
}

func addrKey(a netip.Addr) string {
	a = a.Unmap()
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().String()
	}
	return a.String()
}

// maxClients caps the clients the rate limiter tracks. Past it, new clients share one
// limit, so memory stays bounded whoever sends requests.
const maxClients = 50_000

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
	if c == nil && len(h.clients) >= maxClients {
		key = "overflow"
		c = h.clients[key]
	}
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

func parseCall(body []byte) (mcpCall, json.RawMessage) {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	c := mcpCall{Method: "-", Tool: "-"}
	if json.Unmarshal(body, &m) != nil {
		return c, nil
	}
	if m.Method != "" {
		c.Method = label(m.Method)
	}
	if m.Method == "tools/call" && m.Params.Name != "" {
		c.Tool = label(m.Params.Name)
	}
	return c, m.ID
}

// methods are the MCP methods metrics and logs name; any other is "other".
var methods = map[string]bool{
	"initialize": true, "ping": true, "tools/list": true, "tools/call": true,
	"resources/list": true, "resources/read": true, "resources/templates/list": true,
	"resources/subscribe": true, "resources/unsubscribe": true, "prompts/list": true,
	"prompts/get": true, "completion/complete": true, "logging/setLevel": true,
	"subscriptions/listen": true, "notifications/initialized": true, "notifications/cancelled": true,
	"notifications/roots/list_changed": true, "-": true,
}

// known maps a request's method and tool to the names metrics and logs use: the known
// methods and the server's own tools, and "other" for anything else.
func (h *hosted) known(method, tool string) (string, string) {
	if !methods[method] {
		method = "other"
	}
	if tool != "-" && !h.tools[tool] {
		tool = "other"
	}
	return method, tool
}

// toolNames lists the server's tools, by asking it as a client would.
func toolNames(srv *mcp.Server) (map[string]bool, error) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: Name + "-metrics", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()
	names := map[string]bool{}
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		names[t.Name] = true
	}
	return names, nil
}

// deadline stops a request that runs past RequestTimeout, and answers it with an
// error. The handler's context is cancelled, so a tool that checks it stops too.
func (h *hosted) deadline(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ctx, cancel := context.WithTimeout(ctx, h.opts.RequestTimeout)
		defer cancel()
		type answer struct {
			res mcp.Result
			err error
		}
		done := make(chan answer, 1)
		go func() {
			res, err := next(ctx, method, req)
			done <- answer{res, err}
		}()
		select {
		case a := <-done:
			return a.res, a.err
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ctx.Err()
			}
			tool := "-"
			if c, ok := req.(*mcp.CallToolRequest); ok && c.Params != nil {
				tool = label(c.Params.Name)
			}
			m, t := h.known(label(method), tool)
			h.metrics.timedOut(m, t)
			if h.opts.Log != nil {
				h.opts.Log.Warn("timed out", "method", m, "tool", t, "after_ms", h.opts.RequestTimeout.Milliseconds())
			}
			return nil, fmt.Errorf("the request took longer than %s and was stopped; try again, or narrow it", h.opts.RequestTimeout)
		}
	}
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
	timeouts map[[2]string]int64
}

// maxSeries caps the method and tool pairs metrics keeps; others count as "other".
const maxSeries = 100

func newMetrics() *metrics {
	return &metrics{requests: map[[3]string]int64{}, seconds: map[[2]string]float64{}, counts: map[[2]string]int64{},
		timeouts: map[[2]string]int64{}}
}

// timedOut counts a request the deadline stopped. Its method and tool are known names.
func (m *metrics) timedOut(method, tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeouts[[2]string{method, tool}]++
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
	fmt.Fprintln(w, "# HELP tdm_request_timeouts_total MCP requests stopped at the request deadline.")
	fmt.Fprintln(w, "# TYPE tdm_request_timeouts_total counter")
	late := make([][2]string, 0, len(m.timeouts))
	for k := range m.timeouts {
		late = append(late, k)
	}
	sort.Slice(late, func(i, j int) bool { return late[i][0]+"\x00"+late[i][1] < late[j][0]+"\x00"+late[j][1] })
	for _, k := range late {
		fmt.Fprintf(w, "tdm_request_timeouts_total{method=%q,tool=%q} %d\n", k[0], k[1], m.timeouts[k])
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
