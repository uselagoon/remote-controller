#!/bin/bash

#KIND_VER=v1.13.12
#KIND_VER=v1.14.10
#KIND_VER=v1.15.7
#KIND_VER=v1.16.4
KIND_VER=v1.17.5
# or get the latest tagged version of a specific k8s version of kind
#KIND_VER=$(curl -s https://hub.docker.com/v2/repositories/kindest/node/tags | jq -r '.results | .[].name' | grep 'v1.17' | sort -Vr | head -1)
KIND_NAME=chart-testing
CONTROLLER_IMAGE=uselagoon/remote-controller:test-tag


CONTROLLER_NAMESPACE=remote-controller-system
CHECK_TIMEOUT=20

NS=nginx-example-main
LBUILD=7m5zypx
LBUILD2=8m5zypx

HARBOR_VERSION=${HARBOR_VERSION:-1.6.4}

check_controller_log () {
    echo "=========== CONTROLLER LOG ============"
    kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE}
    if $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Build ${1} Failed")
    then
        # build failed, exit 1
        tear_down
        echo "============== FAILED ==============="
        exit 1
    fi
}

tear_down () {
    echo "============= TEAR DOWN ============="
    echo "==> Get ingress"
    kubectl get ingress --all-namespaces
    echo "==> Get pods"
    kubectl get pods --all-namespaces
    echo "==> Get remote logs (docker-host)"
    kubectl describe pods --namespace=lagoon --selector=app.kubernetes.io/name=lagoon-remote
    kubectl logs --tail=80 --namespace=lagoon --prefix --timestamps --all-containers --selector=app.kubernetes.io/name=lagoon-remote
    echo "==> Remove cluster"
    kind delete cluster --name ${KIND_NAME}
    echo "==> Remove services"
    docker-compose down
}

start_docker_compose_services () {
    echo "================ BEGIN ================"
    echo "==> Bring up local provider"
    docker-compose up -d
    CHECK_COUNTER=1
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
        echo "============== FAILED ==============="
        exit 1
    fi
    done
    echo "==> Database provider is running"
}

install_path_provisioner () {
    echo "==> Install local path provisioner" 
    kubectl apply -f test-resources/local-path-storage.yaml
    echo "==> local path provisioner installed"
    ## add the bulk storageclass for builds to use
    kubectl apply -f test-resources/bulk-storage.yaml
    echo "==> Bulk storage configured"
}

build_deploy_controller () {
    echo "==> Install CRDs and deploy controller"
    make install

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
        echo "============== FAILED ==============="
        exit 1
    fi
    done
    echo "==> Controller is running"
}


check_lagoon_build () {
    CHECK_COUNTER=1
    echo "==> Check build progress"
    until $(kubectl -n ${NS} get pods ${1} --no-headers | grep -iq "Running")
    do
    if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
        let CHECK_COUNTER=CHECK_COUNTER+1
        if $(kubectl -n ${NS} get pods ${1} --no-headers | grep -iq "Error"); then
            echo "Build failed"
            echo "=========== BUILD LOG ============"
            kubectl -n ${NS} logs ${1} -f
            check_controller_log ${1}
            tear_down
            echo "================ END ================"
            echo "============== FAILED ==============="
            exit 1
        fi
        echo "Build not running yet"
        sleep 5
    else
        echo "Timeout of $CHECK_TIMEOUT waiting for build to start reached"
        echo "=========== BUILD LOG ============"
        kubectl -n ${NS} logs ${1} -f
        check_controller_log ${1}
        tear_down
        echo "================ END ================"
        echo "============== FAILED ==============="
        exit 1
    fi
    done
    echo "==> Build running"
    kubectl -n ${NS} logs ${1} -f
}

start_docker_compose_services
install_path_provisioner

echo "==> Install helm-git plugin"
helm plugin install https://github.com/aslafy-z/helm-git 

NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[0].address}')

echo "===> Install Ingress-Nginx"
kubectl create namespace ingress-nginx
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm upgrade --install -n ingress-nginx ingress-nginx ingress-nginx/ingress-nginx -f test-resources/ingress-nginx-values.yaml --version 4.0.16
NUM_PODS=$(kubectl -n ingress-nginx get pods | grep -ow "Running"| wc -l |  tr  -d " ")
if [ $NUM_PODS -ne 1 ]; then
    echo "Install ingress-nginx"
    helm upgrade --install -n ingress-nginx ingress-nginx ingress-nginx/ingress-nginx -f test-resources/ingress-nginx-values.yaml --version 4.0.16
    kubectl get pods --all-namespaces
    echo "Wait for ingress-nginx to become ready"
    sleep 120
else
    echo "===> Ingress-Nginx is running"
fi

