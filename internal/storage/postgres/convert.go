// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package postgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// textToPtr and ptrToText convert between a nullable Postgres text column
// and the *string a domain struct such as storage.User uses for the same
// nullability (nil until internal/auth sets a value).

func textToPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}

	v := t.String

	return &v
}

func ptrToText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}

	return pgtype.Text{String: *s, Valid: true}
}

// int8ToPtr and ptrToInt8 convert between a nullable Postgres bigint
// column (users.totp_last_counter) and an *int64.

func int8ToPtr(i pgtype.Int8) *int64 {
	if !i.Valid {
		return nil
	}

	v := i.Int64

	return &v
}

func ptrToInt8(i *int64) pgtype.Int8 {
	if i == nil {
		return pgtype.Int8{}
	}

	return pgtype.Int8{Int64: *i, Valid: true}
}

// timeFromTimestamptz and toTimestamptz convert a NOT NULL timestamptz
// column (every timestamp column in this schema; none is nullable) to
// and from a plain time.Time.

func timeFromTimestamptz(t pgtype.Timestamptz) time.Time {
	return t.Time
}

func toTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
