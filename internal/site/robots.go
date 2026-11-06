// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Robots holds the robots.txt rules that apply to this client, following RFC 9309: the
// group for the client's product token if there is one, otherwise the "*" group. The
// longest matching rule wins, and Allow wins a tie.
type Robots struct {
	rules []robotsRule
}

type robotsRule struct {
	allow   bool
	pattern string
	re      *regexp.Regexp
}

// ParseRobots reads robots.txt rules for the client whose product token is agent (for
// example "tenable-docs-mcp").
func ParseRobots(r io.Reader, agent string) (*Robots, error) {
	agent = strings.ToLower(agent)
	type group struct {
		agents []string
		rules  []robotsRule
	}
	var groups []*group
	var cur *group
	lastWasAgent := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if cur == nil || !lastWasAgent {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if cur == nil || value == "" {
				continue // an empty Disallow allows everything
			}
			cur.rules = append(cur.rules, robotsRule{allow: key == "allow", pattern: value, re: compileRobotsPattern(value)})
		default:
			lastWasAgent = false
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var star, mine []robotsRule
	foundMine := false
	for _, g := range groups {
		for _, a := range g.agents {
			switch {
			case a == "*":
				star = append(star, g.rules...)
			case agent != "" && a == agent:
				mine = append(mine, g.rules...)
				foundMine = true
			}
		}
	}
	if foundMine {
		return &Robots{rules: mine}, nil
	}
	return &Robots{rules: star}, nil
}

// AllowAll returns rules that allow every path, for a site with no robots.txt.
func AllowAll() *Robots { return &Robots{} }

// Allowed reports whether the client may fetch the path (with its query, if any).
func (rb *Robots) Allowed(path string) bool {
	if rb == nil {
		return true
	}
	if path == "" {
		path = "/"
	}
	best, allowed := -1, true
	for _, r := range rb.rules {
		if !r.re.MatchString(path) {
			continue
		}
		n := len(r.pattern)
		if n > best || (n == best && r.allow) {
			best, allowed = n, r.allow
		}
	}
	return allowed
}

// compileRobotsPattern turns a robots.txt pattern into an anchored regular expression:
// the pattern is a path prefix, * matches any run of characters, and a trailing $
// anchors the end.
func compileRobotsPattern(pattern string) *regexp.Regexp {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	expr := "^" + strings.Join(parts, ".*")
	if anchored {
		expr += "$"
	}
	return regexp.MustCompile(expr)
}

// FetchRobots gets a site's robots.txt rules. A missing robots.txt allows everything.
func FetchRobots(ctx context.Context, src Source, s Site, agent string) (*Robots, error) {
	resp, err := src.Fetch(ctx, Request{URL: s.RobotsURL()})
	if errors.Is(err, ErrNotFound) {
		return AllowAll(), nil
	}
	if err != nil {
		return nil, err
	}
	return ParseRobots(bytes.NewReader(resp.Body), agent)
}
