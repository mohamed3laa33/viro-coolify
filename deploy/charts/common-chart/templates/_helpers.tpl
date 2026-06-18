{{/*
Expand the name of the chart.
*/}}
{{- define "common-chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "common-chart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "common-chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
common-chart labels
*/}}
{{- define "common-chart.labels" -}}
helm.sh/chart: {{ include "common-chart.chart" . }}
{{ include "common-chart.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "common-chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "common-chart.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "common-chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "common-chart.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}


{{/*
Allow the release namespace to be overridden for multi-namespace deployments in combined charts
*/}}
{{- define "common-chart.namespace" -}}
  {{- if .Values.namespaceOverride -}}
    {{- .Values.namespaceOverride -}}
  {{- else -}}
    {{- .Release.Namespace -}}
  {{- end -}}
{{- end -}}

{{/*
Resolve the default probe port for a workload. Prefers the first service port's
targetPort (the actual container port); falls back to its `port`. Returns empty
when no service port is defined, in which case callers skip default probes.
*/}}
{{- define "common-chart.probePort" -}}
{{- $port := "" -}}
{{- range .Values.service.ports -}}
  {{- if eq $port "" -}}
    {{- $port = (.targetPort | default .port) -}}
  {{- end -}}
{{- end -}}
{{- $port -}}
{{- end -}}

{{/*
Render a container probe (liveness/readiness). Centralizes probe defaulting so
tenant workloads always ship sane health checks without app-specific hardcoding:

  1. An explicit per-workload probe (.Values.deployment.<kind>Probe) always wins.
  2. Otherwise, when a service port exists, a sensible default is synthesized:
       - httpGet on .Values.deployment.probes.httpPath if that path is set
         (admin/values-driven HTTP override), else
       - a tcpSocket probe on the resolved service/container port.
  3. With no service port and no override, nothing is rendered.

Call with a dict: (dict "ctx" $ "probe" <explicitProbe> "kind" "liveness"|"readiness").
Emits at base indent; callers re-indent with `nindent`.
*/}}
{{- define "common-chart.probe" -}}
{{- $ctx := .ctx -}}
{{- $explicit := .probe -}}
{{- if $explicit -}}
{{- toYaml $explicit | trim -}}
{{- else -}}
{{- $port := include "common-chart.probePort" $ctx -}}
{{- if $port -}}
{{- $defaults := $ctx.Values.deployment.probes | default dict -}}
{{- $httpPath := $defaults.httpPath | default "" -}}
{{- if $httpPath -}}
httpGet:
  path: {{ $httpPath | quote }}
  port: {{ $port }}
{{- else -}}
tcpSocket:
  port: {{ $port }}
{{- end }}
initialDelaySeconds: {{ (get $defaults (printf "%sInitialDelaySeconds" .kind)) | default $defaults.initialDelaySeconds | default (ternary 10 5 (eq .kind "liveness")) }}
periodSeconds: {{ $defaults.periodSeconds | default 10 }}
timeoutSeconds: {{ $defaults.timeoutSeconds | default 3 }}
failureThreshold: {{ $defaults.failureThreshold | default 3 }}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Renders a value that contains template.
*/}}
{{- define "common-chart.tplvalues.render" -}}
    {{- if typeIs "string" .value }}
        {{- tpl .value .context }}
    {{- else }}
        {{- tpl (.value | toYaml) .context }}
    {{- end }}
{{- end -}}

{{/*
Renders the body of a BackendConfig `spec` from a backendconfig object passed as
the context (`.`). Shared by `backendconfig.yaml` and `extra-backendconfigs.yaml`
so both stay in sync as GKE BackendConfig fields evolve. Emits at base indent;
callers re-indent with `nindent`.
*/}}
{{- define "common-chart.backendConfigSpec" -}}
{{- with .timeoutSec }}
timeoutSec: {{ . }}
{{- end }}
{{- with .connectionDraining }}
connectionDraining:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .sessionAffinity }}
sessionAffinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .cdn }}
cdn:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .securityPolicy }}
securityPolicy:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .logging }}
logging:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .healthCheck }}
healthCheck:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .iap }}
iap:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .customRequestHeaders }}
customRequestHeaders:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .customResponseHeaders }}
customResponseHeaders:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}