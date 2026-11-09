// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package page

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// kind is the kind of a Markdown block.
type kind int

const (
	kPara kind = iota
	kHeading
	kList
	kCode
	kQuote
	kTable
	kRule
)

// block is one Markdown block. Headings are kept apart so the page can be split into
// sections at them; text holds the rendered Markdown of every other kind.
type block struct {
	kind   kind
	text   string
	level  int    // headings: 1 to 3
	anchor string // headings: the id a URL fragment can point at
	plain  string // headings: the heading as plain text
	id     int    // headings: numbered from 1 in page order
}

// maxDepth caps how deeply the renderer follows nested elements; anything deeper is
// read as plain text.
const maxDepth = 200

var blockTags = map[string]bool{
	"p": true, "div": true, "ul": true, "ol": true, "li": true, "table": true, "pre": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "hr": true, "dl": true, "dt": true, "dd": true, "section": true,
	"article": true, "main": true, "figure": true, "figcaption": true, "header": true,
	"footer": true, "aside": true, "iframe": true, "video": true, "details": true,
	"summary": true, "address": true, "center": true, "fieldset": true, "form": true,
}

// Elements that are never content.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "button": true, "nav": true,
	"input": true, "select": true, "textarea": true, "template": true, "object": true,
	"embed": true, "map": true, "area": true, "link": true, "meta": true, "svg": true,
	"head": true, "title": true, "col": true, "colgroup": true,
}

// Classes of elements that are never content: Flare's in-page menu (marked nocontent so
// search skips it), the copy button on code snippets, and the open and closed icons of
// drop-downs, togglers, and popups.
var skipClasses = []string{
	"nocontent", "codeSnippetCopyButton", "MCDropDown_Image_Icon", "MCToggler_Image_Icon",
	"MCExpanding_Image_Icon", "MCTextPopupArrow",
	"MCTextPopupBody", // read only where its popup is rendered
}

// Flare classes for notes, rendered as block quotes.
var noteClasses = []string{"Note", "Tip", "Caution", "Warning", "Important"}

type renderer struct {
	base     *url.URL
	hasBlock map[*html.Node]bool // elements rendered as blocks: block tags and anything holding one
	pending  string              // the name of an empty <a name> just before a heading
	reqs     []*html.Node        // required roles and permissions, in page order

	// Tables, links, and glossary terms, in page order. Until the page is split, their
	// Section fields hold the id of the heading they follow (0 before any).
	tables      []Table
	links       []Link
	terms       []Term
	lastHeading int // the id of the last heading in the page's content
	nextID      int
}

// headingBlock makes a heading that can start a section, and notes it as the heading
// that what follows belongs to.
func (r *renderer) headingBlock(level int, anchor, text, plain string) block {
	r.nextID++
	r.lastHeading = r.nextID
	return block{kind: kHeading, level: level, anchor: anchor, text: text, plain: plain, id: r.nextID}
}

func newRenderer(base *url.URL, root *html.Node) *renderer {
	r := &renderer{base: base, hasBlock: map[*html.Node]bool{}}
	r.markBlocks(root)
	return r
}

// markBlocks records which elements hold block content, without recursion so deeply
// nested input can't exhaust the stack.
func (r *renderer) markBlocks(root *html.Node) {
	type item struct {
		n    *html.Node
		done bool
	}
	stack := []item{{n: root}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !it.done {
			stack = append(stack, item{n: it.n, done: true})
			for c := it.n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && !skip(c) {
					stack = append(stack, item{n: c})
				}
			}
			continue
		}
		b := blockTags[it.n.Data]
		for c := it.n.FirstChild; c != nil && !b; c = c.NextSibling {
			b = r.hasBlock[c]
		}
		if b {
			r.hasBlock[it.n] = true
		}
	}
}

type ctx struct {
	top    bool // directly in the page's content, where headings start sections
	depth  int
	quotes int // blockquotes this is in
}

func (c ctx) inner() ctx { return ctx{depth: c.depth, quotes: c.quotes} }

