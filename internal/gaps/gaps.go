// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package gaps records the questions the documentation didn't answer, in a file only
// the user can read, and summarizes them for the user to share with Tenable if they
// choose. Nothing is sent anywhere.
package gaps

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one unanswered question.
type Record struct {
	Time     time.Time `json:"time"`
	Question string    `json:"question"`
	Product  string    `json:"product,omitempty"`
	Language string    `json:"language,omitempty"`
	// Note is what the assistant tried or found, such as "only covers version 6.3".
	Note string `json:"note,omitempty"`
	// Version is the server's version.
	Version string `json:"version,omitempty"`
	// URL is the page the question is about: the one that came closest, or the
	// product's main guide.
	URL string `json:"url,omitempty"`
	// SentAt is when the question was sent to Tenable's documentation team, if it was.
	SentAt time.Time `json:"sent_at,omitzero"`
}

// Limits on what's recorded. A question is limited as the feedback service limits it,
// so any recorded question can be sent.
const (
	MaxQuestion = 1000
	MaxNote     = 1000
	maxProduct  = 200
	maxLanguage = 20
	maxVersion  = 100
	maxURL      = 2048
)

// maxLine is the longest line Read takes; Add's limits keep a record well under it, and
// a longer line, from an older version or an edit, is skipped.
const maxLine = 1 << 20

// DefaultPath returns where gaps are recorded: tenable-docs-mcp/gaps.jsonl in the user's
// settings folder.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tenable-docs-mcp", "gaps.jsonl"), nil
}

var mu sync.Mutex // serializes writes from this process

// Add appends a record, creating the file (readable only by its owner) if needed. It
// returns how many records the file now holds.
func Add(path string, r Record) (int, error) {
	r.Question = clip(strings.TrimSpace(r.Question), MaxQuestion)
	r.Note = clip(strings.TrimSpace(r.Note), MaxNote)
	r.Product = clip(strings.TrimSpace(r.Product), maxProduct)
	r.Language = clip(strings.TrimSpace(r.Language), maxLanguage)
	r.Version = clip(r.Version, maxVersion)
	if len(r.URL) > maxURL {
		r.URL = ""
	}
	if r.Question == "" {
		return 0, errors.New("the question is empty")
	}
	if r.Time.IsZero() {
		r.Time = time.Now().UTC()
	}
	line, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	recs, err := Read(path)
	return len(recs), err
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Read returns every record, oldest first. A missing file has none; lines that don't
// parse are skipped.
func Read(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	br := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := readLine(br)
		var r Record
		if len(line) > 0 && json.Unmarshal(line, &r) == nil && r.Question != "" {
			out = append(out, r)
		}
		if err == io.EOF {
			return out, nil
		} else if err != nil {
			return out, err
		}
	}
}

// readLine reads one line, without its newline. A line longer than maxLine comes back
// empty, so it's skipped like any line that doesn't parse.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > maxLine+1 {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			line = nil
		}
		return bytes.TrimRight(line, "\r\n"), err
	}
}

// MarkSent records that the record recorded at t with question was sent at sent. Lines
// that don't parse are kept as they are. It returns false if no unsent record matches.
func MarkSent(path string, t time.Time, question string, sent time.Time) (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	marked := false
	for i, line := range lines {
		var r Record
		if marked || json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		if r.Question == question && r.Time.Equal(t) && r.SentAt.IsZero() {
			r.SentAt = sent.UTC()
			b, err := json.Marshal(r)
			if err != nil {
				return false, err
			}
			lines[i] = string(b) + "\n"
			marked = true
		}
	}
	if !marked {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "")), 0o600); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// MarkLatestSent marks the most recent unsent record of question as sent, as when the
// user sends a question from the form right after it was recorded.
func MarkLatestSent(path, question string, sent time.Time) (bool, error) {
	recs, err := Read(path)
	if err != nil {
		return false, err
	}
	question = clip(strings.TrimSpace(question), MaxQuestion)
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Question == question && recs[i].SentAt.IsZero() {
			return MarkSent(path, recs[i].Time, question, sent)
		}
	}
	return false, nil
}

