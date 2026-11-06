// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalSource reads documents from local copies of the sites' published files: one
// folder per site, holding the files at the same paths as on the site. A URL maps to the
// file at the same path under its site's folder.
type LocalSource struct {
	roots        map[string]string // host → folder
	maxBodyBytes int64
}

// NewLocal returns a LocalSource for the given folders, keyed by site code ("en", "jp",
// …; see Sites).
func NewLocal(dirs map[string]string) (*LocalSource, error) {
	ls := &LocalSource{roots: map[string]string{}, maxBodyBytes: 32 << 20}
	for code, dir := range dirs {
		s, ok := ByCode(code)
		if !ok {
			return nil, fmt.Errorf("unknown site %q (use one of %s)", code, siteCodes())
		}
		if err := isDir(dir); err != nil {
			return nil, fmt.Errorf("site %s: %w", code, err)
		}
		ls.roots[s.Host] = dir
	}
	if len(ls.roots) == 0 {
		return nil, errors.New("no local site folders given")
	}
	return ls, nil
}

// ParseSiteDirs reads site folders written as code=folder pairs separated by the
// operating system's list separator (";" on Windows, ":" elsewhere), as in
// "en=/data/docs:jp=/data/docs-jp".
func ParseSiteDirs(v string) (map[string]string, error) {
	dirs := map[string]string{}
	for _, pair := range filepath.SplitList(v) {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		code, dir, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(code) == "" || strings.TrimSpace(dir) == "" {
			return nil, fmt.Errorf("site folder %q: use code=folder", pair)
		}
		code = strings.ToLower(strings.TrimSpace(code))
		if _, dup := dirs[code]; dup {
			return nil, fmt.Errorf("site %q is given twice", code)
		}
		dirs[code] = strings.TrimSpace(dir)
	}
	return dirs, nil
}

func siteCodes() string {
	codes := make([]string, len(Sites))
	for i, s := range Sites {
		codes[i] = s.Code
	}
	return strings.Join(codes, ", ")
}

// Sites returns the sites this source has a folder for, in the order of Sites.
func (ls *LocalSource) Sites() []Site {
	var out []Site
	for _, s := range Sites {
		if _, ok := ls.roots[s.Host]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Fetch reads the document's file. If req.LastModified matches the file's modification
// time, it returns a NotModified response without reading the file.
func (ls *LocalSource) Fetch(_ context.Context, req Request) (*Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", req.URL, err)
	}
	root, ok := ls.roots[strings.ToLower(u.Hostname())]
	if !ok {
		if IsDocsHost(u.Hostname()) {
			return nil, fmt.Errorf("read %s: no local folder for this site: %w", req.URL, ErrNotFound)
		}
		return nil, fmt.Errorf("read %s: %w", req.URL, ErrHostNotAllowed)
	}
	file, err := fileFor(root, u.Path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.URL, err)
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return &Response{URL: req.URL, Status: http.StatusNotFound}, fmt.Errorf("read %s: %w", req.URL, ErrNotFound)
	}
	resp := &Response{
		URL:          req.URL,
		Status:       http.StatusOK,
		LastModified: info.ModTime().UTC().Format(http.TimeFormat),
		ContentType:  mime.TypeByExtension(filepath.Ext(file)),
	}
	if req.LastModified != "" && req.LastModified == resp.LastModified {
		resp.Status, resp.NotModified = http.StatusNotModified, true
		return resp, nil
	}
	if info.Size() > ls.maxBodyBytes {
		return nil, fmt.Errorf("read %s: %w (over %d bytes)", req.URL, ErrTooLarge, ls.maxBodyBytes)
	}
	if resp.Body, err = os.ReadFile(file); err != nil {
		return nil, fmt.Errorf("read %s: %w", req.URL, err)
	}
	return resp, nil
}

// fileFor maps a URL path to a file under root, refusing anything outside it.
func fileFor(root, urlPath string) (string, error) {
	p, err := url.PathUnescape(urlPath)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(p, 0) || strings.Contains(p, "\\") {
		return "", errors.New("invalid path")
	}
	clean := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") || clean == "/" {
		clean = path.Join(clean, "index.htm")
	}
	file := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("path outside the documentation folder")
	}
	return file, nil
}

func isDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "open", Path: dir, Err: errors.New("not a folder")}
	}
	return nil
}
