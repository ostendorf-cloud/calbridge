// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package httpapi

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ostendorf-cloud/govue/api"
)

// apiPathPrefix is every path this application answers as JSON rather than
// as part of the SPA. newSPAHandler checks it directly, rather than
// registering its own "/api/" pattern on the mux, because http.ServeMux
// would then treat an unmatched route under /api/ and an unmatched route
// anywhere else as two different routes, and this application wants
// exactly one catch-all, answering each shape differently.
const apiPathPrefix = "/api/"

// publicSiteURLPlaceholder is the literal text web/index.html carries in
// its <meta name="calbridge-public-site-url"> tag. newSPAHandler
// replaces it once, at construction, with Deps.PublicSiteURL, mirroring
// internal/landingapi's own appURLPlaceholder on the landing build: the
// same built bundle then links back to whatever public site a given
// deployment configured, rather than one fixed at image build time. An
// empty PublicSiteURL leaves the tag's content empty, and web/'s own
// composable renders no link for that.
//
// An index.html carrying no placeholder at all is a different case and
// newSPAHandler refuses it, the same refusal internal/landingapi makes:
// an empty configured value is a deployment's own choice not to link
// back, while a missing placeholder is the tag having been dropped from
// web/index.html, which would silently remove the link from every
// deployment instead.
const publicSiteURLPlaceholder = "__CALBRIDGE_PUBLIC_SITE_URL__"

// newSPAHandler serves the embedded Vue build: a real static asset when
// the request path names one, and index.html for everything else so the
// SPA's own client-side router can take over, matching how a single page
// application is conventionally served behind a catch-all route. An
// unmatched path under apiPathPrefix is answered with the same JSON error
// envelope every other endpoint in this package uses, never the SPA's
// HTML, so a client calling a mistyped or removed API path gets something
// it can actually parse.
func newSPAHandler(distFS fs.FS, publicSiteURL string) (http.Handler, error) {
	rawIndex, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("httpapi: read embedded index.html: %w", err)
	}

	if !strings.Contains(string(rawIndex), publicSiteURLPlaceholder) {
		return nil, fmt.Errorf(
			"httpapi: embedded index.html does not carry the %q placeholder", publicSiteURLPlaceholder)
	}

	// Escaped for the context the placeholder sits in: the value of a
	// double-quoted HTML attribute. internal/config's own
	// checkPublicSiteURL refuses a URL that is not absolute http or
	// https with a host, credentials, a path, a query or a fragment, but
	// a validator answers whether a value is a usable URL, not whether
	// it is safe to paste into markup, and the two answers differ:
	// net/url accepts a double quote inside a host and url.URL.String
	// re-emits it, so an unescaped substitution of https://x" ends the
	// attribute early and the remainder of the tag is reparsed as
	// attribute names. The document has to come out well-formed for
	// whatever the configuration holds, which is this escape and not the
	// validator. internal/landingapi's newStaticHandler escapes its own
	// APP_URL the same way.
	index := []byte(strings.ReplaceAll(string(rawIndex), publicSiteURLPlaceholder, html.EscapeString(publicSiteURL)))

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, apiPathPrefix) {
			api.WriteError(w, r, api.NotFound("unknown endpoint"))

			return
		}

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
