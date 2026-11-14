// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package depsync

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The privileges calbridge's application role holds are written down
// three times: once for the real deployment, across
// docker/postgres/init-app-role.sh and docker-compose.yaml's
// grant-app-role service; once for the cluster, in
// charts/calbridge/README.md, which is the list an operator actually
// applies and which says in prose that this test is what keeps the
// copies together; and once for the test role
// internal/storage/postgres's integration test runs its row level
// security assertions as.
//
// It is worth checking because of what the integration test proves. Its
// assertions are about what the restricted role may and may not do, and
// a privilege present in one list and missing in another turns an
// assertion into a tautology: a write refused because the test role
// happens to lack the privilege proves nothing about the role the
// application actually connects as. That is a test passing for a reason
// that does not exist in production, which is worse than no test. One
// of those assertions,
// TestAGranteeMayReadASharedCalendarAndNothingElseWithIt, depends on
// the distinction directly: it asserts that UPDATE on calendars is
// refused as a missing table privilege and DELETE as a row level
// security refusal, which only holds while every list gives calendars
// SELECT, INSERT and DELETE and withholds UPDATE.
//
// What is compared is a set of (object, privilege) pairs, not statement
// text. Three spellings of the same privilege are the same privilege:
// "ON users" and "ON TABLE users", a reordered privilege list, and one
// multi-object GRANT against the three single-object GRANTs it is
// equivalent to. Comparing text instead reports each of those as drift,
// and a false alarm is as harmful as a miss here: the way to quiet one
// is to reshape a statement, which is exactly the edit that hides a
// real difference.
//
// ALTER DEFAULT PRIVILEGES is compared like any other statement rather
// than skipped. It is a standing rule about tables created later, and
// skipping it left the one hole this file exists to close: both
// deployment lists grant SELECT, INSERT, UPDATE and DELETE on tables by
// default, so the first table a future migration adds arrives with
// UPDATE in the deployment and without it on the integration role, with
// no edit to any list and nothing to notice. Comparing the statement
// means the fixture carries the same standing rule, so a new table
// arrives the same way everywhere. The alternative, asserting a known
// list of covered tables, would catch it too but would need an edit to
// this file on every migration, which is a worse place to put the
// decision than the grant lists themselves.
const (
	// composeGrantsPath carries the per-table and per-function grants,
	// applied after the migrations have created those tables.
	composeGrantsPath = "../../docker-compose.yaml"
	// initRolePath carries the role creation and the schema-level grant,
	// applied during initdb, before any table exists.
	initRolePath = "../../docker/postgres/init-app-role.sh"
	// chartGrantsPath carries the list a cluster operator applies by
	// hand, inside the one SQL code fence in that document.
	chartGrantsPath = "../../charts/calbridge/README.md"
	// integrationGrantsPath carries the test role's own grants.
	integrationGrantsPath = "../../internal/storage/postgres/integration_test.go"
)

// rolePlaceholder is what each file's own role name is rewritten to
// before the lists are compared: the deployment grants calbridge_app,
// the chart names a values key, the test grants its own throwaway role,
// and the role's name is the one difference between the lists that is
// meant to be there.
const rolePlaceholder = "<role>"

// composeRoleName is the role the deployment grants to, literally, in
// both of its own files.
const composeRoleName = "calbridge_app"

// chartRolePlaceholder is how charts/calbridge/README.md spells the role:
// the values key an operator substitutes, not a role name.
const chartRolePlaceholder = "<config.databaseUser>"

