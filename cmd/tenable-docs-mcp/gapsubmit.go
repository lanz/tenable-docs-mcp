// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/feedback"
	"github.com/lanz/tenable-docs-mcp/internal/gaps"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/tools"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// gapsSender returns the sender gaps submit uses; tests replace it with one pointed at
// a fake endpoint.
var gapsSender = func() (*feedback.Sender, error) {
	logFile, err := feedback.DefaultLogFile()
	if err != nil {
		return nil, err
	}
	return &feedback.Sender{Endpoint: feedback.Endpoint, UserAgent: server.UserAgent(), LogFile: logFile}, nil
}

// errQuit is the user choosing to stop.
var errQuit = errors.New("quit")

// gapsSubmit offers each recorded question that hasn't been sent to Tenable's
// documentation team, one at a time. The user sends, edits, or skips each one; nothing
// is sent without their answer. Sent questions are marked so they aren't offered again.
func gapsSubmit(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" gaps submit", flag.ContinueOnError)
	file := fs.String("file", "", "the gaps file (default: tenable-docs-mcp/gaps.jsonl in the user's settings folder)")
	since := fs.String("since", "", "only questions recorded on or after this date (YYYY-MM-DD)")
	dir := fs.String("index", "", "the index folder, for the pages the questions are about (default: $TDM_INDEX_DIR, or the user's cache folder)")
	setUsage(fs, stderr, "gaps submit [-since YYYY-MM-DD] [-file FILE] [-index DIR]", "offer each recorded question that hasn't been sent to Tenable's documentation team, one at a time, to send, edit, or skip. Nothing is sent without your answer")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	var from time.Time
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			fmt.Fprintln(stderr, "gaps submit: -since takes a date such as 2026-11-01")
			return 2
		}
		from = t
	}
	path := *file
	if path == "" {
		p, err := gaps.DefaultPath()
		if err != nil {
			fmt.Fprintln(stderr, "gaps submit:", err)
			return 1
		}
		path = p
	}
	recs, err := gaps.Read(path)
	if err != nil {
		fmt.Fprintln(stderr, "gaps submit:", err)
		return 1
	}
	var pending []gaps.Record
	for _, r := range recs {
		if r.SentAt.IsZero() && !r.Time.Before(from) {
			pending = append(pending, r)
		}
	}
	if len(pending) == 0 {
		fmt.Fprintln(stdout, "No recorded questions are waiting to be sent.")
		return 0
	}
	sender, err := gapsSender()
	if err != nil {
		fmt.Fprintln(stderr, "gaps submit: can't send feedback from this computer:", err)
		return 1
	}

	// The index, to describe the page each question is about. Without one, questions
	// still send, about the docs home page.
	var env *tools.Env
	if d, err := indexDir(*dir); err == nil {
		if ix, err := index.Open(d); err == nil {
			defer ix.Close()
			env = tools.NewEnv(tools.Info{Name: server.Name, Version: version.Get()}, ix)
		}
	}
	if env == nil {
		env = tools.NewEnv(tools.Info{Name: server.Name, Version: version.Get()}, nil)
	}

	r := bufio.NewReader(in)
	ask := func(prompt string) (string, error) {
		fmt.Fprint(stdout, prompt)
		line, err := r.ReadString('\n')
		if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
			return "", errQuit
		}
		return strings.TrimSpace(line), nil
	}

	fmt.Fprintf(stdout, "%d recorded questions haven't been sent to Tenable's documentation team. %s\n", len(pending), feedback.Note)
	sent, skipped := 0, 0
	for i, rec := range pending {
		page := rec.URL
		if page == "" {
			page = env.GapPage(ctx, rec.Product)
		}
		fmt.Fprintf(stdout, "\n[%d of %d] %s\n", i+1, len(pending), rec.Question)
		fmt.Fprintf(stdout, "  Recorded %s", rec.Time.Local().Format("2006-01-02 15:04"))
		if rec.Product != "" {
			fmt.Fprintf(stdout, "; product: %s", rec.Product)
		}
		fmt.Fprintf(stdout, "\n  Page: %s\n", page)
		if rec.Note != "" {
			fmt.Fprintf(stdout, "  Note, which stays on this computer: %s\n", rec.Note)
		}
		answer, err := ask("Send it? [y]es, [e]dit it first, [s]kip, [q]uit: ")
		if err != nil {
			break
		}
		question := rec.Question
		switch strings.ToLower(answer) {
		case "y", "yes":
		case "e", "edit":
			edited, err := ask("Question (Enter keeps it as it is): ")
			if err != nil {
				return finishSubmit(stdout, sent, skipped, 0)
			}
			if edited != "" {
				question = edited
			}
		case "q", "quit":
			return finishSubmit(stdout, sent, skipped, 0)
		default:
			skipped++
			continue
		}
		details, err := ask("What were you trying to do? (optional, Enter skips): ")
		if err != nil {
			return finishSubmit(stdout, sent, skipped, 0)
		}
		email, err := ask("Your email, if you want the docs team to reply (optional, Enter skips): ")
		if err != nil {
			return finishSubmit(stdout, sent, skipped, 0)
		}
		p, err := feedback.Prepare(feedback.Feedback{URL: page, Gap: true, Question: question, Comment: details, Email: email,
			Page: env.PageDetails(ctx, page)}, version.Get())
		if err != nil {
			fmt.Fprintln(stdout, "Not sent:", err)
			skipped++
			continue
		}
		fmt.Fprintf(stdout, "\nThis is what would be sent:\n%s\n", indent(p.Preview, "  "))
		confirm, err := ask("Send this? [y]es, [n]o: ")
		if err != nil {
			return finishSubmit(stdout, sent, skipped, 0)
		}
		if c := strings.ToLower(confirm); c != "y" && c != "yes" {
			skipped++
			continue
		}
		// The limits are shared with the server, across every session on the computer.
		if err := sender.Check(); err != nil {
			fmt.Fprintf(stdout, "Not sent: %v. Run gaps submit again later.\n", err)
			return finishSubmit(stdout, sent, skipped, 1)
		}
		if err := sender.Send(ctx, p); err != nil {
			fmt.Fprintln(stdout, "Not sent:", err)
			return finishSubmit(stdout, sent, skipped, 1)
		}
		if _, err := gaps.MarkSent(path, rec.Time, rec.Question, time.Now()); err != nil {
			fmt.Fprintln(stderr, "gaps submit: sent, but couldn't mark it as sent:", err)
		}
		fmt.Fprintln(stdout, "Sent.")
		sent++
	}
	return finishSubmit(stdout, sent, skipped, 0)
}

func finishSubmit(stdout io.Writer, sent, skipped, code int) int {
	fmt.Fprintf(stdout, "\nSent %d; skipped %d.\n", sent, skipped)
	return code
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
