// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingapi

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"
)

// appURLPlaceholder is the token web-landing/index.html carries in place
// of the application's own URL. newStaticHandler replaces every
// occurrence with Deps.AppURL once, at construction, rather than on
// every request: the value never changes for the lifetime of a running
// process, so there is nothing to gain from redoing the substitution per
// request, and substituting once keeps the request path a plain byte
// serve.
//
// web-landing/src/main.ts reads the result back out of the meta tag this
// substitution lands in, which is what lets every page's call to action
// point at the configured application host instead of one baked into
// the built JavaScript at image build time; see that file's own comment.
const appURLPlaceholder = "__CALBRIDGE_APP_URL__"

// newStaticHandler serves the embedded Vue build: a real static asset
// when the request path names one, and index.html for everything else
// so the client-side router can take over, the same fallback shape
// internal/httpapi's newSPAHandler uses for the application. There is no
// apiPathPrefix carve-out here: this build makes no API call of its own,
// so every unmatched path is the SPA shell.
func newStaticHandler(distFS fs.FS, appURL string) (http.Handler, error) {
	rawIndex, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("landingapi: read embedded index.html: %w", err)
	}

	if !strings.Contains(string(rawIndex), appURLPlaceholder) {
		return nil, fmt.Errorf(
			"landingapi: embedded index.html does not carry the %q placeholder", appURLPlaceholder)
	}

	// Escaped for the context the placeholder sits in: the value of a
	// double-quoted HTML attribute. internal/landingconfig refuses a URL
	// that is not absolute http or https with a host and no path, but a
	// validator answers whether a value is a usable URL, not whether it
	// is safe to paste into markup, and the two answers differ: net/url
	// accepts a double quote inside a host and url.URL.String re-emits
	// it, so an unescaped substitution of https://x" ends the attribute
	// early and the remainder of the tag is reparsed as attribute names.
	// The document has to come out well-formed for whatever the
	// configuration holds, which is this escape and not the validator.
	index := []byte(strings.ReplaceAll(string(rawIndex), appURLPlaceholder, html.EscapeString(appURL)))

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(distFS, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				fileServer.ServeHTTP(w, r)

				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	}), nil
}
