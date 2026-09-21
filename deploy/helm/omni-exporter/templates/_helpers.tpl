{{/*
Expand the name of the chart.
*/}}
{{- define "omni-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name.
*/}}
{{- define "omni-exporter.fullname" -}}
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

{{- define "omni-exporter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "omni-exporter.labels" -}}
helm.sh/chart: {{ include "omni-exporter.chart" . }}
{{ include "omni-exporter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "omni-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "omni-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "omni-exporter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "omni-exporter.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Fully qualified container image reference.
*/}}
{{- define "omni-exporter.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/*
Name of the Secret holding the service account key.
*/}}
{{- define "omni-exporter.keySecretName" -}}
{{- if .Values.omni.serviceAccountKey.existingSecret -}}
{{- .Values.omni.serviceAccountKey.existingSecret -}}
{{- else -}}
{{- include "omni-exporter.fullname" . -}}
{{- end -}}
{{- end -}}

{{/*
Key within the service account key Secret.
*/}}
{{- define "omni-exporter.keySecretKey" -}}
{{- if .Values.omni.serviceAccountKey.existingSecret -}}
{{- .Values.omni.serviceAccountKey.existingSecretKey -}}
{{- else -}}
OMNI_SERVICE_ACCOUNT_KEY
{{- end -}}
{{- end -}}
