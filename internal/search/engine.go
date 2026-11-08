// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Engine searches the whole index: it picks the language and version to search, falls
// back to English when the translated docs have nothing, and links each result to the
// same page in other languages and versions.
type Engine struct {
	Index *index.Index
	// Synonyms reword questions into the docs' terms; nil turns that off.
	Synonyms *Synonyms

	mu        sync.Mutex
	searchers map[*index.Shard]*Searcher // by shard, so a swapped-in shard gets a new one
}

// NewEngine returns an Engine for an index.
func NewEngine(ix *index.Index) *Engine {
	return &Engine{Index: ix, Synonyms: DefaultSynonyms(), searchers: map[*index.Shard]*Searcher{}}
}

// Variants are the same page in other languages and versions.
type Variants struct {
	// Current is the page's current version in its language, when the page is older.
	Current string
	// English is the current English page, when the page is a translation or older.
	English string
	// Languages lists the current page in the other languages it's translated into.
	Languages []LanguageLink
	// Versions lists the page's other versions in its language, newest first; the
	// current version has Version "".
	Versions []VersionLink
}

// LanguageLink is a page in another language.
type LanguageLink struct {
	Site  string // site code
	Lang  string // BCP 47 tag
	URL   string
	Title string
}

// VersionLink is another version of a page.
type VersionLink struct {
	Version string
	URL     string
}

// with runs fn with the Searcher for a shard, if the index has it.
func (e *Engine) with(k index.Key, fn func(*Searcher) error) (bool, error) {
	if e.Index.Shard(k) == nil {
		return false, nil
	}
	err := e.Index.With(k, func(sh *index.Shard) error {
		e.mu.Lock()
		s := e.searchers[sh]
		if s == nil {
			for old := range e.searchers {
				if old.Key == k { // a shard that was swapped out
					delete(e.searchers, old)
				}
			}
			s = New(sh)
			e.searchers[sh] = s
		}
		e.mu.Unlock()
		return fn(s)
	})
	return err == nil, err
}

var languageNames = map[string]string{"en": "English", "jp": "Japanese", "zh-cn": "Simplified Chinese",
	"zh-tw": "Traditional Chinese", "kr": "Korean", "fr": "French", "de": "German", "es": "Spanish"}

