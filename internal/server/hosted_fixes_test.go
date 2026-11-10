// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Clients that listen for change notifications don't hold a request slot: a hosted
// server refuses subscriptions/listen, and the clients carry on.
func TestHostedRefusesListen(t *testing.T) {
	ts, h := hostedServer(t, HostedOptions{MaxInFlight: 2})
	ctx := context.Background()
	for i := range 4 {
		opts := &mcp.ClientOptions{ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) {}}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts).
			Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
		if err != nil {
			t.Fatalf("client %d: connect: %v", i, err)
		}
		defer cs.Close()
		if _, err := cs.ListTools(ctx, nil); err != nil {
			t.Fatalf("client %d: list tools: %v", i, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(h.sem); n != 0 {
		t.Errorf("%d request slots held", n)
	}
	// The refusal is a JSON-RPC error with the request's ID.
	resp, body := post(t, ts.URL+"/mcp", `{"jsonrpc":"2.0","id":7,"method":"subscriptions/listen","params":{}}`, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"id":7`) || !strings.Contains(body, "isn't available") {
		t.Errorf("listen: HTTP %d %s", resp.StatusCode, body)
	}
}

// A request past the deadline gets an error on every protocol version, the tool's
// context is cancelled, and the timeout is counted.
func TestHostedDeadline(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cancelled := make(chan bool, 4)
	mcp.AddTool(srv, &mcp.Tool{Name: "slow", Description: "waits"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		select {
		case <-ctx.Done():
			cancelled <- true
		case <-time.After(3 * time.Second):
			cancelled <- false
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	h, err := newHosted(srv, HostedOptions{AllowHosts: []string{"127.0.0.1"}, RequestTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.mux)
	defer ts.Close()
	ctx := context.Background()
	for _, protocol := range []string{"2025-06-18", "2025-11-25", ""} {
		t.Run("protocol "+protocol, func(t *testing.T) {
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
				Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			start := time.Now()
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "slow", Arguments: map[string]any{}})
			took := time.Since(start)
			if err == nil && (res == nil || !res.IsError) {
				t.Errorf("no error after %v: %+v", took, res)
			}
			if err != nil && !strings.Contains(err.Error(), "took longer than") {
				t.Errorf("error = %v", err)
			}
			if took > 2*time.Second {
				t.Errorf("took %v", took)
			}
			select {
			case c := <-cancelled:
				if !c {
					t.Error("the tool's context wasn't cancelled")
				}
			case <-time.After(2 * time.Second):
				t.Error("the tool didn't finish")
			}
		})
	}
	var b strings.Builder
	h.metrics.write(&b, 0, true)
	if !strings.Contains(b.String(), `tdm_request_timeouts_total{method="tools/call",tool="slow"} 3`) {
		t.Errorf("metrics:\n%s", b.String())
	}
}

// The client header counts only as one valid address; IPv6 clients are their /64.
func TestHostedClientKey(t *testing.T) {
	h := &hosted{opts: HostedOptions{ClientIPHeader: "CF-Connecting-IP"}}
	for name, c := range map[string]struct {
		header []string
		want   string
	}{
		"an IPv4 address":   {[]string{"203.0.113.7"}, "203.0.113.7"},
		"an IPv6 address":   {[]string{"2001:db8:1:2:3:4:5:6"}, "2001:db8:1:2::/64"},
		"another in a /64":  {[]string{"2001:db8:1:2:ffff::1"}, "2001:db8:1:2::/64"},
		"a mapped address":  {[]string{"::ffff:203.0.113.7"}, "203.0.113.7"},
		"not an address":    {[]string{strings.Repeat("x", 60000)}, "192.0.2.1"},
		"a forwarded chain": {[]string{"6.6.6.6, 203.0.113.9"}, "192.0.2.1"},
		"two headers":       {[]string{"6.6.6.6", "203.0.113.9"}, "192.0.2.1"},
		"no header":         {nil, "192.0.2.1"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.RemoteAddr = "192.0.2.1:5555"
		for _, v := range c.header {
			r.Header.Add("CF-Connecting-IP", v)
		}
		if got := h.clientKey(r); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// The limiter tracks a bounded number of clients; past it, new ones share a limit.
func TestHostedClientsAreBounded(t *testing.T) {
	h := &hosted{opts: HostedOptions{RatePerMinute: 60, Burst: 1}, clients: map[string]*client{}}
	now := time.Now()
	for i := range maxClients + 500 {
		h.allow(fmt.Sprintf("client-%d", i), now)
	}
	if n := len(h.clients); n > maxClients+1 {
		t.Errorf("%d clients tracked", n)
	}
	// New clients past the cap share the overflow limit, which is now spent.
	if _, ok := h.allow("one-more", now); ok {
		t.Error("a new client past the cap wasn't limited")
	}
}

// Host names are checked when the server starts, and IPv6 hosts match with or
// without brackets.
func TestHostedHosts(t *testing.T) {
	for in, want := range map[string]string{
		"Docs.Tenable.com":  "docs.tenable.com",
		"docs.tenable.com.": "docs.tenable.com",
		"[::1]":             "::1",
		"::1":               "::1",
	} {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"docs.tenable.com:443", "", "  "} {
		if _, err := NormalizeHost(bad); err == nil {
			t.Errorf("NormalizeHost(%q) should fail", bad)
		}
	}
	if _, err := HostedHandler(New(Options{Hosted: true}), HostedOptions{AllowHosts: []string{"docs.tenable.com:443"}}); err == nil {
		t.Error("a host with a port should be refused")
	}
	h := &hosted{hosts: []string{"::1"}}
	for _, host := range []string{"[::1]", "[::1]:8080"} {
		if !h.allowedHost(host) {
			t.Errorf("%s refused", host)
		}
	}
}

// Metrics name only known methods and the server's own tools.
func TestHostedKnownNames(t *testing.T) {
	_, h := hostedServer(t, HostedOptions{})
	for _, c := range [][4]string{
		{"tools/call", "search_docs", "tools/call", "search_docs"},
		{"tools/call", "report_gap", "tools/call", "other"},
		{"tools/call", "made_up", "tools/call", "other"},
		{"made/up", "-", "other", "-"},
		{"initialize", "-", "initialize", "-"},
	} {
		if m, tl := h.known(c[0], c[1]); m != c[2] || tl != c[3] {
			t.Errorf("known(%s, %s) = %s, %s", c[0], c[1], m, tl)
		}
	}
}