echo "===> Install Harbor"
kubectl create namespace harbor
helm repo add harbor https://helm.goharbor.io
helm upgrade --install -n harbor harbor harbor/harbor -f test-resources/harbor-values.yaml --version "${HARBOR_VERSION}" \
    --set externalURL=http://harbor.${NODE_IP}.nip.io:32080 \
    --set expose.ingress.hosts.core=harbor.${NODE_IP}.nip.io

sleep 90

echo "==> Install lagoon-remote docker-host"
helm repo add lagoon-remote https://uselagoon.github.io/lagoon-charts/
## configure the docker-host to talk to our insecure registry

kubectl create namespace lagoon
helm upgrade --install -n lagoon lagoon-remote lagoon-remote/lagoon-remote \
    --set dockerHost.registry=http://harbor.${NODE_IP}.nip.io:32080 \
    --set dockerHost.storage.size=5Gi \
    --set dioscuri.enabled=false \
    --set dbaas-operator.enabled=false
CHECK_COUNTER=1
echo "===> Ensure docker-host is running"
until $(kubectl -n lagoon get pods $(kubectl -n lagoon get pods | grep "lagoon-remote-docker-host" | awk '{print $1}') --no-headers | grep -q "Running")
do
if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Docker host not running yet"
    sleep 5
else
    echo "Timeout of $CHECK_TIMEOUT for controller startup reached"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
echo "===> Docker-host is running"

# echo "====> Install dbaas-operator"
# helm repo add amazeeio https://amazeeio.github.io/charts/
# kubectl create namespace dbaas-operator
# helm upgrade --install -n dbaas-operator dbaas-operator amazeeio/dbaas-operator 
# helm repo add dbaas-operator https://raw.githubusercontent.com/amazeeio/dbaas-operator/main/charts
# helm upgrade --install -n dbaas-operator mariadbprovider dbaas-operator/mariadbprovider -f test-resources/helm-values-mariadbprovider.yml

echo "==> Configure example environment"
echo "====> Install build deploy controllers"
build_deploy_controller

echo "==> Trigger a lagoon build using kubectl apply"
kubectl -n $CONTROLLER_NAMESPACE apply -f test-resources/example-project1.yaml
# patch the resource with the controller namespace
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.crd.lagoon.sh lagoon-build-${LBUILD} --type=merge --patch '{"metadata":{"labels":{"lagoon.sh/controller":"'$CONTROLLER_NAMESPACE'"}}}'
# patch the resource with a random label to bump the controller event filter
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.crd.lagoon.sh lagoon-build-${LBUILD} --type=merge --patch '{"metadata":{"labels":{"bump":"bump"}}}'
sleep 10
check_lagoon_build lagoon-build-${LBUILD}


