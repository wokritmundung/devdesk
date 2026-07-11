{{- define "rewarm.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "rewarm.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "rewarm.labels" -}}
app.kubernetes.io/name: {{ include "rewarm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
