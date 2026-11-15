// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"fmt"
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
// all on a machine with no docker.
// landingimports_builtfrontend_test.go is the check.
//
// This file holds the filter over go list's output and its unit tests,
// which need nothing built. The two tests that call go list live in
// landingimports_builtfrontend_test.go, behind a build tag, because
// loading cmd/calbridge-landing needs its embedded dist and no job may
// run them without having built it.
// modulePath is this module's import path prefix, which is how a
// calbridge package is told apart from a dependency in go list's output.
const modulePath = "github.com/ostendorf-cloud/calbridge"

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
