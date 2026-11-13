#!/usr/bin/env bash
# Validates charts/calbridge: a must-succeed set for its default values and every documented
# non-default combination, and a must-fail set proving every templates/validations.yaml and
# templates/httproute.yaml guard actually rejects the values it is meant to reject. Shared by
# task lint:helm and task validate:kubeconform so CI and a local run check the exact same
# combinations. Every combination, succeed and fail alike, also carries the baseline below:
# config.databaseUrl, config.databaseUser and existingSecret.name have no usable default and are
# required by values.schema.json, so even "default values" fails to render without them.
#
# Takes exactly one mode argument:
#   lint        - helm lint over the must-succeed set, then helm template over the must-fail
#                 set, one job per check (Taskfile's lint:helm / CI's helm-lint job)
#   kubeconform - helm template piped into kubeconform, its own job (CI's kubeconform job)
# kept as two modes of one script, not two near-duplicate scripts, so the combinations below
# live in exactly one place.
#
# The must-fail set is checked with helm template, never helm lint: a template's fail call is
# only logged by helm lint, which still reports "0 chart(s) failed" and exits zero, so a lint
# pass proves nothing about a guard built on fail. helm template propagates that fail as a
# non-zero exit, so each must-fail entry asserts both the non-zero exit and the exact message
# its guard names, which also catches a guard that starts failing for the wrong reason.
#
# kubeconform's own default schema location has no schema for a Gateway API HTTPRoute: it
# is a CustomResourceDefinition, not a built in Kubernetes type, so the default registry
# never carries it. The "-schema-location default" below keeps every built in resource on
# kubeconform's normal, self updating registry, and the second "-schema-location" adds
# Datree's CRDs-catalog (https://github.com/datreeio/CRDs-catalog) as a second lookup for
# anything the first one does not resolve, HTTPRoute included: this is kubeconform's own
# documented way to validate a custom resource, not "-ignore-missing-schemas", which would
# silently stop validating every resource this combination of locations cannot resolve,
# defeating the point of running kubeconform at all. Pinned to a commit rather than "main",
# so the schema this job validates against cannot change under CI; bump it by hand when the
# Gateway API HTTPRoute schema itself needs a newer one.
set -euo pipefail

mode="${1:?usage: validate-chart.sh <lint|kubeconform>}"

case "$mode" in
  lint|kubeconform) ;;
  *)
    echo "validate-chart: unknown mode '${mode}', expected 'lint' or 'kubeconform'" >&2
    exit 1
    ;;
esac

chart="charts/calbridge"

# config.databaseUrl, config.databaseUser, migrationJob.databaseUser and
# existingSecret.name have no usable default (see values.yaml) and are
# required by values.schema.json, so every combination below needs them
# set, the default values combination included.
baseline="existingSecret.name=calbridge-secrets,config.databaseUrl=postgres://postgres.example.svc:5432/calbridge?sslmode=require,config.databaseUser=calbridge_app,migrationJob.databaseUser=calbridge_migrator"

names=(
  "default values"
  "ingress enabled"
  "httproute enabled"
  "startup probe enabled"
  "service account disabled"
)

# Each entry is one combination's --set arguments, matching the corresponding
# charts/calbridge/tests/*_test.yaml suite's "set:" block, always layered on
# top of baseline above.
sets=(
  "${baseline}"
  "${baseline},ingress.enabled=true,ingress.className=nginx,ingress.hosts[0].host=calbridge.example.org,ingress.hosts[0].paths[0].path=/,ingress.hosts[0].paths[0].pathType=Prefix"
  "${baseline},httproute.enabled=true,httproute.parentRefs[0].name=eg,httproute.parentRefs[0].namespace=gateway-system,httproute.parentRefs[0].sectionName=https,httproute.hostnames[0]=calbridge.example.org"
  "${baseline},startupProbe.enabled=true"
  "${baseline},serviceAccount.create=false"
)

for i in "${!names[@]}"; do
  name="${names[$i]}"
  set_args="${sets[$i]}"

  echo "==> validating combination (${mode}): ${name}"

  case "$mode" in
    lint)
      if ! helm lint "${chart}" --set "${set_args}"; then
        echo "validate-chart: helm lint failed for combination: ${name}" >&2
        exit 1
      fi
      ;;
    kubeconform)
      if ! helm template "${chart}" --set "${set_args}" \
        | kubeconform -strict -summary \
          -schema-location default \
          -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/89c736bdde38b5fecd0f8436ec5f3aa3679a3b86/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'; then
        echo "validate-chart: kubeconform failed for combination: ${name}" >&2
        exit 1
      fi
      ;;
  esac
done

if [ "$mode" = "lint" ]; then
  # Each entry names the guard it exercises in templates/validations.yaml or
  # templates/httproute.yaml, its --set arguments on top of baseline above, and the
  # exact message the guard's fail call names. Asserting the exact message, not merely
  # a non-zero exit, also catches a guard that starts failing for a different reason.
  fail_names=(
    "both ingress.enabled and httproute.enabled true"
    "httproute.enabled true with parentRefs naming no Gateway"
    "config.listenAddr disagreeing with service.port"
    "config.adminListenAddr disagreeing with service.adminPort"
    "migrationJob.databaseUser equal to config.databaseUser"
    "podLabels setting the reserved app.kubernetes.io/name selector key"
  )

  fail_sets=(
    "${baseline},ingress.enabled=true,ingress.className=nginx,ingress.hosts[0].host=calbridge.example.org,ingress.hosts[0].paths[0].path=/,ingress.hosts[0].paths[0].pathType=Prefix,httproute.enabled=true,httproute.parentRefs[0].name=eg"
    "${baseline},httproute.enabled=true"
    "${baseline},config.listenAddr=:9000"
    "${baseline},config.adminListenAddr=:9001"
    "${baseline},migrationJob.databaseUser=calbridge_app"
    "${baseline},podLabels.app\.kubernetes\.io/name=evil"
  )

  fail_messages=(
    "ingress.enabled and httproute.enabled cannot both be true: enable at most one route to the Service."
    "httproute.enabled is true but httproute.parentRefs names no Gateway: set at least one parentRefs entry's name."
    "config.listenAddr (:9000) and service.port (8080) disagree on the port: both must name the same container port."
    "config.adminListenAddr (:9001) and service.adminPort (8081) disagree on the port: both must name the same container port."
    "migrationJob.databaseUser (calbridge_app) must not equal config.databaseUser: the application's own role must never own the tables row level security protects it from. See README.md."
    "podLabels must not set app.kubernetes.io/name: it collides with calbridge's own immutable selector label."
  )

  for i in "${!fail_names[@]}"; do
    name="${fail_names[$i]}"
    set_args="${fail_sets[$i]}"
    expected="${fail_messages[$i]}"

    echo "==> validating must-fail combination (template): ${name}"

    render_output=""
    render_status=0
    render_output="$(helm template "${chart}" --set "${set_args}" 2>&1)" || render_status=$?

    if [ "${render_status}" -eq 0 ]; then
      echo "validate-chart: combination '${name}' was expected to fail its guard but rendered successfully" >&2
      exit 1
    fi

    if [[ "${render_output}" != *"${expected}"* ]]; then
      echo "validate-chart: combination '${name}' failed, but not with its guard's own message" >&2
      echo "validate-chart: expected the output to contain: ${expected}" >&2
      echo "validate-chart: actual output:" >&2
      echo "${render_output}" >&2
      exit 1
    fi
  done
fi

echo "all combinations valid (${mode})"
