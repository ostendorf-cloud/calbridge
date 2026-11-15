// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

//go:build builtfrontend

package depsync

import (
	"os/exec"
	"testing"
)

// The two tests in this file ask the toolchain what
// cmd/calbridge-landing imports, which means the toolchain has to load
// that package, which embeds cmd/calbridge-landing/dist. An absent
// dist is a load error, not an empty list, so these tests need the
// landing frontend built even though they read no asset of it. That
// precondition is why they sit behind the builtfrontend tag and run
// from "task test:builtfrontend", which builds both frontends first,
// the way test:integration sits outside check's cheap jobs because it
// needs a Postgres.
//
// landingimports_test.go holds the filter and its unit tests, which
// need nothing built and run in the ordinary suite.
const (
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

// landingDependenciesOf runs "go list -deps" over the landing command
// and returns this module's own packages in its output. A failure is
// fatal and names the target to run, because the most likely cause by
// far is the missing dist directory the file comment above describes.
func landingDependenciesOf(t *testing.T) []string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", landingCommand)
	cmd.Dir = repositoryRoot

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf(
			"go list -deps %s: %v. The landing command embeds cmd/calbridge-landing/dist, so loading it "+
				"needs that build: run \"task test:builtfrontend\", which builds web/ and web-landing/ first.",
			landingCommand, err,
		)
	}

	return calbridgeDependencies(string(output))
}

// TestTheLandingBinaryReachesNoApplicationPackage fails the moment
// cmd/calbridge-landing's transitive imports name a calbridge package
// outside landingPackages. An import of internal/httpapi or
// internal/storage puts the application's session handling, its store
// and its encryption into the public site's binary, which is what this
// split exists to prevent.
func TestTheLandingBinaryReachesNoApplicationPackage(t *testing.T) {
	t.Parallel()

	for _, pkg := range landingDependenciesOf(t) {
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

	reached := make(map[string]bool)
	for _, pkg := range landingDependenciesOf(t) {
		reached[pkg] = true
	}

	for pkg := range landingPackages {
		if !reached[pkg] {
			t.Errorf("the landing binary no longer imports %s, so this file's own guard checks nothing", pkg)
		}
	}
}
