{{/*
Expand the name of the chart.
*/}}
{{- define "cattery.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
Truncated at 63 chars to stay within Kubernetes DNS name limits.
*/}}
{{- define "cattery.fullname" -}}
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

{{- define "cattery.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "cattery.labels" -}}
helm.sh/chart: {{ include "cattery.chart" . }}
{{ include "cattery.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "cattery.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cattery.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "cattery.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "cattery.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Probe target port: status port if configured separately, otherwise http.
*/}}
{{- define "cattery.probePort" -}}
{{- if .Values.config.server.statusListenAddress -}}
status
{{- else -}}
http
{{- end -}}
{{- end }}
