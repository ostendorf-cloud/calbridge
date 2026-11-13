// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider

// Capabilities is what a provider can do, declared by its connector and
// constant for the connector's lifetime. The engine reads it to pick a
// strategy, so a provider's gap is known at registration instead of
// surfacing as a failed call halfway through a sync.
//
// Every field is false by default, which is the safe reading: a
// capability nobody declared is one the engine does not use.
type Capabilities struct {
	// PushNotifications reports that the provider delivers a change
	// notification to a webhook, so a change reaches its targets within a
	// minute. A provider that reports false is read on the reconcile
	// interval instead, which is the only way to notice a change there.
	PushNotifications bool

	// NativeRecurrence reports that the provider stores and returns RFC
	// 5545 recurrence rules, so a repeating event is copied as one master
	// with its rule. Where either side of an edge reports false, the
	// engine expands the series into concrete instances instead, which
	// costs one event per occurrence but survives the disagreement.
	NativeRecurrence bool

	// CalendarCreation reports that the connector can create a calendar
	// calbridge owns. An edge whose target provider reports false writes
	// into a calendar the user picked, because there is nothing to
	// create.
	CalendarCreation bool

	// Attendees reports that the provider stores attendees on an event
	// the connector writes. A connector that reports false drops the
	// attendee list on write instead of failing, so an edge copying a
	// meeting still copies its time.
	Attendees bool

	// FloatingTimes reports that the provider stores a wall time with no
	// zone attached. A connector whose provider always attaches a zone
	// reports false, and the engine anchors a floating source time in the
	// target calendar's zone before writing, rather than letting the
	// connector guess one.
	FloatingTimes bool
}