// Search runs a search across the index.
func (e *Engine) Search(ctx context.Context, req Request) (*Result, error) {
	res, err := e.search(ctx, req)
	if err != nil {
		return nil, err
	}
	alts := e.Synonyms.Alternatives(req.Query)
	if len(alts) == 0 {
		return res, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	// Search again with each synonym in the question, and keep the best sections from
	// all the searches. Results in another language aren't mixed in.
	seen := map[string]bool{}
	for _, h := range res.Hits {
		seen[h.URL] = true
	}
	var used []string
	for _, alt := range alts {
		r := req
		r.Query = alt.Query
		ar, err := e.search(ctx, r)
		if err != nil || ar == nil || len(ar.Hits) == 0 || ar.Language != res.Language {
			continue
		}
		if len(res.Hits) == 0 {
			// The question's own words found nothing: these results are the answer.
			ar.Notes = append(res.Notes, ar.Notes...)
			res, seen = ar, map[string]bool{}
			for _, h := range res.Hits {
				seen[h.URL] = true
			}
			used = append(used, fmt.Sprintf("%q for %q", alt.Synonym, alt.Term))
			continue
		}
		added := false
		for _, h := range ar.Hits {
			if !seen[h.URL] {
				seen[h.URL] = true
				res.Hits = append(res.Hits, h)
				added = true
			}
		}
		if added {
			res.Relaxed = res.Relaxed && ar.Relaxed
			used = append(used, fmt.Sprintf("%q for %q", alt.Synonym, alt.Term))
		}
	}
	if len(used) == 0 {
		return res, nil
	}
	slices.SortStableFunc(res.Hits, func(a, b Hit) int { return cmpFloat(b.Score, a.Score) })
	if len(res.Hits) > limit {
		res.Hits = res.Hits[:limit]
	}
	res.Suggestions, res.NoResultsShown = nil, ""
	res.Notes = append(res.Notes, "Also searched with the docs' wording: "+strings.Join(used, ", ")+".")
	return res, nil
}

// search answers a question in its own words.
func (e *Engine) search(ctx context.Context, req Request) (*Result, error) {
	lang := ""
	if req.Language != "" {
		l, ok := SiteFor(req.Language)
		if !ok {
			return nil, fmt.Errorf("no documentation in %q (try %s)", req.Language, siteCodes())
		}
		lang = l
	} else {
		lang = DetectLanguage(req.Query)
	}
	parsed := ParseLang(req.Query, lang)
	version := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.Version)), "v")
	if version == "" {
		version = parsed.Version
	}

	res, err := e.searchIn(ctx, lang, parsed, req, version)
	if err != nil {
		return nil, err
	}
	res, err = e.namedProduct(ctx, lang, parsed, req, version, res)
	if err != nil {
		return nil, err
	}
	// Chinese characters alone could be Japanese or the other Chinese script, unless
	// the user chose the language.
	if (res == nil || len(res.Hits) == 0) && req.Language == "" && (lang == "zh-cn" || lang == "zh-tw") {
		for _, other := range []string{map[string]string{"zh-cn": "zh-tw", "zh-tw": "zh-cn"}[lang], "jp"} {
			r, err := e.searchIn(ctx, other, ParseLang(req.Query, other), req, version)
			if err != nil {
				return nil, err
			}
			if r != nil && len(r.Hits) > 0 {
				res, lang = r, other
				break
			}
		}
	}
	if !answers(res, parsed, req) && lang != "en" {
		// Nothing in the translated docs: answer from the English ones, with the words a
		// Latin-script search can use.
		en := parsed
		en.Lang, en.Short = "en", nil
		en.Terms = nil
		for _, t := range parsed.Terms {
			if !isCJK(t) {
				en.Terms = append(en.Terms, t)
			}
		}
		fallback, err := e.searchIn(ctx, "en", en, req, version)
		if err != nil {
			return nil, err
		}
		switch {
		case fallback != nil && len(fallback.Hits) > 0:
			reason := "has nothing"
			if res == nil {
				reason = "isn't in the index"
			}
			fallback.Notes = append(fallback.Notes, fmt.Sprintf("The %s documentation %s for this question, so these results are in English.",
				languageNames[lang], reason))
			res = fallback
		case res == nil && fallback != nil:
			// Neither has anything; say so in English rather than fail.
			fallback.Notes = append(fallback.Notes, fmt.Sprintf("The index has no %s documentation.", languageNames[lang]))
			res = fallback
		}
	}
	if res == nil {
		return nil, fmt.Errorf("the index has no %s documentation", languageNames[lang])
	}
	res.Requested = lang
	for i := range res.Hits {
		h := &res.Hits[i]
		h.Language = res.Language
		v, err := e.Variants(ctx, h.PageURL)
		if err != nil {
			return nil, err
		}
		h.Variants = v
		if v.English != "" {
			h.EnglishURL = englishSection(h.URL, v.English)
		}
	}
	return res, nil
}

// namedProduct searches again within a product the question names, when none of the
// results are for it. Words the product's pages don't use ("rolling out") can keep
// them out of the results, even though naming the product says what the user wants.
func (e *Engine) namedProduct(ctx context.Context, lang string, parsed Parsed, req Request, version string, res *Result) (*Result, error) {
	if res == nil || req.Product != "" || answers(res, parsed, req) {
		return res, nil
	}
	for _, product := range parsed.Products {
		r := req
		r.Product = product
		pr, err := e.searchIn(ctx, lang, parsed, r, version)
		if err != nil {
			return nil, err
		}
		if pr != nil && len(pr.Hits) > 0 {
			pr.Notes = append(pr.Notes, fmt.Sprintf("These results are for %s, which the question names.", product))
			return pr, nil
		}
	}
	return res, nil
}

