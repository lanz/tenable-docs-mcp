// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/crawl"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/site"
	"github.com/lanz/tenable-docs-mcp/internal/snapshot"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

const indexUsage = `Usage:
  %[1]s index pull [-sites en,jp] [-index DIR]
        download the published index from docs.tenable.com, or update it: only shards
        that changed are downloaded and rebuilt
  %[1]s index crawl [-sites en,jp] [-index DIR]
        build or update the index from the live sites instead: a site the index has
        nothing for is read in full (all eight take about an hour); after that, only
        what changed is fetched
  %[1]s index build -source "en=DIR;jp=DIR" [-sites en,jp] [-guide-names FILE] [-out DIR | -index DIR] [-json]
        read the docs from local copies of the sites (one folder per site, as the
        sites publish them) and write a snapshot to -out, or an index to -index;
        -guide-names names the guides, from a JSON file of site codes to guide paths
        to names ({"en": {"nessus": "Tenable Nessus User Guide"}})
  %[1]s index import [-sites en,jp] [-index DIR] SNAPSHOT
        verify a snapshot and build the local index from it
  %[1]s index verify SNAPSHOT
        check a snapshot's files against its manifest
`

// now is the clock, for tests.
var now = time.Now

func indexCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	switch args[0] {
	case "pull":
		return indexPull(ctx, args[1:], stdout, stderr)
	case "crawl":
		return indexCrawl(ctx, args[1:], stdout, stderr)
	case "build":
		return indexBuild(ctx, args[1:], stdout, stderr)
	case "import":
		return indexImport(ctx, args[1:], stdout, stderr)
	case "verify":
		return indexVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "%s index: unknown command %q\n\n"+indexUsage, server.Name, args[0], server.Name)
		return 2
	}
}

// indexDir returns the index folder: dir, $TDM_INDEX_DIR, or the default.
func indexDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if d := os.Getenv("TDM_INDEX_DIR"); d != "" {
		return d, nil
	}
	return index.DefaultDir()
}

// pickSites returns the sites a list of codes names, in the order the site list has
// them, or all of available when the list is empty.
func pickSites(list string, available []site.Site) ([]site.Site, error) {
	if strings.TrimSpace(list) == "" {
		return available, nil
	}
	var want []string
	for _, c := range strings.Split(list, ",") {
		c = strings.TrimSpace(c)
		if _, ok := site.ByCode(c); !ok {
			return nil, fmt.Errorf("no site %q", c)
		}
		want = append(want, c)
	}
	var out []site.Site
	for _, s := range available {
		if slices.Contains(want, s.Code) {
			out = append(out, s)
		}
	}
	if len(out) != len(want) {
		return nil, fmt.Errorf("-sites names a site that isn't available: %s", list)
	}
	return out, nil
}

// progressPrinter reports a build's progress on stderr now and then.
func progressPrinter(stderr io.Writer) func(string, int, int) {
	return func(code string, done, total int) {
		if done%2000 == 0 || done == total {
			fmt.Fprintf(stderr, "%s: %d of %d sitemap entries\n", code, done, total)
		}
	}
}

