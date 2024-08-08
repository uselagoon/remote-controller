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
LBUILD3=9m5zypx
LBUILD4=1m5zypx

LATEST_CRD_VERSION=v1beta2

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
    docker compose down
}

start_docker_compose_services () {
    echo "================ BEGIN ================"
    echo "==> Bring up local provider"
    docker compose up -d
    CHECK_COUNTER=1
}

mariadb_start_check () {
    until $(docker compose exec -T mysql mysql --host=local-dbaas-mariadb-provider --port=3306 -uroot -e 'show databases;' | grep -q "information_schema")
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

    # set the images into config.properties so that kustomize can read them
    echo "OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=$OVERRIDE_BUILD_DEPLOY_DIND_IMAGE" > config/default/config.properties
    echo "HARBOR_URL=$HARBOR_URL" >> config/default/config.properties
    echo "HARBOR_API=$HARBOR_API" >> config/default/config.properties

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

clean_task_test_resources() {
  kubectl -n $NS delete -f test-resources/dynamic-secret-in-task-project1-secret.yaml
  kubectl -n $NS delete -f test-resources/dynamic-secret-in-task-project1.yaml
}

wait_for_task_pod_to_complete () {
    POD_NAME=${1}
    CHECK_COUNTER=1
    echo "==> Check task progress"
    until $(kubectl -n ${NS} get pods ${1} --no-headers | grep -iq "Completed")
    do
    echo "=====> Pods in ns ${NS}:"
    kubectl -n ${NS} get pods ${1} --no-headers
    if [ $CHECK_COUNTER -lt $CHECK_TIMEOUT ]; then
        let CHECK_COUNTER=CHECK_COUNTER+1
        echo "==> Task not completed yet"
        sleep 5
    else
        echo "Timeout of $CHECK_TIMEOUT waiting for task to complete"
        echo "=========== TASK LOG ============"
        kubectl -n ${NS} logs ${1} -f
        clean_task_test_resources
        check_controller_log ${1}
        tear_down
        echo "================ END ================"
        echo "============== FAILED ==============="
        exit 1
    fi
    done
    echo "==> Task completed"
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

echo "==> Trigger a lagoon build using kubectl apply and check organization labels exist"
kubectl -n $CONTROLLER_NAMESPACE apply -f test-resources/example-project2.yaml
# patch the resource with the controller namespace
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.crd.lagoon.sh lagoon-build-${LBUILD2} --type=merge --patch '{"metadata":{"labels":{"lagoon.sh/controller":"'$CONTROLLER_NAMESPACE'"}}}'
# patch the resource with a random label to bump the controller event filter
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.crd.lagoon.sh lagoon-build-${LBUILD2} --type=merge --patch '{"metadata":{"labels":{"bump":"bump"}}}'
sleep 10
check_lagoon_build lagoon-build-${LBUILD2}
echo "==> Check organization.lagoon.sh/name label exists on namespace"
if ! $(kubectl get namespace -l 'organization.lagoon.sh/name=test-org' --no-headers 2> /dev/null | grep -q ${NS}); then 
    echo "==> Build failed to set organization name label on namespace"
    clean_task_test_resources
    check_controller_log ${1}
    tear_down
    echo "============== FAILED ==============="
    exit 1
else
    echo "===> label exists"
fi
echo "==> Check organization.lagoon.sh/id label exists on namespace"
if ! $(kubectl get namespace -l 'organization.lagoon.sh/id=123' --no-headers 2> /dev/null | grep -q ${NS}); then 
    echo "==> Build failed to set organization id label on namespace"
    clean_task_test_resources
    check_controller_log ${1}
    tear_down
    echo "============== FAILED ==============="
    exit 1
else
    echo "===> label exists"
fi

echo "==> deprecated v1beta1 api: Trigger a lagoon build using kubectl apply"
kubectl -n $CONTROLLER_NAMESPACE apply -f test-resources/example-project3.yaml
# patch the resource with the controller namespace
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.v1beta1.crd.lagoon.sh lagoon-build-${LBUILD4} --type=merge --patch '{"metadata":{"labels":{"lagoon.sh/controller":"'$CONTROLLER_NAMESPACE'"}}}'
# patch the resource with a random label to bump the controller event filter
kubectl -n $CONTROLLER_NAMESPACE patch lagoonbuilds.v1beta1.crd.lagoon.sh lagoon-build-${LBUILD4} --type=merge --patch '{"metadata":{"labels":{"bump":"bump"}}}'
sleep 10
check_lagoon_build lagoon-build-${LBUILD4}

echo "==> Trigger a Task using kubectl apply to test dynamic secret mounting"

kubectl -n $NS apply -f test-resources/dynamic-secret-in-task-project1-secret.yaml
kubectl -n $NS apply -f test-resources/dynamic-secret-in-task-project1.yaml
kubectl -n $NS patch lagoontasks.crd.lagoon.sh lagoon-advanced-task-example-task-project-1 --type=merge --patch '{"metadata":{"labels":{"lagoon.sh/controller":"'$CONTROLLER_NAMESPACE'"}}}'
kubectl -n $NS patch lagoontasks.crd.lagoon.sh lagoon-advanced-task-example-task-project-1 --type=merge --patch '{"metadata":{"labels":{"bump":"bump"}}}'
#kubectl get lagoontasks lagoon-advanced-task-example-task-project-1 -n $NS -o yaml

# wait on pod creation
wait_for_task_pod_to_complete lagoon-advanced-task-example-task-project-1
VMDATA=$(kubectl get pod -n $NS lagoon-advanced-task-example-task-project-1 -o jsonpath='{.spec.containers[0].volumeMounts}' | jq -r '.[] | select(.name == "dynamic-test-dynamic-secret") | .mountPath')

if [ ! "$VMDATA" = "/var/run/secrets/lagoon/dynamic/test-dynamic-secret" ]; then
    echo "==> Task failed to mount dynamic secret"
    clean_task_test_resources
    check_controller_log ${1}
    tear_down
    echo "============== FAILED ==============="
    exit 1
  else
    echo "==> Dynamic secret mounting into tasks good"
    clean_task_test_resources
fi


echo "==> Trigger a lagoon build using rabbitmq"
echo '
{
    "properties":{
        "delivery_mode":2
    },
    "routing_key":"ci-local-controller-kubernetes:builddeploy",
    "payload":"{
        \"metadata\": {
            \"name\": \"lagoon-build-9m5zypx\"
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
                \"uiLink\": \"https:\/\/dashboard.amazeeio.cloud\/projects\/project\/project-environment\/deployments\/lagoon-build-9m5zypx\",
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
                }
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
check_lagoon_build lagoon-build-${LBUILD3}

echo "==> Check pod cleanup worked"
CHECK_COUNTER=1
# wait for first build pod to clean up
until ! $(kubectl -n nginx-example-main get pods lagoon-build-${LBUILD} &> /dev/null)
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
CHECK_COUNTER=1
# wait for second build pod to clean up
until ! $(kubectl -n nginx-example-main get pods lagoon-build-${LBUILD2} &> /dev/null)
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

# only check
POD_CLEANUP_OUTPUT=$(kubectl -n nginx-example-main get pods -l crd.lagoon.sh/version=${LATEST_CRD_VERSION} | grep "lagoon-build")
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

# install the k8upv1alpha1 crds for first test
kubectl apply -f test-resources/k8upv1alpha1-crds.yaml
sleep 5

echo "==> Trigger a lagoon restore using rabbitmq"
echo '
{"properties":{"delivery_mode":2},"routing_key":"ci-local-controller-kubernetes:misc",
    "payload":"{
        \"misc\":{
            \"miscResource\":\"eyJhcGlWZXJzaW9uIjoiYmFja3VwLmFwcHVpby5jaC92MWFscGhhMSIsImtpbmQiOiJSZXN0b3JlIiwibWV0YWRhdGEiOnsibmFtZSI6InJlc3RvcmUtYmYwNzJhMC11cXhxbzMifSwic3BlYyI6eyJzbmFwc2hvdCI6ImJmMDcyYTA5ZTE3NzI2ZGE1NGFkYzc5OTM2ZWM4NzQ1NTIxOTkzNTk5ZDQxMjExZGZjOTQ2NmRmZDViYzMyYTUiLCJyZXN0b3JlTWV0aG9kIjp7InMzIjp7fX0sImJhY2tlbmQiOnsiczMiOnsiYnVja2V0IjoiYmFhcy1uZ2lueC1leGFtcGxlIn0sInJlcG9QYXNzd29yZFNlY3JldFJlZiI6eyJrZXkiOiJyZXBvLXB3IiwibmFtZSI6ImJhYXMtcmVwby1wdyJ9fX19\"
        },
        \"key\":\"deploytarget:restic:backup:restore\",
        \"environment\":{
            \"name\":\"main\",
            \"openshiftProjectName\":\"nginx-example-main\"
        },
        \"project\":{
            \"name\":\"nginx-example\"
        },
        \"advancedTask\":{}
    }",
