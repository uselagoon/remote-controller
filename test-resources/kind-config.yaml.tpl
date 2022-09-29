kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."${KIND_NODE_IP}:5000"]
    endpoint = ["http://${KIND_NODE_IP}:5000"]
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."harbor.${KIND_NODE_IP}.nip.io:32080"]
    endpoint = ["http://harbor.${KIND_NODE_IP}.nip.io:32080"]
  [plugins."io.containerd.grpc.v1.cri".registry.configs."harbor.${KIND_NODE_IP}.nip.io:32443".tls]
    insecure_skip_verify = true
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 32080
    hostPort: 32080
    protocol: TCP
  - containerPort: 32443
    hostPort: 32443
    protocol: TCP