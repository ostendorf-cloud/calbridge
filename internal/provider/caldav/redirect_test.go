// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package caldav

import (
	"net/http"
	"testing"
)

func TestNew_DoesNotLetTheHTTPClientFollowARedirect(t *testing.T) {
	t.Parallel()

	// net/http decides whether to carry the Authorization header onto a
	// redirect by comparing hosts and not schemes, so a same-host
	// https to http redirect would put the app specific password on the
	// wire in clear. The client is therefore told to hand the redirect
	// back, and do follows it under the profile's own rules.
	client, ok := New().client.(*http.Client)
	if !ok {
		t.Fatalf("the default client is a %T", New().client)
	}
	if client.CheckRedirect == nil {
		t.Fatal("the default client follows a redirect with no profile check")
	}

	err := client.CheckRedirect(&http.Request{}, nil)
	if err != http.ErrUseLastResponse { //nolint:errorlint // the http package compares this sentinel by identity
		t.Fatalf("got %v, want http.ErrUseLastResponse so the redirect comes back unfollowed", err)
	}
}