var (
	// goStringConcat matches the Go string concatenation the integration
	// fixture splices its role name in with: "` + testAppRole + `".
	goStringConcat = regexp.MustCompile("`\\s*\\+\\s*testAppRole\\s*\\+\\s*`")

	// lineComment strips a comment to the end of its line, in all three
	// of the spellings these files use. It runs before anything is
	// joined, so prose that names a GRANT cannot become part of one, and
	// so a trailing comment on a real statement is not a difference.
	lineComment = regexp.MustCompile(`(?m)(--|//|#).*$`)

	// anyWhitespace collapses any run of whitespace, newlines included,
	// to one space, so a statement wrapped across lines or indented
	// differently in two files still compares equal.
	anyWhitespace = regexp.MustCompile(`\s+`)

	// statementStart finds where a statement begins inside a chunk that
	// may carry anything ahead of it: a YAML sequence marker, psql's own
	// arguments, the opening of a Go raw string, or a sentence of prose.
	// The last match in the chunk is the statement.
	statementStart = regexp.MustCompile(`(?i)ALTER\s+DEFAULT\s+PRIVILEGES\b|GRANT\b`)

	// grantStatement matches GRANT <privileges> ON <objects> TO <role>.
	grantStatement = regexp.MustCompile(`(?is)^GRANT\s+(.+?)\s+ON\s+(.+?)\s+TO\s+(\S+)$`)

	// defaultPrivileges matches the standing rule for objects created
	// later: ALTER DEFAULT PRIVILEGES IN SCHEMA <schema> GRANT
	// <privileges> ON <object kind> TO <role>.
	defaultPrivileges = regexp.MustCompile(
		`(?is)^ALTER\s+DEFAULT\s+PRIVILEGES\s+IN\s+SCHEMA\s+(\S+)\s+GRANT\s+(.+?)\s+ON\s+(\S+)\s+TO\s+(\S+)$`)

	// sqlFence matches a fenced SQL block in a markdown document.
	sqlFence = regexp.MustCompile("(?s)```sql\n(.*?)\n```")

	// goRawString matches a Go raw string literal, which is where the
	// integration fixture keeps its SQL.
	goRawString = regexp.MustCompile("(?s)`([^`]*)`")
)

// objectKinds are the object kinds a GRANT may name ahead of its object
// list. TABLE is the default when none is written, which is what makes
// "ON users" and "ON TABLE users" the same privilege.
var objectKinds = []string{"TABLE", "SCHEMA", "SEQUENCE", "FUNCTION", "ROUTINE", "PROCEDURE", "DATABASE", "TYPE"}

// privilegeOn is one privilege on one object: the unit the three lists
// are compared in. A statement naming four privileges on three tables
// is twelve of these, so how it was written stops mattering.
type privilegeOn struct {
	object    string
	privilege string
}

func (p privilegeOn) String() string { return p.privilege + " ON " + p.object }

// grantSource is one of the files a list is read from, named for the
// failure message.
type grantSource struct {
	name  string
	paths []string
	// role is the file's own spelling of the role name, rewritten to
	// rolePlaceholder. Empty where the file splices the name in through
	// Go concatenation, which goStringConcat rewrites instead.
	role string
	// extract narrows a file to the part of it that holds SQL, for a
	// file whose remainder is a language of its own: markdown prose and
	// Go code both say the word GRANT in places that are not one. Nil
	// where the whole file is read, which is the case for a shell script
	// and a compose file, both of which carry SQL and comments only.
	extract func(string) string
}

// privilegesIn extracts every (object, privilege) pair text grants to
// roleName, together with every statement it could not read.
//
// Nothing is dropped quietly. A chunk that names a GRANT and does not
// match a shape this understands, or that grants to a role other than
// the one asked for, comes back in the second return value, and the
// test fails on it: a statement the extractor cannot parse must not
// simply leave the set, which is how a real difference would go unseen.
func privilegesIn(text, roleName string) ([]privilegeOn, []string) {
	text = goStringConcat.ReplaceAllString(text, rolePlaceholder)
	if roleName != "" {
		text = strings.ReplaceAll(text, roleName, rolePlaceholder)
	}

	text = lineComment.ReplaceAllString(text, "")

	var (
		found    []privilegeOn
		unparsed []string
	)

	// Every statement these files carry ends in a semicolon, so the
	// remainder after the last one is never a statement and is dropped.
	chunks := strings.Split(text, ";")

	for _, chunk := range chunks[:max(len(chunks)-1, 0)] {
		statement := statementIn(chunk)
		if statement == "" {
			continue
		}

		pairs, err := pairsIn(statement)
		if err != nil {
			unparsed = append(unparsed, statement)

			continue
		}

		found = append(found, pairs...)
	}

	slices.SortFunc(found, func(a, b privilegeOn) int { return strings.Compare(a.String(), b.String()) })

	return slices.Compact(found), unparsed
}

