// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

type fixturePage struct {
	key      index.Key
	path     string
	products []string
	body     string
}

const en = "https://docs.tenable.com/"

var longText = strings.Repeat("<p>A paragraph of the long section, repeated to make it long enough to page.</p>", 40)

var fixture = []fixturePage{
	{index.Key{Site: "en"}, "nessus/Content/Welcome.htm", []string{"Nessus"}, `<h1>Welcome to Nessus</h1><p>Start here.</p>`},
	{index.Key{Site: "en"}, "nessus/Content/Install.htm", []string{"Nessus"}, `<h1>Install Nessus on Linux</h1>
		<p class="Permission"><b>Required User Role:</b> Administrator</p><p>Overview of installing.</p>
		<h2 id="Before">Before you begin</h2><p>Check the <a href="Ports.htm">ports</a>.</p>
		<h2 id="Steps">Steps</h2><p>Run the installer package.</p>`},
	{index.Key{Site: "en"}, "nessus/Content/Ports.htm", []string{"Nessus"}, `<h1>Port Requirements</h1>
		<table><thead><tr><th>Port</th><th>Purpose</th></tr></thead><tbody><tr><td>8834</td><td>Web</td></tr></tbody></table>`},
	{index.Key{Site: "en"}, "nessus/Content/Long.htm", []string{"Nessus"}, `<h1>Long Page</h1>` + longText},
	{index.Key{Site: "en"}, "security-center/Content/Install.htm", []string{"Security Center"}, `<h1>Install Nessus on Linux</h1><p>Installing a scanner for Security Center.</p>`},
	{index.Key{Site: "en", Older: true}, "nessus/10_5/Content/Install.htm", []string{"Nessus"}, `<h1>Install Nessus on Linux</h1><p>Install the older installer package.</p>`},
	{index.Key{Site: "jp"}, "nessus/Content/Install.htm", []string{"Nessus"}, `<h1>Linux での Nessus のインストール</h1><h2 id="Steps">手順</h2><p>インストーラーを実行します。</p>`},
}

var nessusToc = []*site.TocEntry{{Title: "Welcome to Nessus", URL: en + "nessus/Content/Welcome.htm", Children: []*site.TocEntry{
	{Title: "Install Nessus on Linux", URL: en + "nessus/Content/Install.htm"},
	{Title: "Port Requirements", URL: en + "nessus/Content/Ports.htm"},
	{Title: "Long Page", URL: en + "nessus/Content/Long.htm"},
}}}

func buildIndex(t *testing.T) *index.Index { return buildIndexFrom(t, fixture) }

// buildIndexFrom builds an index from made-up pages, with the nessus guide's table of
// contents.
func buildIndexFrom(t *testing.T, pages []fixturePage) *index.Index {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	fillIndex(t, ix, pages)
	return ix
}

// fillIndex builds made-up pages into ix's shards, as buildIndexFrom does.
func fillIndex(t *testing.T, ix *index.Index, pages []fixturePage) {
	t.Helper()
	byKey := map[index.Key][]fixturePage{}
	for _, p := range pages {
		byKey[p.key] = append(byKey[p.key], p)
	}
	for k, keyPages := range byKey {
		b, err := ix.Build(k)
		if err != nil {
			t.Fatal(err)
		}
		s, _ := site.ByCode(k.Site)
		err = b.Update(ctx, func(tx *index.Tx) error {
			if k == (index.Key{Site: "en"}) {
				g, _ := site.GuideOf(en + "nessus/Content/x.htm")
				g.Name = "Tenable Nessus User Guide"
				if err := tx.PutToc(g, nessusToc); err != nil {
					return err
				}
			}
			if err := tx.SetMeta("built_at", "2026-11-06T00:00:00Z"); err != nil {
				return err
			}
			for _, fp := range keyPages {
				u := s.BaseURL() + fp.path
				p, err := page.Parse([]byte(`<html lang="`+s.Lang+`"><div id="mc-main-content">`+fp.body+`</div></html>`), u, nil)
				if err != nil {
					return err
				}
				d := index.NewDoc(p, s, site.Entry{URL: u, Products: fp.products, GuideType: "User Guide", LastMod: "2026-10-26"}, site.TocTrail(nessusToc, u))
				if _, err := tx.Put(d); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ix.Install(b); err != nil {
			t.Fatal(err)
		}
	}
}

func connect(t *testing.T, ix *index.Index) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, nil)
	Register(s, NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, ix))
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call calls a tool and decodes its structured output into out. It returns the text
// summary, or fails if the tool reported an error.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	text, isErr := callRaw(t, cs, name, args, out)
	if isErr {
		t.Fatalf("%s(%v) failed: %s", name, args, text)
	}
	return text
}

