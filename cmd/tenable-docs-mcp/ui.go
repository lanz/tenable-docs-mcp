// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-isatty"

	"github.com/lanz/tenable-docs-mcp/internal/server"
	"github.com/lanz/tenable-docs-mcp/internal/version"
)

// width is the column the help text wraps at, so it fits a default terminal.
const width = 80

// wordmark is the program's name, drawn in block characters for the launch screen and
// the help. Each line is 64 columns or fewer.
var wordmark = []string{
	"▀█▀ █▀▀ █▄ █ ▄▀▄ █▀▄ █   █▀▀    █▀▄ ▄▀▄ ▄▀▀ ▄▀▀    █▄ ▄█ ▄▀▀ █▀▄",
	" █  █▀  █ ▀█ █▀█ █▀▄ █   █▀     █ █ █ █ █    ▀▄    █ ▀ █ █   █▀",
	" ▀  ▀▀▀ ▀  ▀ ▀ ▀ ▀▀  ▀▀▀ ▀▀▀    ▀▀   ▀   ▀▀ ▀▀     ▀   ▀  ▀▀ ▀",
}

// tagline says what the program is, under the wordmark.
const tagline = "Cited answers from Tenable's product documentation"

// isTerminal reports whether f is a terminal, including Git Bash's and Cygwin's on
// Windows. Tests replace it.
var isTerminal = func(f *os.File) bool {
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// style is how text written to one stream looks: in color only on a terminal, unless
// NO_COLOR is set or TERM is dumb.
type style struct{ color bool }

// styleFor returns the style for w, and on Windows turns on the console's support for
// color when it can.
func styleFor(w io.Writer) style {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(f) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return style{}
	}
	return style{color: enableColor(f)}
}

func (s style) wrap(code, t string) string {
	if !s.color || t == "" {
		return t
	}
	return "\x1b[" + code + "m" + t + "\x1b[0m"
}

func (s style) bold(t string) string    { return s.wrap("1", t) }
func (s style) dim(t string) string     { return s.wrap("2", t) }
func (s style) accent(t string) string  { return s.wrap("36", t) }
func (s style) heading(t string) string { return s.wrap("1", strings.ToUpper(t)) }

// header writes the wordmark and the tagline with the version, on a terminal; elsewhere,
// such as in a file or a pipe, just the name, version, and tagline.
func header(w io.Writer, s style, terminal bool) {
	if terminal {
		for _, l := range wordmark {
			fmt.Fprintln(w, s.accent(l))
		}
		fmt.Fprintf(w, "\n%s  %s  %s\n", tagline, s.dim("·"), s.dim(version.Get()))
		return
	}
	fmt.Fprintf(w, "%s %s\n%s\n", server.Name, version.Get(), tagline)
}

// helpRow is one line of a help section: a command or flag, and what it does.
type helpRow struct{ left, about string }

// writeRows writes rows in two columns, the second wrapped to fit width.
func writeRows(w io.Writer, s style, rows []helpRow, left func(string) string) {
	writeRowsAt(w, rows, left, leftWidth(rows))
}

// leftWidth is the width of the widest first column among rows, up to 26.
func leftWidth(rows []helpRow) int {
	col := 0
	for _, r := range rows {
		col = max(col, utf8.RuneCountInString(r.left))
	}
	return min(col, 26)
}