// statementIn returns the statement a chunk ends with, with whatever
// preceded it removed, or the empty string if the chunk carries none.
func statementIn(chunk string) string {
	chunk = strings.TrimSpace(anyWhitespace.ReplaceAllString(chunk, " "))

	starts := statementStart.FindAllStringIndex(chunk, -1)
	if starts == nil {
		return ""
	}

	// ALTER DEFAULT PRIVILEGES carries a GRANT of its own, so the last
	// match is the inner one; the outer statement is what has to be
	// read. Where both are present the earlier of the two wins.
	start := starts[len(starts)-1][0]
	if len(starts) > 1 {
		previous := starts[len(starts)-2]
		if strings.EqualFold(chunk[previous[0]:previous[0]+5], "ALTER") {
			start = previous[0]
		}
	}

	return strings.TrimSpace(chunk[start:])
}

// pairsIn turns one statement into its (object, privilege) pairs.
func pairsIn(statement string) ([]privilegeOn, error) {
	if match := defaultPrivileges.FindStringSubmatch(statement); match != nil {
		schema, privileges, kind, role := match[1], match[2], match[3], match[4]
		if role != rolePlaceholder {
			return nil, fmt.Errorf("granted to %q rather than to the role under test", role)
		}

		object := fmt.Sprintf("DEFAULT %s IN SCHEMA %s",
			strings.ToUpper(kind), strings.ToLower(schema))

		return pairsFor([]string{object}, privileges)
	}

	match := grantStatement.FindStringSubmatch(statement)
	if match == nil {
		return nil, fmt.Errorf("not a shape this test reads")
	}

	privileges, objectList, role := match[1], match[2], match[3]
	if role != rolePlaceholder {
		return nil, fmt.Errorf("granted to %q rather than to the role under test", role)
	}

	objects, err := objectsIn(objectList)
	if err != nil {
		return nil, err
	}

	return pairsFor(objects, privileges)
}

// pairsFor crosses a statement's objects with its privileges.
func pairsFor(objects []string, privileges string) ([]privilegeOn, error) {
	var pairs []privilegeOn

	for _, privilege := range splitTopLevel(privileges) {
		privilege = strings.ToUpper(strings.TrimSpace(privilege))
		if privilege == "" {
			return nil, fmt.Errorf("an empty privilege in %q", privileges)
		}

		// ALL and ALL PRIVILEGES are the same thing written two ways.
		if privilege == "ALL PRIVILEGES" {
			privilege = "ALL"
		}

		for _, object := range objects {
			pairs = append(pairs, privilegeOn{object: object, privilege: privilege})
		}
	}

	if pairs == nil {
		return nil, fmt.Errorf("no privilege in %q", privileges)
	}

	return pairs, nil
}

