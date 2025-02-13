kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.configs."registry.${KIND_NODE_IP}.nip.io".tls]
    insecure_skip_verify = true
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."registry.${KIND_NODE_IP}.nip.io"]
    endpoint = ["http://registry.${KIND_NODE_IP}.nip.io"]