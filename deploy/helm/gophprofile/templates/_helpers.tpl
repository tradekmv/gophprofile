{{/*
Expand the name of the chart.
*/}}
{{- define "gophprofile.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some K8s name fields are limited to this (RFC 1123).
*/}}
{{- define "gophprofile.fullname" -}}
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
{{- define "gophprofile.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels — применяются ко всем ресурсам.
Helm best practice: обязательные labels для отслеживания.
*/}}
{{- define "gophprofile.labels" -}}
helm.sh/chart: {{ include "gophprofile.chart" . }}
{{ include "gophprofile.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels — для selector.matchLabels.
*/}}
{{- define "gophprofile.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gophprofile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Component labels — различают server и worker.
*/}}
{{- define "gophprofile.serverLabels" -}}
{{ include "gophprofile.labels" . }}
app.kubernetes.io/component: server
{{- end }}

{{- define "gophprofile.workerLabels" -}}
{{ include "gophprofile.labels" . }}
app.kubernetes.io/component: worker
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "gophprofile.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gophprofile.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image reference — берёт image.tag если задан, иначе appVersion.
*/}}
{{- define "gophprofile.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{- define "gophprofile.workerImage" -}}
{{- $tag := default .Chart.AppVersion .Values.worker.image.tag -}}
{{- printf "%s:%s" .Values.worker.image.repository $tag -}}
{{- end }}

{{/*
Postgres DSN — собирается из config и secret.
*/}}
{{- define "gophprofile.postgresDSN" -}}
{{- $secret := lookup "v1" "Secret" .Release.Namespace "gophprofile-secret" -}}
postgres://{{ .Values.config.postgres.user }}:{{ $secret.data.postgresPassword | default "" | b64dec }}@{{ .Values.config.postgres.host }}:{{ .Values.config.postgres.port }}/{{ .Values.config.postgres.database }}?sslmode={{ .Values.config.postgres.sslmode }}
{{- end }}

{{/*
ServiceAccount name для компонента (server/worker).
*/}}
{{- define "gophprofile.serverServiceAccountName" -}}
{{- include "gophprofile.serviceAccountName" . -}}
{{- end }}