// objectsIn turns a GRANT's object list into one normalised name per
// object, with the kind written out even where the statement left it
// implicit.
func objectsIn(objectList string) ([]string, error) {
	kind := "TABLE"

	for _, candidate := range objectKinds {
		prefix := candidate + " "
		if len(objectList) >= len(prefix) && strings.EqualFold(objectList[:len(prefix)], prefix) {
			kind = candidate
			objectList = objectList[len(prefix):]

			break
		}
	}

	var objects []string

	for _, name := range splitTopLevel(objectList) {
		// Unquoted identifiers fold to lower case in Postgres, so two
		// files differing only in case name the same object. A quoted
		// identifier does not fold, and no list here uses one; one
		// appearing is refused rather than folded wrongly.
		if strings.Contains(name, `"`) {
			return nil, fmt.Errorf("a quoted identifier in %q", objectList)
		}

		// "ALL TABLES IN SCHEMA x" and its relatives are a different
		// statement shape from the one this reads, and treating them as
		// an object named "all tables in schema x" would compare two
		// spellings of the same grant as different objects.
		if strings.Contains(strings.ToUpper(name), " IN SCHEMA ") {
			return nil, fmt.Errorf("an object set this test does not read: %q", name)
		}

		// A function is named with its argument types, and the spacing
		// inside that list is not a difference.
		name = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", ""))
		if name == "" {
			return nil, fmt.Errorf("an empty object in %q", objectList)
		}

		objects = append(objects, kind+" "+name)
	}

	if objects == nil {
		return nil, fmt.Errorf("no object in %q", objectList)
	}

	return objects, nil
}

// splitTopLevel splits a comma separated list, ignoring a comma inside
// parentheses: a function's argument types are part of its name, not a
// second object.
func splitTopLevel(list string) []string {
	var (
		parts []string
		depth int
		start int
	)

	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, list[start:i])
				start = i + 1
			}
		}
	}

	return append(parts, list[start:])
}

// TestPrivilegesIn covers the spellings privilegesIn has to read as the
// same privilege, the ones it has to read as different ones, and the
// statements it has to refuse rather than drop.
func TestPrivilegesIn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		text         string
		role         string
		want         []string
		wantUnparsed int
	}{
		{
			name: "a plain statement naming the role",
			text: "GRANT SELECT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"SELECT ON TABLE users"},
		},
		{
			name: "the role spliced in through go concatenation",
			text: "GRANT SELECT ON users TO ` + testAppRole + `;",
			want: []string{"SELECT ON TABLE users"},
		},
		{
			name: "carried as a yaml sequence item next to psql's own arguments",
			text: "      - -c\n      - GRANT SELECT, INSERT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"INSERT ON TABLE users", "SELECT ON TABLE users"},
		},
		{
			name: "the table keyword written out",
			text: "GRANT SELECT ON TABLE users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"SELECT ON TABLE users"},
		},
		{
			name: "a privilege list in another order",
			text: "GRANT INSERT, SELECT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"INSERT ON TABLE users", "SELECT ON TABLE users"},
		},
		{
			name: "one multi-object grant and the single-object grants it equals",
			text: "GRANT SELECT ON a, b TO calbridge_app;\nGRANT SELECT ON b TO calbridge_app;\nGRANT SELECT ON a TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"SELECT ON TABLE a", "SELECT ON TABLE b"},
		},
		{
			name: "a trailing comment on a statement",
			text: "GRANT SELECT ON users TO calbridge_app; -- and nothing more\n",
			role: "calbridge_app",
			want: []string{"SELECT ON TABLE users"},
		},
		{
			name: "wrapped across lines as a yaml block scalar",
			text: "      - >-\n        GRANT UPDATE ON calendars\n        TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"UPDATE ON TABLE calendars"},
		},
		{
			name: "written in lower case",
			text: "grant select, insert on users to calbridge_app;",
			role: "calbridge_app",
			want: []string{"INSERT ON TABLE users", "SELECT ON TABLE users"},
		},
		{
			name: "a function, whose argument types are part of its name",
			text: "GRANT EXECUTE ON FUNCTION identities_find_by_provider_subject(text, text) TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"EXECUTE ON FUNCTION identities_find_by_provider_subject(text,text)"},
		},
		{
			name: "the schema-level grant",
			text: "GRANT USAGE ON SCHEMA public TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"USAGE ON SCHEMA public"},
		},
		{
			name: "the standing rule for tables created later",
			text: "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, TRIGGER ON TABLES TO calbridge_app;",
			role: "calbridge_app",
			want: []string{
				"SELECT ON DEFAULT TABLES IN SCHEMA public",
				"TRIGGER ON DEFAULT TABLES IN SCHEMA public",
			},
		},
		{
			name: "the standing rule wrapped after its schema",
			text: "ALTER DEFAULT PRIVILEGES IN SCHEMA public\n    GRANT SELECT ON TABLES TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"SELECT ON DEFAULT TABLES IN SCHEMA public"},
		},
		{
			name: "prose that names a grant is not one",
			text: "# EXECUTE on users_find_by_email is the one way calbridge_app may\n" +
				"# read a users row: see why the GRANT cannot live in db/migrations.\n" +
				"GRANT SELECT ON users TO calbridge_app;",
			role: "calbridge_app",
			want: []string{"SELECT ON TABLE users"},
		},
		{
			name: "no grants at all",
			text: "CREATE ROLE calbridge_app LOGIN NOSUPERUSER NOBYPASSRLS;",
			role: "calbridge_app",
			want: nil,
		},
		{
			name:         "a grant to a role this list is not about",
			text:         "GRANT SELECT ON users TO somebody_else;",
			role:         "calbridge_app",
			want:         nil,
			wantUnparsed: 1,
		},
		{
			name:         "a grant shaped in a way this test cannot read",
			text:         "GRANT SELECT ON ALL TABLES IN SCHEMA public TO calbridge_app;",
			role:         "calbridge_app",
			want:         nil,
			wantUnparsed: 1,
		},
		{
			name:         "a truncated statement is refused rather than dropped",
			text:         "GRANT SELECT ON users;",
			role:         "calbridge_app",
			want:         nil,
			wantUnparsed: 1,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			pairs, unparsed := privilegesIn(testCase.text, testCase.role)

			got := make([]string, 0, len(pairs))
			for _, pair := range pairs {
				got = append(got, pair.String())
			}

			if len(got) == 0 {
				got = nil
			}

			if !slices.Equal(got, testCase.want) {
				t.Errorf("privilegesIn() = %q, want %q", got, testCase.want)
			}

			if len(unparsed) != testCase.wantUnparsed {
				t.Errorf("privilegesIn() left %d statements unparsed (%q), want %d",
					len(unparsed), unparsed, testCase.wantUnparsed)
			}
		})
	}
}

