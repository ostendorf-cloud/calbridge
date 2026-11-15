// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// The landing server is a separate binary so that the public site's own
// text can be corrected without rebuilding or redeploying the
// application, and so that the host serving that text holds no database
// pool, no session key and no encryption key. AGENTS.md states that as a
// property of the build, and the privacy policy's own text rests on it.
//
// Nothing in the Go toolchain enforces it. Dockerfile.landing copies
// only cmd/calbridge-landing, internal/landingapi and
// internal/landingconfig into its build stage, so an import of
// internal/httpapi or internal/storage does break the image build, but
// that is a side effect of a COPY list rather than a check, it reports
// as a compile error inside a container build, and it says nothing at
// all on a machine with no docker. The two tests below are the check.
const (
	// modulePath is this module's import path prefix, which is how a
	// calbridge package is told apart from a dependency in go list's
	// output.
	modulePath = "github.com/ostendorf-cloud/calbridge"
	// landingCommand is the landing server's own main package.
	landingCommand = "./cmd/calbridge-landing"
	// repositoryRoot is where a go list invocation has to run, relative
	// to this package.
	repositoryRoot = "../.."
)

// landingPackages is every package of this module the landing binary is
// allowed to reach. The landing server is the public site and nothing
// else: its configuration (internal/landingconfig) and its handler
// (internal/landingapi), neither of which knows a database, a session
// or a sign-in provider.
var landingPackages = map[string]bool{
	modulePath + "/cmd/calbridge-landing":  true,
	modulePath + "/internal/landingapi":    true,
	modulePath + "/internal/landingconfig": true,
}

// calbridgeDependencies picks this module's own packages out of the
// output of "go list -deps", one import path per line, discarding every
// package from the standard library or a dependency.
func calbridgeDependencies(goListOutput string) []string {
	var own []string

	for line := range strings.SplitSeq(goListOutput, "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == modulePath || strings.HasPrefix(pkg, modulePath+"/") {
			own = append(own, pkg)
		}
	}

	return own
}

// TestCalbridgeDependencies covers the shapes go list's output can take.
func TestCalbridgeDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		output string
		want   []string
	}{
		{
			name:   "a dependency and one own package",
			output: "errors\ngithub.com/google/uuid\n" + modulePath + "/internal/landingapi\n",
			want:   []string{modulePath + "/internal/landingapi"},
		},
		{
			name:   "a prefix that only looks like this module",
			output: modulePath + "-extra/internal/httpapi\n",
			want:   nil,
		},
		{
			name:   "the module itself",
			output: modulePath + "\n",
			want:   []string{modulePath},
		},
		{
			name:   "blank lines are not packages",
			output: "\n\n",
			want:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := calbridgeDependencies(tc.output)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("calbridgeDependencies() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTheLandingBinaryReachesNoApplicationPackage fails the moment
// cmd/calbridge-landing's transitive imports name a calbridge package
// outside landingPackages. An import of internal/httpapi or
// internal/storage puts the application's session handling, its store
// and its encryption into the public site's binary, which is what this
// split exists to prevent.
func TestTheLandingBinaryReachesNoApplicationPackage(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", landingCommand)
	cmd.Dir = repositoryRoot

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", landingCommand, err)
	}

	for _, pkg := range calbridgeDependencies(string(output)) {
		if !landingPackages[pkg] {
			t.Errorf(
				"the landing binary imports %s. The public site's binary carries %s and %s only, "+
					"so no database pool, session key or encryption key reaches the host serving it; "+
					"move what is needed into one of those two packages instead.",
				pkg, modulePath+"/internal/landingapi", modulePath+"/internal/landingconfig",
			)
		}
	}
}

// TestTheLandingBinaryStillReachesItsOwnPackages is the other half: a
// list the binary no longer reaches at all would make the test above
// pass for the wrong reason, a refactor having moved the handler
// somewhere else without anybody noticing the guard went quiet.
func TestTheLandingBinaryStillReachesItsOwnPackages(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", landingCommand)
	cmd.Dir = repositoryRoot

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", landingCommand, err)
	}

	reached := make(map[string]bool)
	for _, pkg := range calbridgeDependencies(string(output)) {
		reached[pkg] = true
	}

	for pkg := range landingPackages {
		if !reached[pkg] {
			t.Errorf("the landing binary no longer imports %s, so this file's own guard checks nothing", pkg)
		}
	}
}
