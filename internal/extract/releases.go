// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package extract

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// ReleaseEntry is one release in a release notes page: a version, a date, or both.
type ReleaseEntry struct {
	// Title is the entry's heading, such as "Widget Manager 3.2.1 (2026-10-22)" or
	// "October 28, 2026".
	Title   string
	Version string
	// Date is the entry's date as YYYY-MM-DD, or YYYY-MM when the heading gives only a
	// month.
	Date string
	// Sections lists the indexes of the page's sections the entry spans.
	Sections []int
	// Markdown is the entry's content.
	Markdown string
	Place
}

// IsReleaseNotes reports whether a page is in the release notes guide.
func IsReleaseNotes(pageURL string) bool {
	g, ok := site.GuideOf(pageURL)
	return ok && (g.Path == "release-notes" || strings.HasPrefix(g.Path, "release-notes/"))
}

// ReleaseNotes splits a release notes page into its entries: each h2 heading with a
// date or a version starts one, and the headings after it without either (such as
// "New Features") belong to it.
func ReleaseNotes(p *page.Page) []ReleaseEntry {
	var out []ReleaseEntry
	var parts []string
	flush := func() {
		if n := len(out); n > 0 && len(parts) > 0 {
			out[n-1].Markdown = strings.Join(parts, "\n\n")
		}
		parts = nil
	}
	h2 := ""
	for i, s := range p.Sections {
		if s.Level < 2 || len(s.Trail) < 2 {
			continue
		}
		if s.Trail[1] != h2 {
			h2 = s.Trail[1]
			version, date := ParseRelease(h2)
			if version != "" || date != "" {
				flush()
				out = append(out, ReleaseEntry{Title: h2, Version: version, Date: date, Place: placeOf(p, i)})
			}
		}
		if len(out) == 0 {
			continue
		}
		e := &out[len(out)-1]
		e.Sections = append(e.Sections, i)
		parts = append(parts, s.Markdown)
	}
	flush()
	return out
}

// months maps month names, in English (with abbreviations), French, German, and
// Spanish, to their numbers.
var months = func() map[string]int {
	m := map[string]int{"sept": 9}
	for _, names := range []string{
		"january february march april may june july august september october november december",
		"jan feb mar apr may jun jul aug sep oct nov dec",
		"janvier février mars avril mai juin juillet août septembre octobre novembre décembre",
		"januar februar märz april mai juni juli august september oktober november dezember",
		"enero febrero marzo abril mayo junio julio agosto septiembre octubre noviembre diciembre",
	} {
		for i, name := range strings.Fields(names) {
			m[name] = i + 1
		}
	}
	return m
}()

var (
	// monthAlt matches any month name, trying longer names first so "september" wins
	// over "sep".
	monthAlt = func() string {
		names := slices.Collect(maps.Keys(months))
		slices.SortFunc(names, func(a, b string) int {
			if len(a) != len(b) {
				return len(b) - len(a)
			}
			return strings.Compare(a, b)
		})
		for i, n := range names {
			names[i] = regexp.QuoteMeta(n)
		}
		return strings.Join(names, "|")
	}()
	isoDate       = regexp.MustCompile(`\b(\d{4})-(\d{1,2})-(\d{1,2})\b`)
	cjkDate       = regexp.MustCompile(`(\d{4})\s*[年년]\s*(\d{1,2})\s*[月월](?:\s*(\d{1,2})\s*[日일])?`)
	monthDayYear  = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\.?\s+(\d{1,2}),?\s+(\d{4})\b`)
	dayMonthYear  = regexp.MustCompile(`(?i)\b(\d{1,2})\.?\s+(?:de\s+)?(` + monthAlt + `)\.?\s+(?:de\s+)?(\d{4})\b`)
	monthYear     = regexp.MustCompile(`(?i)\b(` + monthAlt + `)\.?\s+(?:de\s+)?(\d{4})\b`)
	versionNumber = regexp.MustCompile(`\b\d+(?:\.\d+)+\b`)
)

// ParseRelease finds the version and date in a release heading. The date comes back as
// YYYY-MM-DD, or YYYY-MM when only a month is given.
func ParseRelease(heading string) (version, date string) {
	rest := heading
	take := func(re *regexp.Regexp, conv func(m []string) string) {
		if date != "" {
			return
		}
		if m := re.FindStringSubmatch(rest); m != nil {
			if d := conv(m); d != "" {
				date = d
				rest = strings.Replace(rest, m[0], " ", 1)
			}
		}
	}
	take(isoDate, func(m []string) string { return ymd(m[1], m[2], m[3]) })
	take(cjkDate, func(m []string) string { return ymd(m[1], m[2], m[3]) })
	take(monthDayYear, func(m []string) string { return ymd(m[3], month(m[1]), m[2]) })
	take(dayMonthYear, func(m []string) string { return ymd(m[3], month(m[2]), m[1]) })
	take(monthYear, func(m []string) string { return ymd(m[2], month(m[1]), "") })
	version = versionNumber.FindString(rest)
	return version, date
}

func month(name string) string {
	if m, ok := months[strings.ToLower(name)]; ok {
		return strconv.Itoa(m)
	}
	return ""
}

// ymd formats a date, or returns "" when it isn't one.
func ymd(y, m, d string) string {
	yi, err1 := strconv.Atoi(y)
	mi, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || yi < 1990 || yi > 2100 || mi < 1 || mi > 12 {
		return ""
	}
	if d == "" {
		return fmt.Sprintf("%04d-%02d", yi, mi)
	}
	di, err := strconv.Atoi(d)
	if err != nil || di < 1 || di > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", yi, mi, di)
}
