apiVersion: {{.apiVersion}}
kind: {{.kind}}
metadata:
  name: {{.metadata.name}}
  namespace: {{.metadata.namespace}}
spec:
  backend:
    repoPasswordSecretRef:
      key: {{.spec.backend.repoPasswordSecretRef.key}}
      name: {{.spec.backend.repoPasswordSecretRef.name}}
    s3:
      bucket: {{.spec.backend.s3.bucket}}
  snapshot: {{.spec.snapshot}}