// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Some guides are published without FullToc.xml. Their table of contents is only in
// Flare's JavaScript files: Data/HelpSystem.xml names the main file (for example
// Data/Tocs/Guide.js), which holds the tree of entry numbers and the number of chunk
// files. Each chunk (Data/Tocs/Guide_Chunk0.js, …) maps a link to the entry numbers
// that use it and their titles. Entries that only group others are under the link
// "___".

// HelpSystemURL returns the URL of the guide's Flare help-system file.
func (g Guide) HelpSystemURL() string { return g.RootURL + "/Data/HelpSystem.xml" }

// maxTocChunks caps the chunk files read for one table of contents.
const maxTocChunks = 200

// FetchJSToc gets a guide's table of contents from Flare's JavaScript files.
func FetchJSToc(ctx context.Context, src Source, guide Guide) ([]*TocEntry, error) {
	resp, err := src.Fetch(ctx, Request{URL: guide.HelpSystemURL()})
	if err != nil {
		return nil, err
	}
	tocPath, err := helpSystemToc(resp.Body)
	if err != nil {
		return nil, err
	}
	tocURL := guide.RootURL + "/" + strings.TrimPrefix(tocPath, "/")
	resp, err = src.Fetch(ctx, Request{URL: tocURL})
	if err != nil {
		return nil, err
	}
	main, err := parseDefine(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tocURL, err)
	}
	m, _ := main.(map[string]any)
	n, _ := m["numchunks"].(float64)
	prefix, _ := m["prefix"].(string)
	if n < 0 || n > maxTocChunks || n != float64(int(n)) || strings.ContainsAny(prefix, "/\\") {
		return nil, fmt.Errorf("%s: unexpected chunks (%v, %q)", tocURL, n, prefix)
	}
	chunks := make([]any, int(n))
	for i := range chunks {
		chunkURL := tocURL[:strings.LastIndex(tocURL, "/")+1] + prefix + strconv.Itoa(i) + ".js"
		resp, err := src.Fetch(ctx, Request{URL: chunkURL})
		if err != nil {
			return nil, err
		}
		if chunks[i], err = parseDefine(resp.Body); err != nil {
			return nil, fmt.Errorf("%s: %w", chunkURL, err)
		}
	}
	return buildJSToc(main, chunks, guide)
}

func helpSystemToc(data []byte) (string, error) {
	var hs struct {
		Toc string `xml:"Toc,attr"`
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if err := xml.Unmarshal(data, &hs); err != nil {
		return "", fmt.Errorf("help system: %w", err)
	}
	p := path.Clean("/" + strings.TrimSpace(hs.Toc))
	if hs.Toc == "" || !strings.HasSuffix(p, ".js") {
		return "", fmt.Errorf("help system names no JavaScript table of contents (%q)", hs.Toc)
	}
	return p, nil
}

type jsTocItem struct{ title, link string }

// buildJSToc joins the tree in the main file with the titles and links in the chunks.
func buildJSToc(main any, chunks []any, guide Guide) ([]*TocEntry, error) {
	items := map[int]jsTocItem{}
	for _, c := range chunks {
		m, _ := c.(map[string]any)
		for link, v := range m {
			e, _ := v.(map[string]any)
			ids, _ := e["i"].([]any)
			titles, _ := e["t"].([]any)
			marks, _ := e["b"].([]any)
			for k, id := range ids {
				n, ok := id.(float64)
				if !ok {
					continue
				}
				it := jsTocItem{link: link}
				if k < len(titles) {
					it.title, _ = titles[k].(string)
				}
				if k < len(marks) {
					if b, _ := marks[k].(string); b != "" && link != "___" {
						it.link = link + "#" + strings.TrimPrefix(b, "#")
					}
				}
				if link == "___" {
					it.link = ""
				}
				items[int(n)] = it
			}
		}
	}
	m, _ := main.(map[string]any)
	tree, _ := m["tree"].(map[string]any)
	if tree == nil {
		return nil, errors.New("table of contents: no tree")
	}
	base, err := url.Parse(guide.RootURL + "/")
	if err != nil {
		return nil, err
	}
	var convert func(nodes []any) []*TocEntry
	convert = func(nodes []any) []*TocEntry {
		out := make([]*TocEntry, 0, len(nodes))
		for _, v := range nodes {
			node, _ := v.(map[string]any)
			n, ok := node["i"].(float64)
			if !ok {
				continue
			}
			kids, _ := node["n"].([]any)
			it := items[int(n)]
			out = append(out, &TocEntry{
				Title:    strings.TrimSpace(it.title),
				URL:      resolveTocLink(base, it.link),
				Children: convert(kids),
			})
		}
		return out
	}
	nodes, _ := tree["n"].([]any)
	return convert(nodes), nil
}

// parseDefine reads the object literal in a Flare data file, "define({...});".
func parseDefine(data []byte) (any, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "define(") {
		return nil, errors.New("not a Flare data file")
	}
	p := &jsParser{s: s, i: len("define(")}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if !strings.HasPrefix(s[p.i:], ")") {
		return nil, p.errorf("expected )")
	}
	return v, nil
}

