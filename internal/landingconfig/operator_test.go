// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingconfig

import (
	"strings"
	"testing"
)

func TestLoadReadsTheOperator(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(map[string]string{
		"OPERATOR_COUNTRY":             "Deutschland",
		"OPERATOR_PHONE":               "+49 30 1234567",
		"OPERATOR_REPRESENTATIVE":      "Erika Mustermann",
		"OPERATOR_REGISTER":            "Amtsgericht Berlin, HRB 12345",
		"OPERATOR_VAT_ID":              "DE123456789",
		"OPERATOR_CONTENT_RESPONSIBLE": "Erika Mustermann, Musterstrasse 12, 10115 Berlin",
		"OPERATOR_DATA_PROTECTION":     "datenschutz@calbridge.example",
	}))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}

	want := Operator{
		Name:               "Erika Mustermann",
		Street:             "Musterstrasse 12",
		PostalCode:         "10115",
		City:               "Berlin",
		Email:              "kontakt@calbridge.example",
		Country:            "Deutschland",
		Phone:              "+49 30 1234567",
		Representative:     "Erika Mustermann",
		Register:           "Amtsgericht Berlin, HRB 12345",
		VATID:              "DE123456789",
		ContentResponsible: "Erika Mustermann, Musterstrasse 12, 10115 Berlin",
		DataProtection:     "datenschutz@calbridge.example",
	}
	if cfg.Operator != want {
		t.Errorf("Operator = %+v, want %+v", cfg.Operator, want)
	}
}

func TestLoadLeavesTheOptionalOperatorFieldsEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(nil))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}

	op := cfg.Operator
	if op.Phone != "" || op.Representative != "" || op.Register != "" || op.VATID != "" ||
		op.ContentResponsible != "" || op.DataProtection != "" || op.Country != "" {
		t.Errorf("Operator = %+v, want every optional field empty", op)
	}
}

func TestLoadTrimsOperatorWhiteSpace(t *testing.T) {
	t.Parallel()

	cfg, err := Load(validEnv(map[string]string{"OPERATOR_CITY": "  Berlin \n"}))
	if err != nil {
		t.Fatalf("Load = %v, want no error", err)
	}

	if cfg.Operator.City != "Berlin" {
		t.Errorf("City = %q, want %q", cfg.Operator.City, "Berlin")
	}
}

func TestLoadRefusesAMissingRequiredOperatorField(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"OPERATOR_NAME",
		"OPERATOR_STREET",
		"OPERATOR_POSTAL_CODE",
		"OPERATOR_CITY",
		"OPERATOR_EMAIL",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, err := Load(validEnv(map[string]string{key: ""}))
			if err == nil {
				t.Fatalf("Load accepted a config with no %s", key)
			}
			if !strings.Contains(err.Error(), key+": is required") {
				t.Errorf("error %q does not report %s as required", err, key)
			}
		})
	}
}

func TestLoadReportsEveryMissingOperatorFieldAtOnce(t *testing.T) {
	t.Parallel()

	_, err := Load(func(key string) string {
		if key == "APP_URL" {
			return "https://app.calbridge.example"
		}

		return ""
	})
	if err == nil {
		t.Fatal("Load accepted a config with no operator at all")
	}

	for _, key := range []string{"OPERATOR_NAME", "OPERATOR_STREET", "OPERATOR_POSTAL_CODE", "OPERATOR_CITY", "OPERATOR_EMAIL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}

func TestLoadRefusesAPlaceholderOperatorValue(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a bracketed fill-in text":   "[Postal address, filled in by the owner]",
		"todo":                       "TODO",
		"a lower case changeme":      "changeme",
		"tbd with surrounding space": "  tbd ",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(validEnv(map[string]string{"OPERATOR_STREET": value}))
			if err == nil {
				t.Fatalf("Load accepted the placeholder %q", value)
			}
			if !strings.Contains(err.Error(), "OPERATOR_STREET: is still a placeholder") {
				t.Errorf("error %q does not name the placeholder refusal", err)
			}
		})
	}
}

func TestLoadRefusesAPlaceholderInAnOptionalOperatorField(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"OPERATOR_VAT_ID": "[VAT identification number, if any]"}))
	if err == nil {
		t.Fatal("Load accepted a placeholder VAT ID")
	}
	if !strings.Contains(err.Error(), "OPERATOR_VAT_ID: is still a placeholder") {
		t.Errorf("error %q does not name the placeholder refusal", err)
	}
}

func TestLoadAcceptsARealValueThatContainsABracket(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"OPERATOR_STREET": "Musterstrasse 12 [Hinterhaus]"}))
	if err != nil {
		t.Fatalf("Load = %v, want a street that only ends in a bracket accepted", err)
	}
}

func TestLoadRefusesAMalformedOperatorEmail(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"no-at-sign.example", "@calbridge.example", "kontakt@", "a b@calbridge.example", "a@b@calbridge.example", "kontakt@localhost"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			_, err := Load(validEnv(map[string]string{"OPERATOR_EMAIL": value}))
			if err == nil {
				t.Fatalf("Load accepted the email %q", value)
			}
			if !strings.Contains(err.Error(), "OPERATOR_EMAIL: must be an email address") {
				t.Errorf("error %q does not name the email refusal", err)
			}
		})
	}
}

func TestLoadRefusesAControlCharacterInTheOperator(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"OPERATOR_NAME": "Erika\nMustermann"}))
	if err == nil {
		t.Fatal("Load accepted a newline inside the operator name")
	}
	if !strings.Contains(err.Error(), "OPERATOR_NAME: must not contain a control character") {
		t.Errorf("error %q does not name the control character refusal", err)
	}
}

func TestLoadRefusesAnOverlongOperatorValue(t *testing.T) {
	t.Parallel()

	_, err := Load(validEnv(map[string]string{"OPERATOR_NAME": strings.Repeat("a", maxOperatorFieldLength+1)}))
	if err == nil {
		t.Fatal("Load accepted an overlong operator name")
	}
	if !strings.Contains(err.Error(), "OPERATOR_NAME: must be at most") {
		t.Errorf("error %q does not name the length refusal", err)
	}
}

// TestLoadNeverQuotesAnOperatorValue keeps a value pasted into the wrong
// variable out of the startup error, and with it out of a log.
func TestLoadNeverQuotesAnOperatorValue(t *testing.T) {
	t.Parallel()

	const pasted = "hunter2-not-a-street\x07"

	_, err := Load(validEnv(map[string]string{"OPERATOR_STREET": pasted}))
	if err == nil {
		t.Fatal("Load accepted a control character")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q quotes the value", err)
	}
}
