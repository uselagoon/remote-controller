# Lagoon Build Deploy Controllers

This project is comprised of controllers responsible for handling Kubernetes and Openshift build deploy and removal of environments for Lagoon.
It also handles Lagoon tasks that are triggered via the Lagoon UI, and also more advanced tasks that Lagoon can leverage.

## Usage

The controllers have the ability to start with and without messaging queue support.

### With MQ

This is the preferred way to be installed, it reads messages from dedicated queues that are sent from Lagoon. 
The recieved message contains everything that the LagoonBuild or LagoonTask spec needs to start doing the work in the destination cluster.

### Without MQ

This is handy for testing scenarios, a [K3D](https://github.com/rancher/k3d) or [KinD](https://github.com/kubernetes-sigs/kind) can be started locally and the controllers installed into it.
A user can then define or craft a LagoonBuild or LagoonTask spec manually and apply it to the local cluster.
There is currently no documentation for how to do this, we may release more information on how to do this after some more testing has been done.

### Install with Helm

Using [Helm 3](https://helm.sh/docs/intro/install/)

```
helm repo add lagoon-builddeploy https://raw.githubusercontent.com/uselagoon/remote-controller/main/charts

## with rabbitmq support for communicating with a lagoon messaging queue
helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy lagoon-builddeploy/lagoon-builddeploy \
    --set vars.lagoonTargetName=${LAGOON_TARGET_NAME} \
    --set vars.rabbitUsername=${RABBITMQ_USERNAME} \
    --set vars.rabbitPassword=${RABBITMQ_PASSWORD} \
    --set vars.rabbitHostname=${RABBITMQ_HOSTNAME}

## without rabbitmq support for deploying lagoon projects without using lagoon
helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy lagoon-builddeploy/lagoon-builddeploy \
    --set vars.lagoonTargetName=${LAGOON_TARGET_NAME} \
    --set extraArgs.enable-message-queue=false
```

### Install without Helm

You will need to install any prerequisites for kubebuilder [see here](https://book.kubebuilder.io/quick-start.html#prerequisites)

```
# build and push image to dockerhub
./build-push latest

# install any requirements
make install
# deploy the actual handler
make IMG=uselagoon/remote-controller:latest deploy
```

## LagoonBuild Spec

```
kind: LagoonBuild
apiVersion: lagoon.amazee.io/v1alpha1
metadata:
    name: lagoon-build-7m5zypx
spec:
    build:
        ci: 'false' # this is a string, not a bool
        image: uselagoon/kubectl-build-deploy-dind:v1.8.1
        type: branch
    gitReference: origin/main
    openshift: false
    project:
        name: active-standby-example
        environment: main
        uiLink: https://dashboard.amazeeio.cloud/projects/project/project-environment/deployments/lagoon-build-ysxf3a
        routerPattern: 'main-active-standby-example'
        environmentType: production
        productionEnvironment: main
        standbyEnvironment: main2
        gitUrl: git@github.com:shreddedbacon/active-standby-example.git
        deployTarget: KUBERNETES(openshiftName)
        projectSecret: 59890e9ee6f19eafcabb23233ff1f94dc94d93ad98dbaa40570e6f7d50f0bb4b
        key: <BASE64ENCODED KEY>
        monitoring:
            contact: HA-OR-SA
            statuspageID: 'statuspageid'
        variables:
            project: <BASE64ENCODED JSON>
            environment: <BASE64ENCODED JSON>
        registry: registry.myprivate.com:443
# 1 of these must be defined depending on if it is a branch/pr/promote task
    branch: #optional
        name: master2
    pullrequest: #optional
        headBranch: A
        headSha: A
        baseBranch: B
        baseSha: B
        pullrequestTitle: "My PR"
        pullrequestNumber: 1234
    promote: #optional
        promoteSourceEnvironment: C
        promoteSourceProject: projectb-c
```