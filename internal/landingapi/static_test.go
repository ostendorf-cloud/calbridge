// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/calbridge/internal/landingapi"
)

// committedIndexHTMLPath is web-landing's own index.html as committed,
// the file vite copies into the build output and the one the
// substitution has to find its placeholder in.
const committedIndexHTMLPath = "../../web-landing/index.html"

func TestNewHandlerFailsOnAnIndexWithNoPlaceholder(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.DistFS = fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>no placeholder here</title>")},
	}

	_, err := landingapi.NewHandler(deps)
	if err == nil {
		t.Fatal("NewHandler accepted an index.html with no app URL placeholder")
	}
	if !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("error %q does not mention the missing placeholder", err)
	}
}

func TestNewHandlerFailsOnAMissingIndex(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.DistFS = fstest.MapFS{}

	_, err := landingapi.NewHandler(deps)
	if err == nil {
		t.Fatal("NewHandler accepted a dist filesystem with no index.html")
	}
}

// TestTheCommittedIndexCarriesThePlaceholder reads web-landing's own
// index.html rather than a synthetic one. Every other test in this
// package builds its markup in Go, so all of them keep passing if the
// meta tag is dropped from the committed file: the refusal above would
// then fire in production and nowhere else. This is the one test that
// ties the two together.
func TestTheCommittedIndexCarriesThePlaceholder(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(committedIndexHTMLPath)
	if err != nil {
		t.Fatalf("read the committed index.html: %v", err)
	}

	if !strings.Contains(string(committed), "__CALBRIDGE_APP_URL__") {
		t.Errorf(
			"%s carries no __CALBRIDGE_APP_URL__ placeholder; newStaticHandler refuses such a build at startup",
			committedIndexHTMLPath,
		)
	}
}

// TestTheCommittedIndexIsSubstitutedAsMarkup serves the committed
// index.html itself, so the substitution is exercised against the real
// file and not only against a hand-written approximation of it.
func TestTheCommittedIndexIsSubstitutedAsMarkup(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile(committedIndexHTMLPath)
	if err != nil {
		t.Fatalf("read the committed index.html: %v", err)
	}

	deps := testDeps(t)
	deps.DistFS = fstest.MapFS{"index.html": {Data: committed}}

	handlers, err := landingapi.NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	handlers.Public.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "__CALBRIDGE_APP_URL__") {
		t.Error("the placeholder is still present in the served committed shell")
	}
	if !strings.Contains(body, `content="`+testAppURL+`"`) {
		t.Errorf("body = %q, want the app URL inside the meta tag's content attribute", body)
	}
}

// TestAnAppURLWithMarkupInItIsEscaped is about the substitution being an
// escape and not a paste. net/url accepts a double quote, an angle
// bracket and an ampersand inside a host and url.URL.String re-emits
// each of them verbatim, so internal/landingconfig's validator lets
// every value below through. Unescaped, the first of them ends the
// content attribute early and the rest of the tag is reparsed as
// attribute names; the served document has to stay well-formed for
// whatever a deployment configures.
func TestAnAppURLWithMarkupInItIsEscaped(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		appURL string
		want   string
	}{
		"a double quote ends the attribute": {
			appURL: `https://x"`,
			want:   `<!doctype html><meta name="calbridge-app-url" content="https://x&#34;">`,
		},
		"an angle bracket opens a tag": {
			appURL: `https://x"><script>alert(1)</script>`,
			want: `<!doctype html><meta name="calbridge-app-url" ` +
				`content="https://x&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">`,
		},
		"an ampersand starts an entity": {
			appURL: `https://x&y`,
			want:   `<!doctype html><meta name="calbridge-app-url" content="https://x&amp;y">`,
		},
		"an ordinary URL is unchanged": {
			appURL: "https://app.calbridge.example",
			want:   `<!doctype html><meta name="calbridge-app-url" content="https://app.calbridge.example">`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := testDeps(t)
			deps.AppURL = tc.appURL
			handlers, err := landingapi.NewHandler(deps)
			if err != nil {
				t.Fatalf("NewHandler = %v, want no error", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			handlers.Public.ServeHTTP(rec, req)

			if rec.Body.String() != tc.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}