func callRaw(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	if !res.IsError && out != nil {
		// Decode into a fresh value: fields left out of this response mustn't keep an
		// earlier call's values.
		reflect.ValueOf(out).Elem().SetZero()
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: structured content: %v (%s)", name, err, raw)
		}
	}
	return strings.Join(text, "\n"), res.IsError
}

func TestToolsWithoutAnIndex(t *testing.T) {
	cs := connect(t, nil)
	for name, args := range map[string]map[string]any{
		"search_docs":   {"query": "install"},
		"read_doc":      {"url": en + "nessus/Content/Install.htm"},
		"outline":       {"guide": "nessus"},
		"browse_guide":  {"guide": "nessus"},
		"related_pages": {"url": en + "nessus/Content/Install.htm"},
		"list_products": {},
	} {
		text, isErr := callRaw(t, cs, name, args, nil)
		if !isErr || !strings.Contains(text, "isn't built") {
			t.Errorf("%s without an index = %v %q", name, isErr, text)
		}
	}
	var st statusOut
	call(t, cs, "index_status", nil, &st)
	if st.Index.State != "not built" || st.Note == "" {
		t.Errorf("status = %+v", st)
	}
}

func TestSearchDocs(t *testing.T) {
	cs := connect(t, buildIndex(t))
	var out searchOut
	text := call(t, cs, "search_docs", map[string]any{"query": "install nessus on linux"}, &out)
	if len(out.Results) == 0 {
		t.Fatalf("no results: %s", text)
	}
	r := out.Results[0]
	if !strings.HasPrefix(r.URL, en+"nessus/Content/Install.htm") || r.Product != "Nessus" || r.Language != "en" {
		t.Errorf("first = %+v", r)
	}
	if len(r.RequiredRoles) != 1 || !strings.Contains(r.RequiredRoles[0], "Administrator") {
		t.Errorf("roles = %q", r.RequiredRoles)
	}
	if len(r.Languages) != 1 || r.Languages[0].Language != "jp" || len(r.Versions) != 1 || r.Versions[0].Version != "10.5" {
		t.Errorf("variants = %+v", r.variantsOut)
	}
	if len(r.AlsoIn) != 1 || r.AlsoIn[0].Product != "Security Center" {
		t.Errorf("also in = %+v", r.AlsoIn)
	}
	if !strings.HasPrefix(text, "results for") && !strings.Contains(text, "1. Install Nessus on Linux") || !strings.Contains(text, r.URL) {
		t.Errorf("summary = %q", text)
	}

	// Filters.
	call(t, cs, "search_docs", map[string]any{"query": "install linux", "product": "Tenable.sc"}, &out)
	if len(out.Results) != 1 || out.Results[0].Product != "Security Center" {
		t.Errorf("product = %+v", out.Results)
	}
	call(t, cs, "search_docs", map[string]any{"query": "install", "required_role": "administrator"}, &out)
	if len(out.Results) == 0 || !strings.Contains(out.Results[0].URL, "nessus/Content/Install.htm") {
		t.Errorf("role = %+v", out.Results)
	}
	for _, r := range out.Results {
		if !strings.Contains(r.URL, "nessus/Content/Install.htm") {
			t.Errorf("role filter let through %s", r.URL)
		}
	}
	call(t, cs, "search_docs", map[string]any{"query": "install", "version": "10.5"}, &out)
	if out.Version != "10.5" || len(out.Results) == 0 || out.Results[0].Version != "10.5" || out.Results[0].English != en+"nessus/Content/Install.htm" {
		t.Errorf("version = %+v", out)
	}
	call(t, cs, "search_docs", map[string]any{"query": "インストーラー"}, &out)
	if out.Language != "jp" || len(out.Results) == 0 || !strings.HasPrefix(out.Results[0].English, en+"nessus/Content/Install.htm") {
		t.Errorf("jp = %+v", out)
	}
	call(t, cs, "search_docs", map[string]any{"query": "install", "release_notes": true}, &out)
	if len(out.Results) != 0 || out.Message == "" {
		t.Errorf("release notes = %+v", out)
	}
	call(t, cs, "search_docs", map[string]any{"query": "install", "limit": 1}, &out)
	if len(out.Results) != 1 {
		t.Errorf("limit = %d", len(out.Results))
	}
	for _, args := range []map[string]any{{"query": " "}, {"query": "x", "language": "pt"}, {"query": "x", "product": "Nope"}} {
		if text, isErr := callRaw(t, cs, "search_docs", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestReadDoc(t *testing.T) {
	cs := connect(t, buildIndex(t))
	var out readOut
	text := call(t, cs, "read_doc", map[string]any{"url": en + "nessus/Content/Install.htm#Steps"}, &out)
	if out.Heading != "Steps" || out.URL != en+"nessus/Content/Install.htm#Steps" || !strings.Contains(out.Markdown, "Run the installer") {
		t.Errorf("section = %+v", out)
	}
	if out.Previous == nil || out.Previous.Title != "Before you begin" || out.Next == nil || out.Next.URL != en+"nessus/Content/Ports.htm" {
		t.Errorf("neighbors = %+v %+v", out.Previous, out.Next)
	}
	if !strings.Contains(out.Markdown, "> Source: "+out.URL) || !strings.Contains(out.Markdown, "Administrator") ||
		!strings.Contains(out.Markdown, "Welcome to Nessus") || text != out.Markdown {
		t.Errorf("header = %q", out.Markdown)
	}
	if out.English != "" || len(out.Languages) != 1 {
		t.Errorf("variants = %+v", out.variantsOut)
	}

	// A whole page, found without a scheme, between its neighbors in the table of contents.
	call(t, cs, "read_doc", map[string]any{"url": "DOCS.tenable.com/nessus/Content/Install.htm"}, &out)
	if out.Heading != "" || !strings.Contains(out.Markdown, "Before you begin") || !strings.Contains(out.Markdown, "Run the installer") ||
		out.Previous == nil || out.Previous.URL != en+"nessus/Content/Welcome.htm" || out.Next == nil || out.Next.URL != en+"nessus/Content/Ports.htm" {
		t.Errorf("page = %+v", out)
	}
	// A translation, cited by its English page with the same anchor.
	call(t, cs, "read_doc", map[string]any{"url": "https://jp.docs.tenable.com/nessus/Content/Install.htm#Steps"}, &out)
	if out.Language != "jp" || out.English != en+"nessus/Content/Install.htm#Steps" {
		t.Errorf("jp = %+v", out)
	}
	// An anchor the page doesn't have reads the page, and says so.
	call(t, cs, "read_doc", map[string]any{"url": en + "nessus/Content/Install.htm#Nope"}, &out)
	if !strings.Contains(out.Markdown, "no section with that anchor") {
		t.Errorf("missing anchor = %q", out.Markdown)
	}

	// Paging.
	call(t, cs, "read_doc", map[string]any{"url": en + "nessus/Content/Long.htm", "max_chars": 1000}, &out)
	if out.Parts < 2 || out.Part != 1 || !strings.Contains(out.Markdown, "Part 1 of") {
		t.Fatalf("parts = %d, %q", out.Parts, out.Markdown[:min(200, len(out.Markdown))])
	}
	parts := out.Parts
	call(t, cs, "read_doc", map[string]any{"url": en + "nessus/Content/Long.htm", "max_chars": 1000, "part": parts}, &out)
	if out.Part != parts || !strings.Contains(out.Markdown, "paragraph") {
		t.Errorf("last part = %+v", out)
	}
	for _, args := range []map[string]any{
		{"url": en + "nessus/Content/Long.htm", "max_chars": 1000, "part": parts + 1},
		{"url": en + "nessus/Content/Missing.htm"},
		{"url": "https://example.com/x"},
		{"url": ""},
		{"url": "https://de.docs.tenable.com/nessus/Content/Install.htm"},
	} {
		if text, isErr := callRaw(t, cs, "read_doc", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestSplitText(t *testing.T) {
	s := strings.Repeat("word ", 50) + "\n\n" + strings.Repeat("x", 2500) + "\n\nend"
	parts := splitText(s, 1000)
	if len(parts) < 4 {
		t.Fatalf("%d parts", len(parts))
	}
	for _, p := range parts {
		if len([]rune(p)) > 1000 {
			t.Errorf("part of %d characters", len([]rune(p)))
		}
	}
	if !strings.HasSuffix(parts[len(parts)-1], "end") || !strings.HasPrefix(parts[0], "word word") {
		t.Errorf("last part = %q", parts[len(parts)-1])
	}
	if got := splitText("short", 1000); len(got) != 1 {
		t.Errorf("short = %q", got)
	}
}

func TestOutlineAndBrowse(t *testing.T) {
	cs := connect(t, buildIndex(t))
	var o outlineOut
	call(t, cs, "outline", map[string]any{"url": en + "nessus/Content/Install.htm"}, &o)
	if len(o.Items) != 3 || o.Items[1].Anchor != "Before" || o.Items[2].URL != en+"nessus/Content/Install.htm#Steps" || o.Words == 0 {
		t.Errorf("page outline = %+v", o)
	}
	text := call(t, cs, "outline", map[string]any{"guide": "nessus"}, &o)
	if len(o.Items) != 4 || o.Items[0].Level != 1 || o.Items[1].Level != 2 || !strings.Contains(text, "  - Install Nessus on Linux") {
		t.Errorf("guide outline = %+v\n%s", o, text)
	}
	if text, isErr := callRaw(t, cs, "outline", map[string]any{"guide": "ness"}, nil); !isErr || !strings.Contains(text, "did you mean nessus") {
		t.Errorf("unknown guide = %q", text)
	}
	if _, isErr := callRaw(t, cs, "outline", map[string]any{}, nil); !isErr {
		t.Error("outline with nothing should fail")
	}

	var b browseOut
	text = call(t, cs, "browse_guide", map[string]any{"guide": "nessus", "depth": 1}, &b)
	if len(b.Entries) != 1 || b.Entries[0].More != 3 || b.Total != 4 || !strings.Contains(text, "(+3 more)") ||
		b.Name != "Tenable Nessus User Guide" || !strings.HasPrefix(text, "Tenable Nessus User Guide: nessus") {
		t.Errorf("depth 1 = %+v", b)
	}
	call(t, cs, "browse_guide", map[string]any{"guide": en + "nessus/Content/Ports.htm"}, &b)
	if len(b.Entries) != 4 || b.Entries[3].Depth != 2 || b.Guide != "nessus" || b.Language != "en" {
		t.Errorf("by URL = %+v", b)
	}
	for _, args := range []map[string]any{{"guide": "nessus", "language": "jp"}, {"guide": "nessus", "language": "pt"}, {"guide": "nessus/10_5"}} {
		if text, isErr := callRaw(t, cs, "browse_guide", args, nil); !isErr {
			t.Errorf("%v should fail: %s", args, text)
		}
	}
}

func TestRelatedAndProducts(t *testing.T) {
	cs := connect(t, buildIndex(t))
	var r relatedOut
	text := call(t, cs, "related_pages", map[string]any{"url": en + "nessus/Content/Install.htm#Steps"}, &r)
	if r.Parent == nil || r.Parent.Title != "Welcome to Nessus" || r.Previous == nil || r.Next == nil {
		t.Errorf("toc = %+v", r)
	}
	if len(r.LinksOut) != 1 || r.LinksOut[0].URL != en+"nessus/Content/Ports.htm" || r.LinksOut[0].Title != "Port Requirements" {
		t.Errorf("links out = %+v", r.LinksOut)
	}
	if len(r.SameTitle) != 1 || r.SameTitle[0].Product != "Security Center" || len(r.Languages) != 1 || len(r.Versions) != 1 {
		t.Errorf("related = %+v", r)
	}
	if !strings.Contains(text, "Links to 1 pages") {
		t.Errorf("summary = %q", text)
	}
	call(t, cs, "related_pages", map[string]any{"url": en + "nessus/Content/Ports.htm"}, &r)
	if len(r.LinksIn) != 1 || r.LinksIn[0].Title != "Install Nessus on Linux" {
		t.Errorf("links in = %+v", r.LinksIn)
	}

	var p productsOut
	text = call(t, cs, "list_products", nil, &p)
	if len(p.Products) != 2 || p.Products[0].Name != "Nessus" || p.Products[0].Pages != 5 ||
		strings.Join(p.Products[0].Languages, ",") != "en,jp" {
		t.Fatalf("products = %+v", p.Products)
	}
	var nessus *guideOut
	for i, g := range p.Products[0].Guides {
		if g.Path == "nessus" {
			nessus = &p.Products[0].Guides[i]
		}
	}
	if nessus == nil || nessus.Pages != 4 || strings.Join(nessus.Versions, ",") != "10.5" || nessus.Name != "Tenable Nessus User Guide" {
		t.Errorf("nessus guide = %+v", nessus)
	}
	if len(p.Languages) != 2 || p.Languages[0].Language != "en" || p.Languages[0].OlderPages != 1 || p.Languages[0].LastIndexed == "" {
		t.Errorf("languages = %+v", p.Languages)
	}
	if !strings.Contains(text, "Nessus: 5 pages (en, jp)") {
		t.Errorf("summary = %q", text)
	}

	var st statusOut
	call(t, cs, "index_status", nil, &st)
	if st.Index.State != "ready" || len(st.Index.Shards) != 3 || st.Index.Shards[0].BuiltAt == "" || st.Index.Shards[0].Schema == 0 {
		t.Errorf("status = %+v", st)
	}
}
