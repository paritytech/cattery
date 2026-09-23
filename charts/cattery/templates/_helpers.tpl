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
Coordination (leader-election) backend: memory (default), mongo, or k8s.
Safe when config.coordination is unset.
*/}}
{{- define "cattery.coordinationBackend" -}}
{{- $coord := .Values.config.coordination | default dict -}}
{{- $coord.backend | default "memory" -}}
{{- end }}

{{/*
"true" when a config.providers[] entry of type kubernetes uses in-cluster
credentials, i.e. creates its runner Jobs in THIS cluster. Providers with a
kubeconfig or server target another cluster and need nothing from this chart.
*/}}
{{- define "cattery.hasInClusterKubernetesProvider" -}}
{{- $found := "" -}}
{{- range (.Values.config.providers | default list) -}}
{{- if and (eq (toString (.type | default "")) "kubernetes") (not .kubeconfig) (not .server) -}}{{- $found = "true" -}}{{- end -}}
{{- end -}}
{{- $found -}}
{{- end }}

{{/*
"true" when the server pod must talk to this cluster's API: the k8s
coordination backend (Leases) or an in-cluster kubernetes tray provider (Jobs).
*/}}
{{- define "cattery.needsKubeApi" -}}
{{- if or (eq (include "cattery.coordinationBackend" .) "k8s") (include "cattery.hasInClusterKubernetesProvider" .) -}}true{{- end -}}
{{- end }}

{{/*
Namespace the runner Jobs (and their RBAC, PodTemplates and ServiceAccount)
live in. Defaults to the release namespace.
*/}}
{{- define "cattery.runnersNamespace" -}}
{{- .Values.runners.namespace | default .Release.Namespace -}}
{{- end }}

{{- define "cattery.runnerServiceAccountName" -}}
{{- default (printf "%s-runner" (include "cattery.fullname" .)) .Values.runners.serviceAccount.name -}}
{{- end }}

{{/*
Refuses to render when an in-cluster kubernetes provider would create its
Jobs in a namespace other than runners.namespace, where this chart puts the
RBAC: the mismatch would only surface as Forbidden errors at the first
scale-up.
*/}}
{{- define "cattery.checkRunnersNamespace" -}}
{{- $ns := include "cattery.runnersNamespace" . -}}
{{- range (.Values.config.providers | default list) -}}
{{- if and (eq (toString (.type | default "")) "kubernetes") (not .kubeconfig) (not .server) -}}
{{- $pns := toString (.namespace | default $.Release.Namespace) -}}
{{- if ne $pns $ns -}}
{{- fail (printf "config.providers[%s].namespace is %q but runners.namespace is %q: the runner RBAC would land in the wrong namespace. Set both to the same value, or set rbac.create=false to manage RBAC yourself." (toString .name) $pns $ns) -}}
{{- end -}}
{{- end -}}
{{- end -}}
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
