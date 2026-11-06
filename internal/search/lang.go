// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// SiteFor returns the site code for a language: a site code ("jp"), a BCP 47 tag
// ("ja", "zh-Hant", "pt-BR"), or a language's English name. It returns false for a
// language no site is in.
func SiteFor(lang string) (string, bool) {
	l := strings.ToLower(strings.TrimSpace(lang))
	if s, ok := site.ByCode(l); ok {
		return s.Code, true
	}
	switch l {
	case "english":
		return "en", true
	case "japanese", "ja-jp":
		return "jp", true
	case "korean", "ko-kr":
		return "kr", true
	case "french", "français", "francais", "fr-fr", "fr-ca":
		return "fr", true
	case "german", "deutsch", "de-de":
		return "de", true
	case "spanish", "español", "espanol":
		return "es", true
	case "zh", "chinese", "simplified chinese", "zh-hans", "zh-sg":
		return "zh-cn", true
	case "traditional chinese", "zh-hant", "zh-hk", "zh-mo":
		return "zh-tw", true
	}
	for _, s := range site.Sites {
		if strings.EqualFold(s.Lang, l) || strings.HasPrefix(l, strings.ToLower(s.Lang)+"-") {
			return s.Code, true
		}
	}
	if strings.HasPrefix(l, "en-") || strings.HasPrefix(l, "es-") {
		return l[:2], true
	}
	return "", false
}

// Characters written differently in simplified and traditional Chinese, as pairs.
var hanPairs = [][2]string{
	{"们", "們"}, {"这", "這"}, {"说", "說"}, {"时", "時"}, {"会", "會"}, {"对", "對"}, {"现", "現"},
	{"进", "進"}, {"动", "動"}, {"开", "開"}, {"关", "關"}, {"发", "發"}, {"问", "問"}, {"题", "題"},
	{"间", "間"}, {"网", "網"}, {"设", "設"}, {"载", "載"}, {"数", "數"}, {"据", "據"}, {"库", "庫"},
	{"扫", "掃"}, {"描", "描"}, {"请", "請"}, {"务", "務"}, {"机", "機"}, {"实", "實"}, {"应", "應"},
	{"为", "為"}, {"过", "過"}, {"还", "還"}, {"没", "沒"}, {"认", "認"}, {"证", "證"}, {"络", "絡"},
	{"产", "產"}, {"资", "資"}, {"统", "統"}, {"础", "礎"}, {"装", "裝"}, {"么", "麼"}, {"样", "樣"},
	{"录", "錄"}, {"报", "報"}, {"级", "級"}, {"许", "許"}, {"码", "碼"}, {"钥", "鑰"}, {"硬", "硬"},
	{"件", "件"}, {"简", "簡"}, {"体", "體"}, {"标", "標"}, {"签", "籤"}, {"组", "組"}, {"户", "戶"},
}

// DetectLanguage returns the site code for the language a question is written in:
// Japanese if it has kana, Korean if it has Hangul, Chinese (simplified or traditional)
// for Chinese characters alone, and for Latin script, French, German, or Spanish when
// their common words or accents outnumber English ones. Anything else is English.
func DetectLanguage(q string) string {
	var kana, hangul, han int
	for _, r := range q {
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Han, r):
			han++
		}
	}
	switch {
	case kana > 0:
		return "jp"
	case hangul > 0 && hangul >= han:
		return "kr"
	case han > 0:
		simp, trad := 0, 0
		for _, p := range hanPairs {
			if p[0] == p[1] {
				continue
			}
			simp += strings.Count(q, p[0])
			trad += strings.Count(q, p[1])
		}
		if trad > simp {
			return "zh-tw"
		}
		return "zh-cn"
	}
	return latinLanguage(q)
}

// Common words that mark a Latin-script language. (Words shared with English, such as
// "a" and "in", aren't counted.)
var latinMarkers = map[string][]string{
	"fr": strings.Fields("le la les des du une est sont comment quel quelle quels quelles pour dans sur avec sans je mon ma mes nous vous votre vos ce cette ces qui que où être faire configurer créer ajouter supprimer installer exigences paramètres nouveautés"),
	"de": strings.Fields("der die das den dem des ein eine einen und oder ist sind wie was welche welcher welches ich mein meine wir ihr zu zum zur von vom bei kann können muss für mit ohne nicht auch erstellen hinzufügen löschen konfigurieren einrichten installieren anforderungen einstellungen"),
	"es": strings.Fields("el los las una unos unas del y es son está cómo qué cuál cuáles dónde para con sin yo mi mis nos su sus puedo puede debo hay hacer crear agregar añadir eliminar configurar instalar requisitos novedades"),
	"en": strings.Fields("the is are how what which where can do does my to of for with without and or i create add delete configure set install requirements settings new"),
}

func latinLanguage(q string) string {
	words := tokens(q)
	score := map[string]int{}
	for lang, markers := range latinMarkers {
		for _, w := range words {
			if slices.Contains(markers, w.low) {
				score[lang]++
			}
		}
	}
	for _, r := range q {
		switch {
		case strings.ContainsRune("éèêëàâçœîïôûùÿ", unicode.ToLower(r)):
			score["fr"]++
		case strings.ContainsRune("äöüß", unicode.ToLower(r)):
			score["de"]++
		case strings.ContainsRune("ñ¿¡áíóú", unicode.ToLower(r)):
			score["es"]++
		}
	}
	best, bestScore := "en", score["en"]
	for _, lang := range []string{"fr", "de", "es"} {
		if score[lang] > bestScore {
			best, bestScore = lang, score[lang]
		}
	}
	return best
}

