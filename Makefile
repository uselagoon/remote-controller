
# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd"

CONTROLLER_NAMESPACE ?= lagoon-builddeploy

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

OVERRIDE_BUILD_DEPLOY_DIND_IMAGE ?= uselagoon/build-deploy-image:main

INGRESS_VERSION=4.9.1

HARBOR_VERSION=1.14.3

KIND_CLUSTER ?= remote-controller

TIMEOUT = 30m
HELM = helm
KUBECTL = kubectl
JQ = jq

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.29.0
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
ENVTEST_VERSION ?= latest

all: manager

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Build manager binary
.PHONY: manager
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
.PHONY: run
run: generate fmt vet manifests
	go run ./main.go --controller-namespace=${CONTROLLER_NAMESPACE}

# Install CRDs into a cluster
.PHONY: install
install: manifests
	kustomize build config/crd | kubectl apply -f -

.PHONY: outputcrds
outputcrds: manifests
	kustomize build config/crd

.PHONY: uninstall
# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
.PHONY: preview
preview: manifests
	cd config/manager && kustomize edit set image controller=${IMG}
	@export HARBOR_URL="https://registry.$$($(KUBECTL) -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}' || echo 127.0.0.1).nip.io" && \
	echo "OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=${OVERRIDE_BUILD_DEPLOY_DIND_IMAGE}" > config/default/config.properties && \
    echo "HARBOR_URL=$${HARBOR_URL}" >> config/default/config.properties && \
    echo "HARBOR_API=$${HARBOR_URL}/api" >> config/default/config.properties
	OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=${OVERRIDE_BUILD_DEPLOY_DIND_IMAGE} kustomize build config/default
	cp config/default/config.properties.default config/default/config.properties

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
# this is only used locally for development or in the test suite
.PHONY: deploy
deploy: manifests
	cd config/manager && kustomize edit set image controller=${IMG}
	@if kind get clusters | grep -q $(KIND_CLUSTER); then \
		kind export kubeconfig --name=$(KIND_CLUSTER) \
		&& export HARBOR_URL="https://registry.$$($(KUBECTL) -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}').nip.io"; \
	fi && \
	echo "OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=${OVERRIDE_BUILD_DEPLOY_DIND_IMAGE}" > config/default/config.properties && \
    echo "HARBOR_URL=$${HARBOR_URL}" >> config/default/config.properties && \
    echo "HARBOR_API=$${HARBOR_URL}/api" >> config/default/config.properties
	OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=${OVERRIDE_BUILD_DEPLOY_DIND_IMAGE} kustomize build config/default | kubectl apply -f -
	cp config/default/config.properties.default config/default/config.properties

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Build the docker image
docker-build: test
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.2 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

.PHONY: install-metallb
install-metallb:
	LAGOON_KIND_CIDR_BLOCK=$$(docker network inspect $(KIND_CLUSTER) | $(JQ) '. [0].IPAM.Config[0].Subnet' | tr -d '"') && \
	export LAGOON_KIND_NETWORK_RANGE=$$(echo $${LAGOON_KIND_CIDR_BLOCK%???} | awk -F'.' '{print $$1,$$2,$$3,240}' OFS='.')/29 && \
	$(HELM) upgrade \
		--install \
		--create-namespace \
		--namespace metallb-system  \
		--wait \
		--timeout $(TIMEOUT) \
		--version=v0.13.12 \
		metallb \
		metallb/metallb && \
	$$(envsubst < test-resources/test-suite.metallb-pool.yaml.tpl > test-resources/test-suite.metallb-pool.yaml) && \
	$(KUBECTL) apply -f test-resources/test-suite.metallb-pool.yaml \

# cert-manager is used to allow self-signed certificates to be generated automatically by ingress in the same way lets-encrypt would
.PHONY: install-certmanager
install-certmanager: install-metallb
	$(HELM) upgrade \
		--install \
		--create-namespace \
		--namespace cert-manager \
		--wait \
		--timeout $(TIMEOUT) \
		--set installCRDs=true \
		--set ingressShim.defaultIssuerName=lagoon-testing-issuer \
		--set ingressShim.defaultIssuerKind=ClusterIssuer \
		--set ingressShim.defaultIssuerGroup=cert-manager.io \
		--version=v1.11.0 \
		cert-manager \
		jetstack/cert-manager
	$(KUBECTL) apply -f test-resources/test-suite.certmanager-issuer-ss.yaml

# installs ingress-nginx
.PHONY: install-ingress
install-ingress: install-certmanager
	$(HELM) upgrade \
		--install \
		--create-namespace \
		--namespace ingress-nginx \
		--wait \
		--timeout $(TIMEOUT) \
		--set controller.allowSnippetAnnotations=true \
		--set controller.service.type=LoadBalancer \
		--set controller.service.nodePorts.http=32080 \
		--set controller.service.nodePorts.https=32443 \
		--set controller.config.proxy-body-size=0 \
		--set controller.config.hsts="false" \
		--set controller.watchIngressWithoutClass=true \
		--set controller.ingressClassResource.default=true \
		--version=$(INGRESS_VERSION) \
		ingress-nginx \
		ingress-nginx/ingress-nginx