// maxQuotes is how deeply blockquotes nest in the Markdown. Each level prefixes every
// line inside it again, so deep nesting costs time and output out of all proportion;
// deeper ones are rendered as the blocks they hold.
const maxQuotes = 8

// blocks renders an element's children as blocks. Runs of inline content become
// paragraphs.
func (r *renderer) blocks(n *html.Node, c ctx) []block {
	if c.depth > maxDepth {
		if t := escapeText(collapse(plainText(n))); strings.TrimSpace(t) != "" {
			return []block{{kind: kPara, text: strings.TrimSpace(t)}}
		}
		return nil
	}
	c.depth++
	var out []block
	var run []*html.Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		if t := r.inline(run, c); t != "" {
			out = append(out, block{kind: kPara, text: t})
			r.pending = ""
		}
		run = nil
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch {
		case skip(ch):
		case ch.Type == html.ElementNode && r.hasBlock[ch]:
			flush()
			out = append(out, r.block(ch, c)...)
		case ch.Type == html.ElementNode && ch.Data == "a" && attr(ch, "name") != "" && strings.TrimSpace(plainText(ch)) == "":
			flush()
			r.pending = attr(ch, "name")
		default:
			run = append(run, ch)
		}
	}
	flush()
	return out
}

// block renders one element that holds block content.
func (r *renderer) block(n *html.Node, c ctx) []block {
	cls := classes(n)
	if cls["Permission"] {
		r.reqs = append(r.reqs, n)
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return r.heading(n, c)
	case "ul", "ol":
		return r.list(n, c)
	case "table":
		return r.table(n, c)
	case "pre":
		return codeBlock(rawText(n))
	case "hr":
		return []block{{kind: kRule, text: "---"}}
	case "blockquote":
		inner := c.inner()
		if inner.quotes >= maxQuotes {
			return r.blocks(n, inner)
		}
		inner.quotes++
		return quote(r.blocks(n, inner))
	case "iframe", "video":
		return r.media(n)
	case "dt":
		if t := r.inline(children(n), c); t != "" {
			return []block{{kind: kPara, text: "**" + t + "**"}}
		}
		return nil
	}
	switch {
	case hasAny(cls, noteClasses):
		return quote(r.blocks(n, c.inner()))
	case cls["codeSnippet"]:
		if pre := find(n, func(x *html.Node) bool { return x.Data == "pre" }); pre != nil {
			return codeBlock(rawText(pre))
		}
		return codeBlock(codeLines(n))
	case cls["Code"] && (n.Data == "div" || n.Data == "p"):
		return codeBlock(codeLines(n))
	case cls["MCDropDown"]:
		return r.dropDown(n, c)
	case n.Data == "p" && isHeadingClass(cls):
		// Many pages, and many translations, mark headings as paragraphs of class
		// heading2 and heading3. In the page's content they start sections like h2 and
		// h3; anywhere else (in a list step, say), and for smaller ones, they're bold.
		t := r.inline(children(n), c)
		if t == "" {
			return nil
		}
		level := 0
		switch {
		case cls["heading2"]:
			level = 2
		case cls["heading3"]:
			level = 3
		}
		if c.top && level > 0 {
			anchor := attr(n, "id")
			if anchor == "" {
				anchor = r.pending
			}
			r.pending = ""
			return []block{r.headingBlock(level, anchor, t, collapse(plainText(n)))}
		}
		r.pending = ""
		return []block{{kind: kPara, text: bold(t)}}
	}
	return r.blocks(n, c)
}

func isHeadingClass(cls map[string]bool) bool {
	for c := range cls {
		if strings.HasPrefix(c, "heading") {
			return true
		}
	}
	return false
}