func indexBuild(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" index build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", `the sites' local copies, as "en=DIR;jp=DIR" (as TDM_SITE_DIRS)`)
	sitesFlag := fs.String("sites", "", "only these sites, such as en,jp (default: every site in -source)")
	out := fs.String("out", "", "write a snapshot to this folder")
	dir := fs.String("index", "", "write the index in this folder instead (default: $TDM_INDEX_DIR, or the user's cache folder)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	namesFile := fs.String("guide-names", "", `name the guides from this JSON file: {"en": {"nessus": "Tenable Nessus User Guide"}}`)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *source == "" || fs.NArg() > 0 || *out != "" && *dir != "" {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	dirs, err := site.ParseSiteDirs(*source)
	if err != nil {
		fmt.Fprintln(stderr, "index build:", err)
		return 2
	}
	src, err := site.NewLocal(dirs)
	if err != nil {
		fmt.Fprintln(stderr, "index build:", err)
		return 2
	}
	sites, err := pickSites(*sitesFlag, src.Sites())
	if err != nil {
		fmt.Fprintln(stderr, "index build:", err)
		return 2
	}
	var names map[string]map[string]string
	if *namesFile != "" {
		if names, err = readGuideNames(*namesFile); err != nil {
			fmt.Fprintln(stderr, "index build:", err)
			return 2
		}
	}
	builtAt := now().UTC()
	opts := build.Options{Source: src, Sites: sites, Agent: server.Name, GuideNames: names, Progress: progressPrinter(stderr)}

	var rep build.Report
	var where string
	if *out != "" {
		w, err := snapshot.NewWriter(*out, server.Name+" "+version.Get(), builtAt)
		if err != nil {
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		if rep, err = build.Run(ctx, opts, w); err != nil {
			w.Abort()
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		if _, err := w.Close(); err != nil {
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		where = "snapshot in " + *out
	} else {
		d, err := indexDir(*dir)
		if err != nil {
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		ix, err := index.Open(d)
		if err != nil {
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		defer ix.Close()
		sink := build.NewIndexSink(ix, builtAt.Format(time.RFC3339))
		if rep, err = build.Run(ctx, opts, sink); err != nil {
			sink.Discard()
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		if err := sink.Finish(ctx); err != nil {
			sink.Discard()
			fmt.Fprintln(stderr, "index build:", err)
			return 1
		}
		where = "index in " + d
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		fmt.Fprintf(stdout, "Built %d pages into the %s: %s; %d tables of contents; %d left out by robots.txt; %d skipped.\n",
			rep.Total(), where, shardCounts(rep.Pages), rep.Tocs, rep.Blocked, rep.Skipped)
		for _, f := range rep.Failed {
			fmt.Fprintln(stdout, "Failed:", f)
		}
		if len(rep.Unnamed) > 0 {
			what := "guides have"
			if len(rep.Unnamed) == 1 {
				what = "guide has"
			}
			fmt.Fprintf(stdout, "%d %s no name: %s\n", len(rep.Unnamed), what, strings.Join(rep.Unnamed, ", "))
		}
	}
	if len(rep.Failed) > 0 {
		return 1
	}
	return 0
}

// readGuideNames reads a file of guide names: site codes to guide paths to names.
func readGuideNames(file string) (map[string]map[string]string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var names map[string]map[string]string
	if err := json.Unmarshal(b, &names); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	for code := range names {
		if _, ok := site.ByCode(code); !ok {
			return nil, fmt.Errorf("%s: no site %q", file, code)
		}
	}
	return names, nil
}

func shardCounts(pages map[string]int) string {
	var keys []string
	for k := range pages {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, pages[k]))
	}
	return strings.Join(parts, ", ")
}

func indexImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" index import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sitesFlag := fs.String("sites", "", "only these sites, such as en,jp (default: every site in the snapshot)")
	dir := fs.String("index", "", "the index folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	snap := fs.Arg(0)
	m, err := snapshot.ReadManifest(snap)
	if err != nil {
		fmt.Fprintln(stderr, "index import:", err)
		return 1
	}
	var inSnapshot []site.Site
	for _, s := range site.Sites {
		if slices.ContainsFunc(m.Shards, func(sh snapshot.Shard) bool { return sh.Site == s.Code }) {
			inSnapshot = append(inSnapshot, s)
		}
	}
	sites, err := pickSites(*sitesFlag, inSnapshot)
	if err != nil {
		fmt.Fprintln(stderr, "index import:", err)
		return 2
	}
	d, err := indexDir(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "index import:", err)
		return 1
	}
	ix, err := index.Open(d)
	if err != nil {
		fmt.Fprintln(stderr, "index import:", err)
		return 1
	}
	defer ix.Close()
	sink := build.NewIndexSink(ix, m.BuiltAt)
	pages := map[string]int{}
	for _, sh := range m.Shards {
		if !slices.ContainsFunc(sites, func(s site.Site) bool { return s.Code == sh.Site }) {
			continue
		}
		if err := snapshot.Read(ctx, snap, sh, sink); err != nil {
			sink.Discard()
			fmt.Fprintln(stderr, "index import:", err)
			return 1
		}
		pages[sh.Key] = sh.Pages
	}
	if err := sink.Finish(ctx); err != nil {
		sink.Discard()
		fmt.Fprintln(stderr, "index import:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Imported the snapshot built %s into the index in %s: %s.\n", m.BuiltAt, d, shardCounts(pages))
	return 0
}

// publishedUpdater returns the updater for the published snapshot, from $TDM_INDEX_URL
// (default published.DefaultURL; "off" turns updates off, and gives nil) and a list of
// site codes (default every site).
func publishedUpdater(ix *index.Index, siteList string) (*published.Updater, error) {
	url := strings.TrimSpace(os.Getenv("TDM_INDEX_URL"))
	if strings.EqualFold(url, "off") {
		return nil, nil
	}
	if url == "" {
		url = published.DefaultURL
	}
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("TDM_INDEX_URL must be an https:// address, or off: %q", url)
	}
	sites, err := pickSites(siteList, site.Sites)
	if err != nil {
		return nil, err
	}
	u := &published.Updater{URL: url, UserAgent: server.UserAgent(), Index: ix, Now: now}
	if len(sites) < len(site.Sites) {
		for _, s := range sites {
			u.Sites = append(u.Sites, s.Code)
		}
	}
	return u, nil
}

// liveCrawler returns the crawler that keeps the index up to date from the live sites
// when the published snapshot can't be reached, and adds pages on demand; $TDM_CRAWL
// set to off turns both off, and gives nil.
func liveCrawler(ix *index.Index, live site.Source, siteList string) (*crawl.Crawler, error) {
	switch v := strings.TrimSpace(os.Getenv("TDM_CRAWL")); {
	case strings.EqualFold(v, "off"):
		return nil, nil
	case v != "" && !strings.EqualFold(v, "on"):
		return nil, fmt.Errorf("TDM_CRAWL must be on or off: %q", v)
	}
	sites, err := pickSites(siteList, site.Sites)
	if err != nil {
		return nil, err
	}
	return &crawl.Crawler{Index: ix, Source: live, Agent: server.Name, Sites: sites, Now: now}, nil
}

func indexPull(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" index pull", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sitesFlag := fs.String("sites", "", "only these sites, such as en,jp (default: $TDM_SITES, or every site)")
	dir := fs.String("index", "", "the index folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	d, err := indexDir(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "index pull:", err)
		return 1
	}
	ix, err := index.Open(d)
	if err != nil {
		fmt.Fprintln(stderr, "index pull:", err)
		return 1
	}
	defer ix.Close()
	list := *sitesFlag
	if list == "" {
		list = os.Getenv("TDM_SITES")
	}
	u, err := publishedUpdater(ix, list)
	if err != nil {
		fmt.Fprintln(stderr, "index pull:", err)
		return 2
	}
	if u == nil {
		fmt.Fprintln(stderr, "index pull: TDM_INDEX_URL is off")
		return 2
	}
	u.Client = pullClient
	u.Progress = func(msg string) { fmt.Fprintln(stderr, msg) }
	res, err := u.Update(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "index pull:", err)
		return 1
	}
	switch {
	case len(res.Imported) == 0:
		fmt.Fprintf(stdout, "The index in %s is up to date with the snapshot built %s.\n", d, res.BuiltAt)
	default:
		fmt.Fprintf(stdout, "Updated the index in %s from the snapshot built %s: rebuilt %s; %d unchanged; downloaded %.1f MB.\n",
			d, res.BuiltAt, strings.Join(res.Imported, ", "), res.Unchanged, float64(res.Bytes)/(1<<20))
	}
	return 0
}

// pullClient makes index pull's requests; nil is the updater's default. Tests replace
// it.
var pullClient *http.Client

// crawlSource is what index crawl reads from; nil is the live sites. Tests replace it.
var crawlSource site.Source

func indexCrawl(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" index crawl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sitesFlag := fs.String("sites", "", "only these sites, such as en,jp (default: $TDM_SITES, or every site)")
	dir := fs.String("index", "", "the index folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	list := *sitesFlag
	if list == "" {
		list = os.Getenv("TDM_SITES")
	}
	sites, err := pickSites(list, site.Sites)
	if err != nil {
		fmt.Fprintln(stderr, "index crawl:", err)
		return 2
	}
	d, err := indexDir(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "index crawl:", err)
		return 1
	}
	ix, err := index.Open(d)
	if err != nil {
		fmt.Fprintln(stderr, "index crawl:", err)
		return 1
	}
	defer ix.Close()
	src := crawlSource
	if src == nil {
		src = site.NewHTTP(site.HTTPConfig{UserAgent: server.UserAgent()})
	}
	l, err := published.Lock(d, now())
	if err != nil {
		fmt.Fprintln(stderr, "index crawl:", err)
		return 1
	}
	defer l.Release()
	c := &crawl.Crawler{Index: ix, Source: src, Agent: server.Name, Sites: sites, Now: now,
		Progress: func(msg string) { l.Touch(); fmt.Fprintln(stderr, msg) }}
	rep, err := c.Sync(ctx)
	for _, f := range rep.Failed {
		fmt.Fprintln(stdout, "Failed:", f)
	}
	if err != nil {
		fmt.Fprintln(stderr, "index crawl:", err)
		return 1
	}
	st, _ := published.ReadState(d)
	st.CrawledAt, st.CrawlError, st.CrawlErrorAt, st.Source = now().UTC().Format(time.RFC3339), "", "", published.SourceLive
	_ = published.WriteState(d, st)
	built := ""
	if len(rep.Built) > 0 {
		built = "; read in full: " + strings.Join(rep.Built, ", ")
	}
	fmt.Fprintf(stdout, "Updated the index in %s from the live sites: %d pages checked, %d added, %d updated, %d unchanged, %d removed, %d redirected%s.\n",
		d, rep.Checked, rep.Added, rep.Updated, rep.Unchanged, rep.Removed, rep.Redirected, built)
	if len(rep.Failed) > 0 {
		return 1
	}
	return 0
}

func indexVerify(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, indexUsage, server.Name)
		return 2
	}
	m, err := snapshot.ReadManifest(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "index verify:", err)
		return 1
	}
	code := 0
	for _, sh := range m.Shards {
		if err := snapshot.Verify(args[0], sh); err != nil {
			fmt.Fprintf(stdout, "✗ %s: %v\n", sh.File, err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "✓ %s: %d pages, %.1f MB\n", sh.File, sh.Pages, float64(sh.Size)/(1<<20))
	}
	fmt.Fprintf(stdout, "Snapshot format %d, built %s by %s.\n", m.Format, m.BuiltAt, m.Generator)
	return code
}
