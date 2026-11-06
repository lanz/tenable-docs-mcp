// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package site reads Tenable's documentation sites: their sitemaps, robots.txt rules,
// guide tables of contents, and pages, either over HTTP or from local copies of the
// sites' published files.
package site

import (
	"net/url"
	"strings"
)

// Site is one of the documentation sites.
type Site struct {
	// Code identifies the site: its subdomain, or "en" for docs.tenable.com.
	Code string
	// Lang is the content's language as a BCP 47 tag.
	Lang string
	// Host is the site's host name.
	Host string
}

// BaseURL returns the site's root URL, with a trailing slash.
func (s Site) BaseURL() string { return "https://" + s.Host + "/" }

// SitemapURL returns the URL of the site's sitemap.
func (s Site) SitemapURL() string { return s.BaseURL() + "Sitemap.xml" }

// RobotsURL returns the URL of the site's robots.txt.
func (s Site) RobotsURL() string { return s.BaseURL() + "robots.txt" }

// Sites lists every documentation site: English first, then the translated sites.
var Sites = []Site{
	{Code: "en", Lang: "en", Host: "docs.tenable.com"},
	{Code: "jp", Lang: "ja", Host: "jp.docs.tenable.com"},
	{Code: "zh-cn", Lang: "zh-Hans", Host: "zh-cn.docs.tenable.com"},
	{Code: "zh-tw", Lang: "zh-Hant", Host: "zh-tw.docs.tenable.com"},
	{Code: "kr", Lang: "ko", Host: "kr.docs.tenable.com"},
	{Code: "fr", Lang: "fr", Host: "fr.docs.tenable.com"},
	{Code: "de", Lang: "de", Host: "de.docs.tenable.com"},
	{Code: "es", Lang: "es", Host: "es.docs.tenable.com"},
}

// ByHost returns the site with this host name (case-insensitive, port ignored).
func ByHost(host string) (Site, bool) {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	for _, s := range Sites {
		if s.Host == host {
			return s, true
		}
	}
	return Site{}, false
}

// ByCode returns the site with this code ("en", "jp", "zh-cn", …).
func ByCode(code string) (Site, bool) {
	code = strings.ToLower(code)
	for _, s := range Sites {
		if s.Code == code {
			return s, true
		}
	}
	return Site{}, false
}

// IsDocsHost reports whether host is one of the documentation sites.
func IsDocsHost(host string) bool {
	_, ok := ByHost(host)
	return ok
}

// IsDocsURL reports whether raw is an https URL on one of the documentation sites.
func IsDocsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && IsDocsHost(u.Host)
}
