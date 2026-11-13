// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider_test

import (
	"errors"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/provider"
)

// stored is a valid event as a connector hands it back, which every
// change case below starts from.
func stored() provider.Event {
	e := sprintReview()
	e.ID = "evt-1"

	return e
}

func TestSyncTokenIsZero(t *testing.T) {
	t.Parallel()

	if !(provider.SyncToken{}).IsZero() {
		t.Error("SyncToken{}.IsZero() = false, want true, so no initial sync would ever start")
	}
	if (provider.SyncToken{Value: "log:4"}).IsZero() {
		t.Error("a token with a value reports itself as zero")
	}
}

func TestPageState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		state        provider.PageState
		want         string
		wantMore     bool
		wantStorable bool
	}{
		{name: "unset", state: provider.PageStateUnset, want: "unset"},
		{
			name:         "complete",
			state:        provider.PageStateComplete,
			want:         "complete",
			wantStorable: true,
		},
		{
			name:         "resumable",
			state:        provider.PageStateResumable,
			want:         "resumable",
			wantMore:     true,
			wantStorable: true,
		},
		{
			name:     "continued",
			state:    provider.PageStateContinued,
			want:     "continued",
			wantMore: true,
		},
		{name: "outside the four", state: provider.PageState(9), want: "PageState(9)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.state.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			if got := tc.state.More(); got != tc.wantMore {
				t.Errorf("More() = %v, want %v", got, tc.wantMore)
			}
			if got := tc.state.Storable(); got != tc.wantStorable {
				t.Errorf("Storable() = %v, want %v", got, tc.wantStorable)
			}
		})
	}
}

func TestChangeKindString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kind provider.ChangeKind
		want string
	}{
		{name: "unset", kind: provider.ChangeKindUnset, want: "unset"},
		{name: "upsert", kind: provider.ChangeKindUpsert, want: "upsert"},
		{name: "delete", kind: provider.ChangeKindDelete, want: "delete"},
		{name: "outside the three", kind: provider.ChangeKind(9), want: "ChangeKind(9)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.kind.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChangeValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change provider.Change
		valid  bool
	}{
		{
			name:   "an upsert carrying its event",
			change: provider.Change{Kind: provider.ChangeKindUpsert, EventID: "evt-1", Event: stored()},
			valid:  true,
		},
		{
			name:   "a delete carrying only the id",
			change: provider.Change{Kind: provider.ChangeKindDelete, EventID: "evt-1"},
			valid:  true,
		},
		{
			name:   "a change with no event id",
			change: provider.Change{Kind: provider.ChangeKindDelete},
			valid:  false,
		},
		{
			name:   "a change with no kind",
			change: provider.Change{EventID: "evt-1"},
			valid:  false,
		},
		{
			name:   "a kind outside the three",
			change: provider.Change{Kind: provider.ChangeKind(9), EventID: "evt-1"},
			valid:  false,
		},
		{
			name:   "an upsert whose event is a different one",
			change: provider.Change{Kind: provider.ChangeKindUpsert, EventID: "evt-2", Event: stored()},
			valid:  false,
		},
		{
			name: "an upsert carrying an event that breaks the rules",
			change: func() provider.Change {
				broken := stored()
				broken.End = provider.EventTime{}

				return provider.Change{Kind: provider.ChangeKindUpsert, EventID: broken.ID, Event: broken}
			}(),
			valid: false,
		},
		{
			name:   "a delete carrying an event after all",
			change: provider.Change{Kind: provider.ChangeKindDelete, EventID: "evt-1", Event: stored()},
			valid:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.change.Validate()
			if tc.valid {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}

				return
			}
			if !errors.Is(err, provider.ErrInvalidChange) && !errors.Is(err, provider.ErrInvalidEvent) {
				t.Errorf("Validate() error = %v, want ErrInvalidChange or ErrInvalidEvent", err)
			}
		})
	}
}

