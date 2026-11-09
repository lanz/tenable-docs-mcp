// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// rebuild builds a shard from scratch with pages (path → body), takes the history of
// the shard it replaces, and installs it, as an update from a snapshot does.
func rebuild(t *testing.T, ix *Index, pages map[string]string) {
	t.Helper()
	k := Key{Site: "en"}
	b, err := ix.Build(k)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range pages {
		put(t, b, makeDoc(t, "en", "https://docs.tenable.com/"+path, body))
	}
	old := ix.Shard(k)
	if err := b.TakeHistory(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := ix.Install(b); err != nil {
		t.Fatal(err)
	}
}

func changesIn(t *testing.T, ix *Index) []Change {
	t.Helper()
	var out []Change
	err := ix.With(Key{Site: "en"}, func(s *Shard) error {
		var err error
		out, err = s.Changes(ctx, ChangeQuery{Since: "2000-01-01", Sections: true})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func kinds(cs []Change) map[string]string {
	out := map[string]string{}
	for _, c := range cs {
		out[c.URL[len("https://docs.tenable.com/"):]] = c.Change
	}
	return out
}

func TestHistoryAcrossRebuilds(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	install := `<h1>Install</h1><p>Download the package.</p><h2 id="Steps">Steps</h2><p>Run the installer.</p>`
	first := map[string]string{
		"nessus/Content/Install.htm": install,
		"nessus/Content/Old.htm":     `<h1>Old page</h1><p>Going away.</p>`,
	}

	// The first build has no history: its pages aren't changes.
	rebuild(t, ix, first)
	if cs := changesIn(t, ix); len(cs) != 0 {
		t.Fatalf("first build recorded %d changes", len(cs))
	}
	var since string
	_ = ix.With(Key{Site: "en"}, func(s *Shard) error { since, _ = s.HistorySince(ctx); return nil })
	if since == "" {
		t.Fatal("no history_since")
	}

	// The same pages again: nothing changed.
	rebuild(t, ix, first)
	if cs := changesIn(t, ix); len(cs) != 0 {
		t.Fatalf("an unchanged rebuild recorded %+v", cs)
	}

	// One page changed, one removed, one added.
	rebuild(t, ix, map[string]string{
		"nessus/Content/Install.htm": `<h1>Install</h1><p>Download the package.</p><h2 id="Steps">Steps</h2><p>Run the new installer.</p>` +
			`<h2 id="Verify">Verify</h2><p>Check the service.</p>`,
		"nessus/Content/New.htm": `<h1>New page</h1><p>Just published.</p>`,
	})
	cs := changesIn(t, ix)
	if got := kinds(cs); got["nessus/Content/Install.htm"] != "updated" || got["nessus/Content/Old.htm"] != "removed" || got["nessus/Content/New.htm"] != "added" || len(got) != 3 {
		t.Fatalf("changes = %v", got)
	}
	for _, c := range cs {
		switch c.Change {
		case "removed":
			if c.Title != "Old page" {
				t.Errorf("removed page's title = %q", c.Title)
			}
		case "updated":
			status := map[string]string{}
			for _, s := range c.Sections {
				status[s.Heading] = s.Status
			}
			if status["Steps"] != "changed" || status["Verify"] != "added" || len(status) != 2 {
				t.Errorf("sections = %+v", c.Sections)
			}
			for _, s := range c.Sections {
				if s.Heading == "Steps" && (!slices.Contains(s.Added, "Run the new installer.") || !slices.Contains(s.Removed, "Run the installer.")) {
					t.Errorf("steps = %+v", s)
				}
			}
		}
	}

	// The next rebuild keeps that history, and adds to it.
	rebuild(t, ix, map[string]string{
		"nessus/Content/Install.htm": `<h1>Install</h1><p>Download the package.</p><h2 id="Steps">Steps</h2><p>Run the new installer.</p>` +
			`<h2 id="Verify">Verify</h2><p>Check the service.</p>`,
	})
	cs = changesIn(t, ix)
	if len(cs) != 4 || cs[0].URL != "https://docs.tenable.com/nessus/Content/New.htm" || cs[0].Change != "removed" {
		t.Errorf("history after another rebuild = %+v", cs)
	}
	var updated *Change
	for i := range cs {
		if cs[i].Change == "updated" {
			updated = &cs[i]
		}
	}
	if updated == nil || len(updated.Sections) != 2 {
		t.Errorf("the update's sections weren't carried over: %+v", updated)
	}
}

func TestPutRecordsSectionChanges(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	u := "https://docs.tenable.com/nessus/Content/Install.htm"
	put(t, s, makeDoc(t, "en", u, `<h1>Install</h1><h2 id="Steps">Steps</h2><p>Run it.</p>`))
	put(t, s, makeDoc(t, "en", u, `<h1>Install</h1><h2 id="Steps">Steps</h2><p>Run it twice.</p>`))
	cs, err := s.Changes(ctx, ChangeQuery{Since: "2000-01-01", Sections: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Change != "updated" || len(cs[0].Sections) != 1 || cs[0].Sections[0].Heading != "Steps" ||
		!slices.Contains(cs[0].Sections[0].Added, "Run it twice.") || cs[0].Title != "Install" {
		t.Errorf("changes = %+v", cs)
	}
	// Filters: product and guide.
	if cs, _ := s.Changes(ctx, ChangeQuery{Since: "2000-01-01", Guide: "security-center"}); len(cs) != 0 {
		t.Errorf("guide filter: %+v", cs)
	}
	if cs, _ := s.Changes(ctx, ChangeQuery{Since: "2000-01-01", Guide: "nessus", Limit: 1}); len(cs) != 1 {
		t.Errorf("guide filter and limit: %+v", cs)
	}
}

// A shard updated in place, as by a crawl, keeps a year of history too.
func TestInPlaceUpdatesPruneHistory(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	old := time.Now().UTC().AddDate(-2, 0, 0).Format(time.RFC3339)
	err := s.Update(ctx, func(tx *Tx) error {
		if err := tx.SetMeta(historyMeta, old); err != nil {
			return err
		}
		_, err := tx.exec(`INSERT INTO changes (url, change, at) VALUES ('https://docs.tenable.com/nessus/Content/Old.htm', 'added', ?)`, old)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, makeDoc(t, "en", "https://docs.tenable.com/nessus/Content/New.htm", `<h1>New</h1><p>Text.</p>`))
	cs, err := s.Changes(ctx, ChangeQuery{Since: "2000-01-01"})
	if err != nil || len(cs) != 1 || !strings.HasSuffix(cs[0].URL, "New.htm") {
		t.Errorf("changes = %+v, %v", cs, err)
	}
	if since, _ := s.HistorySince(ctx); since <= old {
		t.Errorf("history_since = %s", since)
	}
}
