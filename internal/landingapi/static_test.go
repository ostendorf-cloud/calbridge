// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingapi_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/calbridge/internal/landingapi"
)

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
