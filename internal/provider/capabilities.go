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

	// InstanceWrites reports that the connector writes and deletes one
	// overridden occurrence on its own, as an Event whose Recurrence names
	// a MasterID. A connector whose provider stores its overrides inside
	// the master's own object reports false, and a caller then changes the
	// master instead. See the "Overridden occurrences" section of the
	// package documentation for what false costs.
	InstanceWrites bool

	// NormalisesContent reports that the provider does not store an event
	// exactly as it was written: the connector rewrites some of its
	// content on the way in, on the way out, or both. A text value whose
	// line breaks are rewritten to one form, a markup body reduced to
	// plain text and a zone catalogue translated in both directions are
	// all this.
	//
	// A caller that compares what it wrote against what a later read hands
	// back has to know: without this, the difference reads as somebody
	// editing the copy by hand, and the caller rewrites the event on every
	// pass forever while the sync looks like it is working.
	//
	// A connector reporting true implements ContentNormaliser, so a caller
	// can ask what a write turns into before it compares. NormaliserOf
	// returns that implementation and refuses a connector that declares
	// the capability without it.
	NormalisesContent bool
}
