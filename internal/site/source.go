// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
)

// Request asks a Source for one document.
type Request struct {
	// URL is the document's address on a documentation site.
	URL string
	// ETag and LastModified, when set, come from an earlier fetch of the same URL. If
	// the document hasn't changed since, the Source returns a NotModified response
	// without a body.
	ETag         string
	LastModified string
}

// Response is a fetched document.
type Response struct {
	// URL is the address the document came from, after any redirects.
	URL          string
	Status       int
	NotModified  bool
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
}

// Source fetches documents from the documentation sites.
type Source interface {
	Fetch(ctx context.Context, req Request) (*Response, error)
}

var (
	// ErrNotFound means the document doesn't exist (HTTP 404 or 410, or no such file).
	ErrNotFound = errors.New("not found")
	// ErrHostNotAllowed means the URL isn't on a documentation site.
	ErrHostNotAllowed = errors.New("not a Tenable documentation site")
	// ErrTooLarge means the document is larger than the size limit.
	ErrTooLarge = errors.New("document too large")
)