"payload_encoding":"string"
}' >payload.json
curl -s -u guest:guest -H "Accept: application/json" -H "Content-Type:application/json" -X POST -d @payload.json http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish
echo ""
sleep 10
CHECK_COUNTER=1
kubectl -n nginx-example-main get restores
until $(kubectl -n nginx-example-main get restores restore-bf072a0-uqxqo3 &> /dev/null)
do
if [ $CHECK_COUNTER -lt 14 ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Restore not created yet"
    sleep 5
else
    echo "Timeout of 70seconds for restore to be created"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
kubectl -n nginx-example-main get restores restore-bf072a0-uqxqo3 -o yaml | kubectl-neat > test-resources/results/k8upv1alpha1-cluster.yaml
if cmp --silent -- "test-resources/results/k8upv1alpha1.yaml" "test-resources/results/k8upv1alpha1-cluster.yaml"; then
    echo "Resulting restores match"
else
    echo "Files don't match"
    echo "============"
    cat test-resources/results/k8upv1alpha1.yaml
    echo "============"
    cat test-resources/results/k8upv1alpha1-cluster.yaml
    echo "============"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi

# install the k8upv1 crds for testing
kubectl apply -f test-resources/k8upv1-crds.yaml
sleep 5

echo "==> Trigger a lagoon restore using rabbitmq"
echo '
{"properties":{"delivery_mode":2},"routing_key":"ci-local-controller-kubernetes:misc",
    "payload":"{
        \"misc\":{
            \"miscResource\":\"eyJtZXRhZGF0YSI6eyJuYW1lIjoicmVzdG9yZS1iZjA3MmEwLXVxeHFvNCJ9LCJzcGVjIjp7InNuYXBzaG90IjoiYmYwNzJhMDllMTc3MjZkYTU0YWRjNzk5MzZlYzg3NDU1MjE5OTM1OTlkNDEyMTFkZmM5NDY2ZGZkNWJjMzJhNSIsInJlc3RvcmVNZXRob2QiOnsiczMiOnt9fSwiYmFja2VuZCI6eyJzMyI6eyJidWNrZXQiOiJiYWFzLW5naW54LWV4YW1wbGUifSwicmVwb1Bhc3N3b3JkU2VjcmV0UmVmIjp7ImtleSI6InJlcG8tcHciLCJuYW1lIjoiYmFhcy1yZXBvLXB3In19fX0=\"
        },
        \"key\":\"deploytarget:restic:backup:restore\",
        \"environment\":{
            \"name\":\"main\",
            \"openshiftProjectName\":\"nginx-example-main\"
        },
        \"project\":{
            \"name\":\"nginx-example\"
        },
        \"advancedTask\":{}
    }",
