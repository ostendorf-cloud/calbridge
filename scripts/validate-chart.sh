#!/usr/bin/env bash
# Validates charts/calbridge for its default values and every documented non-default
# combination, so CI and a local run share the exact same logic. The combinations below
# mirror charts/calbridge/tests/*_test.yaml exactly: default, ingress enabled, httproute
# enabled.
#
# Takes exactly one mode argument:
#   lint        - helm lint, one job per check (Taskfile's lint:helm / CI's helm-lint job)
#   kubeconform - helm template piped into kubeconform, its own job (CI's kubeconform job)
# kept as two modes of one script, not two near-duplicate scripts, so the three --set
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
# defeating the point of running kubeconform at all.
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

names=(
  "default values"
  "ingress enabled"
  "httproute enabled"
)

# Each entry is one combination's --set arguments, matching the corresponding
# charts/calbridge/tests/*_test.yaml suite's "set:" block.
sets=(
  ""
  "ingress.enabled=true,ingress.className=nginx,ingress.hosts[0].host=calbridge.example.org,ingress.hosts[0].paths[0].path=/,ingress.hosts[0].paths[0].pathType=Prefix"
  "httproute.enabled=true,httproute.parentRefs[0].name=eg,httproute.parentRefs[0].namespace=gateway-system,httproute.parentRefs[0].sectionName=https,httproute.hostnames[0]=calbridge.example.org"
)

for i in "${!names[@]}"; do
  name="${names[$i]}"
  set_args="${sets[$i]}"

  echo "==> validating combination (${mode}): ${name}"

  # "default values" sets nothing (set_args is empty); every other combination
  # passes its --set argument. Branching on set_args directly, rather than
  # building a possibly empty args array and expanding it, sidesteps bash
  # 3.2's "unbound variable" behaviour for an empty array under "set -u"
  # (macOS still ships 3.2 as /bin/bash).
  case "$mode" in
    lint)
      if [ -n "$set_args" ]; then
        lint_ok=0; helm lint "${chart}" --set "$set_args" || lint_ok=1
      else
        lint_ok=0; helm lint "${chart}" || lint_ok=1
      fi
      if [ "$lint_ok" -ne 0 ]; then
        echo "validate-chart: helm lint failed for combination: ${name}" >&2
        exit 1
      fi
      ;;
    kubeconform)
      if [ -n "$set_args" ]; then
        template_output=$(helm template "${chart}" --set "$set_args")
      else
        template_output=$(helm template "${chart}")
      fi
      if ! printf '%s\n' "$template_output" | kubeconform -strict -summary \
        -schema-location default \
        -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'; then
        echo "validate-chart: kubeconform failed for combination: ${name}" >&2
        exit 1
      fi
      ;;
  esac
done

echo "all combinations valid (${mode})"
