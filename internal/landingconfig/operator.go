// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

package landingconfig

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ostendorf-cloud/govue/envconf"
)

// maxOperatorFieldLength bounds every operator field. The values end up
// in a meta tag of every served page, so a runaway value would bloat
// each response.
const maxOperatorFieldLength = 300

// Operator is the real-world operator behind this deployment: the
// person the imprint names and the controller the privacy policy names.
// None of it lives in the repository. The deployment supplies it, so
// the same image serves any operator and no address is ever invented
// here.
//
// The JSON tags are the contract with web-landing/src/operator.ts,
// which reads the same shape back out of the served page.
type Operator struct {
	// Name, Street, PostalCode, City and Email are required. An imprint
	// without them names nobody, and the consent screens of the sign-in
	// providers link to it.
	Name       string `json:"name"`
	Street     string `json:"street"`
	PostalCode string `json:"postalCode"`
	City       string `json:"city"`
	Email      string `json:"email"`

	// The rest is optional. An empty value leaves its line out of the
	// pages instead of printing a placeholder.
	Country            string `json:"country,omitempty"`
	Phone              string `json:"phone,omitempty"`
	Representative     string `json:"representative,omitempty"`
	Register           string `json:"register,omitempty"`
	VATID              string `json:"vatId,omitempty"`
	ContentResponsible string `json:"contentResponsible,omitempty"`
	DataProtection     string `json:"dataProtection,omitempty"`
}

// loadOperator reads the operator from the environment and reports every
// missing or unusable field at once.
func loadOperator(l *envconf.Loader) (Operator, error) {
	var problems []error

	required := func(key string) string {
		return checkOperatorField(key, l.String(key, ""), true, &problems)
	}
	optional := func(key string) string {
		return checkOperatorField(key, l.String(key, ""), false, &problems)
	}

	op := Operator{
		Name:               required("OPERATOR_NAME"),
		Street:             required("OPERATOR_STREET"),
		PostalCode:         required("OPERATOR_POSTAL_CODE"),
		City:               required("OPERATOR_CITY"),
		Email:              required("OPERATOR_EMAIL"),
		Country:            optional("OPERATOR_COUNTRY"),
		Phone:              optional("OPERATOR_PHONE"),
		Representative:     optional("OPERATOR_REPRESENTATIVE"),
		Register:           optional("OPERATOR_REGISTER"),
		VATID:              optional("OPERATOR_VAT_ID"),
		ContentResponsible: optional("OPERATOR_CONTENT_RESPONSIBLE"),
		DataProtection:     optional("OPERATOR_DATA_PROTECTION"),
	}

	if op.Email != "" && !looksLikeEmail(op.Email) {
		problems = append(problems, errors.New("OPERATOR_EMAIL: must be an email address"))
	}

	if err := errors.Join(problems...); err != nil {
		return Operator{}, err
	}

	return op, nil
}

// checkOperatorField trims one value and appends a problem for a missing
// required value, an overlong value, a control character or a
// placeholder. No message quotes the value, so a half-typed secret
// pasted into the wrong variable never reaches a log.
func checkOperatorField(key, raw string, required bool, problems *[]error) string {
	value := strings.TrimSpace(raw)

	switch {
	case value == "":
		if required {
			*problems = append(*problems, fmt.Errorf("%s: is required", key))
		}
	case utf8.RuneCountInString(value) > maxOperatorFieldLength:
		*problems = append(*problems, fmt.Errorf("%s: must be at most %d characters", key, maxOperatorFieldLength))
	case strings.ContainsFunc(value, unicode.IsControl):
		*problems = append(*problems, fmt.Errorf("%s: must not contain a control character", key))
	case isPlaceholder(value):
		*problems = append(*problems, fmt.Errorf("%s: is still a placeholder", key))
	}

	return value
}

// placeholderWords are values a template or a half-finished deployment
// leaves behind. The comparison ignores case.
var placeholderWords = []string{"todo", "tbd", "changeme", "change me", "fixme", "xxx"}

// isPlaceholder reports whether a value is a fill-in marker rather than
// real data: bracketed text such as "[Postal address]", or one of the
// usual stand-in words on its own.
func isPlaceholder(value string) bool {
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return true
	}

	for _, word := range placeholderWords {
		if strings.EqualFold(value, word) {
			return true
		}
	}

	return false
}

// looksLikeEmail is a shape check, not a deliverability check: one "@"
// with text on both sides and no white space. Whether the mailbox
// exists is the operator's business.
func looksLikeEmail(value string) bool {
	local, domain, found := strings.Cut(value, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}

	return !strings.ContainsFunc(value, unicode.IsSpace) && strings.Contains(domain, ".")
}
