// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	// repoRoot is the repository's root, relative to this package.
	repoRoot = "../.."

	// licenseFile is the one file that says which license the repository
	// uses. Every other statement of a license has to agree with it.
	licenseFile = "LICENSE"

	// headerTemplateFile is the header text `task lint:license` hands to
	// addlicense. addlicense reads it only to add a header and to find the
	// files that lack one, so nothing else notices when it names another
	// license than LICENSE does.
	headerTemplateFile = ".license-header.txt"

	// headerScanLines is how far into a file the SPDX line may sit. A Go
	// file puts its build constraint above the header, so the line is not
	// always the first or second one.
	headerScanLines = 15
)

// spdxLine finds the license identifier in a header comment, whatever the
// comment syntax around it is: `//` in Go and TypeScript, `<!-- -->` in a
// Vue file.
var spdxLine = regexp.MustCompile(`SPDX-License-Identifier:[ \t]*([A-Za-z0-9.+-]+)`)

// licenseDirsSkipped holds the directory names the walk does not enter.
// They match the `-prune` list in Taskfile.yaml's lint:license target and
// the "license" job in checks.yaml, so this check and addlicense look at
// the same Go files: another tool's checkout, the dependencies, sqlc's
// generated output, which task generate:sqlc rewrites and no header would
// survive, and the fixtures this check's own test reads.
var licenseDirsSkipped = []string{".claude", "node_modules", "sqlcgen", "testdata"}

// licenseFrontendRoots holds the only places a TypeScript or Vue file is
// covered, the same two roots lint:license names.
var licenseFrontendRoots = []string{"web/src/", "web-landing/src/"}

// knownLicenses maps the title a LICENSE file starts with to the SPDX
// identifier a header names for it. A LICENSE this list does not know
// fails the check instead of passing, because a check that cannot read
// the license it compares against checks nothing.
var knownLicenses = []struct {
	id    string
	title *regexp.Regexp
}{
	{"Elastic-2.0", regexp.MustCompile(`\A\s*Elastic License 2\.0\b`)},
	{"Apache-2.0", regexp.MustCompile(`\A\s*Apache License\s+Version 2\.0\b`)},
	{"MIT", regexp.MustCompile(`\A\s*MIT License\b`)},
}

// licenseID returns the SPDX identifier of a LICENSE file's text.
func licenseID(text []byte) (string, error) {
	for _, known := range knownLicenses {
		if known.title.Match(text) {
			return known.id, nil
		}
	}

	return "", errors.New("the text starts with no title this check knows, add the license to knownLicenses")
}

// headerID returns the SPDX identifier a file's header names, or an empty
// string when none of its first lines carries one.
func headerID(content []byte) string {
	lines := strings.SplitN(string(content), "\n", headerScanLines+1)
	lines = lines[:min(len(lines), headerScanLines)]

	for _, line := range lines {
		if match := spdxLine.FindStringSubmatch(line); match != nil {
			return match[1]
		}
	}

	return ""
}

// headerProblem describes how one file's header disagrees with the
// license, or returns an empty string when it agrees.
func headerProblem(name string, content []byte, want string) string {
	switch got := headerID(content); got {
	case want:
		return ""
	case "":
		return fmt.Sprintf("%s: no SPDX-License-Identifier line in its first %d lines", name, headerScanLines)
	default:
		return fmt.Sprintf("%s: names %s, %s is %s", name, got, licenseFile, want)
	}
}

// coveredByLicenseCheck reports whether a header is expected in the file
// at name, a slash separated path from the repository root.
func coveredByLicenseCheck(name string) bool {
	switch path.Ext(name) {
	case ".go":
		return true
	case ".ts", ".vue":
		return slices.ContainsFunc(licenseFrontendRoots, func(root string) bool {
			return strings.HasPrefix(name, root)
		})
	default:
		return false
	}
}

