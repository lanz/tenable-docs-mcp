// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-documentation-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command tenable-docs-mcp is an MCP server that answers questions from Tenable's product
// documentation, with links to the exact section.
//
// Usage:
//
//	tenable-docs-mcp                       serve MCP over stdio (what MCP clients run)
//	tenable-docs-mcp serve [-http ADDR]    serve over stdio, or over streamable HTTP on
//	                                       ADDR (this machine only, e.g. 127.0.0.1:8080)
//	tenable-docs-mcp version               print the version
//
// Over stdio, stdout carries only protocol messages; diagnostics go to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-documentation-mcp/internal/server"
	"github.com/lanz/tenable-documentation-mcp/internal/version"
)

const usage = `Usage:
  tenable-docs-mcp                       serve MCP over stdio (what MCP clients run)
  tenable-docs-mcp serve [-http ADDR]    serve over stdio, or over streamable HTTP on ADDR
                                         (this machine only, for example 127.0.0.1:8080)
  tenable-docs-mcp version               print the version
  tenable-docs-mcp help                  show this help
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd, rest := "serve", args
	if len(args) > 0 {
		switch a := args[0]; {
		case a == "-h" || a == "-help" || a == "--help":
			cmd, rest = "help", nil
		case a != "" && a[0] != '-':
			cmd, rest = a, args[1:]
		}
	}
	switch cmd {
	case "serve":
		return serve(ctx, rest, stderr)
	case "version":
		fmt.Fprintln(stdout, server.Name, version.Get())
		return 0
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n\n%s", server.Name, cmd, usage)
		return 2
	}
}

func serve(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	httpAddr := fs.String("http", "", "serve streamable HTTP on this address (this machine only), instead of stdio")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s serve: unexpected argument %q\n", server.Name, fs.Arg(0))
		return 2
	}
	srv := server.New()
	if *httpAddr == "" {
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, server.Name+":", err)
			return 1
		}
		return 0
	}
	if err := server.CheckLoopback(*httpAddr); err != nil {
		fmt.Fprintln(stderr, server.Name+":", err)
		return 2
	}
	return serveHTTP(ctx, server.NewHTTPServer(*httpAddr, server.HTTPHandler(srv)), stderr)
}

func serveHTTP(ctx context.Context, hs *http.Server, stderr io.Writer) int {
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	fmt.Fprintf(stderr, "%s: serving MCP at http://%s/mcp\n", server.Name, hs.Addr)
	select {
	case err := <-errc:
		fmt.Fprintln(stderr, server.Name+":", err)
		return 1
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hs.Shutdown(sctx); err != nil {
		fmt.Fprintln(stderr, server.Name+": shutdown:", err)
		return 1
	}
	return 0
}
