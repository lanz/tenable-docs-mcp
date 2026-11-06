// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

func openTest(t *testing.T, key Key) *Shard {
	t.Helper()
	s, err := OpenShard(filepath.Join(t.TempDir(), key.FileName()), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// makeDoc parses a made-up page into a Doc.
func makeDoc(t *testing.T, siteCode, pageURL, body string) Doc {
	t.Helper()
	p, err := page.Parse([]byte(`<html><body><div id="mc-main-content">`+body+`</div></body></html>`), pageURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := site.ByCode(siteCode)
	return NewDoc(p, s, site.Entry{URL: pageURL, Products: []string{"Widget Manager"}, GuideType: "User Guide"}, []string{"Get Started", "Install"})
}

func put(t *testing.T, s *Shard, docs ...Doc) {
	t.Helper()
	if err := s.Update(ctx, func(tx *Tx) error {
		for _, d := range docs {
			if _, err := tx.Put(d); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func search(t *testing.T, s *Shard, q string) []string {
	t.Helper()
	hits, err := s.SearchSections(ctx, MatchQuery(q), 10)
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.URL)
	}
	return out
}

func TestTokenizers(t *testing.T) {
	for _, c := range []struct {
		site, text string
		match      []string
		miss       []string
	}{
		{"en", "Scanning the hosts configures scanners.", []string{"scan", "scanned", "configure", "HOSTS"}, []string{"scant"}},
		{"fr", "Configurer les paramètres de l'analyse réseau.", []string{"parametres", "analyse", "RÉSEAU"}, []string{"paramètre"}},
		{"de", "Die Übersicht der Prüfungen öffnen.", []string{"ubersicht", "prufungen", "Öffnen"}, []string{"Prüfung"}},
		{"es", "La configuración del escáner.", []string{"configuracion", "escaner"}, []string{"configura"}},
		{"jp", "スキャンの設定を変更します。", []string{"スキャン", "設定を変更", "の設定"}, []string{"スキャナー"}},
		{"zh-cn", "配置扫描器的设置。", []string{"扫描器", "配置扫描"}, []string{"扫描仪"}},
		{"kr", "스캐너 설정을 변경합니다.", []string{"스캐너", "설정을 변경"}, []string{"스캔하기"}},
	} {
		t.Run(c.site, func(t *testing.T) {
			s := openTest(t, Key{Site: c.site})
			u := "https://docs.tenable.com/widget/Content/Page.htm"
			put(t, s, makeDoc(t, c.site, u, `<h1>Page</h1><p>`+c.text+`</p>`))
			for _, q := range c.match {
				if got := search(t, s, q); len(got) != 1 {
					t.Errorf("%q: %d hits", q, len(got))
				}
			}
			for _, q := range c.miss {
				if got := search(t, s, q); len(got) != 0 {
					t.Errorf("%q should not match", q)
				}
			}
		})
	}
}

func TestRanking(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	put(t, s,
		makeDoc(t, "en", "https://docs.tenable.com/widget/Content/Body.htm", `<h1>Overview</h1><p>Mentions credentials once.</p>`),
		makeDoc(t, "en", "https://docs.tenable.com/widget/Content/Title.htm", `<h1>Credentials</h1><p>About something.</p>`),
	)
	got := search(t, s, "credentials")
	if len(got) != 2 || !strings.HasSuffix(got[0], "Title.htm") {
		t.Errorf("a title match should rank first: %q", got)
	}
	hits, _ := s.SearchSections(ctx, MatchQuery(`"credentials" OR x`), 10) // quotes and operators are read as text
	if len(hits) != 0 {
		t.Errorf("hits for quoted operators: %+v", hits)
	}
	if hits, err := s.SearchSections(ctx, "  ", 10); err != nil || hits != nil {
		t.Errorf("empty query = %v, %v", hits, err)
	}
	if len(search(t, s, "widget manager")) != 2 {
		t.Error("the product is part of each section's location")
	}
}

const widgetPage = `<h1>Install</h1><p>Install the widget. See <a href="Ports.htm#Top">ports</a>.</p>
	<h2 id="Settings">Settings</h2>
	<table><thead><tr><th>Option</th><th>Description</th><th>Default</th></tr></thead>
	<tbody><tr><td>Port</td><td>The listening port.</td><td>8443</td></tr></tbody></table>
	<h2 id="Reqs">System Requirements</h2>
	<table><thead><tr><th>Port</th><th>Purpose</th></tr></thead><tbody><tr><td>8443</td><td>Web</td></tr></tbody></table>`

func TestPutUpdateRemove(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	u := "https://docs.tenable.com/widget/Content/Install.htm"
	d := makeDoc(t, "en", u, widgetPage)
	d.ETag = `"v1"`
	put(t, s, d)
	counts, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"pages": 1, "sections": 3, "settings": 1, "requirements": 1, "releases": 0, "glossary": 0, "links": 1, "tocs": 0, "changes": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("%s = %d, want %d", k, counts[k], v)
		}
	}
	if got, _ := s.LinksTo(ctx, "https://docs.tenable.com/widget/Content/Ports.htm"); len(got) != 1 || got[0] != u {
		t.Errorf("links to = %q", got)
	}

	// The same page again: nothing changes but the validators.
	d.ETag = `"v2"`
	var changed bool
	if err := s.Update(ctx, func(tx *Tx) (err error) { changed, err = tx.Put(d); return }); err != nil || changed {
		t.Errorf("unchanged put = %v, %v", changed, err)
	}
	var etag string
	s.db.QueryRow("SELECT etag FROM pages").Scan(&etag)
	if etag != `"v2"` {
		t.Errorf("etag = %s", etag)
	}

	// A changed page replaces everything, in the search index too.
	put(t, s, makeDoc(t, "en", u, `<h1>Install</h1><p>Rewritten text about gadgets.</p>`))
	if got := search(t, s, "listening"); len(got) != 0 {
		t.Errorf("old text still found: %q", got)
	}
	if got := search(t, s, "gadgets"); len(got) != 1 {
		t.Errorf("new text not found: %q", got)
	}
	var settingsFTS int
	s.db.QueryRow("SELECT count(*) FROM settings_fts WHERE settings_fts MATCH 'listening'").Scan(&settingsFTS)
	if counts, _ = s.Counts(ctx); counts["settings"] != 0 || counts["sections"] != 1 || settingsFTS != 0 {
		t.Errorf("after update: %v, %d settings matches", counts, settingsFTS)
	}

	// Removing it.
	var removed, again bool
	if err := s.Update(ctx, func(tx *Tx) (err error) {
		if removed, err = tx.Remove(u); err != nil {
			return err
		}
		again, err = tx.Remove(u)
		return err
	}); err != nil || !removed || again {
		t.Errorf("remove = %v %v %v", removed, again, err)
	}
	if got := search(t, s, "gadgets"); len(got) != 0 {
		t.Errorf("removed page still found: %q", got)
	}
	rows, _ := s.db.Query("SELECT change FROM changes ORDER BY id")
	var changes []string
	for rows.Next() {
		var c string
		rows.Scan(&c)
		changes = append(changes, c)
	}
	rows.Close()
	if strings.Join(changes, ",") != "added,updated,removed" {
		t.Errorf("changes = %q", changes)
	}

	// A failed transaction leaves nothing behind.
	err = s.Update(ctx, func(tx *Tx) error {
		if _, err := tx.Put(d); err != nil {
			return err
		}
		return errors.New("stop")
	})
	if counts, _ = s.Counts(ctx); err == nil || counts["pages"] != 0 {
		t.Errorf("rolled back: %v, %v", err, counts)
	}
	if err := s.Update(ctx, func(tx *Tx) error { _, err := tx.Put(Doc{}); return err }); err == nil {
		t.Error("a Doc with no page should be an error")
	}
}

func TestReleasesTocsAndNames(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	put(t, s, makeDoc(t, "en", "https://docs.tenable.com/release-notes/Content/widget/2026.htm",
		`<h1>Widget 2026 Release Notes</h1><h2>Widget Manager 3.2.1 (2026-10-22)</h2><p>Fixes.</p>`))
	g, _ := site.GuideOf("https://docs.tenable.com/widget/Content/A.htm")
	err := s.Update(ctx, func(tx *Tx) error {
		toc := []*site.TocEntry{{Title: "Welcome", URL: "https://docs.tenable.com/widget/Content/A.htm"}}
		if err := tx.PutToc(g, toc); err != nil {
			return err
		}
		if err := tx.PutToc(g, toc); err != nil { // replacing it
			return err
		}
		if err := tx.PutSynonyms("scan", "assessment", "assessment"); err != nil {
			return err
		}
		if err := tx.PutAlias("Tenable.io", "Vulnerability Management", "product"); err != nil {
			return err
		}
		return tx.SetMeta("built_at", "2026-11-06")
	})
	if err != nil {
		t.Fatal(err)
	}
	var version, date string
	s.db.QueryRow("SELECT version, date FROM releases").Scan(&version, &date)
	if version != "3.2.1" || date != "2026-10-22" {
		t.Errorf("release = %q %q", version, date)
	}
	counts, _ := s.Counts(ctx)
	var syn, alias int
	s.db.QueryRow("SELECT count(*) FROM synonyms").Scan(&syn)
	s.db.QueryRow("SELECT count(*) FROM aliases WHERE alias = 'TENABLE.IO'").Scan(&alias)
	if counts["tocs"] != 1 || syn != 1 || alias != 1 {
		t.Errorf("tocs %d, synonyms %d, aliases %d", counts["tocs"], syn, alias)
	}
	if v, _ := s.Meta(ctx, "built_at"); v != "2026-11-06" {
		t.Errorf("meta = %q", v)
	}
	if v, _ := s.Meta(ctx, "missing"); v != "" {
		t.Errorf("missing meta = %q", v)
	}
}

func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "en-current.db")
	k := Key{Site: "en"}
	s, err := openShard(path, k, migrations)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, makeDoc(t, "en", "https://docs.tenable.com/widget/Content/A.htm", `<h1>Kept</h1><p>Survives the migration.</p>`))
	s.Close()

	v2 := func(tx *sql.Tx, _ string) error {
		_, err := tx.Exec("ALTER TABLE pages ADD COLUMN extra TEXT NOT NULL DEFAULT 'x'")
		return err
	}
	s, err = openShard(path, k, append(migrations[:len(migrations):len(migrations)], v2))
	if err != nil {
		t.Fatal(err)
	}
	var extra string
	if v, _ := s.SchemaVersion(ctx); v != len(migrations)+1 {
		t.Errorf("version = %d", v)
	}
	if err := s.db.QueryRow("SELECT extra FROM pages").Scan(&extra); err != nil || extra != "x" {
		t.Errorf("migrated row = %q, %v", extra, err)
	}
	if got := search(t, s, "survives"); len(got) != 1 {
		t.Errorf("search after migration = %q", got)
	}
	s.Close()

	// This build can't read the newer file, or a file for another shard.
	if _, err := openShard(path, k, migrations); !errors.Is(err, ErrSchemaTooNew) {
		t.Errorf("newer schema = %v", err)
	}
	other := filepath.Join(t.TempDir(), "x.db")
	s, _ = OpenShard(other, Key{Site: "fr"})
	s.Close()
	if _, err := OpenShard(other, Key{Site: "en"}); !errors.Is(err, ErrWrongShard) {
		t.Errorf("wrong shard = %v", err)
	}
	if _, err := OpenShard(other, Key{Site: "xx"}); err == nil {
		t.Error("unknown site should be an error")
	}
	bad := v2
	if _, err := openShard(filepath.Join(t.TempDir(), "y.db"), k, []migration{bad}); err == nil {
		t.Error("a failing migration should be an error")
	}
}

func TestBuildAndInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if len(ix.Keys()) != 0 {
		t.Fatalf("keys = %v", ix.Keys())
	}
	install := func(k Key, text string) {
		t.Helper()
		b, err := ix.Build(k)
		if err != nil {
			t.Fatal(err)
		}
		put(t, b, makeDoc(t, k.Site, "https://"+mustSite(t, k.Site).Host+"/widget/Content/A.htm", `<h1>A</h1><p>`+text+`</p>`))
		if err := b.Optimize(ctx); err != nil {
			t.Fatal(err)
		}
		if err := ix.Install(b); err != nil {
			t.Fatal(err)
		}
	}
	install(Key{Site: "en"}, "first build")
	install(Key{Site: "en", Older: true}, "older versions")
	install(Key{Site: "fr"}, "première version")
	if got := ix.Keys(); len(got) != 3 || got[0] != (Key{Site: "en"}) || got[1] != (Key{Site: "en", Older: true}) {
		t.Errorf("keys = %v", got)
	}
	// Rebuilding one shard leaves the others alone.
	install(Key{Site: "en"}, "second build")
	err = ix.With(Key{Site: "en"}, func(s *Shard) error {
		if got := search(t, s, "second"); len(got) != 1 {
			t.Errorf("rebuilt shard: %q", got)
		}
		if got := search(t, s, "first"); len(got) != 0 {
			t.Errorf("old build still found: %q", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := search(t, ix.Shard(Key{Site: "fr"}), "premiere"); len(got) != 1 {
		t.Errorf("fr shard: %q", got)
	}
	if err := ix.With(Key{Site: "de"}, func(*Shard) error { return nil }); err == nil {
		t.Error("a missing shard should be an error")
	}
	// A discarded build leaves no files.
	b, err := ix.Build(Key{Site: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Discard(b); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	// Reopening finds the installed shards.
	ix2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()
	if got := len(ix2.Keys()); got != 3 {
		t.Errorf("reopened %d shards", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "building") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func mustSite(t *testing.T, code string) site.Site {
	s, ok := site.ByCode(code)
	if !ok {
		t.Fatalf("site %q", code)
	}
	return s
}

func TestOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits don't apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "a", "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ix.Build(Key{Site: "en"})
	ix.Install(b)
	ix.Close()
	for _, p := range []string{dir, filepath.Join(dir, "en-current.db")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s is %v", p, perm)
		}
	}
}

func TestDefaultDirAndKeys(t *testing.T) {
	if d, err := DefaultDir(); err != nil || !strings.HasSuffix(filepath.ToSlash(d), "tenable-docs-mcp/index") {
		t.Errorf("DefaultDir = %q, %v", d, err)
	}
	if ks := Keys(); len(ks) != 2*len(site.Sites) || ks[1].String() != "en-older" || ks[0].FileName() != "en-current.db" {
		t.Errorf("keys = %v", ks)
	}
	if _, err := Tokenizer("xx"); err == nil {
		t.Error("unknown site")
	}
}
