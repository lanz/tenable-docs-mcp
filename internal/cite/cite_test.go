// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package cite

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

func TestExtract(t *testing.T) {
	text := "See [install](https://docs.tenable.com/nessus/Content/Install.htm#Steps) and\n" +
		`<a href="Ports.htm">ports</a>, <MadCap:xref href='../Content/Other.htm#x'>x</MadCap:xref>.` + "\n" +
		"Bare: docs.tenable.com/nessus/Content/Bare.htm. Again https://docs.tenable.com/nessus/Content/Install.htm#Steps!\n" +
		"Japanese: https://JP.docs.tenable.com/nessus/Content/Install.htm\n" +
		"Other: [downloads](https://www.tenable.com/downloads) and [img](../Resources/Images/a.png) and [pdf](https://docs.tenable.com/x/guide.pdf)\n" +
		`Same page: <a href="#Top">top</a> <a href="javascript:void(0);">x</a> <a href="mailto:a@example.com">m</a>` + "\n" +
		"[titled](<https://docs.tenable.com/a/Content/B.htm> \"Title\") and *https://docs.tenable.com/a/Content/C.htm*"
	found, skipped := Extract(text, "https://docs.tenable.com/nessus/Content/Draft.htm")
	var got []string
	for _, f := range found {
		got = append(got, fmt.Sprintf("%s %v", f.URL, f.Lines))
	}
	want := []string{
		"https://docs.tenable.com/nessus/Content/Install.htm#Steps [1 3]",
		"https://docs.tenable.com/nessus/Content/Ports.htm [2]",
		"https://docs.tenable.com/nessus/Content/Other.htm#x [2]",
		"https://docs.tenable.com/nessus/Content/Bare.htm [3]",
		"https://jp.docs.tenable.com/nessus/Content/Install.htm [4]",
		"https://docs.tenable.com/nessus/Content/Draft.htm#Top [6]",
		"https://docs.tenable.com/a/Content/B.htm [7]",
		"https://docs.tenable.com/a/Content/C.htm [7]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("found:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if skipped != (Skipped{Other: 1, Files: 2}) {
		t.Errorf("skipped = %+v", skipped)
	}
	if found[1].Raw != "Ports.htm" {
		t.Errorf("raw = %q", found[1].Raw)
	}
	// Without a base, relative links aren't checked.
	found, skipped = Extract(`<a href="Ports.htm">p</a> <a href="#x">x</a> https://docs.tenable.com/n/Content/A.htm`, "")
	if len(found) != 1 || skipped.Relative != 2 {
		t.Errorf("no base = %+v, %+v", found, skipped)
	}
	if found, _ := Extract("no links here", ""); len(found) != 0 {
		t.Errorf("none = %+v", found)
	}
}

func TestFileWords(t *testing.T) {
	for u, want := range map[string]string{
		"https://docs.tenable.com/nessus/Content/InstallNessusLinux.htm":                         "Install Nessus Linux",
		"https://docs.tenable.com/nessus/Content/generate-csr-and-import-signed-certificate.htm": "generate csr and import signed certificate",
		"https://docs.tenable.com/x/Content/Scan_Settings2Advanced.htm":                          "Scan Settings2 Advanced",
		"https://docs.tenable.com/": "",
	} {
		if got := fileWords(u); got != want {
			t.Errorf("fileWords(%q) = %q", u, got)
		}
	}
}

// fakeLive serves pages from memory, with redirects.
type fakeLive struct {
	pages     map[string]string
	redirects map[string]string
	fail      bool
}

func (f *fakeLive) Fetch(_ context.Context, req site.Request) (*site.Response, error) {
	if f.fail {
		return nil, fmt.Errorf("network down")
	}
	u := req.URL
	if to, ok := f.redirects[u]; ok {
		u = to
	}
	body, ok := f.pages[u]
	if !ok {
		return nil, fmt.Errorf("fetch %s: %w", u, site.ErrNotFound)
	}
	return &site.Response{URL: u, Status: 200, Body: []byte(body)}, nil
}

type fixturePage struct {
	key      index.Key
	path     string
	products []string
	body     string
}

func checker(t *testing.T) *Checker {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	pages := []fixturePage{
		{index.Key{Site: "en"}, "nessus/Content/Install.htm", []string{"Nessus"}, `<h1>Install Nessus</h1><h2 id="Steps">Steps</h2><p>Run it.</p>`},
		{index.Key{Site: "en"}, "nessus/Content/Ports.htm", []string{"Nessus"}, `<h1>Port Requirements</h1><p>Ports.</p>`},
		{index.Key{Site: "en"}, "nessus/Content/Generate-Certificates.htm", []string{"Nessus"}, `<h1>Generate Certificates</h1><p>Make certificates.</p>`},
		{index.Key{Site: "en", Older: true}, "nessus/10_5/Content/Install.htm", []string{"Nessus"}, `<h1>Install Nessus</h1><h2 id="Steps">Steps</h2><p>Older.</p>`},
		{index.Key{Site: "en", Older: true}, "nessus/10_5/Content/Removed.htm", []string{"Nessus"}, `<h1>Removed Since</h1><p>Gone now.</p>`},
		{index.Key{Site: "jp"}, "nessus/Content/Install.htm", []string{"Nessus"}, `<h1>インストール</h1><h2 id="Steps">手順</h2><p>実行します。</p>`},
	}
	byKey := map[index.Key][]fixturePage{}
	for _, p := range pages {
		byKey[p.key] = append(byKey[p.key], p)
	}
	for k, ps := range byKey {
		b, err := ix.Build(k)
		if err != nil {
			t.Fatal(err)
		}
		s, _ := site.ByCode(k.Site)
		err = b.Update(ctx, func(tx *index.Tx) error {
			for _, fp := range ps {
				u := s.BaseURL() + fp.path
				p, err := page.Parse([]byte(`<div id="mc-main-content">`+fp.body+`</div>`), u, nil)
				if err != nil {
					return err
				}
				if _, err := tx.Put(index.NewDoc(p, s, site.Entry{URL: u, Products: fp.products, GuideType: "User Guide"}, nil)); err != nil {
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
	return &Checker{Index: ix, Engine: search.NewEngine(ix)}
}

func checkOne(t *testing.T, c *Checker, u string) Result {
	t.Helper()
	res, err := c.Check(ctx, []Found{{URL: u, Lines: []int{1}}})
	if err != nil {
		t.Fatal(err)
	}
	return res[0]
}

const en = "https://docs.tenable.com/"

func TestCheck(t *testing.T) {
	c := checker(t)
	for _, tc := range []struct {
		url, status, current, english, suggestion string
	}{
		{en + "nessus/Content/Install.htm", OK, "", "", ""},
		{en + "nessus/Content/Install.htm#Steps", OK, "", "", ""},
		{en + "nessus/Content/Install.htm#Nope", AnchorUnknown, "", "", ""},
		{en + "nessus/10_5/Content/Install.htm#Steps", OlderVersion, en + "nessus/Content/Install.htm#Steps", en + "nessus/Content/Install.htm#Steps", ""},
		{en + "nessus/10_5/Content/Removed.htm", OlderVersion, "", "", ""},
		{"https://jp.docs.tenable.com/nessus/Content/Install.htm#Steps", Translation, "", en + "nessus/Content/Install.htm#Steps", ""},
		// Missing pages: the current or English page when the index has it, or a search.
		{en + "nessus/10_9/Content/Install.htm", NotFound, "", "", en + "nessus/Content/Install.htm"},
		{"https://jp.docs.tenable.com/nessus/Content/Ports.htm", NotFound, "", "", en + "nessus/Content/Ports.htm"},
		{en + "nessus/Content/generate-certificates-old.htm", NotFound, "", "", en + "nessus/Content/Generate-Certificates.htm"},
		{"https://fr.docs.tenable.com/nessus/Content/Install.htm", NotIndexed, "", "", ""},
	} {
		r := checkOne(t, c, tc.url)
		sug := ""
		if r.Suggestion != nil {
			sug = r.Suggestion.URL
		}
		if r.Status != tc.status || r.Current != tc.current || r.English != tc.english || sug != tc.suggestion {
			t.Errorf("%s = %s current %q English %q suggestion %q; %s", tc.url, r.Status, r.Current, r.English, sug, r.Note)
		}
	}
	if !Problem(NotFound) || !Problem(AnchorMissing) || Problem(AnchorUnknown) || Problem(OlderVersion) {
		t.Error("Problem")
	}
}

func TestCheckLive(t *testing.T) {
	c := checker(t)
	c.Live = &fakeLive{
		pages: map[string]string{
			en + "nessus/Content/Install.htm": `<html><p id="Hidden">x</p><a name="Named"></a></html>`,
			en + "nessus/Content/New.htm":     `<html>new</html>`,
			en + "nessus/Content/Moved.htm":   `<html>moved</html>`,
		},
		redirects: map[string]string{en + "nessus/Content/Old.htm": en + "nessus/Content/Moved.htm"},
	}
	for u, want := range map[string]string{
		en + "nessus/Content/Install.htm#Hidden": OK,
		en + "nessus/Content/Install.htm#Named":  OK,
		en + "nessus/Content/Install.htm#Gone":   AnchorMissing,
		en + "nessus/Content/New.htm":            OK,
		en + "nessus/Content/Old.htm":            Moved,
		en + "nessus/Content/Nowhere.htm":        NotFound,
	} {
		if r := checkOne(t, c, u); r.Status != want {
			t.Errorf("%s = %s (%s), want %s", u, r.Status, r.Note, want)
		}
	}
	if r := checkOne(t, c, en+"nessus/Content/Old.htm"); r.Current != en+"nessus/Content/Moved.htm" {
		t.Errorf("moved to %q", r.Current)
	}
	// A failing live check leaves the index's answer.
	c.Live = &fakeLive{fail: true}
	if r := checkOne(t, c, en+"nessus/Content/Install.htm#Gone"); r.Status != AnchorUnknown || !strings.Contains(r.Note, "live check failed") {
		t.Errorf("failed anchor check = %+v", r)
	}
	if r := checkOne(t, c, en+"nessus/Content/Nowhere.htm"); r.Status != NotFound || !strings.Contains(r.Note, "live check failed") {
		t.Errorf("failed page check = %+v", r)
	}
}
