// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package depsync

import (
	"fmt"
	"os"
	"regexp"
	"testing"
)

const (
	// dockerfilePath is where the web build stage pins pnpm by hand,
	// relative to this package.
	dockerfilePath = "../../Dockerfile"
	// miseTOMLPath is where every tool this repository needs is pinned,
	// pnpm's major included, relative to this package.
	miseTOMLPath = "../../mise.toml"
)

// dockerfilePnpmPin matches the exact pnpm version the web build stage's
// corepack activates: "corepack prepare pnpm@12.19.0 --activate".
var dockerfilePnpmPin = regexp.MustCompile(`corepack prepare pnpm@(\d+)\.(\d+)\.(\d+) --activate`)

// miseTOMLPnpmPin matches mise.toml's own pnpm pin, a floating major such
// as `pnpm = "12"`.
var miseTOMLPnpmPin = regexp.MustCompile(`(?m)^pnpm = "(\d+)"$`)

// dockerfilePnpmMajor extracts the major version off the Dockerfile's
// hand-pinned pnpm version.
func dockerfilePnpmMajor(content string) (string, error) {
	match := dockerfilePnpmPin.FindStringSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("no %q line found", "corepack prepare pnpm@<version> --activate")
	}
	return match[1], nil
}

// miseTOMLPnpmMajor extracts mise.toml's own pnpm major pin.
func miseTOMLPnpmMajor(content string) (string, error) {
	match := miseTOMLPnpmPin.FindStringSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("no %q line found", `pnpm = "<major>"`)
	}
	return match[1], nil
}

// TestDockerfilePnpmMajor covers the line shapes the Dockerfile's pnpm pin
// can take.
func TestDockerfilePnpmMajor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{name: "pinned version", content: "RUN corepack prepare pnpm@12.19.0 --activate\n", want: "12"},
		{name: "different major", content: "RUN corepack prepare pnpm@10.2.1 --activate\n", want: "10"},
		{name: "missing activate flag", content: "RUN corepack prepare pnpm@12.19.0\n", wantErr: true},
		{name: "no pnpm line at all", content: "RUN echo hello\n", wantErr: true},
		{name: "empty", content: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := dockerfilePnpmMajor(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("dockerfilePnpmMajor(%q) = %q, want error", tc.content, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("dockerfilePnpmMajor(%q) returned unexpected error: %v", tc.content, err)
			}
			if got != tc.want {
				t.Fatalf("dockerfilePnpmMajor(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// TestMiseTOMLPnpmMajor covers the line shapes mise.toml's own pnpm pin
// can take.
func TestMiseTOMLPnpmMajor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{name: "pinned major", content: "node = \"26\"\npnpm = \"12\"\ngo = \"1.27.0\"\n", want: "12"},
		{name: "different major", content: "pnpm = \"10\"\n", want: "10"},
		{name: "indented, not a match", content: "  pnpm = \"12\"\n", wantErr: true},
		{name: "no pnpm line at all", content: "go = \"1.27.0\"\n", wantErr: true},
		{name: "empty", content: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := miseTOMLPnpmMajor(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("miseTOMLPnpmMajor(%q) = %q, want error", tc.content, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("miseTOMLPnpmMajor(%q) returned unexpected error: %v", tc.content, err)
			}
			if got != tc.want {
				t.Fatalf("miseTOMLPnpmMajor(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// TestDockerfilePnpmPinMatchesMise fails when the Dockerfile's hand-pinned
// pnpm version and mise.toml's own floating major pin disagree. The
// Dockerfile's own comment claims it builds "with the same toolchain task
// web:build does, not merely the same majors"; that claim silently stops
// being true the moment mise.toml's pin moves to a new major and the
// Dockerfile's hand-written patch is left behind, with nothing before
// this test to notice.
func TestDockerfilePnpmPinMatchesMise(t *testing.T) {
	t.Parallel()

	dockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfileMajor, err := dockerfilePnpmMajor(string(dockerfile))
	if err != nil {
		t.Fatalf("resolve the Dockerfile's pnpm major: %v", err)
	}

	miseTOML, err := os.ReadFile(miseTOMLPath)
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}
	miseMajor, err := miseTOMLPnpmMajor(string(miseTOML))
	if err != nil {
		t.Fatalf("resolve mise.toml's pnpm major: %v", err)
	}

	if dockerfileMajor != miseMajor {
		t.Fatalf(
			"pnpm major mismatch: Dockerfile pins pnpm %s.x, mise.toml pins pnpm %s. "+
				"Pin both files to the same major, or the image no longer builds with the toolchain task web:build does.",
			dockerfileMajor, miseMajor,
		)
	}
}