func (r *renderer) heading(n *html.Node, c ctx) []block {
	level := int(n.Data[1] - '0')
	text := r.inline(children(n), c)
	anchor := attr(n, "id")
	if anchor == "" {
		if a := find(n, func(x *html.Node) bool { return x.Data == "a" && attr(x, "name") != "" }); a != nil {
			anchor = attr(a, "name")
		}
	}
	if anchor == "" {
		anchor = r.pending
	}
	r.pending = ""
	if text == "" {
		return nil
	}
	switch {
	case c.top && level <= 3:
		return []block{r.headingBlock(level, anchor, text, collapse(plainText(n)))}
	case c.top:
		return []block{{kind: kPara, text: strings.Repeat("#", level) + " " + text}}
	}
	return []block{{kind: kPara, text: bold(text)}}
}

// dropDown renders a Flare drop-down expanded: its head in bold, then its body.
func (r *renderer) dropDown(n *html.Node, c ctx) []block {
	head := find(n, hasClass("MCDropDownHotSpot"))
	if head == nil {
		head = find(n, hasClass("MCDropDownHead"))
	}
	body := find(n, hasClass("MCDropDownBody"))
	if head == nil && body == nil {
		return r.blocks(n, c)
	}
	var out []block
	if head != nil {
		if t := r.inline(children(head), c); t != "" {
			out = append(out, block{kind: kPara, text: bold(t)})
		}
	}
	if body != nil {
		out = append(out, r.blocks(body, c.inner())...)
	}
	return out
}

func (r *renderer) media(n *html.Node) []block {
	src := attr(n, "src")
	if src == "" {
		if s := find(n, func(x *html.Node) bool { return x.Data == "source" && attr(x, "src") != "" }); s != nil {
			src = attr(s, "src")
		}
	}
	u := r.resolve(src)
	if u == "" {
		return nil
	}
	label := "Video"
	if t := strings.TrimSpace(attr(n, "title")); t != "" {
		label += ": " + escapeText(collapse(t))
	}
	return []block{{kind: kPara, text: "[" + label + "](" + linkTarget(u) + ")"}}
}

func (r *renderer) list(n *html.Node, c ctx) []block {
	ordered := n.Data == "ol"
	num := 1
	if s, err := strconv.Atoi(attr(n, "start")); err == nil {
		num = s
	}
	ic := c.inner()
	var items []string
	var width []int
	loose := false
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if skip(ch) || ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		isItem := ch.Type == html.ElementNode && ch.Data == "li"
		var bs []block
		switch {
		case isItem && classes(ch)["GlossaryPageEntry"]:
			bs = r.glossaryEntry(ch, ic)
		case isItem:
			if v, err := strconv.Atoi(attr(ch, "value")); err == nil && ordered {
				num = v
			}
			bs = r.blocks(ch, ic)
		case ch.Type == html.ElementNode && r.hasBlock[ch]:
			bs = r.block(ch, ic)
		default:
			if t := r.inline([]*html.Node{ch}, ic); t != "" {
				bs = []block{{kind: kPara, text: t}}
			}
		}
		marker := "- "
		if ordered && isItem {
			marker = strconv.Itoa(num) + ". "
			num++
		}
		body := joinBlocks(bs, true)
		if body == "" {
			continue
		}
		if strings.Contains(body, "\n\n") {
			loose = true
		}
		if !isItem && len(items) > 0 { // stray content between items belongs to the one before
			pad := strings.Repeat(" ", width[len(width)-1])
			items[len(items)-1] += "\n" + indent(body, pad, pad)
			continue
		}
		items = append(items, indent(body, marker, strings.Repeat(" ", len(marker))))
		width = append(width, len(marker))
	}
	if len(items) == 0 {
		return nil
	}
	sep := "\n"
	if loose {
		sep = "\n\n"
	}
	return []block{{kind: kList, text: strings.Join(items, sep)}}
}

