// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestPtrToText(t *testing.T) {
	t.Parallel()

	value := "hunter2"

	tests := []struct {
		name  string
		input *string
		want  pgtype.Text
	}{
		{name: "nil pointer", input: nil, want: pgtype.Text{}},
		{name: "empty string", input: ptr(""), want: pgtype.Text{String: "", Valid: true}},
		{name: "set value", input: &value, want: pgtype.Text{String: value, Valid: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ptrToText(tt.input)
			if got != tt.want {
				t.Errorf("ptrToText(%v) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTextToPtr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input pgtype.Text
		want  *string
	}{
		{name: "not valid", input: pgtype.Text{String: "ignored", Valid: false}, want: nil},
		{name: "empty string", input: pgtype.Text{String: "", Valid: true}, want: ptr("")},
		{name: "set value", input: pgtype.Text{String: "hunter2", Valid: true}, want: ptr("hunter2")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := textToPtr(tt.input)
			assertPtrStringEqual(t, got, tt.want)
		})
	}
}

func TestPtrToInt8(t *testing.T) {
	t.Parallel()

	value := int64(42)

	tests := []struct {
		name  string
		input *int64
		want  pgtype.Int8
	}{
		{name: "nil pointer", input: nil, want: pgtype.Int8{}},
		{name: "zero value", input: ptrInt64(0), want: pgtype.Int8{Int64: 0, Valid: true}},
		{name: "set value", input: &value, want: pgtype.Int8{Int64: value, Valid: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ptrToInt8(tt.input)
			if got != tt.want {
				t.Errorf("ptrToInt8(%v) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestInt8ToPtr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input pgtype.Int8
		want  *int64
	}{
		{name: "not valid", input: pgtype.Int8{Int64: 7, Valid: false}, want: nil},
		{name: "zero value", input: pgtype.Int8{Int64: 0, Valid: true}, want: ptrInt64(0)},
		{name: "set value", input: pgtype.Int8{Int64: 42, Valid: true}, want: ptrInt64(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := int8ToPtr(tt.input)
			assertPtrInt64Equal(t, got, tt.want)
		})
	}
}

func TestTimeFromTimestamptzAndToTimestamptz(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if got := timeFromTimestamptz(toTimestamptz(when)); !got.Equal(when) {
		t.Errorf("timeFromTimestamptz(toTimestamptz(%v)) = %v, want %v", when, got, when)
	}
}

func ptr(s string) *string {
	return &s
}

func ptrInt64(i int64) *int64 {
	return &i
}

func assertPtrStringEqual(t *testing.T, got, want *string) {
	t.Helper()

	switch {
	case got == nil && want == nil:
		return
	case got == nil || want == nil:
		t.Errorf("got %v, want %v", got, want)
	case *got != *want:
		t.Errorf("got %q, want %q", *got, *want)
	}
}

func assertPtrInt64Equal(t *testing.T, got, want *int64) {
	t.Helper()

	switch {
	case got == nil && want == nil:
		return
	case got == nil || want == nil:
		t.Errorf("got %v, want %v", got, want)
	case *got != *want:
		t.Errorf("got %d, want %d", *got, *want)
	}
}