// checkLicenseHeaders compares the license every covered source file and
// the header template name against the one LICENSE holds. It returns one
// line per file that disagrees, in path order.
func checkLicenseHeaders(root fs.FS) ([]string, error) {
	licenseText, err := fs.ReadFile(root, licenseFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", licenseFile, err)
	}

	want, err := licenseID(licenseText)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", licenseFile, err)
	}

	template, err := fs.ReadFile(root, headerTemplateFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", headerTemplateFile, err)
	}

	var problems []string
	if problem := headerProblem(headerTemplateFile, template, want); problem != "" {
		problems = append(problems, problem)
	}

	checked := 0

	err = fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			if name != "." && slices.Contains(licenseDirsSkipped, entry.Name()) {
				return fs.SkipDir
			}

			return nil
		}

		if !coveredByLicenseCheck(name) {
			return nil
		}

		content, readErr := fs.ReadFile(root, name)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", name, readErr)
		}

		checked++

		if problem := headerProblem(name, content, want); problem != "" {
			problems = append(problems, problem)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk the source files: %w", err)
	}

	if checked == 0 {
		return nil, errors.New("no source file is covered, so the walk checks nothing")
	}

	return problems, nil
}

// TestSourceHeadersNameTheRepositoryLicense is the half of the license
// check addlicense cannot do. `addlicense -check` answers only whether a
// header is present: a file carrying an Apache-2.0 identifier passes
// against the Elastic-2.0 header file. This fails when a covered file,
// or the header template, names a license other than the one LICENSE
// holds, which is what a history rewrite from one license to another
// leaves behind in the files it missed.
//
// The other half, a header that is missing altogether, is `task
// lint:license`. Each half passes where the other fails, so neither
// replaces the other.
func TestSourceHeadersNameTheRepositoryLicense(t *testing.T) {
	t.Parallel()

	problems, err := checkLicenseHeaders(os.DirFS(repoRoot))
	if err != nil {
		t.Fatalf("check the license headers: %v", err)
	}

	for _, problem := range problems {
		t.Error(problem)
	}
}

// TestLicenseHeaderCheckFindsADisagreement proves the check above fails
// in both directions: when a file names the wrong license, and when
// LICENSE itself is the file that changed. Each case is a small
// repository under testdata/license.
func TestLicenseHeaderCheckFindsADisagreement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dir         string
		wantProblem []string
		wantErr     string
	}{
		{
			name: "accepts a repository whose headers all name its license",
			dir:  "matching",
		},
		{
			name: "rejects a Go file that names another license",
			dir:  "wrong-header-go",
			wantProblem: []string{
				"cmd/service/main.go: names Apache-2.0, LICENSE is Elastic-2.0",
			},
		},
		{
			name: "rejects a Vue file that names another license",
			dir:  "wrong-header-frontend",
			wantProblem: []string{
				"web/src/App.vue: names Apache-2.0, LICENSE is Elastic-2.0",
			},
		},
		{
			name: "rejects a header that carries a copyright line and no identifier",
			dir:  "missing-identifier",
			wantProblem: []string{
				"cmd/service/main.go: no SPDX-License-Identifier line in its first 15 lines",
			},
		},
		{
			name: "rejects every header when LICENSE is swapped for another license",
			dir:  "swapped-license",
			wantProblem: []string{
				".license-header.txt: names Elastic-2.0, LICENSE is Apache-2.0",
				"cmd/service/main.go: names Elastic-2.0, LICENSE is Apache-2.0",
				"web/src/App.vue: names Elastic-2.0, LICENSE is Apache-2.0",
			},
		},
		{
			name: "rejects a header template that names another license than LICENSE",
			dir:  "stale-header-template",
			wantProblem: []string{
				".license-header.txt: names Apache-2.0, LICENSE is Elastic-2.0",
			},
		},
		{
			name:    "fails on a LICENSE it cannot identify",
			dir:     "unknown-license",
			wantErr: "LICENSE: the text starts with no title this check knows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			problems, err := checkLicenseHeaders(os.DirFS("testdata/license/" + tt.dir))

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("check the license headers: %v", err)
			}

			if !slices.Equal(problems, tt.wantProblem) {
				t.Errorf("problems = %q, want %q", problems, tt.wantProblem)
			}
		})
	}
}