// glossaryEntry reads one entry of a Flare glossary: its term (with the anchor that
// links to it) and its definition.
func (r *renderer) glossaryEntry(li *html.Node, c ctx) []block {
	termNode := find(li, hasClass("GlossaryPageTerm"))
	defNode := find(li, hasClass("GlossaryPageDefinition"))
	if termNode == nil || defNode == nil {
		return r.blocks(li, c)
	}
	term := strings.TrimSpace(collapse(plainText(termNode)))
	def := joinBlocks(r.blocks(defNode, c), true)
	if term == "" {
		return nil
	}
	anchor := ""
	if a := find(termNode, func(x *html.Node) bool { return x.Data == "a" && attr(x, "name") != "" }); a != nil {
		anchor = attr(a, "name")
	}
	r.terms = append(r.terms, Term{Section: r.lastHeading, Term: term, Definition: def, Anchor: anchor})
	text := bold(escapeText(term))
	if def != "" {
		text += ": " + def
	}
	return []block{{kind: kPara, text: text}}
}

// joinBlocks joins rendered blocks. In a list item, a nested list follows the line
// before it directly.
func joinBlocks(bs []block, item bool) string {
	var b strings.Builder
	for i, x := range bs {
		t := x.text
		if x.kind == kHeading {
			t = strings.Repeat("#", x.level) + " " + x.text
		}
		if i > 0 {
			if item && x.kind == kList && bs[i-1].kind == kPara {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(t)
	}
	return b.String()
}

func indent(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case i == 0:
			lines[i] = first + l
		case l != "":
			lines[i] = rest + l
		}
	}
	return strings.Join(lines, "\n")
}

func quote(bs []block) []block {
	t := joinBlocks(bs, false)
	if t == "" {
		return nil
	}
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return []block{{kind: kQuote, text: strings.Join(lines, "\n")}}
}

func codeBlock(text string) []block {
	text = strings.Trim(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return []block{{kind: kCode, text: fence + "\n" + text + "\n" + fence}}
}

// codeLines reads a Flare code block (a div or paragraph of class Code), one line per
// paragraph or line break.
func codeLines(n *html.Node) string {
	lines := strings.Split(plainText(n), "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(collapse(l)); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// inline renders inline content as one Markdown paragraph.
func (r *renderer) inline(nodes []*html.Node, c ctx) string {
	var b strings.Builder
	for _, n := range nodes {
		r.inl(&b, n, c.depth)
	}
	return cleanInline(b.String())
}

func (r *renderer) inl(b *strings.Builder, n *html.Node, depth int) {
	if skip(n) {
		return
	}
	switch n.Type {
	case html.TextNode:
		b.WriteString(escapeText(collapse(n.Data)))
		return
	case html.ElementNode:
	default:
		return
	}
	if depth > maxDepth {
		b.WriteString(escapeText(collapse(plainText(n))))
		return
	}
	depth++
	cls := classes(n)
	switch {
	case n.Data == "br":
		b.WriteString("\n")
		return
	case n.Data == "img":
		b.WriteString(r.image(n, cls))
		return
	case n.Data == "code" || n.Data == "tt" || n.Data == "kbd" || n.Data == "samp" || cls["Code"]:
		if t := strings.TrimSpace(collapse(plainText(n))); t != "" {
			s := collapse(rawText(n))
			b.WriteString(s[:len(s)-len(strings.TrimLeft(s, " "))] + codeSpan(t) + s[len(strings.TrimRight(s, " ")):])
		}
		return
	case n.Data == "b" || n.Data == "strong":
		r.wrap(b, n, "**", depth)
		return
	case n.Data == "i" || n.Data == "em" || n.Data == "cite":
		r.wrap(b, n, "*", depth)
		return
	case cls["MCTextPopup"]:
		r.popup(b, n, depth)
		return
	case n.Data == "a":
		r.link(b, n, cls, depth)
		return
	}
	if blockTags[n.Data] {
		b.WriteString(" ")
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		r.inl(b, ch, depth)
	}
	if blockTags[n.Data] {
		b.WriteString(" ")
	}
}

func (r *renderer) inner(n *html.Node, depth int) string {
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		r.inl(&b, ch, depth)
	}
	return b.String()
}

// wrap writes an element's content between emphasis marks, keeping spaces outside them.
func (r *renderer) wrap(b *strings.Builder, n *html.Node, mark string, depth int) {
	s := r.inner(n, depth)
	t := strings.TrimSpace(s)
	switch {
	case t == "":
		b.WriteString(s)
		return
	case strings.HasPrefix(t, mark) && strings.HasSuffix(t, mark) && len(t) > 2*len(mark):
		mark = "" // already emphasized
	}
	if strings.Contains(t, "\n") { // emphasis can't span lines
		mark = ""
	}
	lead, trail := s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))], s[len(strings.TrimRightFunc(s, unicode.IsSpace)):]
	b.WriteString(lead + mark + t + mark + trail)
}

