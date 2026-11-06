// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package search finds the documentation sections that answer a question: it reads
// product names (including retired ones) and the kind of question from it, searches
// the index, reranks the matches, and explains when nothing matches.
package search

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

// Request is a search.
type Request struct {
	Query string
	// Product, if set, limits the search to one product. It takes any of the product's
	// names, including retired ones.
	Product   string
	GuideType string
	Guide     string
	// Language picks the docs' language: a site code ("jp"), a BCP 47 tag ("ja"), or a
	// language's name. Empty means the question's own language. (Engine only.)
	Language string
	// Version picks a guide version ("6.3"). Empty means the version the question
	// names, or the current one. (Engine only, apart from filtering a shard.)
	Version string
	Limit   int
}

// Hit is one section in the results.
type Hit struct {
	URL       string
	PageURL   string
	Title     string
	Heading   string
	Location  string
	Snippet   string // matched terms are marked «like this»
	Product   string
	GuideType string
	Guide     string
	Version   string
	LastMod   string
	Score     float64 // higher is better
	// AlsoIn lists pages with the same title in other products or guides, which may be
	// what the user means instead.
	AlsoIn []Alternate
	// Language is the site code of the hit's site.
	Language string
	// EnglishURL is the section on the current English page, for citing a translation or
	// an older version; it's empty when there's no such page, or the hit is that page.
	EnglishURL string
	// Variants are the same page in other languages and versions. (Engine only.)
	Variants *Variants
}

// Alternate is a same-titled page in another product or guide.
type Alternate struct {
	Product string
	Guide   string
	URL     string
}

// Result is the answer to a search.
type Result struct {
	Hits []Hit
	// Parsed is how the question was read.
	Parsed Parsed
	// Product is the product filter applied, by its current name.
	Product string
	// Relaxed is set when no section had every word, and the results have only some.
	Relaxed bool
	// When nothing matches: other ways to spell the question, the products that do
	// have matches (when a product filter excluded them), and a message saying so.
	Suggestions    []string
	OtherProducts  []string
	NoResultsShown string
	// Language is the site code of the docs searched, and Requested the language asked
	// for or detected. They differ when the translated docs had nothing and the results
	// are in English instead.
	Language  string
	Requested string
	// Version is the guide version searched, or "" for the current versions.
	Version string
	// Notes explain choices made along the way, such as falling back to English.
	Notes []string
}

// Searcher searches one shard.
type Searcher struct {
	Shard *index.Shard

	vocabOnce sync.Once
	vocab     map[string]int
	vocabErr  error
}

// New returns a Searcher for a shard.
func New(s *index.Shard) *Searcher { return &Searcher{Shard: s} }

const poolSize = 120

// Search runs a search on this shard, reading the question in the shard's language.
// It ignores Request.Language; use Engine to search across languages and versions.
func (s *Searcher) Search(ctx context.Context, req Request) (*Result, error) {
	return s.searchParsed(ctx, ParseLang(req.Query, s.Shard.Key.Site), req, req.Version)
}

func (s *Searcher) searchParsed(ctx context.Context, parsed Parsed, req Request, version string) (*Result, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	res := &Result{Parsed: parsed}
	filter := index.Filter{GuideType: req.GuideType, Guide: req.Guide, Version: version}
	if req.Product != "" {
		p, ok := ProductNamed(req.Product)
		if !ok {
			return nil, fmt.Errorf("unknown product %q", req.Product)
		}
		filter.Product, res.Product = p, p
	}
	terms := parsed.Terms
	if len(terms) == 0 && len(parsed.Phrases) == 0 && len(parsed.Short) == 0 {
		// The question was only a product name, or only filler: search the product's
		// own name.
		for _, p := range parsed.Products {
			for _, t := range tokens(p) {
				terms = appendUnique(terms, t.low)
			}
		}
	}
	if len(terms) == 0 && len(parsed.Phrases) == 0 && len(parsed.Short) == 0 {
		res.NoResultsShown = "Ask about a task, setting, or feature, such as \"configure SAML\"."
		return res, nil
	}
	words := make([]word, 0, len(terms)+len(parsed.Short))
	for _, t := range terms {
		words = append(words, word{text: t})
	}
	for _, t := range parsed.Short {
		words = append(words, word{text: t, short: true})
	}

	cands, relaxed, err := s.candidates(ctx, words, parsed.Phrases, filter)
	if err != nil {
		return nil, err
	}
	res.Relaxed = relaxed
	res.Hits = rerank(cands, parsed, filter.Product, limit)
	if len(res.Hits) > 0 {
		return res, nil
	}

	// Nothing matched.
	res.Suggestions = s.suggest(ctx, terms)
	if filter.Product != "" {
		wide := filter
		wide.Product = ""
		if others, _, err := s.candidates(ctx, words, parsed.Phrases, wide); err == nil {
			for _, c := range others {
				if c.Product != "" {
					res.OtherProducts = appendUnique(res.OtherProducts, c.Product)
				}
				if len(res.OtherProducts) == 5 {
					break
				}
			}
		}
	}
	res.NoResultsShown = noResults(req.Query, res)
	return res, nil
}

