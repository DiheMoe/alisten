{{/* 资源名后面还要加上 -worker 之类的组件后缀，因此留出 8 个字符。 */}}
{{- define "alisten.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 55 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 55 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "alisten.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "alisten.selectorLabels" -}}
app.kubernetes.io/name: {{ .root.Chart.Name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "alisten.image" -}}
{{- printf "%s:%s" .image.repository (.image.tag | default .root.Chart.AppVersion) -}}
{{- end -}}

{{- define "alisten.secretName" -}}
{{- .Values.existingSecret | default (include "alisten.fullname" .) -}}
{{- end -}}

{{- define "alisten.domain" -}}
{{- $domain := required "domain 不能为空" .Values.domain -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $domain) -}}
{{- fail "domain 必须是小写的主机名，不带协议、端口和路径" -}}
{{- end -}}
{{- $domain -}}
{{- end -}}

{{- define "alisten.secretEnv" -}}
- name: {{ .name }}
  valueFrom:
    secretKeyRef:
      name: {{ include "alisten.secretName" .root }}
      key: {{ .name }}
      {{- if .optional }}
      optional: true
      {{- end }}
{{- end -}}

{{- define "alisten.databaseEnv" -}}
- name: DATABASE_HOST
  value: {{ required "database.host 不能为空" .Values.database.host | quote }}
- name: DATABASE_PORT
  value: {{ .Values.database.port | quote }}
- name: DATABASE_USERNAME
  value: {{ required "database.username 不能为空" .Values.database.username | quote }}
{{ include "alisten.secretEnv" (dict "root" . "name" "DATABASE_PASSWORD") }}
- name: DATABASE_NAME
  value: {{ required "database.name 不能为空" .Values.database.name | quote }}
- name: DATABASE_SSLMODE
  value: {{ .Values.database.sslmode | quote }}
{{- end -}}

{{- define "alisten.redisEnv" -}}
- name: REDIS_HOST
  value: {{ required "redis.host 不能为空" .Values.redis.host | quote }}
- name: REDIS_PORT
  value: {{ .Values.redis.port | quote }}
{{ include "alisten.secretEnv" (dict "root" . "name" "REDIS_PASSWORD" "optional" true) }}
- name: REDIS_DB
  value: {{ .Values.redis.db | quote }}
{{- end -}}

{{- define "alisten.storageEnv" -}}
- name: STORAGE_ENDPOINT_URL
  value: {{ required "storage.endpointUrl 不能为空" .Values.storage.endpointUrl | quote }}
- name: STORAGE_REGION
  value: {{ .Values.storage.region | quote }}
- name: STORAGE_BUCKET_NAME
  value: {{ .Values.storage.bucketName | quote }}
{{ include "alisten.secretEnv" (dict "root" . "name" "STORAGE_ACCESS_KEY_ID") }}
{{ include "alisten.secretEnv" (dict "root" . "name" "STORAGE_SECRET_ACCESS_KEY") }}
{{- end -}}

{{- define "alisten.podSecurityContext" -}}
runAsNonRoot: true
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "alisten.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
{{- end -}}

{{- define "alisten.secretChecksum" -}}
{{- if not .Values.existingSecret -}}
checksum/secret: {{ include (print .Template.BasePath "/secret.yaml") . | sha256sum }}
{{- end -}}
{{- end -}}
