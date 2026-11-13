#!/usr/bin/env bash
# Validates charts/calbridge for its default values and every documented non-default
# combination, so CI and a local run share the exact same logic. The combinations below
# mirror charts/calbridge/tests/*_test.yaml exactly: default, ingress enabled, httproute
# enabled, startup probe enabled, service account disabled. Every one of them also carries
# the baseline below: config.databaseUrl, config.databaseUser and existingSecret.name have
# no usable default and are required by values.schema.json, so even "default values" fails
# to render without them.
#
# Takes exactly one mode argument:
#   lint        - helm lint, one job per check (Taskfile's lint:helm / CI's helm-lint job)
#   kubeconform - helm template piped into kubeconform, its own job (CI's kubeconform job)
# kept as two modes of one script, not two near-duplicate scripts, so the five --set
# combinations above live in exactly one place.
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

# config.databaseUrl, config.databaseUser and existingSecret.name have no
# usable default (see values.yaml) and are required by values.schema.json,
# so every combination below needs them set, the default values
# combination included.
baseline="existingSecret.name=calbridge-secrets,config.databaseUrl=postgres://postgres.example.svc:5432/calbridge?sslmode=require,config.databaseUser=calbridge_app"

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

echo "all combinations valid (${mode})"
