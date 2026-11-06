// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// mapSource serves documents from memory, and counts requests.
type mapSource struct {
	docs     map[string]string
	requests int
}

func (m *mapSource) Fetch(_ context.Context, req Request) (*Response, error) {
	m.requests++
	body, ok := m.docs[req.URL]
	if !ok {
		return &Response{URL: req.URL, Status: 404}, fmt.Errorf("fetch %s: %w", req.URL, ErrNotFound)
	}
	return &Response{URL: req.URL, Status: 200, Body: []byte(body)}, nil
}

// A made-up guide published the way Flare writes guides without FullToc.xml: the help
// system names the main file, which holds the tree and points at two chunk files.
const (
	jsGuideRoot   = "https://kr.docs.tenable.com/widget/SaaS"
	jsHelpSystem  = "\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8"?><WebHelpSystem DefaultUrl="Content/Welcome.htm" Toc="Data/Tocs/Widget.js" Index="Data/Index.js"></WebHelpSystem>`
	jsTocMain     = `define({numchunks:2,prefix:'Widget_Chunk',chunkstart:['/Content/A.htm','/Content/M.htm'],tree:{n:[{i:0,c:0},{i:1,c:1,n:[{i:2,c:0,n:[{i:3,c:1}]},{i:4,f:'_blank',c:0}]}]}});`
	jsTocChunk0   = `define({'/Content/Welcome.htm':{i:[0],t:['시작하기'],b:['']},'/Content/Setup/Install.htm':{i:[2],t:['설치 \'빠른\' 안내'],b:['#Steps']},'https://docs.tenable.com/release-notes/Content/widget.htm':{i:[4],t:["Release \"Notes\""],b:['']}});`
	jsTocChunk1   = `define({'___':{i:[1],t:['관리 \u0026 설정'],b:['']},'/Content/Setup/Ports.htm':{i:[3],t:['포트'],b:['']}});`
	jsTocMainURL  = jsGuideRoot + "/Data/Tocs/Widget.js"
	jsTocChunkURL = jsGuideRoot + "/Data/Tocs/Widget_Chunk"
)

func jsGuideSource() *mapSource {
	return &mapSource{docs: map[string]string{
		jsGuideRoot + "/Data/HelpSystem.xml": jsHelpSystem,
		jsTocMainURL:                         jsTocMain,
		jsTocChunkURL + "0.js":               jsTocChunk0,
		jsTocChunkURL + "1.js":               jsTocChunk1,
	}}
}

func TestFetchTocFallsBackToJavaScript(t *testing.T) {
	src := jsGuideSource()
	g, _ := GuideOf(jsGuideRoot + "/Content/Welcome.htm")
	toc, err := FetchToc(context.Background(), src, g)
	if err != nil {
		t.Fatal(err)
	}
	want := []*TocEntry{
		{Title: "시작하기", URL: jsGuideRoot + "/Content/Welcome.htm", Children: []*TocEntry{}},
		{Title: "관리 & 설정", Children: []*TocEntry{
			{Title: "설치 '빠른' 안내", URL: jsGuideRoot + "/Content/Setup/Install.htm#Steps", Children: []*TocEntry{
				{Title: "포트", URL: jsGuideRoot + "/Content/Setup/Ports.htm", Children: []*TocEntry{}},
			}},
			{Title: `Release "Notes"`, URL: "https://docs.tenable.com/release-notes/Content/widget.htm", Children: []*TocEntry{}},
		}},
	}
	if !reflect.DeepEqual(toc, want) {
		t.Errorf("toc = %s\nwant  %s", dumpToc(toc), dumpToc(want))
	}
	if src.requests != 5 { // FullToc.xml (missing), the help system, the main file, and two chunks
		t.Errorf("%d requests", src.requests)
	}
	if got := TocTrail(toc, jsGuideRoot+"/Content/Setup/Ports.htm#Top"); !reflect.DeepEqual(got, []string{"관리 & 설정", "설치 '빠른' 안내", "포트"}) {
		t.Errorf("trail = %q", got)
	}
	if got := TocTrail(toc, jsGuideRoot+"/Content/Missing.htm"); got != nil {
		t.Errorf("trail for an unlisted page = %q", got)
	}
}

func dumpToc(entries []*TocEntry) string {
	var b strings.Builder
	var walk func([]*TocEntry, int)
	walk = func(es []*TocEntry, depth int) {
		for _, e := range es {
			fmt.Fprintf(&b, "\n%s%q %s", strings.Repeat("  ", depth), e.Title, e.URL)
			walk(e.Children, depth+1)
		}
	}
	walk(entries, 0)
	return b.String()
}

func TestJSTocErrors(t *testing.T) {
	g, _ := GuideOf(jsGuideRoot + "/Content/Welcome.htm")
	for name, change := range map[string]func(m map[string]string){
		"no help system":   func(m map[string]string) { delete(m, jsGuideRoot+"/Data/HelpSystem.xml") },
		"no Toc attribute": func(m map[string]string) { m[jsGuideRoot+"/Data/HelpSystem.xml"] = `<WebHelpSystem/>` },
		"missing chunk":    func(m map[string]string) { delete(m, jsTocChunkURL+"1.js") },
		"bad main file":    func(m map[string]string) { m[jsTocMainURL] = `var x = 1;` },
		"too many chunks": func(m map[string]string) {
			m[jsTocMainURL] = `define({numchunks:5000,prefix:'Widget_Chunk',tree:{n:[]}});`
		},
		"prefix with slash": func(m map[string]string) { m[jsTocMainURL] = `define({numchunks:1,prefix:'../x',tree:{n:[]}});` },
		"no tree":           func(m map[string]string) { m[jsTocMainURL] = `define({numchunks:0,prefix:'W'});` },
	} {
		src := jsGuideSource()
		change(src.docs)
		if _, err := FetchJSToc(context.Background(), src, g); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseDefine(t *testing.T) {
	v, err := parseDefine([]byte(` define({a:[1,-2.5e1,true,false,null],'b c':"x\ty\\z\x41",$d:{}}) ;`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": []any{1.0, -25.0, true, false, nil}, "b c": "x\ty\\zA", "$d": map[string]any{}}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("v = %#v", v)
	}
	for _, bad := range []string{
		``, `define(`, `define({a:1}`, `define({a 1})`, `define({a:1,,})`, `define([1 2])`,
		`define('open)`, "define('a\nb')", `define(nope)`, `define({:1})`, `define('\u12')`,
		`define(1.2.3)`, `define(` + strings.Repeat("[", 200) + strings.Repeat("]", 200) + `)`,
	} {
		if _, err := parseDefine([]byte(bad)); err == nil {
			t.Errorf("parseDefine(%q): no error", bad)
		}
	}
}

func FuzzParseDefine(f *testing.F) {
	for _, s := range []string{jsTocMain, jsTocChunk0, jsTocChunk1, `define({a:'\x4'})`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if v, err := parseDefine(data); err == nil {
			_, _ = buildJSToc(v, []any{v}, Guide{RootURL: jsGuideRoot})
		}
	})
}

func TestVersionAndLanguageURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://docs.tenable.com/nessus/10_8/Content/A.htm#x":  "https://docs.tenable.com/nessus/Content/A.htm#x",
		"https://docs.tenable.com/a/widget/6_4_1/Content/B.htm": "https://docs.tenable.com/a/widget/Content/B.htm",
		"https://docs.tenable.com/nessus/Content/A.htm":         "https://docs.tenable.com/nessus/Content/A.htm",
		"https://docs.tenable.com/glossary.htm":                 "https://docs.tenable.com/glossary.htm",
	} {
		if got := CurrentURL(in); got != want {
			t.Errorf("CurrentURL(%q) = %q", in, got)
		}
	}
	jp, _ := ByCode("jp")
	if got := OnSite("https://docs.tenable.com/nessus/Content/A.htm", jp); got != "https://jp.docs.tenable.com/nessus/Content/A.htm" {
		t.Errorf("OnSite = %q", got)
	}
	if got := OnSite("https://example.com/x", jp); got != "https://example.com/x" {
		t.Errorf("OnSite off-site = %q", got)
	}
}
