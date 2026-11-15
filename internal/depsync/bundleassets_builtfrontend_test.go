// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

//go:build builtfrontend

package depsync

import (
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// The two tests in this file read the built sites, so they need both
// frontends on disk: cmd/calbridge/dist and
// cmd/calbridge-landing/dist, neither of which is committed. That
// precondition is why they sit behind the builtfrontend tag and run
// from their own target, "task test:builtfrontend", which builds both
// first, the way test:integration sits outside check's cheap jobs
// because it needs a Postgres. Without the tag, every job running
// "go test ./internal/depsync/..." straight after a checkout would
// fail here for want of a build it never asked for.
//
// bundleassets_test.go holds the scan itself and its unit tests, which
// need nothing built and run in the ordinary suite.
const (
	// landingDistPath is the landing server's built site, relative to
	// this package.
	landingDistPath = "../../cmd/calbridge-landing/dist"
	// appDistPath is the application's own built SPA. The privacy
	// policy's claim covers both, so both are scanned.
	appDistPath = "../../cmd/calbridge/dist"
)

// scannedExtensions are the files worth reading: the document, the
// stylesheets and the scripts. A font or an image in the bundle is
// already a local file by the time it is there.
var scannedExtensions = []string{".html", ".css", ".js", ".mjs"}

// scannedFiles reads every document, stylesheet and script in a build
// directory, keyed by its path inside that directory.
func scannedFiles(t *testing.T, distPath string) map[string]string {
	t.Helper()

	if _, err := os.Stat(distPath); err != nil {
		t.Fatalf(
			"%s is not readable: %v. These tests read the built sites: run \"task test:builtfrontend\", "+
				"which builds web/ and web-landing/ first.",
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
