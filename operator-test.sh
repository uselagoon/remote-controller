#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.5
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=kbd-operator-test
OPERATOR_IMAGE=amazeeio/lagoon-builddeploy:test-tag
OPERATOR_NAMESPACE=lagoon-kbd-system
CHECK_TIMEOUT=60

check_operator_log () {
  echo "=========== OPERATOR LOG ============"
  kubectl logs $(kubectl get pods  -n ${OPERATOR_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${OPERATOR_NAMESPACE}
}

tear_down () {
  echo "============= TEAR DOWN ============="
  kind delete cluster --name ${KIND_NAME}
  docker-compose down
}

start_up () {
  echo "================ BEGIN ================"
  echo "==> Bring up local provider"
  docker-compose up -d
  CHECK_COUNTER=1
  echo "==> Ensure mariadb database provider is running"
  mariadb_start_check
}

mariadb_start_check () {
  until $(docker-compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Database provider not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for database provider startup reached"
    exit 1
  fi
  done
}

start_kind () {
  echo "==> Start kind ${KIND_VER}" 
  kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME}
  kubectl cluster-info --context kind-${KIND_NAME}

  echo "==> Switch kube context to kind" 
  kubectl config use-context kind-${KIND_NAME}
}

build_deploy_operator () {
  echo "==> Build and deploy operator"
  make test
  make docker-build IMG=${OPERATOR_IMAGE}
  kind load docker-image ${OPERATOR_IMAGE} --name ${KIND_NAME}
  make deploy IMG=${OPERATOR_IMAGE}

  CHECK_COUNTER=1
  echo "==> Ensure operator is running"
  until $(kubectl get pods  -n ${OPERATOR_NAMESPACE} --no-headers | grep -q "Running")
  do
  if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Operator not running yet"
    sleep 5
  else
    echo "Timeout of $CHECK_TIMEOUT for operator startup reached"
    check_operator_log
    exit 1
  fi
  done
}

start_up
start_kind
# build_deploy_operator

echo "==> Add a provider"
kubectl create namespace lagoon-builddeploy
helm repo add lagoon-builddeploy https://raw.githubusercontent.com/amazeeio/lagoon-kbd/main/charts
helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy lagoon-builddeploy/lagoon-builddeploy \
	--set vars.lagoonTargetName=ci-local-operator-k8s \
	--set vars.rabbitPassword=guest \
	--set vars.rabbitUsername=guest \
	--set vars.rabbitHostname=172.17.0.1:5672
kubectl create namespace dbaas-operator
helm repo add dbaas-operator https://raw.githubusercontent.com/amazeeio/dbaas-operator/master/charts
helm upgrade --install -n dbaas-operator dbaas-operator dbaas-operator/dbaas-operator
helm upgrade --install -n dbaas-operator mariadbprovider dbaas-operator/mariadbprovider -f test-resources/helm-values-mariadbprovider.yml
kubectl -n default apply -f test-resources/example-project1.yaml

sleep 60

check_operator_log
tear_down
echo "================ END ================"