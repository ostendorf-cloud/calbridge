// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/storage"
	"github.com/ostendorf-cloud/calbridge/internal/storage/postgres/sqlcgen"
)

// TestASharedCalendarCarriesNoProviderIdentifier holds down the one
// column calendars_grantee_read admits and nothing is meant to read.
//
// The policy is row level, so it admits every column of a shared
// calendar's row, external_id included. For a Google or Microsoft
// primary calendar that value is the owner's provider mailbox address,
// which need not be the calbridge account address the grant already
// tells the grantee. Two things keep it in the table: the projection in
// queries/calendars.sql, and storage.SharedCalendar having no field to
// put it in. Either one alone would be undone by a later edit to the
// other, so both are checked here, against the generated row type
// rather than against the SQL text, so that regenerating sqlc after
// widening the projection is what fails.
func TestASharedCalendarCarriesNoProviderIdentifier(t *testing.T) {
	t.Parallel()

	for _, subject := range []struct {
		name  string
		value any
	}{
		{"the generated row of ListCalendarsSharedWithMe", sqlcgen.ListCalendarsSharedWithMeRow{}},
		{"storage.SharedCalendar", storage.SharedCalendar{}},
	} {
		t.Run(subject.name, func(t *testing.T) {
			t.Parallel()

			for _, field := range fieldNames(reflect.TypeOf(subject.value)) {
				if strings.Contains(strings.ToLower(field), "externalid") {
					t.Errorf("%s carries %s; a grantee reads the owner's provider identifier the "+
						"moment there is somewhere to put it, and the policy admits the column",
						subject.name, field)
				}
			}
		})
	}
}

// TestFieldNamesReachesAnEmbeddedStruct covers the walk the assertion
// above depends on: a field reached through an embedded struct has to
// count, because embedding Calendar is exactly how ExternalID would come
// back.
func TestFieldNamesReachesAnEmbeddedStruct(t *testing.T) {
	t.Parallel()

	type inner struct {
		Hidden string
	}

	type outer struct {
		inner

		Named string
	}

	got := fieldNames(reflect.TypeOf(outer{}))

	want := []string{"Hidden", "Named"}
	if len(got) != len(want) {
		t.Fatalf("fieldNames() = %q, want %q", got, want)
	}

	for i, name := range want {
		if got[i] != name {
			t.Errorf("fieldNames()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// fieldNames lists every exported field of a struct type, descending
// into an embedded struct so a field a type inherits counts as one it
// carries.
func fieldNames(structType reflect.Type) []string {
	var names []string

	for i := range structType.NumField() {
		field := structType.Field(i)

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			names = append(names, fieldNames(field.Type)...)

			continue
		}

		names = append(names, field.Name)
	}

	return names
}