// Clear deletes the file of records.
func Clear(path string) error {
	mu.Lock()
	defer mu.Unlock()
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Summary groups records for sharing.
type Summary struct {
	Total    int            `json:"total"`
	First    time.Time      `json:"first"`
	Last     time.Time      `json:"last"`
	Products []ProductGaps  `json:"products"`
	Language map[string]int `json:"languages"`
}

// ProductGaps lists the questions for one product ("" for questions without one).
type ProductGaps struct {
	Product   string     `json:"product"`
	Count     int        `json:"count"`
	Questions []Question `json:"questions"`
}

// Question is a question and how many times it was recorded.
type Question struct {
	Text  string   `json:"text"`
	Count int      `json:"count"`
	Notes []string `json:"notes,omitempty"`
}

// Summarize groups records since a time (zero for all) by product, and the same
// question (ignoring case) within a product, most asked first.
func Summarize(recs []Record, since time.Time) Summary {
	s := Summary{Language: map[string]int{}}
	byProduct := map[string]*ProductGaps{}
	byQuestion := map[string]*Question{}
	for _, r := range recs {
		if r.Time.Before(since) {
			continue
		}
		s.Total++
		if s.First.IsZero() || r.Time.Before(s.First) {
			s.First = r.Time
		}
		if r.Time.After(s.Last) {
			s.Last = r.Time
		}
		lang := r.Language
		if lang == "" {
			lang = "en"
		}
		s.Language[lang]++
		pg := byProduct[r.Product]
		if pg == nil {
			pg = &ProductGaps{Product: r.Product}
			byProduct[r.Product] = pg
		}
		pg.Count++
		key := r.Product + "\x00" + strings.ToLower(strings.Join(strings.Fields(r.Question), " "))
		q := byQuestion[key]
		if q == nil {
			q = &Question{Text: r.Question}
			byQuestion[key] = q
		}
		q.Count++
		if r.Note != "" && len(q.Notes) < 5 && !contains(q.Notes, r.Note) {
			q.Notes = append(q.Notes, r.Note)
		}
	}
	for key, q := range byQuestion {
		product, _, _ := strings.Cut(key, "\x00")
		byProduct[product].Questions = append(byProduct[product].Questions, *q)
	}
	for _, pg := range byProduct {
		sort.Slice(pg.Questions, func(i, j int) bool {
			if pg.Questions[i].Count != pg.Questions[j].Count {
				return pg.Questions[i].Count > pg.Questions[j].Count
			}
			return pg.Questions[i].Text < pg.Questions[j].Text
		})
		s.Products = append(s.Products, *pg)
	}
	sort.Slice(s.Products, func(i, j int) bool {
		if s.Products[i].Count != s.Products[j].Count {
			return s.Products[i].Count > s.Products[j].Count
		}
		return s.Products[i].Product < s.Products[j].Product
	})
	return s
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Markdown writes a summary for sharing.
func (s Summary) Markdown() string {
	var b strings.Builder
	b.WriteString("# Questions the Tenable documentation didn't answer\n\n")
	if s.Total == 0 {
		b.WriteString("None recorded.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%d questions recorded by Tenable Docs MCP between %s and %s.\n",
		s.Total, s.First.Format("2006-01-02"), s.Last.Format("2006-01-02"))
	var langs []string
	for l, n := range s.Language {
		langs = append(langs, fmt.Sprintf("%s %d", l, n))
	}
	sort.Strings(langs)
	fmt.Fprintf(&b, "Languages: %s.\n", strings.Join(langs, ", "))
	for _, pg := range s.Products {
		name := pg.Product
		if name == "" {
			name = "No product named"
		}
		fmt.Fprintf(&b, "\n## %s (%d)\n\n", name, pg.Count)
		for _, q := range pg.Questions {
			line := "- " + strings.ReplaceAll(q.Text, "\n", " ")
			if q.Count > 1 {
				line += fmt.Sprintf(" (asked %d times)", q.Count)
			}
			b.WriteString(line + "\n")
			for _, n := range q.Notes {
				fmt.Fprintf(&b, "  - Note: %s\n", strings.ReplaceAll(n, "\n", " "))
			}
		}
	}
	return b.String()
}
