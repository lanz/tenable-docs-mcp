// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command tenable-docs-mcp is an MCP server that answers questions from Tenable's product
// documentation, with links to the exact section.
//
// Usage:
//
//	tenable-docs-mcp                       serve MCP over stdio (what MCP clients run)
//	tenable-docs-mcp serve [-http ADDR] [-index DIR]
//	                                       serve over stdio, or over streamable HTTP on
//	                                       ADDR (this machine only, e.g. 127.0.0.1:8080),
//	                                       from the index in DIR
//	tenable-docs-mcp verify-links [-base URL] [-live] FILE...
//	                                       check the docs links in answers or draft topics
//	tenable-docs-mcp gaps export|clear     summarize or clear the questions report_gap recorded
//	tenable-docs-mcp index pull|build|import|verify
//	                                       download or update the official index; build
//	                                       the index, or a snapshot of it, from local
//	                                       copies of the sites; import or check a snapshot
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

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/official"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

const usage = `Usage:
  tenable-docs-mcp                       serve MCP over stdio (what MCP clients run)
  tenable-docs-mcp serve [-http ADDR] [-index DIR]
                                         serve over stdio, or over streamable HTTP on ADDR
                                         (this machine only, for example 127.0.0.1:8080),
                                         from the documentation index in DIR (default:
                                         $TDM_INDEX_DIR, or tenable-docs-mcp/index in the
                                         user's cache folder)
  tenable-docs-mcp verify-links [-base URL] [-live] [-json] FILE...
                                         check the docs links in answers or draft topics
                                         (- reads standard input); exits 1 if any is broken
  tenable-docs-mcp gaps export [-since DATE] [-json]
                                         summarize the questions the docs didn't answer
  tenable-docs-mcp gaps clear -yes       delete the recorded questions
  tenable-docs-mcp index build -source "en=DIR;jp=DIR" [-sites en,jp] [-out DIR | -index DIR]
                                         build the index, or a snapshot of it, from local
                                         copies of the sites
  tenable-docs-mcp index pull [-sites en,jp] [-index DIR]
                                         download the official index, or update it
  tenable-docs-mcp index import [-sites en,jp] [-index DIR] SNAPSHOT
                                         build the index from a snapshot
  tenable-docs-mcp index verify SNAPSHOT check a snapshot against its manifest
  tenable-docs-mcp version               print the version
  tenable-docs-mcp help                  show this help
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// stdin is standard input, for verify-links -; tests replace it.
var stdin io.Reader = os.Stdin

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
	case "verify-links":
		return verifyLinks(ctx, rest, stdin, stdout, stderr)
	case "gaps":
		return gapsCmd(rest, stdout, stderr)
	case "index":
		return indexCmd(ctx, rest, stdout, stderr)
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
	indexDir := fs.String("index", "", "the documentation index's folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s serve: unexpected argument %q\n", server.Name, fs.Arg(0))
		return 2
	}
	ix := openIndex(*indexDir, stderr)
	if ix != nil {
		defer ix.Close()
	}
	gapsFile, err := gaps.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, server.Name+": no settings folder for recording gaps:", err)
	}
	var sender *feedback.Sender
	if logFile, err := feedback.DefaultLogFile(); err != nil {
		fmt.Fprintln(stderr, server.Name+": no settings folder for the feedback log, so open_feedback_form is off:", err)
	} else {
		sender = &feedback.Sender{Endpoint: feedback.Endpoint, UserAgent: server.UserAgent(), LogFile: logFile}
	}
	opts := server.Options{Index: ix, Live: site.NewHTTP(site.HTTPConfig{UserAgent: server.UserAgent()}), GapsFile: gapsFile, Feedback: sender}
	if ix != nil {
		if u, err := officialUpdater(ix, os.Getenv("TDM_SITES")); err != nil {
			fmt.Fprintln(stderr, server.Name+": TDM_INDEX_URL or TDM_SITES:", err)
			return 2
		} else if u != nil {
			opts.Updates = &official.Watcher{Updater: u, Log: func(msg string) { fmt.Fprintln(stderr, server.Name+": "+msg) }}
		}
	}
	srv := server.New(opts)
	if opts.Updates != nil {
		go opts.Updates.Run(ctx)
	}
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

// openIndex opens the documentation index, or returns nil (and says why on stderr) so
// the server can still start and report that the index isn't available.
func openIndex(dir string, stderr io.Writer) *index.Index {
	if dir == "" {
		dir = os.Getenv("TDM_INDEX_DIR")
	}
	if dir == "" {
		d, err := index.DefaultDir()
		if err != nil {
			fmt.Fprintln(stderr, server.Name+": no index folder:", err)
			return nil
		}
		dir = d
	}
	ix, err := index.Open(dir)
	if err != nil {
		fmt.Fprintln(stderr, server.Name+": can't open the index:", err)
		return nil
	}
	if len(ix.Keys()) == 0 {
		fmt.Fprintf(stderr, "%s: the index in %s is empty\n", server.Name, dir)
	}
	return ix
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
