{{/*
Compile all validation checks here.
*/}}
{{- define "omni-exporter.validateValues" -}}
{{- if not .Values.omni.endpoint -}}
{{- fail "omni.endpoint is required (e.g. https://<account>.omni.siderolabs.io)" -}}
{{- end -}}
{{- if and (not .Values.omni.serviceAccountKey.existingSecret) (not .Values.omni.serviceAccountKey.value) -}}
{{- fail "omni.serviceAccountKey: set either .existingSecret or .value" -}}
{{- end -}}
{{- end -}}