# installs harbor
.PHONY: install-registry
install-registry: install-ingress
	$(HELM) upgrade \
		--install \
		--create-namespace \
		--namespace registry \
		--wait \
		--timeout $(TIMEOUT) \
		--set expose.tls.enabled=true \
		--set expose.tls.certSource=secret \
		--set expose.tls.secret.secretName=harbor-ingress \
		--set expose.ingress.className=nginx \
		--set-string expose.ingress.annotations.kubernetes\\.io/tls-acme=true \
		--set "expose.ingress.hosts.core=registry.$$($(KUBECTL) -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}').nip.io" \
		--set "externalURL=https://registry.$$($(KUBECTL) -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}').nip.io" \
		--set chartmuseum.enabled=false \
		--set clair.enabled=false \
		--set notary.enabled=false \
		--set trivy.enabled=false \
		--version=$(HARBOR_VERSION) \
		registry \
		harbor/harbor

# installs lagoon-remote mainly for the docker-host
.PHONY: install-lagoon-remote
install-lagoon-remote: install-registry
	$(HELM) upgrade \
		--install \
		--create-namespace \
		--namespace lagoon \
		--wait \
		--timeout $(TIMEOUT) \
		--set "lagoon-build-deploy.enabled=false" \
		--set "dockerHost.registry=registry.$$($(KUBECTL) -n ingress-nginx get services ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}').nip.io" \
		--set "dockerHost.storage.size=10Gi" \
		--set "dbaas-operator.enabled=false" \
		lagoon \
		lagoon/lagoon-remote

# .PHONY: create-kind-cluster
# create-kind-cluster:
# 	@if ! kind get clusters | grep -q $(KIND_CLUSTER); then \
# 		docker network inspect $(KIND_CLUSTER) >/dev/null 2>&1 || docker network create $(KIND_CLUSTER) \
# 		&& export KIND_NODE_IP=$$(docker run --network $(KIND_CLUSTER) --rm alpine ip -o addr show eth0 | sed -nE 's/.* ([0-9.]{7,})\/.*/\1/p') \
# 		&& envsubst < test-resources/test-suite.kind-config.yaml.tpl > test-resources/test-suite.kind-config.yaml \
# 		&& kind create cluster --wait=60s --name=$(KIND_CLUSTER) --config=test-resources/test-suite.kind-config.yaml; \
# 	else \
# 		echo "Cluster $(KIND_CLUSTER) already exists"; \
# 	fi

.PHONY: create-kind-cluster
create-kind-cluster:
	docker network inspect $(KIND_CLUSTER) >/dev/null || docker network create $(KIND_CLUSTER) \
		&& LAGOON_KIND_CIDR_BLOCK=$$(docker network inspect $(KIND_CLUSTER) | $(JQ) '. [0].IPAM.Config[0].Subnet' | tr -d '"') \
		&& export KIND_NODE_IP=$$(echo $${LAGOON_KIND_CIDR_BLOCK%???} | awk -F'.' '{print $$1,$$2,$$3,240}' OFS='.') \
 		&& envsubst < test-resources/test-suite.kind-config.yaml.tpl > test-resources/test-suite.kind-config.yaml \
 		&& kind create cluster --wait=60s --name=$(KIND_CLUSTER) --config=test-resources/test-suite.kind-config.yaml

# Create a kind cluster locally and run the test e2e test suite against it
.PHONY: kind/test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up locally
kind/test-e2e: create-kind-cluster install-lagoon-remote kind/re-test-e2e
	
.PHONY: local-kind/test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up locally
kind/re-test-e2e:
	export KIND_CLUSTER=$(KIND_CLUSTER) && \
	kind export kubeconfig --name=$(KIND_CLUSTER) && \
	export HARBOR_VERSION=$(HARBOR_VERSION) && \
	export OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=$(OVERRIDE_BUILD_DEPLOY_DIND_IMAGE) && \
	$(MAKE) test-e2e

.PHONY: clean
kind/clean:
	docker compose down && \
	kind delete cluster --name=$(KIND_CLUSTER) && docker network rm $(KIND_CLUSTER)

# Utilize Kind or modify the e2e tests to load the image locally, enabling compatibility with other vendors.
.PHONY: test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up inside github action.
test-e2e:
	export HARBOR_VERSION=$(HARBOR_VERSION) && \
	export OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=$(OVERRIDE_BUILD_DEPLOY_DIND_IMAGE) && \
	go test ./test/e2e/ -v -ginkgo.v

.PHONY: github/test-e2e
github/test-e2e: install-lagoon-remote test-e2e

.PHONY: kind/set-kubeconfig
kind/set-kubeconfig:
	export KIND_CLUSTER=$(KIND_CLUSTER) && \
	kind export kubeconfig --name=$(KIND_CLUSTER)

.PHONY: kind/logs-remote-controller
kind/logs-remote-controller:
	export KIND_CLUSTER=$(KIND_CLUSTER) && \
	kind export kubeconfig --name=$(KIND_CLUSTER) && \
	kubectl -n remote-controller-system logs -f \
		$$(kubectl -n remote-controller-system  get pod -l control-plane=controller-manager -o jsonpath="{.items[0].metadata.name}") \
		-c manager

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary (ideally with version)
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef