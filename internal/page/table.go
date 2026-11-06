// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package page

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Limits on the tables the renderer lays out.
const (
	maxTableRows = 2000
	maxTableCols = 100
	maxSpan      = 50
)

type tableCell struct {
	blocks []block
	header bool
	id     int
}

// table renders a table as a GitHub table when its cells form a simple grid. A table
// with merged cells, or cells holding code or other tables, becomes a list instead: one
// item per row, with each cell labeled by its column's heading.
func (r *renderer) table(n *html.Node, c ctx) []block {
	ic := c.inner()
	var trs []*html.Node
	var inHead []bool
	var caption string
	var walk func(x *html.Node, head bool)
	walk = func(x *html.Node, head bool) {
		for ch := x.FirstChild; ch != nil && len(trs) < maxTableRows; ch = ch.NextSibling {
			if ch.Type != html.ElementNode || skip(ch) {
				continue
			}
			switch ch.Data {
			case "tr":
				trs = append(trs, ch)
				inHead = append(inHead, head)
			case "thead":
				walk(ch, true)
			case "tbody", "tfoot":
				walk(ch, false)
			case "caption":
				caption = r.inline(children(ch), ic)
			}
		}
	}
	walk(n, false)

	grid := make([][]*tableCell, len(trs))
	set := func(row, col int, cell *tableCell) {
		for len(grid[row]) <= col {
			grid[row] = append(grid[row], nil)
		}
		grid[row][col] = cell
	}
	complex := false
	id := 0
	for ri, tr := range trs {
		col := 0
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode || td.Data != "td" && td.Data != "th" || skip(td) {
				continue
			}
			for col < len(grid[ri]) && grid[ri][col] != nil {
				col++
			}
			if col >= maxTableCols {
				break
			}
			cs := span(attr(td, "colspan"), min(maxSpan, maxTableCols-col))
			rs := span(attr(td, "rowspan"), min(maxSpan, len(trs)-ri))
			if cs > 1 || rs > 1 {
				complex = true
			}
			cell := &tableCell{blocks: r.blocks(td, ic), header: td.Data == "th", id: id}
			id++
			for _, b := range cell.blocks {
				if b.kind == kCode || b.kind == kTable {
					complex = true
				}
			}
			for dr := range rs {
				for dc := range cs {
					set(ri+dr, col+dc, cell)
				}
			}
			col += cs
		}
	}
	cols := 0
	for _, row := range grid {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return nil
	}

	// Header rows: the rows in <thead>, or the leading rows made only of <th> cells.
	heads := 0
	for heads < len(grid) {
		all := len(grid[heads]) > 0
		for _, cell := range grid[heads] {
			if cell != nil && !cell.header {
				all = false
			}
		}
		if !inHead[heads] && !all {
			break
		}
		heads++
	}

	var out []block
	if caption != "" {
		out = append(out, block{kind: kPara, text: bold(caption)})
	}
	if !complex {
		if t := gfmTable(grid, heads, cols); t != "" {
			out = append(out, block{kind: kTable, text: t})
		}
		return out
	}

	labels := make([]string, cols)
	for col := range cols {
		var parts []string
		seen := map[int]bool{}
		for _, row := range grid[:heads] {
			if col < len(row) && row[col] != nil && !seen[row[col].id] {
				seen[row[col].id] = true
				if t := inlineCell(row[col]); t != "" && (len(parts) == 0 || parts[len(parts)-1] != t) {
					parts = append(parts, t)
				}
			}
		}
		labels[col] = strings.Join(parts, " › ")
	}
	var items []string
	flush := func() {
		if len(items) > 0 {
			out = append(out, block{kind: kList, text: strings.Join(items, "\n")})
			items = nil
		}
	}
	for _, row := range grid[heads:] {
		var cells []*tableCell
		var at []int
		for col, cell := range row {
			if cell != nil && (len(cells) == 0 || cells[len(cells)-1].id != cell.id) {
				cells = append(cells, cell)
				at = append(at, col)
			}
		}
		if len(cells) == 1 && cols > 1 { // a row that spans the table is a subheading
			if t := inlineCell(cells[0]); t != "" {
				flush()
				out = append(out, block{kind: kPara, text: bold(t)})
			}
			continue
		}
		// Cells under one heading (a heading spanning several columns) form one field.
		type field struct {
			label  string
			values []string
		}
		var fields []*field
		for i, cell := range cells {
			v := joinBlocks(cell.blocks, true)
			if v == "" {
				continue
			}
			l := labels[at[i]]
			if n := len(fields); n > 0 && l != "" && fields[n-1].label == l {
				fields[n-1].values = append(fields[n-1].values, v)
				continue
			}
			fields = append(fields, &field{label: l, values: []string{v}})
		}
		var lines []string
		for _, f := range fields {
			sep := " / "
			for _, v := range f.values {
				if strings.Contains(v, "\n") {
					sep = "\n"
				}
			}
			v := strings.Join(f.values, sep)
			if f.label != "" && f.label != v {
				v = bold(f.label+":") + " " + v
			}
			if len(lines) == 0 {
				lines = append(lines, indent(v, "- ", "  "))
			} else {
				lines = append(lines, indent(v, "  - ", "    "))
			}
		}
		if len(lines) > 0 {
			items = append(items, strings.Join(lines, "\n"))
		}
	}
	flush()
	return out
}

func span(v string, limit int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, max(limit, 1))
}

// inlineCell renders a cell on one line, for a GitHub table or a column label.
func inlineCell(c *tableCell) string {
	if c == nil {
		return ""
	}
	t := strings.ReplaceAll(joinBlocks(c.blocks, true), "\n", "<br>")
	for strings.Contains(t, "<br><br>") {
		t = strings.ReplaceAll(t, "<br><br>", "<br>")
	}
	return strings.ReplaceAll(t, "|", `\|`)
}

func gfmTable(grid [][]*tableCell, heads, cols int) string {
	row := func(cells []*tableCell) (string, bool) {
		parts := make([]string, cols)
		filled := false
		for i := range cols {
			if i < len(cells) {
				parts[i] = inlineCell(cells[i])
			}
			filled = filled || parts[i] != ""
		}
		return "| " + strings.Join(parts, " | ") + " |", filled
	}
	var lines []string
	body := grid
	if heads > 0 || len(grid) > 1 { // with no marked header, the first row reads as one
		h, _ := row(grid[0])
		lines = append(lines, h)
		body = grid[1:]
	} else {
		lines = append(lines, "|"+strings.Repeat("  |", cols))
	}
	lines = append(lines, "|"+strings.Repeat(" --- |", cols))
	n := 0
	for _, cells := range body {
		if l, ok := row(cells); ok {
			lines = append(lines, l)
			n++
		}
	}
	if n == 0 && heads == 0 && len(grid) <= 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
