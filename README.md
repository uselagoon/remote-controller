# Lagoon Remote Controller

[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/10582/badge)](https://www.bestpractices.dev/projects/10582)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/uselagoon/remote-controller/badge)](https://securityscorecards.dev/viewer/?uri=github.com/uselagoon/remote-controller)
[![coverage](https://raw.githubusercontent.com/uselagoon/remote-controller/badges/.badges/main/coverage.svg)](https://github.com/uselagoon/remote-controller/actions/workflows/coverage.yaml)

This project is comprised of controllers responsible for handling Kubernetes and Openshift build deploy and removal of environments for Lagoon.
It also handles Lagoon tasks that are triggered via the Lagoon UI, and also more advanced tasks that Lagoon can leverage.

## Install

See [lagoon-charts](https://github.com/uselagoon/lagoon-charts)

## Usage

The controllers have the ability to start with and without messaging queue support.

### With MQ

This is the preferred way to be installed, it reads messages from dedicated queues that are sent from Lagoon. 
The recieved message contains everything that the LagoonBuild or LagoonTask spec needs to start doing the work in the destination cluster.

### Without MQ

This is handy for testing scenarios, a [K3D](https://github.com/rancher/k3d) or [KinD](https://github.com/kubernetes-sigs/kind) can be started locally and the controllers installed into it.
A user can then define or craft a LagoonBuild or LagoonTask spec manually and apply it to the local cluster.
There is currently no documentation for how to do this, we may release more information on how to do this after some more testing has been done.

## Docker-host scaling

The controller can scale a `LagoonDockerHost` resource (`crd.lagoon.sh/v1alpha1`) up and down with the length of the build queue, so the number of concurrent builds supported grows with the number of docker-hosts available.

The resource to scale is named by `--docker-host-scale-name` (env `DOCKERHOST_SCALE_NAME`, default `lagoon-remote-docker-host`) and is looked up in the `--docker-host-namespace` namespace. Only `spec.replicas` is changed, and it is always kept between the resource's `spec.minReplicas` and `spec.maxReplicas`. If the resource or the CRD does not exist, scaling is skipped and the QoS limits are unchanged. Set the name to an empty string to disable scaling.

### Scaling up

The `--qos-max-builds` and `--qos-total-builds` limits describe the capacity of a docker-host pool running `minReplicas` docker-hosts. One replica is added for every `multiplier * qos-max-builds` builds in the queue, where the multiplier is set by `--docker-host-scale-multiplier` (env `DOCKERHOST_SCALE_MULTIPLIER`, default `3`). A value of 0 or less disables scaling up.

For example, with `--qos-max-builds=10`, `minReplicas: 1`, `maxReplicas: 4` and the default multiplier, a queue of 30 builds scales to 2 replicas, 60 to 3, and 90 or more to 4.

### QoS limits

Once extra docker-hosts are ready, both the `qos-max-builds` and `qos-total-builds` limits are multiplied by `readyReplicas / minReplicas`, so the queue is processed faster. In the example above, 2 ready docker-hosts raise `qos-max-builds` from 10 to 20. The ready count is used rather than the requested replicas, so builds are only admitted once a new docker-host is serving. Builds are still spread across the docker-hosts, with each host handling about the original `qos-max-builds` builds.

### Scaling down

The docker-host is held at its current size while builds are queued or running, so hosts in use are not removed. When the queue is empty and no builds are running, `spec.replicas` is set back to `minReplicas`.

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `--docker-host-scale-name` | `DOCKERHOST_SCALE_NAME` | `lagoon-remote-docker-host` | Name of the `LagoonDockerHost` to scale |
| `--docker-host-scale-multiplier` | `DOCKERHOST_SCALE_MULTIPLIER` | `3` | Scale up one replica per `multiplier * qos-max-builds` queued builds |