// answers reports whether results answer the question: there are some, and when the
// question names products, some are for one of them. (A translated site that doesn't
// cover a product can still match a word or two of the question on other products'
// pages.)
func answers(r *Result, p Parsed, req Request) bool {
	if r == nil || len(r.Hits) == 0 {
		return false
	}
	if len(p.Products) == 0 || req.Product != "" {
		return true
	}
	for _, h := range r.Hits {
		if slices.Contains(p.Products, h.Product) {
			return true
		}
	}
	return false
}

// searchIn searches one language, in the version asked for, if any. It returns nil if
// the index doesn't have that language.
func (e *Engine) searchIn(ctx context.Context, lang string, parsed Parsed, req Request, version string) (*Result, error) {
	var res *Result
	run := func(k index.Key, v string) error {
		_, err := e.with(k, func(s *Searcher) error {
			r, err := s.searchParsed(ctx, parsed, req, v)
			res = r
			return err
		})
		return err
	}
	if version == "" {
		if err := run(index.Key{Site: lang}, ""); err != nil || res == nil {
			return res, err
		}
		res.Language = lang
		return res, nil
	}
	// An older version: the shard of older versions, by the exact version, then by its
	// major and minor numbers (6.3.1 finds 6.3).
	tries := []string{version}
	if parts := strings.Split(version, "."); len(parts) > 2 {
		tries = append(tries, parts[0]+"."+parts[1])
	}
	for _, v := range tries {
		if err := run(index.Key{Site: lang, Older: true}, v); err != nil {
			return nil, err
		}
		if res != nil && len(res.Hits) > 0 {
			res.Language, res.Version = lang, v
			return res, nil
		}
	}
	// Not an older version: it may be the current one.
	if err := run(index.Key{Site: lang}, ""); err != nil || res == nil {
		return res, err
	}
	res.Language = lang
	res.Notes = append(res.Notes, fmt.Sprintf("The documentation for older versions has no pages for version %s, so these results are for the current version.", version))
	return res, nil
}

func siteCodes() string {
	var c []string
	for _, s := range site.Sites {
		c = append(c, s.Code)
	}
	return strings.Join(c, ", ")
}

// englishSection points a hit's section URL at the current English page, keeping the
// heading's anchor, which translations and older versions share with it.
func englishSection(sectionURL, englishPage string) string {
	if _, frag, ok := strings.Cut(sectionURL, "#"); ok {
		return englishPage + "#" + frag
	}
	return englishPage
}

// Variants returns the same page in other languages and versions.
func (e *Engine) Variants(ctx context.Context, pageURL string) (*Variants, error) {
	pageURL, _, _ = strings.Cut(pageURL, "#")
	own, ok := site.ByHost(hostOf(pageURL))
	if !ok {
		return &Variants{}, nil
	}
	v := &Variants{}
	current := site.CurrentURL(pageURL)
	exists := func(k index.Key, u string) (index.PageRef, bool, error) {
		var ref index.PageRef
		var found bool
		_, err := e.with(k, func(s *Searcher) error {
			var err error
			ref, found, err = s.Shard.Page(ctx, u)
			return err
		})
		return ref, found, err
	}
	// The current version in this language.
	_, curOK, err := exists(index.Key{Site: own.Code}, current)
	if err != nil {
		return nil, err
	}
	if current != pageURL && curOK {
		v.Current = current
	}
	// Other languages, and English.
	for _, s := range site.Sites {
		u := site.OnSite(current, s)
		ref, ok, err := exists(index.Key{Site: s.Code}, u)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if s.Code == "en" && u != pageURL {
			v.English = u
		}
		if s.Code != own.Code {
			v.Languages = append(v.Languages, LanguageLink{Site: s.Code, Lang: s.Lang, URL: u, Title: ref.Title})
		}
	}
	// Other versions in this language.
	if curOK && current != pageURL {
		v.Versions = append(v.Versions, VersionLink{URL: current})
	}
	_, err = e.with(index.Key{Site: own.Code, Older: true}, func(s *Searcher) error {
		refs, err := s.Shard.Versions(ctx, current)
		for _, r := range refs {
			if r.URL != pageURL {
				v.Versions = append(v.Versions, VersionLink{Version: r.Version, URL: r.URL})
			}
		}
		return err
	})
	return v, err
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	h, _, _ := strings.Cut(u, "/")
	return h
}