// jsParser reads the subset of JavaScript literals Flare writes: objects with bare or
// quoted keys, arrays, strings in single or double quotes, numbers, true, false, and
// null.
type jsParser struct {
	s string
	i int
}

const maxJSDepth = 100

func (p *jsParser) errorf(format string, args ...any) error {
	return fmt.Errorf("offset %d: "+format, append([]any{p.i}, args...)...)
}

func (p *jsParser) space() {
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.i]) >= 0 {
		p.i++
	}
}

func (p *jsParser) value(depth int) (any, error) {
	if depth > maxJSDepth {
		return nil, p.errorf("nested too deeply")
	}
	p.space()
	if p.i >= len(p.s) {
		return nil, p.errorf("unexpected end")
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '\'' || c == '"':
		return p.str()
	case c == '-' || c >= '0' && c <= '9':
		j := p.i + 1
		for j < len(p.s) && strings.IndexByte("0123456789.eE+-", p.s[j]) >= 0 {
			j++
		}
		f, err := strconv.ParseFloat(p.s[p.i:j], 64)
		if err != nil {
			return nil, p.errorf("bad number")
		}
		p.i = j
		return f, nil
	default:
		w := p.word()
		switch w {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return nil, p.errorf("unexpected %q", w)
	}
}

func (p *jsParser) word() string {
	j := p.i
	for j < len(p.s) {
		c := p.s[j]
		if c != '_' && c != '$' && (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			break
		}
		j++
	}
	w := p.s[p.i:j]
	p.i = j
	return w
}

func (p *jsParser) object(depth int) (any, error) {
	p.i++ // {
	m := map[string]any{}
	for {
		p.space()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return m, nil
		}
		var key string
		if p.i < len(p.s) && (p.s[p.i] == '\'' || p.s[p.i] == '"') {
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			key = k
		} else if key = p.word(); key == "" {
			return nil, p.errorf("expected a key")
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, p.errorf("expected :")
		}
		p.i++
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		m[key] = v
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		} else if p.i >= len(p.s) || p.s[p.i] != '}' {
			return nil, p.errorf("expected , or }")
		}
	}
}

func (p *jsParser) array(depth int) (any, error) {
	p.i++ // [
	var a []any
	for {
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return a, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		a = append(a, v)
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
		} else if p.i >= len(p.s) || p.s[p.i] != ']' {
			return nil, p.errorf("expected , or ]")
		}
	}
}

func (p *jsParser) str() (string, error) {
	q := p.s[p.i]
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == q:
			p.i++
			return b.String(), nil
		case c == '\\' && p.i+1 < len(p.s):
			e := p.s[p.i+1]
			p.i += 2
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'u', 'x':
				n := 4
				if e == 'x' {
					n = 2
				}
				if p.i+n > len(p.s) {
					return "", p.errorf("bad escape")
				}
				r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
				if err != nil {
					return "", p.errorf("bad escape")
				}
				b.WriteRune(rune(r))
				p.i += n
			default:
				b.WriteByte(e)
			}
		case c == '\n':
			return "", p.errorf("line break in a string")
		default:
			_, size := utf8.DecodeRuneInString(p.s[p.i:])
			b.WriteString(p.s[p.i : p.i+size])
			p.i += size
		}
	}
	return "", p.errorf("unterminated string")
}
