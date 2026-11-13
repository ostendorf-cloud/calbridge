// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package sync

// The engine resolves an event's IANA zone to a moment, and the
// container image it runs in carries no zone database of its own. This
// import embeds one as a fallback, used only when the host has none, so a
// zoned event resolves the same way in a scratch image as it does on a
// developer's machine.
import _ "time/tzdata"
