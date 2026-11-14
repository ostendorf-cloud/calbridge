// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package depsync

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The privileges calbridge's application role holds are written down
// twice: once for the real deployment, across
// docker/postgres/init-app-role.sh and docker-compose.yaml's
// grant-app-role service, and once for the test role
// internal/storage/postgres's integration test runs its row level
// security assertions as. docker-compose.yaml's own comment already
// claims the two match ("matching
// internal/storage/postgres/integration_test.go's own test role
// grant"); nothing checked that claim until this test.
//
// It is worth checking because of what the integration test proves. Its
// assertions are about what the restricted role may and may not do, and
// a privilege present in one list and missing in the other turns an
// assertion into a tautology: a write refused because the test role
// happens to lack the privilege proves nothing about the role the
// application actually connects as. That is a test passing for a reason
// that does not exist in production, which is worse than no test. One
// of those assertions,
// TestAGranteeMayReadASharedCalendarAndNothingElseWithIt, depends on
// the distinction directly: it asserts that UPDATE on calendars is
// refused as a missing table privilege and DELETE as a row level
// security refusal, which only holds while both lists give calendars
// SELECT, INSERT and DELETE and withhold UPDATE.
const (
	// composeGrantsPath carries the per-table and per-function grants,
	// applied after the migrations have created those tables.
	composeGrantsPath = "../../docker-compose.yaml"
	// initRolePath carries the role creation and the schema-level grant,
	// applied during initdb, before any table exists.
	initRolePath = "../../docker/postgres/init-app-role.sh"
	// integrationGrantsPath carries the test role's own grants.
	integrationGrantsPath = "../../internal/storage/postgres/integration_test.go"
)

// rolePlaceholder is what each file's own role name is rewritten to
// before the two sides are compared: the deployment grants
// calbridge_app, the test grants its own throwaway role, and the role's
// name is the one difference between the lists that is meant to be
// there.
const rolePlaceholder = "<role>"

// goStringConcat matches the Go string concatenation the integration
// fixture splices its role name in with: "` + testAppRole + `".
var goStringConcat = regexp.MustCompile("`\\s*\\+\\s*testAppRole\\s*\\+\\s*`")

// whitespace collapses any run of spaces and tabs to one space, so a
// statement spaced differently in two files still compares equal.
var whitespace = regexp.MustCompile(`[ \t]+`)

// listItem strips a YAML sequence marker, which is how
// docker-compose.yaml carries each statement as one argument to psql.
var listItem = regexp.MustCompile(`^-\s+`)

// grantsIn extracts every GRANT statement from text, with the role name
// replaced by rolePlaceholder, sorted so two files listing the same
// grants in a different order still agree.
//
// It reads line by line and keeps only a line that is itself a complete
// statement, because every file this compares writes one grant per line:
// docker-compose.yaml as one "-c" argument each,
// docker/postgres/init-app-role.sh inside its heredoc, and the
// integration fixture inside one Go raw string. Matching across lines
// instead would swallow the prose around them, all three files having
// comments that say the word GRANT.
//
// ALTER DEFAULT PRIVILEGES is skipped by the same rule, which is the
// right outcome for its own reason: it is a standing rule about tables
// created later, not a privilege on a table that exists now, and the
// integration fixture has no counterpart to it because it names every
// table it needs explicitly.
func grantsIn(text, roleName string) []string {
	var found []string

	for line := range strings.SplitSeq(text, "\n") {
		line = goStringConcat.ReplaceAllString(line, rolePlaceholder)
		if roleName != "" {
			line = strings.ReplaceAll(line, roleName, rolePlaceholder)
		}

		line = listItem.ReplaceAllString(strings.TrimSpace(line), "")
		line = whitespace.ReplaceAllString(line, " ")

		// The integration fixture's last grant is also the last line of a
		// Go raw string, so it carries the closing backtick after its
		// semicolon. Nothing else this reads contains a backtick at all.
		line = strings.TrimRight(line, "`")

		if !strings.HasPrefix(line, "GRANT ") || !strings.HasSuffix(line, ";") {
			continue
		}

		found = append(found, line)
	}

	slices.Sort(found)

	return slices.Compact(found)
}

