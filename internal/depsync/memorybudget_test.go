// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package depsync

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/ostendorf-cloud/calbridge/internal/auth"
)

// chartValuesPath is charts/calbridge/values.yaml, relative to this
// package.
const chartValuesPath = "../../charts/calbridge/values.yaml"

// hashBudgetShare is the largest share of the container's memory limit
// that concurrent Argon2id derivations may account for. Half: the other
// half is the Go runtime, the embedded SPA, the pgx pool, the session
// store and the headroom a collector needs to not be running against
// the limit continuously. It is the number that decides whether
// auth.MaxConcurrentPasswordHashes and the chart's limit still agree,
// and raising it is a decision about how close to an OOM kill this
// deployment runs.
const hashBudgetShare = 2

// containerMemoryLimit matches the memory limit of the top level
// resources block, the one that belongs to the application container.
// The anchor is a line that starts in column zero, so migrationJob's own
// nested resources block, which is indented, cannot match.
var containerMemoryLimit = regexp.MustCompile(`(?m)^resources:(?:\n[ \t]+.*)*?\n[ \t]+limits:(?:\n[ \t]+.*)*?\n[ \t]+memory: ([0-9]+)Mi`)

// TestPasswordHashingFitsTheChartsMemoryLimit is the other half of
// auth.MaxConcurrentPasswordHashes: that constant is chosen against the
// pod's own memory limit, and a limit lives in a Helm chart no Go
// compiler reads. This fails when either side moves without the other,
// in both directions, which is what keeps the two a single decision.
//
// Changing them together is the supported move. Raising only the chart's
// limit buys no capacity, because the code still runs the same number of
// derivations; lowering only the chart's limit, or raising only the
// constant, puts the process back where the burst that OOM-kills it is
// one unauthenticated client's.
func TestPasswordHashingFitsTheChartsMemoryLimit(t *testing.T) {
	t.Parallel()

	limitMiB, err := chartContainerMemoryLimitMiB()
	if err != nil {
		t.Fatalf("read the chart's container memory limit: %v", err)
	}

	hashMiB := auth.MaxConcurrentPasswordHashes * auth.PasswordHashMemoryMiB

	if want := limitMiB / hashBudgetShare; hashMiB > want {
		t.Errorf("%d concurrent Argon2id derivations at %d MiB hold %d MiB, more than the %d MiB "+
			"(%s's %d MiB limit divided by %d) they may account for: lower "+
			"auth.MaxConcurrentPasswordHashes or raise the chart's limit, and say in both places why",
			auth.MaxConcurrentPasswordHashes, auth.PasswordHashMemoryMiB, hashMiB, want,
			chartValuesPath, limitMiB, hashBudgetShare)
	}

	// The other direction: a limit raised far beyond what the code will
	// ever use is a pod reserving memory nothing claims, and more likely
	// a constant somebody forgot to raise with it.
	if hashMiB*4 < limitMiB {
		t.Errorf("%s limits the container to %d MiB while at most %d MiB of it can go to password "+
			"hashing: raise auth.MaxConcurrentPasswordHashes to use it, or lower the limit",
			chartValuesPath, limitMiB, hashMiB)
	}
}

// chartContainerMemoryLimitMiB reads resources.limits.memory out of the
// chart's values. It insists on MiB rather than normalising every unit
// Kubernetes accepts: the chart has always written this one in MiB, and
// a test that quietly understood Gi too would be a test nobody notices
// has stopped checking the value they actually set.
func chartContainerMemoryLimitMiB() (int, error) {
	values, err := os.ReadFile(chartValuesPath)
	if err != nil {
		return 0, err
	}

	match := containerMemoryLimit.FindSubmatch(values)
	if match == nil {
		return 0, fmt.Errorf("no top level resources.limits.memory in MiB found in %s", chartValuesPath)
	}

	return strconv.Atoi(string(match[1]))
}