// popup renders a Flare text popup inline, with its text in parentheses.
func (r *renderer) popup(b *strings.Builder, n *html.Node, depth int) {
	var head, body strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && classes(ch)["MCTextPopupBody"] {
			body.WriteString(r.inner(ch, depth))
		} else {
			r.inl(&head, ch, depth)
		}
	}
	b.WriteString(head.String())
	if t := strings.TrimSpace(body.String()); t != "" {
		b.WriteString(" (" + t + ")")
	}
}

func (r *renderer) link(b *strings.Builder, n *html.Node, cls map[string]bool, depth int) {
	s := r.inner(n, depth)
	text := strings.TrimSpace(s)
	if text == "" {
		return
	}
	href := strings.TrimSpace(attr(n, "href"))
	plain := href == "" || href == "#" || cls["MCDropDownHotSpot"] || cls["MCToggler"] ||
		cls["MCPopupThumbnailLink"] || cls["MCExpandingHead"] || strings.Contains(text, "\n")
	u := ""
	if !plain {
		u = r.resolve(href)
	}
	if u == "" {
		b.WriteString(s)
		return
	}
	r.links = append(r.links, Link{Section: r.lastHeading, URL: u, Text: strings.TrimSpace(collapse(plainText(n)))})
	lead, trail := s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))], s[len(strings.TrimRightFunc(s, unicode.IsSpace)):]
	b.WriteString(lead + "[" + text + "](" + linkTarget(u) + ")" + trail)
}

// linkTarget writes a URL so Markdown reads all of it as the link.
func linkTarget(u string) string {
	if strings.ContainsAny(u, "() <>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E", " ", "%20").Replace(u) + ">"
	}
	return u
}

// resolve returns an absolute http or https URL for a link, or "" for anything else.
func (r *renderer) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	u := r.base.ResolveReference(ref)
	if u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return ""
	}
	// The translated sites serve their pages with links to docs.tenable.com pointing at
	// the same paths on their own site; do the same, so a page reads the same whether it
	// comes from the site or from a local copy of its files.
	if strings.EqualFold(u.Host, site.Sites[0].Host) && r.base.Host != u.Host && site.IsDocsHost(r.base.Host) {
		u.Host = strings.ToLower(r.base.Host)
	}
	return u.String()
}

// image renders an image as its text: icons as their names in brackets, as the page
// reads ("select the [more] button"), and other images as their alt text, if any.
func (r *renderer) image(n *html.Node, cls map[string]bool) string {
	alt := strings.TrimSpace(collapse(attr(n, "alt")))
	if alt == "" {
		alt = strings.TrimSpace(collapse(attr(n, "title")))
	}
	src := attr(n, "src")
	icon := cls["ReduceButtonSize"] || strings.Contains(strings.ToLower(attr(n, "class")), "icon") ||
		strings.Contains(strings.ToLower(src), "/icons/")
	if icon {
		if alt == "" {
			alt = iconName(src)
		}
		if alt == "" {
			return ""
		}
		return "[" + escapeText(alt) + "]"
	}
	if alt == "" {
		return ""
	}
	return "[Image: " + escapeText(alt) + "]"
}

// iconName makes a name from an icon's file name: shared_more-vertical.svg is "more
// vertical".
func iconName(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return ""
	}
	name := path.Base(u.Path)
	name = strings.TrimSuffix(name, path.Ext(name))
	name = strings.TrimPrefix(name, "shared_")
	name = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(name))
	if name == "." || name == "/" || !strings.ContainsFunc(name, unicode.IsLetter) {
		return ""
	}
	return name
}

func codeSpan(t string) string {
	fence := "`"
	for strings.Contains(t, fence) {
		fence += "`"
	}
	if strings.HasPrefix(t, "`") || strings.HasSuffix(t, "`") {
		t = " " + t + " "
	}
	return fence + t + fence
}

func bold(t string) string {
	if strings.Contains(t, "\n") || strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**") {
		return t
	}
	return "**" + t + "**"
}

// collapse turns every run of white space into one space, as a browser shows it, and
// drops zero-width characters.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, c := range s {
		switch {
		case c == 0x200b || c == 0xfeff: // zero-width space, byte-order mark
			continue
		case unicode.IsSpace(c):
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(c)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

// escapeText escapes the characters Markdown would read as formatting. An underscore is
// escaped only where it could start or end emphasis, so names like PAIRING_KEY stay
// readable.
func escapeText(s string) string {
	rs := []rune(s)
	var b strings.Builder
	word := func(i int) bool {
		return i >= 0 && i < len(rs) && (unicode.IsLetter(rs[i]) || unicode.IsDigit(rs[i]))
	}
	for i, c := range rs {
		switch c {
		case '\\', '*', '`', '[', ']', '<':
			b.WriteByte('\\')
		case '_':
			if !word(i-1) || !word(i+1) {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

// cleanInline tidies rendered inline content: single spaces, no space at the ends of
// lines, and no empty lines.
func cleanInline(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimSpace(strings.Join(strings.Fields(l), " "))
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// plainText returns an element's text: line breaks for <br> and around blocks, and
// skipped elements left out. It reads the tree without recursion.
func plainText(n *html.Node) string { return text(n, false) }

// rawText returns an element's text exactly, as for <pre>.
func rawText(n *html.Node) string { return text(n, true) }

func text(n *html.Node, raw bool) string {
	var b strings.Builder
	type item struct {
		n    *html.Node
		exit bool
	}
	stack := []item{{n: n}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x := it.n
		if it.exit {
			if !raw && blockTags[x.Data] {
				b.WriteString("\n")
			}
			continue
		}
		if x != n && skip(x) {
			continue
		}
		switch x.Type {
		case html.TextNode:
			if raw {
				b.WriteString(x.Data)
			} else {
				b.WriteString(collapse(x.Data))
			}
			continue
		case html.ElementNode:
			if x.Data == "br" {
				b.WriteString("\n")
				continue
			}
			if !raw && blockTags[x.Data] {
				b.WriteString("\n")
			}
		}
		stack = append(stack, item{n: x, exit: true})
		var kids []*html.Node
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			kids = append(kids, c)
		}
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{n: kids[i]})
		}
	}
	if raw {
		return b.String()
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func skip(n *html.Node) bool {
	switch n.Type {
	case html.CommentNode, html.DoctypeNode:
		return true
	case html.ElementNode:
	default:
		return false
	}
	if skipTags[n.Data] || attr(n, "id") == "backToTop" {
		return true
	}
	cls := classes(n)
	for c := range cls {
		if strings.HasPrefix(c, "GlossaryPageHeading") { // the letters between glossary entries
			return true
		}
	}
	return hasAny(cls, skipClasses)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func classes(n *html.Node) map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Fields(attr(n, "class")) {
		m[c] = true
	}
	return m
}

func hasAny(cls map[string]bool, names []string) bool {
	for _, c := range names {
		if cls[c] {
			return true
		}
	}
	return false
}

func hasClass(name string) func(*html.Node) bool {
	return func(x *html.Node) bool { return classes(x)[name] }
}

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// find returns the first element under n (not n itself), in document order, that
// matches, without recursion.
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	stack := children(n)
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x.Type != html.ElementNode {
			continue
		}
		if match(x) {
			return x
		}
		kids := children(x)
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, kids[i])
		}
	}
	return nil
}
