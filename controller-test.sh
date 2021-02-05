#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.5
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=kbd-controller-test
CONTROLLER_IMAGE=amazeeio/lagoon-builddeploy:test-tag


BUILD_CONTROLLER=true
CONTROLLER_NAMESPACE=lagoon-builddeploy
if [ ! -z "$BUILD_CONTROLLER" ]; then
    CONTROLLER_NAMESPACE=lagoon-kbd-system
fi
CHECK_TIMEOUT=10

NS=drupal-example-install
LBUILD=lagoon-build-7m5zypx
LBUILD2=lagoon-build-8m5zypx

check_controller_log () {
    echo "=========== CONTROLLER LOG ============"
    kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE}
    if $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Build ${1} Failed")
    then
        # build failed, exit 1
        tear_down
        exit 1
    fi
}
check_controller_log_build () {
    echo "=========== CONTROLLER LOG ============"
    kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE}
    if $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Build ${1} Failed")
    then
        # build failed, exit 1
        tear_down
        exit 1
    fi
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
    echo "==> Database provider is running"
}

start_kind () {
    echo "==> Start kind ${KIND_VER}" 

    TEMP_DIR=$(mktemp -d /tmp/cluster-api.XXXX)
    ## configure KinD to talk to our insecure registry
    cat << EOF > ${TEMP_DIR}/kind-config.json
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
# configure a local insecure registry
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."172.17.0.1:5000"]
    endpoint = ["http://172.17.0.1:5000"]
EOF
    ## create the cluster now
    kind create cluster --image kindest/node:${KIND_VER} --name ${KIND_NAME} --config ${TEMP_DIR}/kind-config.json

    kubectl cluster-info --context kind-${KIND_NAME}

    echo "==> Switch kube context to kind" 
    kubectl config use-context kind-${KIND_NAME}

    ## add the bulk storageclass for builds to use
    cat <<EOF | kubectl apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: bulk
provisioner: rancher.io/local-path
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
EOF
}

build_deploy_controller () {
    echo "==> Build and deploy controller"
    make test
    make docker-build IMG=${CONTROLLER_IMAGE}
    kind load docker-image ${CONTROLLER_IMAGE} --name ${KIND_NAME}
    make deploy IMG=${CONTROLLER_IMAGE}

    CHECK_COUNTER=1
    echo "==> Ensure controller is running"
    until $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | grep -q "Running")
    do
    if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
        let CHECK_COUNTER=CHECK_COUNTER+1
        echo "Controller not running yet"
        sleep 5
    else
        echo "Timeout of $CHECK_TIMEOUT for controller startup reached"
        check_controller_log
        tear_down
        echo "================ END ================"
        exit 1
    fi
    done
    echo "==> Controller is running"
}


check_lagoon_build () {

    CHECK_COUNTER=1
    echo "==> Check build progress"
    until $(kubectl -n ${NS} get pods  --no-headers | grep -iq "Running")
    do
    if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
        let CHECK_COUNTER=CHECK_COUNTER+1
        echo "Build not running yet"
        sleep 30
    else
        echo "Timeout of $CHECK_TIMEOUT waiting for build to start reached"
        echo "=========== BUILD LOG ============"
        kubectl -n ${NS} logs ${1} -f
        check_controller_log ${1}
        tear_down
        echo "================ END ================"
        exit 1
    fi
    done
    echo "==> Build running"
    kubectl -n ${NS} logs ${1} -f
}

start_up
start_kind

echo "==> Configure example environment"
echo "====> Install build deploy controllers"
if [ ! -z "$BUILD_CONTROLLER" ]; then
    build_deploy_controller
else
    kubectl create namespace lagoon-builddeploy
    helm repo add lagoon-builddeploy https://raw.githubusercontent.com/amazeeio/lagoon-kbd/main/charts
    helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy lagoon-builddeploy/lagoon-builddeploy \
        --set vars.lagoonTargetName=ci-local-controller-kubernetes \
        --set vars.rabbitPassword=guest \
        --set vars.rabbitUsername=guest \
        --set vars.rabbitHostname=172.17.0.1:5672
fi

echo "====> Install lagoon-remote docker-host"
kubectl create namespace lagoon
helm repo add lagoon-remote https://uselagoon.github.io/lagoon-charts/
## configure the docker-host to talk to our insecure registry
helm upgrade --install -n lagoon lagoon-remote lagoon-remote/lagoon-remote \
    --set dockerHost.registry=172.17.0.1:5000 \
    --set dioscuri.enabled=false
kubectl -n lagoon rollout status deployment docker-host -w

echo "====> Install dbaas-operator"
kubectl create namespace dbaas-operator
helm repo add dbaas-operator https://raw.githubusercontent.com/amazeeio/dbaas-operator/main/charts
helm upgrade --install -n dbaas-operator dbaas-operator dbaas-operator/dbaas-operator
helm upgrade --install -n dbaas-operator mariadbprovider dbaas-operator/mariadbprovider -f test-resources/helm-values-mariadbprovider.yml

sleep 20