// TestSplitTopLevelKeepsAFunctionsArgumentsTogether covers the one
// subtlety the comma splitting carries: a function's argument list is
// part of its name.
func TestSplitTopLevelKeepsAFunctionsArgumentsTogether(t *testing.T) {
	t.Parallel()

	got := splitTopLevel("a(text, text), b, c(int)")
	want := []string{"a(text, text)", " b", " c(int)"}

	if !slices.Equal(got, want) {
		t.Errorf("splitTopLevel() = %q, want %q", got, want)
	}
}

// TestTheApplicationRoleAndTheTestRoleHoldTheSameGrants is the check
// itself. See this file's own doc comment for why a difference here
// would make the integration test's assertions prove less than they
// claim.
func TestTheApplicationRoleAndTheTestRoleHoldTheSameGrants(t *testing.T) {
	t.Parallel()

	sources := []grantSource{
		{
			// The deployment's half is split across two files for a
			// reason docker-compose.yaml records: the schema-level grant
			// runs during initdb, before any table exists, and the
			// per-table grants have to wait until the migrations have
			// created them. The other lists hold both in one place, so
			// the comparison takes the union.
			name:  "docker-compose.yaml and docker/postgres/init-app-role.sh",
			paths: []string{initRolePath, composeGrantsPath},
			role:  composeRoleName,
		},
		{
			name:    "charts/calbridge/README.md",
			paths:   []string{chartGrantsPath},
			role:    chartRolePlaceholder,
			extract: sqlFencesIn,
		},
		{
			// The test role's name never appears literally in that
			// file: it is spliced in through Go concatenation, which
			// goRawStringsIn rewrites before it splits the literals, so
			// there is no role name to give.
			name:    "internal/storage/postgres/integration_test.go",
			paths:   []string{integrationGrantsPath},
			extract: goRawStringsIn,
		},
	}

	lists := make([][]privilegeOn, len(sources))

	for i, source := range sources {
		var pairs []privilegeOn

		for _, path := range source.paths {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			text := string(content)
			if source.extract != nil {
				text = source.extract(text)
			}

			found, unparsed := privilegesIn(text, source.role)
			if len(unparsed) != 0 {
				t.Errorf("%s carries a statement this test cannot read: %q; a statement that cannot "+
					"be parsed must not leave the comparison, because a real difference would leave "+
					"with it", path, unparsed)
			}

			pairs = append(pairs, found...)
		}

		if len(pairs) == 0 {
			t.Fatalf("found no grants in %s; the extraction stopped matching rather than the grants "+
				"having gone away", source.name)
		}

		slices.SortFunc(pairs, func(a, b privilegeOn) int { return strings.Compare(a.String(), b.String()) })
		lists[i] = slices.Compact(pairs)
	}

	for i, source := range sources {
		for j, other := range sources {
			if i == j {
				continue
			}

			for _, pair := range lists[i] {
				if !slices.Contains(lists[j], pair) {
					t.Errorf("%s grants %s and %s does not; an assertion about what the restricted "+
						"role may or may not do proves nothing while the lists disagree",
						source.name, pair, other.name)
				}
			}
		}
	}
}