func TestChangePageValidate(t *testing.T) {
	t.Parallel()

	upsert := provider.Change{Kind: provider.ChangeKindUpsert, EventID: "evt-1", Event: stored()}

	cases := []struct {
		name  string
		page  provider.ChangePage
		valid bool
	}{
		{
			name:  "a complete page with no changes",
			page:  provider.ChangePage{State: provider.PageStateComplete},
			valid: true,
		},
		{
			name: "a complete page carrying a position",
			page: provider.ChangePage{
				Changes: []provider.Change{upsert},
				Token:   provider.SyncToken{Value: "log:1"},
				State:   provider.PageStateComplete,
			},
			valid: true,
		},
		{
			name: "a resumable page carrying its token",
			page: provider.ChangePage{
				Changes: []provider.Change{upsert},
				Token:   provider.SyncToken{Value: "log:1"},
				State:   provider.PageStateResumable,
			},
			valid: true,
		},
		{
			name:  "a page the connector never set a state on",
			page:  provider.ChangePage{Changes: []provider.Change{upsert}},
			valid: false,
		},
		{
			name:  "a state outside the four",
			page:  provider.ChangePage{State: provider.PageState(9)},
			valid: false,
		},
		{
			name: "a page with more to come and no token to continue from",
			page: provider.ChangePage{
				Changes: []provider.Change{upsert},
				State:   provider.PageStateContinued,
			},
			valid: false,
		},
		{
			name: "a page carrying a change that breaks the rules",
			page: provider.ChangePage{
				Changes: []provider.Change{{Kind: provider.ChangeKindUpsert}},
				State:   provider.PageStateComplete,
			},
			valid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.page.Validate()
			if tc.valid {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}

				return
			}
			if !errors.Is(err, provider.ErrInvalidChange) && !errors.Is(err, provider.ErrInvalidEvent) {
				t.Errorf("Validate() error = %v, want ErrInvalidChange or ErrInvalidEvent", err)
			}
		})
	}
}

func TestEnumStrings(t *testing.T) {
	t.Parallel()

	t.Run("a time kind", func(t *testing.T) {
		t.Parallel()

		cases := map[provider.TimeKind]string{
			provider.TimeKindUnset:    "unset",
			provider.TimeKindDate:     "date",
			provider.TimeKindZoned:    "zoned",
			provider.TimeKindFloating: "floating",
			provider.TimeKind(9):      "TimeKind(9)",
		}
		for kind, want := range cases {
			if got := kind.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		}
	})

	t.Run("a status", func(t *testing.T) {
		t.Parallel()

		cases := map[provider.Status]string{
			provider.StatusConfirmed: "confirmed",
			provider.StatusTentative: "tentative",
			provider.StatusCancelled: "cancelled",
			provider.Status(9):       "Status(9)",
		}
		for status, want := range cases {
			if got := status.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
			if want := status <= provider.StatusCancelled; status.Valid() != want {
				t.Errorf("Valid() = %v, want %v", status.Valid(), want)
			}
		}
	})

	t.Run("a transparency", func(t *testing.T) {
		t.Parallel()

		cases := map[provider.Transparency]string{
			provider.TransparencyOpaque:      "opaque",
			provider.TransparencyTransparent: "transparent",
			provider.Transparency(9):         "Transparency(9)",
		}
		for transparency, want := range cases {
			if got := transparency.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		}
	})

	t.Run("a visibility", func(t *testing.T) {
		t.Parallel()

		cases := map[provider.Visibility]string{
			provider.VisibilityDefault:      "default",
			provider.VisibilityPublic:       "public",
			provider.VisibilityPrivate:      "private",
			provider.VisibilityConfidential: "confidential",
			provider.Visibility(9):          "Visibility(9)",
		}
		for visibility, want := range cases {
			if got := visibility.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		}
	})

	t.Run("an attendee response", func(t *testing.T) {
		t.Parallel()

		cases := map[provider.Response]string{
			provider.ResponseNeedsAction: "needs-action",
			provider.ResponseAccepted:    "accepted",
			provider.ResponseDeclined:    "declined",
			provider.ResponseTentative:   "tentative",
			provider.Response(9):         "Response(9)",
		}
		for response, want := range cases {
			if got := response.String(); got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		}
	})
}