// TestGrantsIn covers the two spellings grantsIn has to read: a plain
// SQL statement naming the role, and the integration fixture's Go
// string concatenation.
func TestGrantsIn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		role string
		want []string
	}{
		{
			name: "a plain statement naming the role",
			text: "GRANT SELECT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"GRANT SELECT ON users TO <role>;"},
		},
		{
			name: "the role spliced in through go concatenation",
			text: "GRANT SELECT ON users TO ` + testAppRole + `;",
			want: []string{"GRANT SELECT ON users TO <role>;"},
		},
		{
			name: "carried as a yaml sequence item",
			text: "      - GRANT SELECT, INSERT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"GRANT SELECT, INSERT ON users TO <role>;"},
		},
		{
			name: "alter default privileges is not a grant on an existing table",
			text: "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO calbridge_app;",
			role: "calbridge_app",
			want: nil,
		},
		{
			name: "the last line of a go raw string, closing backtick included",
			text: "GRANT SELECT ON users TO ` + testAppRole + `;`",
			want: []string{"GRANT SELECT ON users TO <role>;"},
		},
		{
			name: "prose that names a grant is not one",
			text: "# EXECUTE on users_find_by_email is the one way calbridge_app may\n" +
				"# read a users row: see why the GRANT cannot live in db/migrations.\n" +
				"GRANT SELECT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"GRANT SELECT ON users TO <role>;"},
		},
		{
			name: "sorted and deduplicated",
			text: "GRANT SELECT ON b TO r;\nGRANT SELECT ON a TO r;\nGRANT SELECT ON a TO r;",
			role: "r",
			want: []string{"GRANT SELECT ON a TO <role>;", "GRANT SELECT ON b TO <role>;"},
		},
		{
			name: "no grants at all",
			text: "CREATE ROLE calbridge_app LOGIN NOSUPERUSER NOBYPASSRLS;",
			role: "calbridge_app",
			want: nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := grantsIn(testCase.text, testCase.role)

			if !slices.Equal(got, testCase.want) {
				t.Errorf("grantsIn() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestTheApplicationRoleAndTheTestRoleHoldTheSameGrants is the check
// itself. See this file's own doc comment for why a difference here
// would make the integration test's assertions prove less than they
// claim.
func TestTheApplicationRoleAndTheTestRoleHoldTheSameGrants(t *testing.T) {
	t.Parallel()

	// The deployment's half is split across two files for a reason
	// docker-compose.yaml records: the schema-level grant runs during
	// initdb, before any table exists, and the per-table grants have to
	// wait until the migrations have created them. The test role gets
	// both from one string, so the comparison takes the union.
	var deployment []string

	for _, path := range []string{initRolePath, composeGrantsPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		deployment = append(deployment, grantsIn(string(content), "calbridge_app")...)
	}

	slices.Sort(deployment)
	deployment = slices.Compact(deployment)

	content, err := os.ReadFile(integrationGrantsPath)
	if err != nil {
		t.Fatalf("read %s: %v", integrationGrantsPath, err)
	}

	// The test role's name never appears literally in that file: it is
	// spliced in through Go concatenation, which grantsIn rewrites, so
	// there is no role name to pass here.
	test := grantsIn(string(content), "")

	if len(deployment) == 0 {
		t.Fatal("found no grants in the deployment files; the extraction stopped matching rather " +
			"than the grants having gone away")
	}

	if len(test) == 0 {
		t.Fatal("found no grants in the integration test; the extraction stopped matching rather " +
			"than the grants having gone away")
	}

	for _, statement := range deployment {
		if !slices.Contains(test, statement) {
			t.Errorf("the deployment grants %q and the integration test role does not; an assertion "+
				"about what the restricted role may not do would pass there for a privilege the "+
				"application actually holds", statement)
		}
	}

	for _, statement := range test {
		if !slices.Contains(deployment, statement) {
			t.Errorf("the integration test role is granted %q and the deployment is not; an "+
				"assertion about what the restricted role may do proves nothing about the role the "+
				"application connects as", statement)
		}
	}
}
