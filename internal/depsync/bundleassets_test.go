// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"fmt"
	"io/fs"
	"os"
	"path"
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
// a linter reads. The two tests below are what makes the claim
// checkable.
//
// They read the build output, so they need it to exist: "task test:go"
// depends on "task web:build" and "task landing-web:build", both of
// which are required anyway because each binary embeds its own dist.
const (
	// landingDistPath is the landing server's built site, relative to
	// this package.
	landingDistPath = "../../cmd/calbridge-landing/dist"
	// appDistPath is the application's own built SPA. The privacy
	// policy's claim covers both, so both are scanned.
	appDistPath = "../../cmd/calbridge/dist"
)

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

// scannedExtensions are the files worth reading: the document, the
// stylesheets and the scripts. A font or an image in the bundle is
// already a local file by the time it is there.
var scannedExtensions = []string{".html", ".css", ".js", ".mjs"}

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

// scannedFiles reads every document, stylesheet and script in a build
// directory, keyed by its path inside that directory.
func scannedFiles(t *testing.T, distPath string) map[string]string {
	t.Helper()

	if _, err := os.Stat(distPath); err != nil {
		t.Fatalf(
			"%s is not readable: %v. Run \"task web:build\" and \"task landing-web:build\" first; "+
				"\"task test:go\" depends on both.",
			distPath, err,
		)
	}

	files := make(map[string]string)

	err := fs.WalkDir(os.DirFS(distPath), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !slices.Contains(scannedExtensions, strings.ToLower(path.Ext(name))) {
			return nil
		}

		content, err := os.ReadFile(path.Join(distPath, name))
		if err != nil {
			return err
		}
		files[name] = string(content)

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", distPath, err)
	}

	if len(files) == 0 {
		t.Fatalf("%s holds no document, stylesheet or script, so this scan checks nothing", distPath)
	}

	return files
}

// TestTheBuiltSitesFetchNothingOffTheirOwnOrigin is the privacy
// policy's noAnalytics claim as a test: a tracking script, a
// third-party embed or a stylesheet pulling a font off another server
// all land in the built output as an off-origin reference, and all of
// them fail here.
func TestTheBuiltSitesFetchNothingOffTheirOwnOrigin(t *testing.T) {
	t.Parallel()

	for name, distPath := range map[string]string{"landing": landingDistPath, "application": appDistPath} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for file, content := range scannedFiles(t, distPath) {
				for _, reference := range offOriginReferences(content) {
					t.Errorf(
						"%s/%s fetches %s from another origin. "+
							"Both sites' privacy policy states that neither loads an analytics service, "+
							"a third-party embed or a runtime font; bundle the asset instead, "+
							"or correct privacy.noAnalytics in both locales.",
						distPath, file, reference,
					)
				}
			}
		})
	}
}

// TestTheBuiltSitesNameNoHostOutsideTheAllowList is the same claim read
// from the other side, and it is the half that catches a fetch no tag
// and no stylesheet declares: a script assembling a request at runtime
// still has to carry the host as text. Every host either bundle names
// today appears in a namespace, a comment or an error message, which is
// why the allow list spells out why each is there. A new host is a new
// decision: either it is not fetched and belongs in the list with its
// reason, or it is, and the policy's claim no longer holds.
func TestTheBuiltSitesNameNoHostOutsideTheAllowList(t *testing.T) {
	t.Parallel()

	for name, distPath := range map[string]string{"landing": landingDistPath, "application": appDistPath} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for file, content := range scannedFiles(t, distPath) {
				for _, host := range foreignHosts(content) {
					t.Errorf(
						"%s/%s names the host %s. Add it to allowedAbsoluteHosts with the reason it is "+
							"text rather than a request, or, if it is fetched, correct privacy.noAnalytics "+
							"in both locales.",
						distPath, file, host,
					)
				}
			}
		})
	}
}
