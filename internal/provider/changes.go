// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package provider

import (
	"fmt"
	"reflect"
)

// SyncToken is a provider's opaque position in one calendar's change
// stream. Only the connector that issued it reads it, and the engine
// stores it as the bytes it got.
//
// The zero SyncToken means no position: passing it to Connector.Changes
// asks for an initial sync.
type SyncToken struct {
	// Value is the position, exactly as the provider wrote it.
	Value string
}

// IsZero reports whether the token holds no position, which asks for an
// initial sync.
func (t SyncToken) IsZero() bool { return t.Value == "" }

// PageState says what a ChangePage's token is good for, which is the one
// thing the providers genuinely disagree on.
//
// Some providers hand out a position after every page, so a run cut short
// resumes where it stopped. Others hand out a continuation that is only
// valid inside the current run, and a position only once the run reaches
// its end. The engine has to know which it holds before it stores
// anything.
type PageState uint8

const (
	// PageStateUnset is the zero value and never valid. A connector that
	// forgot to set the state would otherwise be read as having finished,
	// and the changes it had not handed over yet would be lost.
	PageStateUnset PageState = iota

	// PageStateComplete means the provider has handed over everything it
	// had. ChangePage.Token is the position for the next run, and the
	// caller stores it.
	PageStateComplete

	// PageStateResumable means the provider has more to hand over and
	// ChangePage.Token is a real position for the changes delivered so
	// far. The caller stores it and calls Changes again with it. A run
	// cut short between pages, by a truncated response or by a restart,
	// resumes from that token and costs no initial sync.
	PageStateResumable

	// PageStateContinued means the provider has more to hand over and
	// ChangePage.Token only continues the current run. The caller calls
	// Changes again with it, and must not store it as a position: the run
	// has to reach a PageStateComplete page before there is anything
	// worth keeping. A run cut short before that costs an initial sync.
	PageStateContinued
)

// String returns the state's name.
func (s PageState) String() string {
	switch s {
	case PageStateUnset:
		return "unset"
	case PageStateComplete:
		return "complete"
	case PageStateResumable:
		return "resumable"
	case PageStateContinued:
		return "continued"
	default:
		return fmt.Sprintf("PageState(%d)", uint8(s))
	}
}

// More reports whether the caller calls Connector.Changes again with the
// page's token.
func (s PageState) More() bool {
	return s == PageStateResumable || s == PageStateContinued
}

// Storable reports whether the page's token may be stored as the
// calendar's position. A continuation token is not, because it is only
// meaningful inside the run that produced it.
func (s PageState) Storable() bool {
	return s == PageStateComplete || s == PageStateResumable
}

// ChangeKind says what happened to an event. No provider separates a
// creation from an update in a change stream, so neither does this: a
// caller that holds no mapping for the event creates one, and otherwise
// updates it.
type ChangeKind uint8

const (
	// ChangeKindUnset is the zero value and never valid.
	ChangeKindUnset ChangeKind = iota
	// ChangeKindUpsert means the event exists in its reported state.
	ChangeKindUpsert
	// ChangeKindDelete means the event is gone from the calendar, whether
	// it was deleted or cancelled. Change.Event is the zero Event, so
	// Change.EventID is all the caller gets.
	ChangeKindDelete
)

// String returns the kind's name.
func (k ChangeKind) String() string {
	switch k {
	case ChangeKindUnset:
		return "unset"
	case ChangeKindUpsert:
		return "upsert"
	case ChangeKindDelete:
		return "delete"
	default:
		return fmt.Sprintf("ChangeKind(%d)", uint8(k))
	}
}

// Change is one event's change, as a connector read it.
type Change struct {
	// Kind says what happened.
	Kind ChangeKind
	// EventID names the event in its calendar, and is set for both kinds.
	EventID string
	// Event is the event's state for ChangeKindUpsert, and the zero Event
	// for ChangeKindDelete.
	Event Event
}

// Validate reports whether the change is one a caller can act on.
func (c Change) Validate() error {
	if c.EventID == "" {
		return fmt.Errorf("change carries no event id: %w", ErrInvalidChange)
	}
	switch c.Kind {
	case ChangeKindUpsert:
		if err := c.Event.Validate(); err != nil {
			return fmt.Errorf("change %q: %w", c.EventID, err)
		}
		if c.Event.ID != c.EventID {
			return fmt.Errorf("change %q carries event %q: %w", c.EventID, c.Event.ID, ErrInvalidChange)
		}
	case ChangeKindDelete:
		if !reflect.DeepEqual(c.Event, Event{}) {
			return fmt.Errorf("delete of %q carries an event: %w", c.EventID, ErrInvalidChange)
		}
	case ChangeKindUnset:
		return fmt.Errorf("change %q has no kind: %w", c.EventID, ErrInvalidChange)
	default:
		return fmt.Errorf("change %q has kind %d: %w", c.EventID, uint8(c.Kind), ErrInvalidChange)
	}

	return nil
}

// ChangePage is one answer from Connector.Changes: the changes the
// provider handed over in this round trip, the token that goes with them,
// and what the caller does next.
//
// A page is applied in the order its changes arrive. An initial sync, the
// one started with the zero SyncToken, reports every event in the
// calendar and no deletion at all.
type ChangePage struct {
	// Changes are the events that changed, in the order the provider
	// returned them.
	Changes []Change
	// Token is the position or the continuation, depending on State.
	Token SyncToken
	// State says whether to call again and whether Token may be stored.
	State PageState
}

// Validate reports whether the page is one a caller can act on.
func (p ChangePage) Validate() error {
	if p.State == PageStateUnset {
		return fmt.Errorf("change page has no state: %w", ErrInvalidChange)
	}
	if p.State > PageStateContinued {
		return fmt.Errorf("change page has state %d: %w", uint8(p.State), ErrInvalidChange)
	}
	if p.State.More() && p.Token.IsZero() {
		return fmt.Errorf("change page is %s and carries no token: %w", p.State, ErrInvalidChange)
	}
	for i, c := range p.Changes {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("change %d: %w", i, err)
		}
	}

	return nil
}
