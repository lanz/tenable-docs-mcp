// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package llmstxt writes llms.txt files (https://llmstxt.org) for the docs sites, from
// the index: each site's current guides by product, with their landing pages and
// descriptions, and its older-version guides under Optional.
package llmstxt

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// text is the prose of a site's llms.txt, in its language.
type text struct {
	Title, Summary, Intro, Other, Languages string
	// Language is the language's name in itself, for other sites' files to link to it.
	Language string
}

var texts = map[string]text{
	"en": {
		Title:     "Tenable Documentation",
		Summary:   "Product documentation for Tenable One and Tenable's other products: user guides, best practices, reference and integration guides, and release notes.",
		Intro:     "Each guide below links to its first page, whose table of contents links to the rest.",
		Other:     "Other guides",
		Languages: "The documentation in other languages:",
		Language:  "English",
	},
	"jp": {
		Title:     "Tenable ドキュメント",
		Summary:   "Tenable 製品ドキュメントの日本語版: ユーザーガイド、統合ガイド、リリースノートなど。",
		Intro:     "各ガイドへのリンクは最初のページを指し、そのページの目次から残りのページにアクセスできます。翻訳されていないガイドは英語のドキュメントにあります。",
		Other:     "その他のガイド",
		Languages: "他の言語のドキュメント:",
		Language:  "日本語",
	},
	"zh-cn": {
		Title:     "Tenable 文档",
		Summary:   "部分 Tenable 产品文档的简体中文版。",
		Intro:     "每个指南链接到其首页，首页的目录链接到其余页面。未翻译的指南请参阅英文文档。",
		Other:     "其他指南",
		Languages: "其他语言的文档：",
		Language:  "简体中文",
	},
	"zh-tw": {
		Title:     "Tenable 文件",
		Summary:   "部分 Tenable 產品文件的繁體中文版。",
		Intro:     "每份指南連結至其首頁，首頁的目錄連結至其餘頁面。未翻譯的指南請參閱英文文件。",
		Other:     "其他指南",
		Languages: "其他語言的文件：",
		Language:  "繁體中文",
	},
	"kr": {
		Title:     "Tenable 문서",
		Summary:   "Tenable 제품 문서 중 일부의 한국어 번역본입니다.",
		Intro:     "각 가이드는 첫 페이지로 연결되며, 그 페이지의 목차에서 나머지 페이지로 이동할 수 있습니다. 번역되지 않은 가이드는 영어 문서를 참조하세요.",
		Other:     "기타 가이드",
		Languages: "다른 언어로 된 문서:",
		Language:  "한국어",
	},
	"fr": {
		Title:     "Documentation Tenable",
		Summary:   "Une sélection de la documentation des produits Tenable, traduite en français.",
		Intro:     "Chaque guide renvoie à sa première page, dont la table des matières mène aux autres. Les guides non traduits se trouvent dans la documentation en anglais.",
		Other:     "Autres guides",
		Languages: "La documentation dans d'autres langues :",
		Language:  "Français",
	},
	"de": {
		Title:     "Tenable-Dokumentation",
		Summary:   "Eine Auswahl der Tenable-Produktdokumentation in deutscher Übersetzung.",
		Intro:     "Jeder Leitfaden verweist auf seine erste Seite, deren Inhaltsverzeichnis zu den übrigen führt. Nicht übersetzte Leitfäden finden Sie in der englischen Dokumentation.",
		Other:     "Weitere Leitfäden",
		Languages: "Die Dokumentation in anderen Sprachen:",
		Language:  "Deutsch",
	},
	"es": {
		Title:     "Documentación de Tenable",
		Summary:   "Una selección de la documentación de los productos de Tenable, traducida al español.",
		Intro:     "Cada guía enlaza con su primera página, cuya tabla de contenido lleva al resto. Las guías no traducidas están en la documentación en inglés.",
		Other:     "Otras guías",
		Languages: "La documentación en otros idiomas:",
		Language:  "Español",
	},
}

// Path is where a site serves its llms.txt.
func Path(s site.Site) string { return s.BaseURL() + "llms.txt" }

// guideTypes orders guides within a product: user guides first.
var guideTypes = []string{"User Guide", "Best Practices Guide", "Reference Guide", "Integration Guide"}

func typeRank(t string) int {
	if i := slices.Index(guideTypes, t); i >= 0 {
		return i
	}
	return len(guideTypes)
}

// productHeading is a product's section heading: its name, with Tenable.
func productHeading(product string) string {
	if strings.HasPrefix(product, "Tenable") {
		return product
	}
	return "Tenable " + product
}

// Write writes a site's llms.txt: its current and older-version guides, and links to
// the other sites' files.
func Write(w io.Writer, s site.Site, current, older []index.GuideInfo, others []site.Site) error {
	t, ok := texts[s.Code]
	if !ok {
		return fmt.Errorf("no llms.txt text for site %q", s.Code)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s\n\n%s\n", t.Title, t.Summary, t.Intro)
	if len(others) > 0 {
		fmt.Fprintf(&b, "\n%s\n\n", t.Languages)
		for _, o := range others {
			fmt.Fprintf(&b, "- [%s](%s)\n", texts[o.Code].Language, Path(o))
		}
	}

	byProduct := map[string][]index.GuideInfo{}
	pages := map[string]int{}
	for _, g := range current {
		byProduct[g.Product] = append(byProduct[g.Product], g)
		pages[g.Product] += g.Pages
	}
	var products []string
	for p := range byProduct {
		if p != "" {
			products = append(products, p)
		}
	}
	slices.SortFunc(products, func(a, b string) int { return cmp.Or(pages[b]-pages[a], strings.Compare(a, b)) })
	if byProduct[""] != nil {
		products = append(products, "")
	}
	for _, p := range products {
		heading := t.Other
		if p != "" {
			heading = productHeading(p)
		}
		gs := byProduct[p]
		slices.SortFunc(gs, func(a, b index.GuideInfo) int {
			return cmp.Or(typeRank(a.GuideType)-typeRank(b.GuideType), strings.Compare(a.Title(), b.Title()), strings.Compare(a.Path, b.Path))
		})
		fmt.Fprintf(&b, "\n## %s\n\n", heading)
		for _, g := range gs {
			b.WriteString(item(s, g, true))
		}
	}

	if len(older) > 0 {
		slices.SortFunc(older, func(a, b index.GuideInfo) int {
			current := func(g index.GuideInfo) string {
				return strings.TrimSuffix(g.Path, "/"+strings.ReplaceAll(g.Version, ".", "_"))
			}
			return cmp.Or(strings.Compare(current(a), current(b)), index.CompareVersions(b.Version, a.Version), strings.Compare(a.Path, b.Path))
		})
		b.WriteString("\n## Optional\n\n")
		for _, g := range older {
			b.WriteString(item(s, g, false))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// item is a guide's list item: its name, linked to its landing page, and its
// description when describe is set.
func item(s site.Site, g index.GuideInfo, describe bool) string {
	u := g.Landing
	if u == "" {
		u = s.BaseURL() + g.Path
	}
	line := fmt.Sprintf("- [%s](%s)", linkText(g.Title()), u)
	if d := strings.Join(strings.Fields(g.LandingDescription), " "); describe && d != "" {
		line += ": " + d
	}
	return line + "\n"
}

// linkText escapes what would end a Markdown link's text.
func linkText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(strings.Join(strings.Fields(s), " "))
}