"payload_encoding":"string"
}' >payload.json
curl -s -u guest:guest -H "Accept: application/json" -H "Content-Type:application/json" -X POST -d @payload.json http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish
echo ""
sleep 10
CHECK_COUNTER=1
kubectl -n nginx-example-main get restores.k8up.io
until $(kubectl -n nginx-example-main get restores.k8up.io restore-bf072a0-uqxqo4 &> /dev/null)
do
if [ $CHECK_COUNTER -lt 14 ]; then
    let CHECK_COUNTER=CHECK_COUNTER+1
    echo "Restore not created yet"
    sleep 5
else
    echo "Timeout of 70seconds for restore to be created"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi
done
kubectl -n nginx-example-main get restores.k8up.io restore-bf072a0-uqxqo4 -o yaml | kubectl-neat > test-resources/results/k8upv1-cluster.yaml
if cmp --silent -- "test-resources/results/k8upv1.yaml" "test-resources/results/k8upv1-cluster.yaml"; then
    echo "Resulting restores match"
else
    echo "Files don't match"
    echo "============"
    cat test-resources/results/k8upv1.yaml
    echo "============"
    cat test-resources/results/k8upv1-cluster.yaml
    echo "============"
    check_controller_log
    tear_down
    echo "================ END ================"
    echo "============== FAILED ==============="
    exit 1
fi

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
until $(kubectl logs $(kubectl get pods  -n ${CONTROLLER_NAMESPACE} --no-headers | awk '{print $1}') -c manager -n ${CONTROLLER_NAMESPACE} | grep -q "Deleted namespace nginx-example-main for project nginx-example, environment main")
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