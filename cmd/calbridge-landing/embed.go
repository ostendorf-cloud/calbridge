// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package main

import "embed"

// dist is the built static site, written by "task landing:build" (vite
// build, with outDir pointed at this directory). embed resolves
// relative to this file, which is why web-landing/vite.config.ts's
// outDir is "../cmd/calbridge-landing/dist" rather than a path inside
// web-landing itself; see that file's own comment.
//
//go:embed dist
var distFS embed.FS
