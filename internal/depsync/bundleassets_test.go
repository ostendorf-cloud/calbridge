// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Both published sites claim, in their own privacy policy, to load no
// analytics or tracking service, no third-party embed and no font
// fetched from another server at runtime (privacy.noAnalytics in
// web-landing/src/i18n/locales/de.json and en.json). That is a claim
// about the built output, not about the source: a theme or a component
// pulling in a Google Fonts stylesheet, or an index.html gaining a
// script tag, changes what a browser fetches without changing anything
// a linter reads. bundleassets_builtfrontend_test.go is what makes the
// claim checkable.
//
// This file holds the scan and its unit tests, which need nothing built.
// The two tests that read the real build output live in
// bundleassets_builtfrontend_test.go, behind a build tag, because they
// need both frontends on disk and no job may run them without having
// built them.

// allowedAbsoluteHosts is every host an absolute URL in either bundle
// may name. None of them is fetched: each appears as text.
var allowedAbsoluteHosts = map[string]string{
	// XML namespace identifiers Vue's own runtime carries for SVG,
	// MathML, XHTML and xlink. A namespace is a name, never a request.
	"www.w3.org": "XML namespace identifiers in Vue's runtime",
	// A URL inside a comment Tailwind's generated stylesheet carries.
	"tailwindcss.com": "a documentation link in Tailwind's generated CSS",
	// The documentation link Vue's production runtime prints with a
	// runtime error code.
	"vuejs.org": "Vue's own error-reference link in an error message",
}

// Each pattern captures the target of something a browser fetches:
// a tag's src or href, a stylesheet's url(), and an @import.
var (
	tagReferencePattern = regexp.MustCompile(
		`(?is)<(?:script|link|img|iframe|source|audio|video|embed|object)\b[^>]*?\b(?:src|href|data)\s*=\s*["']([^"']*)["']`)
	cssURLPattern    = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]*)`)
	cssImportPattern = regexp.MustCompile(`(?i)@import\s+(?:url\(\s*)?["']([^"']*)["']`)
	absoluteURLHost  = regexp.MustCompile(`(?i)\bhttps?://([a-z0-9.\-]+)`)
)

// isOffOrigin reports whether a fetched reference leaves the host
// serving the document: an absolute URL, or a protocol-relative one.
// Everything else resolves against the site's own origin.
func isOffOrigin(reference string) bool {
	trimmed := strings.TrimSpace(reference)
	lower := strings.ToLower(trimmed)

	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//")
}

// offOriginReferences returns every off-origin resource the content
// fetches, deduplicated and sorted so a failure reads the same way
// twice.
func offOriginReferences(content string) []string {
	var found []string

	for _, pattern := range []*regexp.Regexp{tagReferencePattern, cssURLPattern, cssImportPattern} {
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			if reference := match[1]; isOffOrigin(reference) {
				found = append(found, strings.TrimSpace(reference))
			}
		}
	}

	sort.Strings(found)

	return slices.Compact(found)
}

// foreignHosts returns every host an absolute URL in the content names
// that allowedAbsoluteHosts does not cover.
func foreignHosts(content string) []string {
	var found []string

	for _, match := range absoluteURLHost.FindAllStringSubmatch(content, -1) {
		host := strings.ToLower(match[1])
		if _, ok := allowedAbsoluteHosts[host]; !ok {
			found = append(found, host)
		}
	}

	sort.Strings(found)

	return slices.Compact(found)
}

// TestOffOriginReferences covers what the scan counts as a fetch off
// this origin and what it leaves alone.
func TestOffOriginReferences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "the bundle's own script and stylesheet",
			content: `<script src="/assets/index-abc.js"></script><link rel="stylesheet" href="/assets/index-abc.css">`,
			want:    nil,
		},
		{
			name:    "an analytics script",
			content: `<script src="https://analytics.example.com/t.js"></script>`,
			want:    []string{"https://analytics.example.com/t.js"},
		},
		{
			name:    "a protocol-relative script",
			content: `<script src="//analytics.example.com/t.js"></script>`,
			want:    []string{"//analytics.example.com/t.js"},
		},
		{
			name:    "a web font stylesheet",
			content: `<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">`,
			want:    []string{"https://fonts.googleapis.com/css2?family=Inter"},
		},
		{
			name:    "a runtime font file",
			content: `@font-face{font-family:Inter;src:url("https://fonts.gstatic.com/s/inter.woff2") format("woff2")}`,
			want:    []string{"https://fonts.gstatic.com/s/inter.woff2"},
		},
		{
			name:    "an embedded font",
			content: `@font-face{font-family:Inter;src:url(data:font/woff2;base64,AAAA) format("woff2")}`,
			want:    nil,
		},
		{
			name:    "an imported remote stylesheet",
			content: `@import url("https://cdn.example.com/reset.css");`,
			want:    []string{"https://cdn.example.com/reset.css"},
		},
		{
			name:    "a namespace, which is a name and not a fetch",
			content: "const svg = `http://www.w3.org/2000/svg`",
			want:    nil,
		},
		{
			name:    "the same remote script twice",
			content: `<script src="https://a.example/t.js"></script><script src="https://a.example/t.js"></script>`,
			want:    []string{"https://a.example/t.js"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := offOriginReferences(tc.content)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("offOriginReferences() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestForeignHosts covers the allow list the host scan works from.
func TestForeignHosts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "an allowed namespace host",
			content: "http://www.w3.org/2000/svg and https://vuejs.org/error-reference/",
			want:    nil,
		},
		{
			name:    "a tracker's host",
			content: `fetch("https://analytics.example.com/collect")`,
			want:    []string{"analytics.example.com"},
		},
		{
			name:    "a host named in upper case",
			content: "HTTPS://Analytics.Example.COM/collect",
			want:    []string{"analytics.example.com"},
		},
		{
			name:    "no absolute URL at all",
			content: `fetch("/api/v1/me")`,
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := foreignHosts(tc.content)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("foreignHosts() = %v, want %v", got, tc.want)
			}
		})
	}
}
