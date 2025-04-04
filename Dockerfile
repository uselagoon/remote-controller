# Build the manager binary
FROM golang:1.23-alpine AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/metrics internal/metrics
COPY internal/utilities internal/utilities
COPY internal/harbor internal/harbor
COPY internal/helpers internal/helpers
COPY internal/messenger internal/messenger
COPY internal/dockerhost internal/dockerhost
COPY internal/controllers internal/controllers

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} GO111MODULE=on go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER nonroot:nonroot

ENTRYPOINT ["/manager"]