// Filler words for French, German, and Spanish questions, used along with the English
// ones.
var latinStopwords = map[string]map[string]bool{
	"fr": wordSet(`le la les l un une des du de d et ou à au aux en dans sur pour par avec sans est sont être comment
		quel quelle quels quelles que qu qui quoi où je j me m mon ma mes nous vous votre vos il elle ils elles ce cet
		cette ces se s ne n pas plus faire fait peut puis dois doit faut y a-t-il est-ce afin quand`),
	"de": wordSet(`der die das den dem des ein eine einen einem einer und oder in im an am auf für mit ohne ist sind
		wie was welche welcher welches wo ich mich mein meine wir ihr sie es zu zum zur von vom bei kann können muss
		soll man nicht auch als bitte gibt mache machen`),
	"es": wordSet(`el la los las un una unos unas de del y o en a al por para con sin es son está cómo como qué que
		cuál cuáles dónde yo me mi mis nos se su sus lo le no puedo puede debo hay hacer cuando`),
}

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// Words that only hold Japanese, Chinese, and Korean questions together. Japanese and
// Chinese questions are split at them; Korean ones lose them at the ends of words.
var (
	jaFunction = sortLongestFirst(strings.Fields(`について における ための ように ような として による により から まで より では
		には とは での への との しますか しました します したい しない する した して できますか できる できます ですか ますか
		ません ください でしょうか ありますか どうやって どのように どうすれば どれ なに 何 方法 手順 教えて 必要 の を は が に で と へ
		も や か ね よ`))
	zhFunction = sortLongestFirst(strings.Fields(`如何在 怎么样 怎麼樣 如何 怎么 怎麼 怎样 怎樣 什么 什麼 哪些 哪个 哪個 可以 需要
		进行 進行 方法 步骤 步驟 的 了 在 是 吗 嗎 呢 和 与 與 及 或 把 被 让 讓 从 從 到 并 並 对 對 为 為 我们 我們 我 你 要
		能 用 个 個 种 種 哪`))
	koEndings = sortLongestFirst(strings.Fields(`에서는 으로는 에서의 하려면 합니까 하나요 인가요 에서 으로 하는 하기 하고 은 는 이 가
		을 를 에 의 로 와 과 도 만`))
	koStopwords = wordSet(`어떻게 무엇 무엇인가요 방법 있나요 있습니까 합니까 하는 하나요 되나요 어디 어디서 무엇을 대한 대해`)
)

func sortLongestFirst(words []string) []string {
	slices.SortStableFunc(words, func(a, b string) int { return utf8.RuneCountInString(b) - utf8.RuneCountInString(a) })
	return words
}

// scriptClass groups characters for splitting Japanese and Chinese text.
func scriptClass(r rune) int {
	switch {
	case unicode.Is(unicode.Katakana, r) || r == 'ー':
		return 1
	case unicode.Is(unicode.Hiragana, r):
		return 2
	case unicode.Is(unicode.Han, r):
		return 3
	case unicode.Is(unicode.Hangul, r):
		return 4
	}
	return 0
}

// cjkPieces splits a run of Japanese or Chinese text into searchable pieces: at the
// function words, and (for Japanese) where the script changes between katakana,
// hiragana, and kanji. Hiragana pieces of fewer than three characters are dropped as
// grammar.
func cjkPieces(s, lang string) []string {
	function := zhFunction
	if lang == "jp" {
		function = jaFunction
	}
	for _, w := range function {
		s = strings.ReplaceAll(s, w, " ")
	}
	var out []string
	for _, f := range strings.Fields(s) {
		if lang != "jp" {
			out = append(out, f)
			continue
		}
		var cur []rune
		class := -1
		flush := func() {
			if len(cur) > 0 && !(class == 2 && len(cur) < 3) {
				out = append(out, string(cur))
			}
			cur = nil
		}
		for _, r := range f {
			c := scriptClass(r)
			if c != class {
				flush()
				class = c
			}
			cur = append(cur, r)
		}
		flush()
	}
	return out
}

// koWord removes a particle or ending from the end of a Korean word.
func koWord(w string) string {
	for _, e := range koEndings {
		if strings.HasSuffix(w, e) && utf8.RuneCountInString(w) > utf8.RuneCountInString(e)+1 {
			return strings.TrimSuffix(w, e)
		}
	}
	return w
}

// isCJK reports whether a word is written in Japanese, Chinese, or Korean script.
func isCJK(w string) bool {
	return strings.ContainsFunc(w, func(r rune) bool { return scriptClass(r) != 0 })
}

// cjkTerms turns one word of a Japanese, Chinese, or Korean question into terms for
// the trigram index (three characters or more) and short words to look for as text.
// Chinese runs longer than four characters, which are usually several words, are split
// into pairs of characters.
func cjkTerms(w, lang string) (terms, short []string) {
	var pieces []string
	switch lang {
	case "kr":
		if koStopwords[w] {
			return nil, nil
		}
		pieces = []string{koWord(w)}
	default:
		pieces = cjkPieces(w, lang)
	}
	for _, p := range pieces {
		n := utf8.RuneCountInString(p)
		switch {
		case n >= 5 && lang != "jp" && lang != "kr":
			r := []rune(p)
			for i := 0; i+1 < len(r); i += 2 {
				short = appendUnique(short, string(r[i:i+2]))
			}
			if len(r)%2 == 1 {
				short = appendUnique(short, string(r[len(r)-2:]))
			}
		case n >= 3:
			terms = appendUnique(terms, p)
		case n == 2:
			short = appendUnique(short, p)
		}
	}
	return terms, short
}
