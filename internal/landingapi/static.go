// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingapi

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ostendorf-cloud/calbridge/internal/landingconfig"
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

// operatorPlaceholder is the token web-landing/index.html carries in
// place of the operator's details, as the content of a meta tag.
// newStaticHandler replaces it once, at construction, with the operator
// as JSON, escaped for the attribute it lands in. web-landing/src/
// operator.ts reads it back, so the imprint and the privacy policy show
// the deployment's real operator and the repository holds none.
const operatorPlaceholder = "__CALBRIDGE_OPERATOR__"

// spaPages lists every path the client-side router answers with a page,
// and so every path the server answers 200 for. Any other path is a 404,
// so a verifier or a crawler never takes the home page for a page that
// does not exist. web-landing/src/router/index.test.ts pins the same
// list from the other side.
func spaPages() map[string]struct{} {
	pages := map[string]struct{}{"/": {}, "/pricing": {}}

	for _, code := range languageCodes {
		for _, page := range legalPages {
			pages["/"+code+"/"+page] = struct{}{}
		}
	}

	return pages
}

// newStaticHandler serves the embedded Vue build: a real static asset
// when the request path names one, index.html with a 200 for each page
// the client-side router owns, and index.html with a 404 for everything
// else, so the router renders its own not found page while the status
// tells a crawler the truth. There is no apiPathPrefix carve-out here:
// this build makes no API call of its own.
func newStaticHandler(distFS fs.FS, appURL string, operator landingconfig.Operator) (http.Handler, error) {
	rawIndex, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("landingapi: read embedded index.html: %w", err)
	}

	for _, placeholder := range []string{appURLPlaceholder, operatorPlaceholder} {
		if !strings.Contains(string(rawIndex), placeholder) {
			return nil, fmt.Errorf(
				"landingapi: embedded index.html does not carry the %q placeholder", placeholder)
		}
	}

	operatorJSON, err := json.Marshal(operator)
	if err != nil {
		return nil, fmt.Errorf("landingapi: encode operator: %w", err)
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
	// The operator JSON gets the same escape for the same reason: it is
	// text from the deployment, and the document has to stay well-formed
	// whatever it holds.
	replacer := strings.NewReplacer(
		appURLPlaceholder, html.EscapeString(appURL),
		operatorPlaceholder, html.EscapeString(string(operatorJSON)),
	)
	index := []byte(replacer.Replace(string(rawIndex)))

	fileServer := http.FileServer(http.FS(distFS))
	pages := spaPages()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(distFS, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				fileServer.ServeHTTP(w, r)

				return
			}
		}

		// A path under /assets/ that names no file is a missing file, not
		// a page: answering it with HTML would hand a script tag a
		// document.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)

			return
		}

		status := http.StatusNotFound
		if _, ok := pages[r.URL.Path]; ok {
			status = http.StatusOK
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(index)
	}), nil
}