// word is a word of the question: a term for the full-text index, or a short word to
// look for as text.
type word struct {
	text  string
	short bool
}

// query runs a search for every one of the words and phrases.
func (s *Searcher) query(ctx context.Context, words []word, phrases []string, f index.Filter, n int) ([]index.Candidate, error) {
	var terms []string
	f.Contains = slices.Clone(f.Contains)
	for _, w := range words {
		if w.short {
			f.Contains = append(f.Contains, w.text)
		} else {
			terms = append(terms, w.text)
		}
	}
	return s.Shard.Candidates(ctx, match(terms, phrases), f, n)
}

// candidates finds sections with every word; failing that, with all but one of them;
// failing that, with any of them.
func (s *Searcher) candidates(ctx context.Context, terms []word, phrases []string, f index.Filter) ([]index.Candidate, bool, error) {
	cands, err := s.query(ctx, terms, phrases, f, poolSize)
	if err != nil {
		return nil, false, err
	}
	// With few sections having every word, one stray word ("open", "need") may be
	// hiding the answer: add the sections that have all but one, ranked lower. With
	// none, do that even for two words.
	widen := len(cands) == 0 && len(terms) >= 2 || len(cands) < widenBelow && len(terms) >= 3 ||
		len(cands) < widenBelow/3 && len(terms) == 2
	if !widen || len(terms) > 6 {
		if len(cands) > 0 || len(terms) < 2 {
			return cands, false, nil
		}
	} else {
		exact := len(cands)
		seen := map[int64]bool{}
		for _, c := range cands {
			seen[c.SectionID] = true
		}
		for i := range terms {
			fewer := slices.Delete(slices.Clone(terms), i, i+1)
			cs, err := s.query(ctx, fewer, phrases, f, poolSize/2)
			if err != nil {
				return nil, false, err
			}
			for _, c := range cs {
				if !seen[c.SectionID] {
					seen[c.SectionID] = true
					c.Score *= penaltyMissingWord // bm25 scores are negative; this makes them worse
					cands = append(cands, c)
				}
			}
		}
		if len(cands) > 0 {
			slices.SortStableFunc(cands, func(a, b index.Candidate) int { return cmpFloat(a.Score, b.Score) })
			return cands, exact == 0, nil
		}
	}
	var full []string
	for _, w := range terms {
		if !w.short {
			full = append(full, w.text)
		}
	}
	if len(full) == 0 {
		return nil, false, nil
	}
	cands, err = s.Shard.Candidates(ctx, matchAny(full, phrases), f, poolSize)
	return cands, len(cands) > 0, err
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Boosts applied in reranking.
const (
	boostProduct        = 1.8 // the page's product is one the question names
	boostRelatedProduct = 1.25
	boostUserGuide      = 1.3 // how-to questions, on user guides
	penaltyNotHowTo     = 0.7 // how-to questions, on release notes, studies, and white papers
	boostReleaseNotes   = 2.2 // "what's new" questions, on release notes
	penaltyNotRelease   = 0.75
	boostRequirements   = 1.7 // requirements questions, on pages with requirements tables
	boostReqTitle       = 1.3 // … whose title says requirements
	boostInstallReqs    = 1.15
	boostTitleWords     = 0.8 // × the share of the question's words in the page title
	boostTitleExact     = 1.3 // the title is the question's words, give or take two
	boostLatestYear     = 1.6 // "what's new" questions, on the newest year's release notes
	penaltyOlderYear    = 0.8
	penaltyMissingWord  = 0.6 // sections missing one of the question's words
	widenBelow          = 30
	maxPerPage          = 2
)

// productWords are the words of product names, which a page title can add without
// changing what it's about.
var productWords = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range Products {
		for _, t := range tokens(p.Name + " " + strings.Join(p.Aliases, " ")) {
			m[stem(t.low)] = true
		}
	}
	return m
}()

var (
	parenthetical = regexp.MustCompile(`\([^)]*\)`)
	yearRe        = regexp.MustCompile(`\b(19|20)\d\d\b`)
)

// stem crudely reduces an English word to a stem, so "Requirements" and "requirement"
// compare equal in titles.
func stem(w string) string {
	w = strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "’s")
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 5 && strings.HasSuffix(w, "ing"):
		return w[:len(w)-3]
	case len(w) > 4 && strings.HasSuffix(w, "ed"):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "es") && strings.ContainsAny(w[len(w)-3:len(w)-2], "sxz"):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