echo "==> Trigger a lagoon build using kubectl apply"
kubectl -n $CONTROLLER_NAMESPACE apply -f test-resources/example-project1.yaml
# patch the resource with the controller namespace
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds lagoon-build-7m5zypx --type=merge --patch '{"metadata":{"labels":{"lagoon.sh/controller":"'$CONTROLLER_NAMESPACE'"}}}'
# patch the resource with a random label to bump the controller event filter
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds lagoon-build-7m5zypx --type=merge --patch '{"metadata":{"labels":{"bump":"bump"}}}'
sleep 10
check_lagoon_build ${LBUILD}


echo "==> Trigger a lagoon build using rabbitmq"
echo '
{"properties":{"delivery_mode":2},"routing_key":"ci-local-controller-kubernetes:builddeploy",
  "payload":"{
      \"kind\": \"LagoonBuild\",
        \"apiVersion\": \"lagoon.amazee.io\/v1alpha1\",
        \"metadata\": {
          \"name\": \"lagoon-build-8m5zypx\"
        },
        \"spec\": {
          \"build\": {
            \"ci\": \"true\",
            \"image\": \"amazeeio\/kubectl-build-deploy-dind:latest\",
            \"type\": \"branch\"
          },
          \"gitReference\": \"origin\/install\",
          \"project\": {
            \"name\": \"drupal-example\",
            \"environment\": \"install\",
            \"uiLink\": \"https:\/\/dashboard.amazeeio.cloud\/projects\/project\/project-environment\/deployments\/lagoon-build-8m5zypx\",
            \"routerPattern\": \"install-drupal-example\",
            \"environmentType\": \"production\",
            \"productionEnvironment\": \"install\",
            \"standbyEnvironment\": \"master\",
            \"gitUrl\": \"https:\/\/github.com\/amazeeio\/drupal-example-simple.git\",
            \"deployTarget\": \"kind\",
            \"projectSecret\": \"4d6e7dd0f013a75d62a0680139fa82d350c2a1285f43f867535bad1143f228b1\",
            \"key\": \"LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlDWFFJQkFBS0JnUUNjc1g2RG5KNXpNb0RqQ2R6a1JFOEg2TEh2TDQzaUhsekJLTWo4T1VNV05ZZG5YekdqCkR5Mkp1anQ3ZDNlMTVLeC8zOFo5UzJLdHNnVFVtWi9lUlRQSTdabE1idHRJK250UmtyblZLblBWNzhEeEFKNW8KTGZtQndmdWE2MnlVYnl0cnpYQ2pwVVJrQUlBMEZiR2VqS2Rvd3cxcnZGMzJoZFUzQ3ZIcG5rKzE2d0lEQVFBQgpBb0dCQUkrV0dyL1NDbVMzdCtIVkRPVGtMNk9vdVR6Y1QrRVFQNkVGbGIrRFhaV0JjZFhwSnB3c2NXZFBEK2poCkhnTEJUTTFWS3hkdnVEcEE4aW83cUlMTzJWYm1MeGpNWGk4TUdwY212dXJFNVJydTZTMXJzRDl2R0c5TGxoR3UKK0pUSmViMVdaZFduWFZ2am5LbExrWEV1eUthbXR2Z253Um5xNld5V05OazJ6SktoQWtFQThFenpxYnowcFVuTApLc241K2k0NUdoRGVpRTQvajRtamo1b1FHVzJxbUZWT2pHaHR1UGpaM2lwTis0RGlTRkFyMkl0b2VlK085d1pyCkRINHBkdU5YOFFKQkFLYnVOQ3dXK29sYXA4R2pUSk1TQjV1MW8wMVRHWFdFOGhVZG1leFBBdjl0cTBBT0gzUUQKUTIrM0RsaVY0ektoTlMra2xaSkVjNndzS0YyQmJIby81NXNDUVFETlBJd24vdERja3loSkJYVFJyc1RxZEZuOApCUWpZYVhBZTZEQ3o1eXg3S3ZFSmp1K1h1a01xTXV1ajBUSnpITFkySHVzK3FkSnJQVG9VMDNSS3JHV2hBa0JFCnB3aXI3Vk5pYy9jMFN2MnVLcWNZWWM1a2ViMnB1R0I3VUs1Q0lvaWdGakZzNmFJRDYyZXJwVVJ3S0V6RlFNbUgKNjQ5Y0ZXemhMVlA0aU1iZFREVHJBa0FFMTZXU1A3WXBWOHV1eFVGMGV0L3lFR3dURVpVU2R1OEppSTBHN0tqagpqcVR6RjQ3YkJZc0pIYTRYcWpVb2E3TXgwcS9FSUtRWkJ2NGFvQm42bGFOQwotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQ==\",
            \"monitoring\": {
              \"contact\": \"1234\",
              \"statuspageID\": \"1234\"
            },
            \"variables\": {
              \"project\": \"W10=\",
              \"environment\": \"W10=\"
            },
            \"registry\": \"172.17.0.1:5000\"
          },
          \"branch\": {
            \"name\": \"install\"
          }
        }
      }",
"payload_encoding":"string"
}' >payload.json
curl -s -u guest:guest -H "Accept: application/json" -H "Content-Type:application/json" -X POST -d @payload.json http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish
echo ""
sleep 10
check_lagoon_build ${LBUILD2}

check_controller_log
tear_down
echo "================ END ================"