echo "==> Trigger a lagoon build using rabbitmq"
echo '
{
    "properties":{
        "delivery_mode":2
    },
    "routing_key":"ci-local-controller-kubernetes:builddeploy",
    "payload":"{
        \"metadata\": {
            \"name\": \"lagoon-build-8m5zypx\"
        },
        \"spec\": {
            \"build\": {
                \"ci\": \"true\",
                \"type\": \"branch\"
            },
            \"gitReference\": \"origin\/main\",
            \"project\": {
            \"name\": \"nginx-example\",
            \"environment\": \"main\",
            \"uiLink\": \"https:\/\/dashboard.amazeeio.cloud\/projects\/project\/project-environment\/deployments\/lagoon-build-8m5zypx\",
            \"routerPattern\": \"main-nginx-example\",
            \"environmentType\": \"production\",
            \"productionEnvironment\": \"main\",
            \"standbyEnvironment\": \"\",
            \"gitUrl\": \"https:\/\/github.com\/shreddedbacon\/lagoon-nginx-example.git\",
            \"deployTarget\": \"kind\",
            \"projectSecret\": \"4d6e7dd0f013a75d62a0680139fa82d350c2a1285f43f867535bad1143f228b1\",
            \"key\": \"LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlDWFFJQkFBS0JnUUNjc1g2RG5KNXpNb0RqQ2R6a1JFOEg2TEh2TDQzaUhsekJLTWo4T1VNV05ZZG5YekdqCkR5Mkp1anQ3ZDNlMTVLeC8zOFo5UzJLdHNnVFVtWi9lUlRQSTdabE1idHRJK250UmtyblZLblBWNzhEeEFKNW8KTGZtQndmdWE2MnlVYnl0cnpYQ2pwVVJrQUlBMEZiR2VqS2Rvd3cxcnZGMzJoZFUzQ3ZIcG5rKzE2d0lEQVFBQgpBb0dCQUkrV0dyL1NDbVMzdCtIVkRPVGtMNk9vdVR6Y1QrRVFQNkVGbGIrRFhaV0JjZFhwSnB3c2NXZFBEK2poCkhnTEJUTTFWS3hkdnVEcEE4aW83cUlMTzJWYm1MeGpNWGk4TUdwY212dXJFNVJydTZTMXJzRDl2R0c5TGxoR3UKK0pUSmViMVdaZFduWFZ2am5LbExrWEV1eUthbXR2Z253Um5xNld5V05OazJ6SktoQWtFQThFenpxYnowcFVuTApLc241K2k0NUdoRGVpRTQvajRtamo1b1FHVzJxbUZWT2pHaHR1UGpaM2lwTis0RGlTRkFyMkl0b2VlK085d1pyCkRINHBkdU5YOFFKQkFLYnVOQ3dXK29sYXA4R2pUSk1TQjV1MW8wMVRHWFdFOGhVZG1leFBBdjl0cTBBT0gzUUQKUTIrM0RsaVY0ektoTlMra2xaSkVjNndzS0YyQmJIby81NXNDUVFETlBJd24vdERja3loSkJYVFJyc1RxZEZuOApCUWpZYVhBZTZEQ3o1eXg3S3ZFSmp1K1h1a01xTXV1ajBUSnpITFkySHVzK3FkSnJQVG9VMDNSS3JHV2hBa0JFCnB3aXI3Vk5pYy9jMFN2MnVLcWNZWWM1a2ViMnB1R0I3VUs1Q0lvaWdGakZzNmFJRDYyZXJwVVJ3S0V6RlFNbUgKNjQ5Y0ZXemhMVlA0aU1iZFREVHJBa0FFMTZXU1A3WXBWOHV1eFVGMGV0L3lFR3dURVpVU2R1OEppSTBHN0tqagpqcVR6RjQ3YkJZc0pIYTRYcWpVb2E3TXgwcS9FSUtRWkJ2NGFvQm42bGFOQwotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQ==\",
            \"monitoring\": {
                \"contact\": \"1234\",
                \"statuspageID\": \"1234\"
            },
            \"variables\": {
                \"project\": \"W3sibmFtZSI6IkxBR09PTl9TWVNURU1fUk9VVEVSX1BBVFRFUk4iLCJ2YWx1ZSI6IiR7ZW52aXJvbm1lbnR9LiR7cHJvamVjdH0uZXhhbXBsZS5jb20iLCJzY29wZSI6ImludGVybmFsX3N5c3RlbSJ9XQ==\",
                \"environment\": \"W10=\"
            },
            \"registry\": \"172.17.0.1:5000\"
            },
            \"branch\": {
                \"name\": \"main\"
            }
        }
    }",
    "payload_encoding":"string"
}' >payload.json
curl -s -u guest:guest -H "Accept: application/json" -H "Content-Type:application/json" -X POST -d @payload.json http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish
echo ""
sleep 10
check_lagoon_build lagoon-build-${LBUILD2}


echo "==> Check pod cleanup worked"
CHECK_COUNTER=1
until ! $(kubectl -n nginx-example-main get pods lagoon-build-7m5zypx &> /dev/null)
do
if [ $CHECK_COUNTER -lt 14 ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Build pod not deleted yet"
    sleep 5
else
    echo "Timeout of 70seconds for build pod clean up check"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
echo "==> Pod cleanup output (should only be 1 lagoon-build pod)"
POD_CLEANUP_OUTPUT=$(kubectl -n nginx-example-main get pods | grep "lagoon-build")
echo "${POD_CLEANUP_OUTPUT}"
POD_CLEANUP_COUNT=$(echo "${POD_CLEANUP_OUTPUT}" | wc -l |  tr  -d " ")
if [ $POD_CLEANUP_COUNT -gt 1 ]; then
    echo "There is more than 1 build pod left, there should only be 1"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi


echo "==> Check robot credential rotation worked"
CHECK_COUNTER=1
until $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Robot credentials rotated for")
do
if [ $CHECK_COUNTER -lt 20 ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Credentials not rotated yet"
    sleep 5
else
    echo "Timeout of 100seconds for robot credential rotation check"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep "handlers.RotateRobotCredentials"

echo "==> Delete the environment"
echo '
{"properties":{"delivery_mode":2},"routing_key":"ci-local-controller-kubernetes:remove",
    "payload":"{
        \"projectName\": \"nginx-example\",
        \"type\":\"branch\",
        \"forceDeleteProductionEnvironment\":true,
        \"branch\":\"main\",
        \"openshiftProjectName\":\"nginx-example-main\"
    }",
"payload_encoding":"string"
}' >payload.json
curl -s -u guest:guest -H "Accept: application/json" -H "Content-Type:application/json" -X POST -d @payload.json http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish
echo ""
CHECK_COUNTER=1
until $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Deleted namespace nginx-example-main for project nginx-example, branch main")
do
if [ $CHECK_COUNTER -lt 20 ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Environment not deleted yet"
    sleep 5
else
    echo "Timeout of 100seconds for environment to be deleted"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep "handlers.LagoonTasks.Deletion"

check_controller_log
tear_down
echo "================ END ================"