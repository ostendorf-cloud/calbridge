// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package provider

// Calendar is one calendar a credential reaches, in the normalised form.
// It carries what an edge needs to pick a source or a target, and nothing
// the provider happens to add on top.
type Calendar struct {
	// ID is the provider's own id for the calendar, opaque outside the
	// connector, and the value every other method takes as calendarID.
	ID string
	// Name is the display name the user recognises.
	Name string
	// Description is the calendar's own description, empty where the
	// provider has none.
	Description string
	// TimeZone is the calendar's default IANA zone, used to anchor a
	// floating time written into a provider that has no floating time. It
	// is empty where the provider does not give the calendar one.
	TimeZone string
	// ReadOnly reports that the credential may read the calendar but not
	// write it, so it can be an edge's source and never its target.
	ReadOnly bool
	// Primary reports the account's main calendar, which is the one a
	// user means when they name no calendar.
	Primary bool
	// Managed reports that calbridge created the calendar and owns it, so
	// deleting the edge that fills it may remove it entirely.
	Managed bool
}
