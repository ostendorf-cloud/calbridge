{{/*
Expand the name of the chart.
*/}}
{{- define "calbridge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to
this (by the DNS naming spec).
*/}}
{{- define "calbridge.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "calbridge.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "calbridge.labels" -}}
helm.sh/chart: {{ include "calbridge.chart" . }}
{{ include "calbridge.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/component: server
app.kubernetes.io/part-of: {{ include "calbridge.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels. Deliberately excludes app.kubernetes.io/version: a selector
must stay stable across an image upgrade, matchLabels is immutable once a
Deployment exists.
*/}}
{{- define "calbridge.selectorLabels" -}}
app.kubernetes.io/name: {{ include "calbridge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Create the name of the service account to use.
*/}}
{{- define "calbridge.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "calbridge.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Fully qualified name of the migration Job and its ConfigMap.
*/}}
{{- define "calbridge.migration.fullname" -}}
{{- printf "%s-migrate" (include "calbridge.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Migration Job labels, mirroring calbridge.labels with its own component.
*/}}
{{- define "calbridge.migration.labels" -}}
helm.sh/chart: {{ include "calbridge.chart" . }}
app.kubernetes.io/name: {{ include "calbridge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/component: migration
app.kubernetes.io/part-of: {{ include "calbridge.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Extract the numeric port from an "address:port" string such as ":8080",
"0.0.0.0:8080" or "[::1]:8080", by taking everything after its last colon.
Used by templates/validations.yaml to fail the render when a listen
address and its matching Service port value disagree, rather than let a
misconfigured deployment crash-loop on its own probes.
*/}}
{{- define "calbridge.portFromAddr" -}}
{{- $parts := splitList ":" . -}}
{{- last $parts -}}
{{- end -}}

{{/*
The landing server's own fully qualified name: calbridge.fullname with a
"-landing" suffix, so it never collides with the application's own
Deployment, Service or ServiceAccount inside the same release while both
are rendered from the one chart.
*/}}
{{- define "calbridge.landing.fullname" -}}
{{- printf "%s-landing" (include "calbridge.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Landing server labels, mirroring calbridge.labels with its own component
and its own selector labels: a selector must stay stable across an image
upgrade like the application's own, and must not collide with it either,
since both workloads share one release.
*/}}
{{- define "calbridge.landing.labels" -}}
helm.sh/chart: {{ include "calbridge.chart" . }}
{{ include "calbridge.landing.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/component: landing
app.kubernetes.io/part-of: {{ include "calbridge.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "calbridge.landing.selectorLabels" -}}
app.kubernetes.io/name: {{ include "calbridge.landing.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the landing server's own ServiceAccount, mirroring
calbridge.serviceAccountName for landing.serviceAccount.
*/}}
{{- define "calbridge.landing.serviceAccountName" -}}
{{- if .Values.landing.serviceAccount.create -}}
{{- default (include "calbridge.landing.fullname" .) .Values.landing.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.landing.serviceAccount.name -}}
{{- end -}}
{{- end -}}