// sqlFencesIn returns the contents of every SQL code fence in a markdown
// document, joined, so the prose around them is never read as a
// statement.
func sqlFencesIn(text string) string {
	var fences []string

	for _, match := range sqlFence.FindAllStringSubmatch(text, -1) {
		fences = append(fences, match[1])
	}

	return strings.Join(fences, "\n")
}

// goRawStringsIn returns the contents of every Go raw string literal in
// a source file, joined, so the code around them is never read as a
// statement: a type named CalendarGrant and a method named Grants both
// carry the word.
//
// The role concatenation is rewritten first, because it splits one raw
// string into two: "GRANT ... TO ` + testAppRole + `;" is a literal, an
// expression and a literal, and extracting the literals on their own
// would lose the statement's end.
func goRawStringsIn(text string) string {
	text = goStringConcat.ReplaceAllString(text, rolePlaceholder)

	var literals []string

	for _, match := range goRawString.FindAllStringSubmatch(text, -1) {
		literals = append(literals, match[1])
	}

	return strings.Join(literals, "\n")
}

// TestGoRawStringsInReadsOnlyTheLiterals covers what keeps the
// integration fixture's own Go code out of its grant list.
func TestGoRawStringsInReadsOnlyTheLiterals(t *testing.T) {
	t.Parallel()

	// One double quoted string, so the backticks and the identifier in
	// it are the text this reads rather than Go syntax of its own.
	source := "grant, err := store.CalendarGrants().Create(ctx, storage.CalendarGrant{});\n" +
		"if _, err := adminPool.Exec(ctx, `\nGRANT SELECT ON users TO ` + testAppRole + `;`); err != nil {\n"

	got := goRawStringsIn(source)
	want := "\nGRANT SELECT ON users TO <role>;"

	if got != want {
		t.Errorf("goRawStringsIn() = %q, want %q", got, want)
	}
}

// TestSQLFencesInReadsOnlyTheCodeFences covers what keeps the chart's
// prose out of its grant list: the document says the word GRANT in
// sentences, and only the block an operator applies is a statement.
func TestSQLFencesInReadsOnlyTheCodeFences(t *testing.T) {
	t.Parallel()

	document := "A privilege granted here and not elsewhere weakens a test;\n" +
		"```sh\nhelm upgrade calbridge ./charts/calbridge;\n```\n" +
		"```sql\nGRANT SELECT ON users TO <role>;\n```\n" +
		"and nothing after it is a statement;\n"

	got := sqlFencesIn(document)
	want := "GRANT SELECT ON users TO <role>;"

	if got != want {
		t.Errorf("sqlFencesIn() = %q, want %q", got, want)
	}
}
