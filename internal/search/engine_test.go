// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

func TestDetectLanguage(t *testing.T) {
	for q, want := range map[string]string{
		"How do I install Nessus?":                      "en",
		"Linux に Nessus をインストールする方法":                    "jp",
		"スキャン設定":                                        "jp",
		"如何安装扫描器":                                       "zh-cn",
		"如何安裝掃描器":                                       "zh-tw",
		"스캐너를 설치하는 방법":                                  "kr",
		"Comment installer le capteur OT ?":             "fr",
		"Wie installiere ich den Sensor?":               "de",
		"¿Cómo configuro el escáner?":                   "es",
		"Nessus":                                        "en",
		"Configurer les paramètres de l'analyse":        "fr",
		"create a scan in the scanner settings":         "en",
		"Exigences système pour Tenable OT":             "fr",
		"Systemanforderungen für Nessus":                "de",
		"requisitos del sistema para Identity Exposure": "es",
	} {
		if got := DetectLanguage(q); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", q, got, want)
		}
	}
	for in, want := range map[string]string{"ja": "jp", "jp": "jp", "Japanese": "jp", "ko-KR": "kr", "zh-Hant": "zh-tw",
		"zh-Hans": "zh-cn", "zh": "zh-cn", "fr-CA": "fr", "en-GB": "en", "de": "de", "es-MX": "es", "ES": "es"} {
		if got, ok := SiteFor(in); !ok || got != want {
			t.Errorf("SiteFor(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := SiteFor("pt-BR"); ok {
		t.Error("there's no Portuguese site")
	}
}

func TestParseOtherLanguages(t *testing.T) {
	p := ParseLang("Linux に Nessus をインストールする方法", "jp")
	if strings.Join(p.Terms, ",") != "linux,インストール" || len(p.Short) != 0 || p.Intent != HowTo || !p.Install || p.Products[0] != "Nessus" {
		t.Errorf("jp = %+v", p)
	}
	p = ParseLang("システム要件", "jp")
	if strings.Join(p.Terms, ",") != "システム" || strings.Join(p.Short, ",") != "要件" || p.Intent != Requirements {
		t.Errorf("jp short = %+v", p)
	}
	// Chinese runs of five or more characters are usually several words: they're
	// looked for two characters at a time.
	p = ParseLang("如何安装扫描器", "zh-cn")
	if len(p.Terms) != 0 || strings.Join(p.Short, ",") != "安装,扫描,描器" || p.Intent != HowTo || !p.Install {
		t.Errorf("zh = %+v", p)
	}
	p = ParseLang("扫描策略的设置", "zh-cn")
	if strings.Join(p.Terms, ",") != "扫描策略" || strings.Join(p.Short, ",") != "设置" {
		t.Errorf("zh pieces = %+v", p)
	}
	p = ParseLang("스캐너를 설치하는 방법", "kr")
	if strings.Join(p.Terms, ",") != "스캐너" || strings.Join(p.Short, ",") != "설치" {
		t.Errorf("kr = %+v", p)
	}
	p = ParseLang("Comment installer le capteur dans OT Security ?", "fr")
	if strings.Join(p.Terms, ",") != "installer,capteur" || p.Products[0] != "OT Exposure" || p.Intent != HowTo {
		t.Errorf("fr = %+v", p)
	}
	for q, want := range map[string]string{
		"Security Center 6.3 hardware requirements": "6.3",
		"Nessus 10.8.1 install":                     "10.8.1",
		"configure version 6.5 settings":            "6.5",
		"Tenable.sc v6.2 upgrade":                   "6.2",
		"scan 10.0.0.1 with Nessus":                 "",
		"requires 2.5 GB of disk":                   "",
		"What's new in Nessus 10.12?":               "",
		"OT Security 4.3.":                          "4.3",
	} {
		p := Parse(q)
		if p.Version != want {
			t.Errorf("Parse(%q).Version = %q, want %q (terms %q)", q, p.Version, want, p.Terms)
		}
		if want != "" && strings.Contains(strings.Join(p.Terms, " "), want) {
			t.Errorf("Parse(%q) kept the version in its terms: %q", q, p.Terms)
		}
	}
	if p := Parse("What's new in Nessus 10.12?"); strings.Join(p.Terms, ",") != "10.12" {
		t.Errorf("a release-notes question searches for the version: %q", p.Terms)
	}
}

type langPage struct {
	key               index.Key
	path, title, body string
	products          []string
}

func newEngine(t *testing.T, pages []langPage) *Engine {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	byKey := map[index.Key][]langPage{}
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
			for _, lp := range ps {
				u := s.BaseURL() + lp.path
				p, err := page.Parse([]byte(`<div id="mc-main-content"><h1>`+lp.title+`</h1>`+lp.body+`</div>`), u, nil)
				if err != nil {
					return err
				}
				if _, err := tx.Put(index.NewDoc(p, s, site.Entry{URL: u, Products: lp.products, GuideType: "User Guide"}, nil)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Optimize(ctx); err != nil {
			t.Fatal(err)
		}
		if err := ix.Install(b); err != nil {
			t.Fatal(err)
		}
	}
	return NewEngine(ix)
}

var (
	enCur         = index.Key{Site: "en"}
	enOld         = index.Key{Site: "en", Older: true}
	jpCur         = index.Key{Site: "jp"}
	jpOld         = index.Key{Site: "jp", Older: true}
	frCur         = index.Key{Site: "fr"}
	nessus        = []string{"Nessus"}
	sc            = []string{"Security Center"}
	ot            = []string{"OT Exposure"}
	engineFixture = []langPage{
		{enCur, "nessus/Content/Install.htm", "Install Nessus on Linux", `<h2 id="Steps">Steps</h2><p>Install the package on Linux.</p>`, nessus},
		{enCur, "security-center/Content/Hardware.htm", "Hardware Requirements", `<p>Current hardware sizing.</p>`, sc},
		{enCur, "OT-security/Content/Sensor.htm", "Install the OT Sensor", `<p>Install the sensor appliance.</p>`, ot},
		{enOld, "security-center/6_3/Content/Hardware.htm", "Hardware Requirements", `<p>Hardware sizing for this release.</p>`, sc},
		{enOld, "security-center/6_5/Content/Hardware.htm", "Hardware Requirements", `<p>Hardware sizing, newer release.</p>`, sc},
		{enOld, "nessus/10_5/Content/Install.htm", "Install Nessus on Linux", `<p>Install the older package on Linux.</p>`, nessus},
		{jpCur, "nessus/Content/Install.htm", "Linux での Nessus のインストール", `<h2 id="Steps">手順</h2><p>Linux にパッケージをインストールします。設定を確認します。</p>`, nessus},
		{jpOld, "nessus/10_5/Content/Install.htm", "Linux での Nessus のインストール", `<p>古いパッケージをインストールします。</p>`, nessus},
		{frCur, "OT-security/Content/Sensor.htm", "Installer le capteur OT", `<p>Installer le capteur sur l'appliance.</p>`, ot},
	}
)

func TestEngineLanguages(t *testing.T) {
	e := newEngine(t, engineFixture)
	search := func(req Request) *Result {
		t.Helper()
		r, err := e.Search(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// A Japanese question finds the Japanese page, linked to the English one.
	r := search(Request{Query: "Linux に Nessus をインストールする方法"})
	if r.Language != "jp" || r.Requested != "jp" || len(r.Hits) == 0 || !strings.HasPrefix(r.Hits[0].URL, "https://jp.docs.tenable.com/nessus/Content/Install.htm") {
		t.Fatalf("jp = %+v", r)
	}
	h := r.Hits[0]
	if h.Language != "jp" || h.Variants.English != "https://docs.tenable.com/nessus/Content/Install.htm" ||
		len(h.Variants.Languages) != 1 || h.Variants.Languages[0].Site != "en" || h.Variants.Languages[0].Title != "Install Nessus on Linux" {
		t.Errorf("jp variants = %+v", h.Variants)
	}
	if len(h.Variants.Versions) != 1 || h.Variants.Versions[0].Version != "10.5" {
		t.Errorf("jp versions = %+v", h.Variants.Versions)
	}
	if !strings.HasPrefix(h.EnglishURL, "https://docs.tenable.com/nessus/Content/Install.htm") {
		t.Errorf("English citation = %q", h.EnglishURL)
	}
	// A two-character Japanese word alone is looked for as text.
	// Kanji alone read as Chinese, but the Japanese docs answer when the Chinese ones
	// can't.
	if r := search(Request{Query: "設定"}); len(r.Hits) != 1 || r.Language != "jp" {
		t.Errorf("short jp = %+v", r)
	}
	// A French question finds the French page …
	r = search(Request{Query: "Comment installer le capteur OT ?"})
	if r.Language != "fr" || len(r.Hits) == 0 || !strings.HasPrefix(r.Hits[0].URL, "https://fr.docs.tenable.com/") {
		t.Errorf("fr = %+v", r)
	}
	// … and, where the French docs have nothing, the English one, saying so.
	r = search(Request{Query: "Comment installer Nessus sur Linux ?"})
	if r.Language != "en" || r.Requested != "fr" || len(r.Hits) == 0 || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "French") {
		t.Errorf("fr fallback = %+v", r)
	}
	// A language the index doesn't have falls back to English too.
	r = search(Request{Query: "Wie installiere ich Nessus auf Linux?"})
	if r.Language != "en" || r.Requested != "de" || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "isn't in the index") {
		t.Errorf("de fallback = %+v", r)
	}
	// The language filter overrides detection.
	r = search(Request{Query: "Nessus Linux", Language: "ja"})
	if r.Language != "jp" || len(r.Hits) == 0 {
		t.Errorf("filter = %+v", r)
	}
	if _, err := e.Search(ctx, Request{Query: "x", Language: "pt"}); err == nil {
		t.Error("a language with no site should be an error")
	}
	// An English hit has no English citation of its own, but lists its translation.
	r = search(Request{Query: "install nessus linux"})
	if len(r.Hits) == 0 || r.Hits[0].EnglishURL != "" || len(r.Hits[0].Variants.Languages) != 1 {
		t.Errorf("en = %+v", r.Hits)
	}
}

func TestEngineVersions(t *testing.T) {
	e := newEngine(t, engineFixture)
	search := func(req Request) *Result {
		t.Helper()
		r, err := e.Search(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// A version in the question selects it.
	r := search(Request{Query: "Security Center 6.3 hardware requirements"})
	if r.Version != "6.3" || len(r.Hits) == 0 || r.Hits[0].PageURL != "https://docs.tenable.com/security-center/6_3/Content/Hardware.htm" {
		t.Fatalf("6.3 = %+v", r)
	}
	v := r.Hits[0].Variants
	if v.Current != "https://docs.tenable.com/security-center/Content/Hardware.htm" || v.English != v.Current ||
		len(v.Versions) != 2 || v.Versions[0].Version != "" || v.Versions[1].Version != "6.5" {
		t.Errorf("6.3 variants = %+v", v)
	}
	if r.Hits[0].EnglishURL != v.Current {
		t.Errorf("citation = %q", r.Hits[0].EnglishURL)
	}
	// So does the filter, and a patch version finds its release.
	if r := search(Request{Query: "hardware requirements", Version: "6.5"}); r.Version != "6.5" || !strings.Contains(r.Hits[0].PageURL, "6_5") {
		t.Errorf("filter = %+v", r)
	}
	if r := search(Request{Query: "hardware requirements", Version: "v6.3.2"}); r.Version != "6.3" {
		t.Errorf("patch version = %q", r.Version)
	}
	// A version the older docs don't have gets the current docs, with a note.
	r = search(Request{Query: "Security Center 7.1 hardware requirements"})
	if r.Version != "" || len(r.Hits) == 0 || strings.Contains(r.Hits[0].PageURL, "6_") || len(r.Notes) != 1 {
		t.Errorf("7.1 = %+v", r)
	}
	// Without a version, the current docs, with older versions listed.
	r = search(Request{Query: "Security Center hardware requirements"})
	if r.Version != "" || r.Hits[0].PageURL != "https://docs.tenable.com/security-center/Content/Hardware.htm" || len(r.Hits[0].Variants.Versions) != 2 {
		t.Errorf("current = %+v", r.Hits[0])
	}
	// Variants of a URL outside the docs are empty.
	if v, err := e.Variants(ctx, "https://example.com/x"); err != nil || v.English != "" || len(v.Languages) != 0 {
		t.Errorf("off-site variants = %+v, %v", v, err)
	}
}

// FuzzParseLang checks that reading a question in any language never panics, and keeps
// its pieces to valid text.
func FuzzParseLang(f *testing.F) {
	for _, q := range []string{"Linux に Nessus をインストールする方法", "如何安装扫描器", "스캐너를 설치하는 방법",
		"Comment installer ?", "Security Center 6.3.", "設", "のの", "ー", "\xff\xfe", "v10.0.0.1"} {
		f.Add(q)
	}
	f.Fuzz(func(t *testing.T, q string) {
		_ = DetectLanguage(q)
		for _, s := range site.Sites {
			p := ParseLang(q, s.Code)
			for _, w := range append(append([]string{}, p.Terms...), p.Short...) {
				if w == "" || strings.TrimSpace(w) != w {
					t.Errorf("%s %q: piece %q", s.Code, q, w)
				}
			}
		}
	})
}

func TestEngineMissingShards(t *testing.T) {
	e := newEngine(t, []langPage{{jpCur, "nessus/Content/A.htm", "インストール", `<p>テスト</p>`, nessus}})
	if _, err := e.Search(ctx, Request{Query: "install"}); err == nil {
		t.Error("an index without English should report that")
	}
	// With English but not the language asked for, and nothing in English either.
	e = newEngine(t, []langPage{{enCur, "nessus/Content/A.htm", "Install", `<p>Install it.</p>`, nessus}})
	r, err := e.Search(ctx, Request{Query: "Wie konfiguriere ich Zebras?"})
	if err != nil || len(r.Hits) != 0 || r.Requested != "de" || len(r.Notes) != 1 {
		t.Errorf("nothing anywhere = %+v, %v", r, err)
	}
}