// titleMatch returns how many of the terms a page title has, and how many other words
// (besides filler and product names) the title has.
func titleMatch(title string, terms []string) (covered, extra int) {
	words := map[string]bool{}
	for _, t := range tokens(parenthetical.ReplaceAllString(title, " ")) {
		words[stem(t.low)] = true
	}
	want := map[string]bool{}
	for _, t := range terms {
		want[stem(t)] = true
	}
	for w := range want {
		if words[w] {
			covered++
		}
	}
	for w := range words {
		if !want[w] && !stopwords[w] && !productWords[w] {
			extra++
		}
	}
	return covered, extra
}

// rerank scores candidates for the question, keeps at most two sections a page, and
// groups pages with the same title in other products or guides.
func rerank(cands []index.Candidate, p Parsed, productFilter string, limit int) []Hit {
	type scored struct {
		c     index.Candidate
		score float64
	}
	latest := ""
	if p.Intent == WhatsNew {
		for _, c := range cands {
			if y := yearRe.FindString(c.Title); y > latest {
				latest = y
			}
		}
	}
	var list []scored
	for _, c := range cands {
		score := -c.Score // bm25: lower is better; flip it
		if score <= 0 {
			score = 1e-9
		}
		// Release notes' titles name a product and a year, not features, so titles don't
		// count for "what's new" questions.
		if len(p.Terms) > 0 && p.Intent != WhatsNew {
			covered, extra := titleMatch(c.Title, p.Terms)
			score *= 1 + boostTitleWords*float64(covered)/float64(len(p.Terms))
			if covered == len(p.Terms) && extra <= 2 {
				score *= boostTitleExact
			}
		}
		if latest != "" {
			switch y := yearRe.FindString(c.Title); {
			case y == latest:
				score *= boostLatestYear
			case y != "":
				score *= penaltyOlderYear
			}
		}
		if productFilter == "" && len(p.Products) > 0 {
			switch {
			case slices.Contains(p.Products, c.Product):
				score *= boostProduct
			case slices.ContainsFunc(c.Related, func(r string) bool { return slices.Contains(p.Products, r) }):
				score *= boostRelatedProduct
			}
		}
		isRelease := c.GuideType == "Release Notes" || strings.HasPrefix(c.Guide, "release-notes")
		switch p.Intent {
		case HowTo:
			switch {
			case c.GuideType == "User Guide" && !isRelease:
				score *= boostUserGuide
			case isRelease || c.GuideType == "Cyber Exposure Study" || c.GuideType == "Whitepaper":
				score *= penaltyNotHowTo
			}
		case WhatsNew:
			if isRelease {
				score *= boostReleaseNotes
			} else {
				score *= penaltyNotRelease
			}
		case Requirements:
			if c.Requirements > 0 {
				score *= boostRequirements
			}
			if strings.Contains(strings.ToLower(c.Title), "requirement") {
				score *= boostReqTitle
			}
		}
		if p.Install && p.Intent != Requirements && c.Requirements > 0 {
			score *= boostInstallReqs
		}
		list = append(list, scored{c, score})
	}
	slices.SortStableFunc(list, func(a, b scored) int { return cmpFloat(b.score, a.score) })

	var hits []Hit
	perPage := map[string]int{}
	byTitle := map[string]int{} // lowercased page title → index in hits
	for _, s := range list {
		c := s.c
		if perPage[c.PageURL] >= maxPerPage {
			continue
		}
		key := strings.ToLower(c.Title)
		if i, ok := byTitle[key]; ok && hits[i].PageURL != c.PageURL && (hits[i].Product != c.Product || hits[i].Guide != c.Guide) {
			if !slices.ContainsFunc(hits[i].AlsoIn, func(a Alternate) bool { return a.URL == c.PageURL }) {
				hits[i].AlsoIn = append(hits[i].AlsoIn, Alternate{Product: c.Product, Guide: c.Guide, URL: c.PageURL})
			}
			continue
		}
		if len(hits) == limit {
			continue // keep grouping same-titled pages into the hits already chosen
		}
		perPage[c.PageURL]++
		if _, ok := byTitle[key]; !ok {
			byTitle[key] = len(hits)
		}
		hits = append(hits, Hit{
			URL: c.URL, PageURL: c.PageURL, Title: c.Title, Heading: c.Heading, Location: c.Location,
			Snippet: c.Snippet, Product: c.Product, GuideType: c.GuideType, Guide: c.Guide, Version: c.Version, LastMod: c.LastMod,
			Score: s.score,
		})
	}
	return hits
}

func noResults(query string, r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "No documentation found for %q", strings.TrimSpace(query))
	if r.Product != "" {
		fmt.Fprintf(&b, " in %s", r.Product)
	}
	b.WriteString(".")
	if len(r.Suggestions) > 0 {
		fmt.Fprintf(&b, " Did you mean %q?", r.Suggestions[0])
	}
	if len(r.OtherProducts) > 0 {
		fmt.Fprintf(&b, " Other products have matches: %s.", strings.Join(r.OtherProducts, ", "))
	}
	return b.String()
}
