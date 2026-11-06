// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPHandler serves MCP over streamable HTTP at /mcp, with a health check at /healthz.
//
// The SDK rejects requests whose Host header isn't a loopback name (DNS rebinding), and
// cross-origin browser requests are refused as well, so a web page can't drive a local
// server.
func HTTPHandler(srv *mcp.Server) http.Handler {
	mux := http.NewServeMux()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(mcpHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// ErrNotLoopback is returned for an HTTP address that isn't on this machine.
var ErrNotLoopback = errors.New("the HTTP server only listens on this machine (127.0.0.1, ::1, or localhost)")

// CheckLoopback refuses listen addresses other than loopback ones. A hosted mode with its
// own safeguards comes later.
func CheckLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w; got %q", ErrNotLoopback, addr)
}

// NewHTTPServer returns an http.Server for the handler with conservative timeouts.
// WriteTimeout stays unset: streamed responses can stay open for a long time.
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
}
