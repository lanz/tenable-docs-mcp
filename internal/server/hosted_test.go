// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const listTools = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

// hostedServer serves a hosted server for the test, reached as 127.0.0.1.
func hostedServer(t *testing.T, o HostedOptions) (*httptest.Server, *hosted) {
	t.Helper()
	if o.AllowHosts == nil {
		o.AllowHosts = []string{"127.0.0.1", "docs.example.com"}
	}
	h, err := newHosted(New(Options{Hosted: true}), o)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.mux)
	t.Cleanup(ts.Close)
	return ts, h
}

func post(t *testing.T, url, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// An MCP client works against a hosted server, which leaves out the tools that write
// or send, and whose instructions don't mention them.
func TestHostedServesMCP(t *testing.T) {
	ts, _ := hostedServer(t, HostedOptions{})
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(res.Tools) == 0 {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	for _, off := range hostedOff {
		if slices.Contains(names, off) {
			t.Errorf("a hosted server lists %s", off)
		}
	}
	if !slices.Contains(names, "search_docs") || !slices.Contains(names, "verify_citations") {
		t.Errorf("tools = %v", names)
	}
	in := cs.InitializeResult().Instructions
	if strings.Contains(in, "report_gap") || strings.Contains(in, "open_feedback_form") || !strings.Contains(in, "feedback form is at the bottom") {
		t.Errorf("instructions:\n%s", in)
	}
	// A call works, with no session: any instance can answer any request.
	call, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_products", Arguments: map[string]any{}})
	if err != nil || call == nil {
		t.Fatalf("call: %v", err)
	}
	// Prompts don't offer report_gap.
	p, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "how-to", Arguments: map[string]string{"task": "install Nessus"}})
	if err != nil || strings.Contains(p.Messages[0].Content.(*mcp.TextContent).Text, "report_gap") {
		t.Errorf("prompt: %v", err)
	}
}

// Requests for other hosts, and cross-origin requests from browsers, are refused.
func TestHostedRefuses(t *testing.T) {
	ts, _ := hostedServer(t, HostedOptions{})
	for name, c := range map[string]struct {
		header map[string]string
		code   int
	}{
		"its host":          {map[string]string{"Host": "docs.example.com"}, http.StatusOK},
		"its host, a port":  {map[string]string{"Host": "DOCS.example.com:443"}, http.StatusOK},
		"another host":      {map[string]string{"Host": "evil.example"}, http.StatusMisdirectedRequest},
		"a look-alike host": {map[string]string{"Host": "docs.example.com.evil.example"}, http.StatusMisdirectedRequest},
		"a browser, cross-origin": {map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"},
			http.StatusForbidden},
	} {
		if resp, body := post(t, ts.URL+"/mcp", listTools, c.header); resp.StatusCode != c.code {
			t.Errorf("%s: HTTP %d, want %d: %s", name, resp.StatusCode, c.code, body)
		}
	}
	// Health checks come by address, so they don't check the host.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	req.Host = "10.0.0.5"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %v %v", resp, err)
	}
	if _, err := HostedHandler(New(Options{Hosted: true}), HostedOptions{}); err == nil {
		t.Error("no allowed hosts should be an error")
	}
}

// Each client has a rate limit; clients behind a proxy are told apart by its header.
func TestHostedRateLimit(t *testing.T) {
	ts, _ := hostedServer(t, HostedOptions{RatePerMinute: 6, Burst: 2, ClientIPHeader: "CF-Connecting-IP"})
	alice := map[string]string{"CF-Connecting-IP": "203.0.113.1"}
	for i := range 2 {
		if resp, _ := post(t, ts.URL+"/mcp", listTools, alice); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: HTTP %d", i+1, resp.StatusCode)
		}
	}
	resp, _ := post(t, ts.URL+"/mcp", listTools, alice)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("third: HTTP %d, Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if resp, _ := post(t, ts.URL+"/mcp", listTools, map[string]string{"CF-Connecting-IP": "203.0.113.2"}); resp.StatusCode != http.StatusOK {
		t.Errorf("another client: HTTP %d", resp.StatusCode)
	}
	// Without the header, the proxy's address is the client.
	ts2, _ := hostedServer(t, HostedOptions{RatePerMinute: 6, Burst: 1})
	post(t, ts2.URL+"/mcp", listTools, alice)
	if resp, _ := post(t, ts2.URL+"/mcp", listTools, map[string]string{"CF-Connecting-IP": "203.0.113.9"}); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the header isn't trusted unless named: HTTP %d", resp.StatusCode)
	}
}

// A request over the size limit is refused, and so is one past the limit in flight.
func TestHostedLimits(t *testing.T) {
	ts, h := hostedServer(t, HostedOptions{MaxBody: 100, MaxInFlight: 1})
	if resp, _ := post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"x":"`+strings.Repeat("a", 200)+`"}}`, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("large: HTTP %d", resp.StatusCode)
	}
	h.sem <- struct{}{} // one request in flight
	resp, _ := post(t, ts.URL+"/mcp", listTools, nil)
	<-h.sem
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("busy: HTTP %d", resp.StatusCode)
	}
	if resp, _ := post(t, ts.URL+"/mcp", listTools, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("after: HTTP %d", resp.StatusCode)
	}
}

// /readyz says not ready until the index is loaded.
func TestHostedReady(t *testing.T) {
	ready := false
	ts, _ := hostedServer(t, HostedOptions{Ready: func() bool { return ready }})
	get := func() int {
		resp, err := http.Get(ts.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get() != http.StatusServiceUnavailable {
		t.Error("ready before the index")
	}
	ready = true
	if get() != http.StatusOK {
		t.Error("not ready after")
	}
}

// Logs and metrics say which method and tool, but never what was asked, nor who asked.
func TestHostedLogsAndMetrics(t *testing.T) {
	var logs bytes.Buffer
	ts, _ := hostedServer(t, HostedOptions{Log: slog.New(slog.NewJSONHandler(&logs, nil)), ClientIPHeader: "CF-Connecting-IP"})
	secret := "how do I reset the password for acme-corp-admin"
	post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_docs","arguments":{"query":"`+secret+`"}}}`,
		map[string]string{"CF-Connecting-IP": "198.51.100.77"})
	post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"bad\"} name\n{"}}`, nil)
	line := logs.String()
	if !strings.Contains(line, `"method":"tools/call"`) || !strings.Contains(line, `"tool":"search_docs"`) ||
		strings.Contains(line, "acme") || strings.Contains(line, "198.51.100.77") {
		t.Errorf("log:\n%s", line)
	}
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := string(b)
	if !strings.Contains(m, `tdm_requests_total{method="tools/call",tool="search_docs",status="200"} 1`) ||
		!strings.Contains(m, `tool="bad___name__"`) || !strings.Contains(m, "tdm_ready 1") || strings.Contains(m, "acme") {
		t.Errorf("metrics:\n%s", m)
	}
}

// Metrics keep a bounded number of series, whatever names requests make up.
func TestHostedMetricsAreBounded(t *testing.T) {
	m := newMetrics()
	for i := range 500 {
		m.observe("tools/call", "tool"+strings.Repeat("x", i%300), 200, time.Millisecond)
	}
	if len(m.counts) > maxSeries+1 {
		t.Errorf("%d series", len(m.counts))
	}
}
