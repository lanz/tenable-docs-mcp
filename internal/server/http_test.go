// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCheckLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:8080": true,
		"[::1]:8080":     true,
		"localhost:0":    true,
		"0.0.0.0:8080":   false,
		":8080":          false,
		"192.0.2.7:8080": false,
		"example.com:80": false,
		"no-port":        false,
	} {
		err := CheckLoopback(addr)
		if ok != (err == nil) {
			t.Errorf("CheckLoopback(%q) = %v, want ok=%v", addr, err, ok)
		}
		if !ok && strings.Contains(addr, ":") && addr != "no-port" && !errors.Is(err, ErrNotLoopback) {
			t.Errorf("CheckLoopback(%q) = %v, want ErrNotLoopback", addr, err)
		}
	}
}

func TestHTTPServesMCP(t *testing.T) {
	ts := httptest.NewServer(HTTPHandler(New(nil)))
	defer ts.Close()
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(res.Tools) == 0 {
		t.Fatalf("list tools over HTTP: %v, %d tools", err, len(res.Tools))
	}
}

func TestHTTPRefusesCrossOriginBrowserRequests(t *testing.T) {
	ts := httptest.NewServer(HTTPHandler(New(nil)))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin request: status %d, want 403", resp.StatusCode)
	}
}

func TestHTTPRefusesForeignHost(t *testing.T) {
	ts := httptest.NewServer(HTTPHandler(New(nil)))
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Host = "attacker.example" // DNS rebinding: the request reaches localhost under another name
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode < 400 {
		t.Errorf("request for a foreign Host: status %d, want a refusal", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	HTTPHandler(New(nil)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}
