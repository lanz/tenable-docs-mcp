// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Command tenable-docs-mcp is an MCP server that answers questions from Tenable's product
// documentation, with links to the exact section.
//
// Run with no arguments, it's the MCP server over stdio, as MCP clients start it; run
// with a command, it's a command-line tool. `tenable-docs-mcp help` lists the commands.
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/crawl"
	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

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
		if u, ok := updateAvailable(""); ok {
			fmt.Fprintln(stdout, u.Message())
		}
		return 0
	case "help":
		return help(ctx, rest, stdout, stderr)
	case "doctor":
		return doctor(ctx, rest, stdout, stderr)
	case "config":
		return configCmd(rest, stdout, stderr)
	default:
		if c, ok := findToolCommand(cmd); ok {
			return runTool(ctx, c, rest, stdout, stderr)
		}
		unknownCommand(stderr, server.Name, cmd, commandNames())
		return 2
	}
}

func serve(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" serve", flag.ContinueOnError)
	setUsage(fs, stderr, "serve [-http ADDR] [-index DIR]",
		"run the MCP server over stdio, as MCP clients start it, or with -http, over streamable HTTP on this computer",
		"With no command at all, tenable-docs-mcp does the same as serve.")
	httpAddr := fs.String("http", "", "serve streamable HTTP on this address (this machine only), instead of stdio")
	indexDir := fs.String("index", "", "the documentation index's folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
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
	live := site.NewHTTP(site.HTTPConfig{UserAgent: server.UserAgent()})
	opts := server.Options{Index: ix, Live: live, GapsFile: gapsFile, Feedback: sender, Synonyms: loadSynonyms(stderr)}
	if ix != nil {
		u, err := publishedUpdater(ix, os.Getenv("TDM_SITES"))
		if err != nil {
			fmt.Fprintln(stderr, server.Name+": TDM_INDEX_URL or TDM_SITES:", err)
			return 2
		}
		c, err := liveCrawler(ix, live, os.Getenv("TDM_SITES"))
		if err != nil {
			fmt.Fprintln(stderr, server.Name+": TDM_CRAWL or TDM_SITES:", err)
			return 2
		}
		if u != nil {
			opts.Updates = &published.Watcher{Updater: u, Crawler: c, Log: func(msg string) { fmt.Fprintln(stderr, server.Name+": "+msg) }}
		}
		if c != nil {
			opts.OnDemand = &crawl.OnDemand{Crawler: c}
		}
	}
	srv := server.New(opts)
	if opts.Updates != nil {
		go opts.Updates.Run(ctx)
	}
	if *httpAddr == "" {
		if stdinIsTerminal() && isTerminalWriter(stderr) {
			launchScreen(stderr, ix, "")
		}
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
	if isTerminalWriter(stderr) {
		launchScreen(stderr, ix, *httpAddr)
	}
	return serveHTTP(ctx, server.NewHTTPServer(*httpAddr, server.HTTPHandler(srv)), stderr)
}

// help writes the full help, or a command's: help search is search -h.
func help(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		writeHelp(stdout)
		return 0
	case len(args) == 1 && args[0] == "gaps":
		writeGroupHelp(stdout, "gaps", "Summarize, send, or clear the questions report_gap recorded", gapsRows)
		return 0
	case len(args) == 1 && args[0] == "index":
		writeGroupHelp(stdout, "index", "Download, build, update, or check the documentation index", indexRows)
		return 0
	case args[0] == "help":
		return help(ctx, args[1:], stdout, stderr)
	}
	return run(ctx, append(args, "-h"), stdout, stderr)
}

// isTerminalWriter reports whether w is a terminal.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

// synonymsPath returns the user's synonyms file: $TDM_SYNONYMS, or synonyms.txt in the
// settings folder.
func synonymsPath() (string, error) {
	if p := os.Getenv("TDM_SYNONYMS"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tenable-docs-mcp", "synonyms.txt"), nil
}

// loadSynonyms returns the shipped synonyms with the user's added. A problem with the
// user's file is reported on stderr, and the shipped synonyms are used alone.
func loadSynonyms(stderr io.Writer) *search.Synonyms {
	path, err := synonymsPath()
	if err != nil {
		return search.DefaultSynonyms()
	}
	s, err := search.LoadSynonyms(path)
	if err != nil {
		fmt.Fprintln(stderr, server.Name+": your synonyms aren't used:", err)
	}
	return s
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

// updateAvailable reports whether the last update check, recorded in the index folder,
// found a newer release than this one. It makes no request.
func updateAvailable(dir string) (version.Update, bool) {
	d, err := indexDir(dir)
	if err != nil {
		return version.Update{}, false
	}
	st, err := published.ReadState(d)
	if err != nil {
		return version.Update{}, false
	}
	return version.Available(st.LatestRelease, version.Get())
}
