// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// doctorURLs are what doctor fetches to check the network; tests replace them, and
// doctorClient.
var (
	doctorURLs = []string{
		"https://docs.tenable.com/robots.txt",
		published.DefaultURL + "manifest.json",
	}
	doctorClient = &http.Client{Timeout: 20 * time.Second}
)

// publicCAs are the certificate authorities docs.tenable.com's certificates come from.
// A certificate from anyone else means something between this computer and the site
// is inspecting TLS.
var publicCAs = []string{"Google Trust Services", "Let's Encrypt", "DigiCert", "Sectigo", "Cloudflare", "GlobalSign",
	"Amazon", "Entrust", "GoDaddy", "IdenTrust", "ISRG", "Baltimore", "SSL.com"}

// doctor checks what the server needs, and prints what it finds. It exits with 1 when a
// check fails.
func doctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(server.Name+" doctor", flag.ContinueOnError)
	dir := fs.String("index", "", "the index folder (default: $TDM_INDEX_DIR, or the user's cache folder)")
	setUsage(fs, stderr, "doctor [-index DIR]", "check the index, the network, and this program's path, and say what to fix. It exits with 1 when a check fails")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	failed := false
	ok := func(format string, a ...any) { fmt.Fprintf(stdout, "✓ "+format+"\n", a...) }
	warn := func(format string, a ...any) { fmt.Fprintf(stdout, "! "+format+"\n", a...) }
	bad := func(format string, a ...any) { failed = true; fmt.Fprintf(stdout, "✗ "+format+"\n", a...) }

	fmt.Fprintf(stdout, "%s %s (%s, %s/%s)\n\n", server.Name, version.Get(), runtime.Version(), runtime.GOOS, runtime.GOARCH)

	// The index.
	d, err := indexDir(*dir)
	if err != nil {
		bad("no index folder: %v", err)
	} else {
		checkIndex(d, ok, warn, bad)
	}

	// Updates.
	if d != "" {
		st, _ := published.ReadState(d)
		switch {
		case st.LastError != "":
			warn("the last update check failed at %s: %s", st.ErrorAt, st.LastError)
		case st.CheckedAt != "":
			ok("last checked for an update at %s; the snapshot was built %s", st.CheckedAt, st.BuiltAt)
		default:
			warn("not checked for an update yet; the server checks when it starts, and `%s index pull` does now", server.Name)
		}
		if st.Source == published.SourceLive {
			warn("the index is kept up to date from the live sites, since the published snapshot couldn't be reached")
		}
		if u, ok := version.Available(st.LatestRelease, version.Get()); ok {
			warn("%s", u.Message())
		}
	}
	if v := strings.TrimSpace(os.Getenv("TDM_INDEX_URL")); strings.EqualFold(v, "off") {
		warn("automatic updates are off (TDM_INDEX_URL=off)")
	}

	// The network.
	for _, u := range doctorURLs {
		checkURL(ctx, indexURL(u), ok, warn, bad)
	}

	// The binary, as client configs will use it.
	if bin, err := executable(); err != nil {
		bad("can't find this program's path: %v", err)
	} else if isTemporary(bin) {
		warn("%s is a temporary build (go run); install the binary before pointing an MCP client at it", bin)
	} else {
		ok("this program is %s; `%s config` prints client settings with that path", bin, server.Name)
	}

	// The user's synonyms.
	if path, err := synonymsPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			if _, err := search.LoadSynonyms(path); err != nil {
				bad("your synonyms aren't used: %v", err)
			} else {
				ok("your synonyms in %s are used", path)
			}
		}
	}

	if failed {
		return 1
	}
	return 0
}

// indexURL is u, unless it's the default manifest and TDM_INDEX_URL names another
// https:// folder, which is what the server downloads from.
func indexURL(u string) string {
	v := strings.TrimSpace(os.Getenv("TDM_INDEX_URL"))
	if u != published.DefaultURL+"manifest.json" || !strings.HasPrefix(v, "https://") {
		return u
	}
	if !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v + "manifest.json"
}

func checkIndex(d string, ok, warn, bad func(string, ...any)) {
	info, err := os.Stat(d)
	switch {
	case errors.Is(err, os.ErrNotExist):
		bad("the index folder %s doesn't exist yet; `%s index pull` downloads the index", d, server.Name)
		return
	case err != nil:
		bad("the index folder %s: %v", d, err)
		return
	case runtime.GOOS == "windows":
		// Permission bits don't apply; the user's cache folder is private to them.
	case info.Mode().Perm()&0o077 != 0:
		bad("the index folder %s can be read by others (%v); the server makes it 0700 when it opens it", d, info.Mode().Perm())
	default:
		ok("the index folder %s is readable only by you", d)
	}
	ix, err := index.Open(d)
	if err != nil {
		bad("the index doesn't open: %v", err)
		return
	}
	defer ix.Close()
	keys := ix.Keys()
	if len(keys) == 0 {
		bad("the index in %s isn't built; `%s index pull` downloads it", d, server.Name)
		return
	}
	pages, old := 0, 0
	for _, k := range keys {
		_ = ix.With(k, func(sh *index.Shard) error {
			if v, _ := sh.SchemaVersion(context.Background()); v != index.SchemaVersion() {
				old++
			}
			c, err := sh.Counts(context.Background())
			if err == nil {
				pages += c["pages"]
			}
			return nil
		})
	}
	if old > 0 {
		bad("%d shards aren't at schema %d", old, index.SchemaVersion())
		return
	}
	ok("the index opens: %d shards, %d pages, schema %d", len(keys), pages, index.SchemaVersion())
}

func checkURL(ctx context.Context, u string, ok, warn, bad func(string, ...any)) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		bad("%s: %v", u, err)
		return
	}
	req.Header.Set("User-Agent", server.UserAgent())
	resp, err := doctorClient.Do(req)
	if err != nil {
		var unknown x509.UnknownAuthorityError
		if errors.As(err, &unknown) {
			issuer := "an unknown authority"
			if unknown.Cert != nil {
				issuer = issuerName(unknown.Cert)
			}
			bad("%s: its certificate is from %s, which this computer doesn't trust. A TLS-inspecting proxy is likely in the way: "+
				"install its CA certificate in the system's certificate store", hostOf(u), issuer)
			return
		}
		bad("%s can't be reached: %v", hostOf(u), err)
		if os.Getenv("HTTPS_PROXY") == "" && os.Getenv("https_proxy") == "" {
			warn("HTTPS_PROXY isn't set; if your network needs a proxy, set it in the server's environment")
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bad("%s: HTTP %d", u, resp.StatusCode)
		return
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[len(resp.TLS.PeerCertificates)-1]
		if name := issuerName(cert); !isPublicCA(name) && !isPublicCA(issuerName(resp.TLS.PeerCertificates[0])) {
			warn("%s is reachable, through a TLS-inspecting proxy (%s). That works, because this computer trusts its certificate", hostOf(u), name)
			return
		}
	}
	ok("%s is reachable", strings.TrimSuffix(strings.TrimPrefix(u, "https://"), "/"))
}

func issuerName(c *x509.Certificate) string {
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return c.Issuer.CommonName
}

func isPublicCA(name string) bool {
	for _, ca := range publicCAs {
		if strings.Contains(strings.ToLower(name), strings.ToLower(ca)) {
			return true
		}
	}
	return false
}

func hostOf(u string) string {
	h := strings.TrimPrefix(u, "https://")
	h, _, _ = strings.Cut(h, "/")
	return h
}