// writeRowsAt writes rows with the second column at col, so that several sections can
// line up.
func writeRowsAt(w io.Writer, rows []helpRow, left func(string) string, col int) {
	for _, r := range rows {
		pad := col - utf8.RuneCountInString(r.left)
		lines := wrapText(r.about, width-col-4)
		if pad < 0 {
			// Too long for the column: the description starts on the next line.
			fmt.Fprintf(w, "  %s\n", left(r.left))
			for _, l := range lines {
				fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", col), l)
			}
			continue
		}
		fmt.Fprintf(w, "  %s%s  %s\n", left(r.left), strings.Repeat(" ", pad), first(lines))
		for _, l := range rest(lines) {
			fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", col), l)
		}
	}
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func rest(lines []string) []string {
	if len(lines) < 2 {
		return nil
	}
	return lines[1:]
}

// wrapText breaks t into lines of at most n characters, at spaces.
func wrapText(t string, n int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(t) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > n:
			lines = append(lines, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// writeParagraph writes t wrapped to width, indented two spaces.
func writeParagraph(w io.Writer, t string) {
	for _, l := range wrapText(t, width-2) {
		fmt.Fprintln(w, "  "+l)
	}
}

// writeText writes t wrapped to width, without an indent.
func writeText(w io.Writer, t string) {
	for _, l := range wrapText(t, width) {
		fmt.Fprintln(w, l)
	}
}

// setUsage makes a command's -h print its usage line, what it does, and its flags,
// aligned, with each flag's default.
func setUsage(fs *flag.FlagSet, w io.Writer, usage, about string, notes ...string) {
	fs.SetOutput(w)
	fs.Usage = func() {
		s := styleFor(w)
		fmt.Fprintf(w, "%s\n  %s %s\n\n", s.heading("Usage"), server.Name, usage)
		writeParagraph(w, sentence(about))
		for _, n := range notes {
			fmt.Fprintln(w)
			writeParagraph(w, sentence(n))
		}
		var rows []helpRow
		fs.VisitAll(func(f *flag.Flag) {
			name, desc := flag.UnquoteUsage(f)
			left := "-" + f.Name
			if p := placeholder(f.Name, name); p != "" {
				left += " " + p
			}
			if d := f.DefValue; d != "" && !isZeroFlag(f) {
				desc += " (default " + d + ")"
			}
			rows = append(rows, helpRow{left, upperFirst(desc)})
		})
		if len(rows) > 0 {
			fmt.Fprintf(w, "\n%s\n", s.heading("Flags"))
			writeRows(w, s, rows, s.accent)
		}
	}
}

// sentence capitalizes t and ends it with a period.
func sentence(t string) string {
	t = upperFirst(strings.TrimSpace(t))
	if t != "" && !strings.HasSuffix(t, ".") {
		t += "."
	}
	return t
}

// placeholders name what a flag takes, by the flag's name, in place of its Go type.
var placeholders = map[string]string{
	"index": "DIR", "out": "DIR", "file": "FILE", "since": "DATE", "sites": "LIST",
	"base": "URL", "url": "URL", "client": "NAME", "http": "ADDR", "source": "DIRS",
	"guide-names": "FILE", "guide": "GUIDE", "product": "PRODUCT", "version": "VERSION",
	"from-version": "VERSION", "to-version": "VERSION", "language": "LANG",
	"allow-host": "HOSTS", "client-ip-header": "HEADER",
}

// placeholder is what a flag's value is called in the help: DIR for -index, N for a
// number, TEXT for other strings, and nothing for a boolean.
func placeholder(flagName, typeName string) string {
	switch {
	case typeName == "":
		return ""
	case placeholders[flagName] != "":
		return placeholders[flagName]
	case typeName == "int" || typeName == "uint" || typeName == "float":
		return "N"
	case typeName == "string":
		return "TEXT"
	}
	return strings.ToUpper(typeName)
}

// parseExit is the exit status for a flag parse error: 0 when the user asked for the
// help with -h, and 2 for a mistake.
func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// isZeroFlag reports whether a flag's default is its type's zero value, which goes
// without saying, as flag.PrintDefaults leaves it out.
func isZeroFlag(f *flag.Flag) bool {
	t := reflect.TypeOf(f.Value)
	var z reflect.Value
	if t.Kind() == reflect.Pointer {
		z = reflect.New(t.Elem())
	} else {
		z = reflect.Zero(t)
	}
	v, ok := z.Interface().(flag.Value)
	return ok && f.DefValue == v.String()
}

// closest returns the command name nearest to name, for "did you mean", or "".
func closest(name string, names []string) string {
	best, bestDist := "", 3
	for _, n := range names {
		if d := editDistance(name, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}
