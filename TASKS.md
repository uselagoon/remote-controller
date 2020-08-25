# Tasks

Tasks are a way for Lagoon to perform actions against an environment.

## Standard Tasks

Standard tasks are just the default tasks that ship with Lagoon, typically Drush commands.

These tasks send a command to a specific service pod (usually CLI) and run the command in it.

The way these work is by using the service to find the current running pod, then getting all the details of the containers in that pod and then starting a new pod using the same details. This ensures that the task will run using all the same tools that would be available in that service.

## Advanced Tasks

Advanced tasks expand on the standard task mechanics. These sorts of tasks are useful if you need to do some work that is outside of the standard functionality of the standard tasks, like creating resources in Kubernetes, or a more advanced task that isn't fully suited to running using Shell scripts.

Advanced tasks get some defaults volume mounts and environment variables mounted into them when they are started.

### Volume Mounts

* `lagoon-deployer` token mounted in it for any tasks that require access to the kubernetes API.
* `lagoon-sshkey` mounted for any tasks that may need to talk to the lagoon api for that specific environment

### Variables

#### `JSON_PAYLOAD`

The JSON_PAYLOAD variable is just a way to pass in a specific JSON payload that your advanced task will consume.
There is no specific structure for this payload as it is dependent on your task. The only thing you need to do is base64 decode it to JSON for your task to consume.

#### `NAMESPACE`

The NAMESPACE variable contains the namespace where the task is running.

#### `PODNAME`

The PODNAME variable contains the name of the pod running the task.

#### `TASK_API_HOST`

The TASK_API_HOST variable contains the Lagoon API url/host information, this can be used to talk to the lagoon API.

#### `TASK_SSH_HOST`

The TASK_SSH_HOST variables contains the ssh host information, this can be used if your task needs to ssh to any other environments (or Lagoon to get an auth token)

#### `TASK_SSH_PORT`

The TASK_SSH_PORT variables contains the ssh port, used with the ssh host.

#### `TASK_DATA_ID`

The TASK_DATA_ID variable can be used in your scripts/tasks if required.

### Custom Image

To create a advanced task, you will need to write a custom task image. There are a few things you need to know to use a custom image though, there are some basics which every image should do. This is vital to the success of running your task.

Then there are some advanced options if you need Lagoon to take an action once the task is completed.

#### Basics
There are some basic things your task needs to do for the task monitor controller to handle your pod cleanly.

Your script or task must:
* `exit 1` on any failure
    * the output will be returned to Lagoon and displayed in the task logs
* `exit 0` on completion
    * the output will be returned to Lagoon and displayed in the task logs
* ensure that your task output does not contain anything too revealing

#### Advanced

If your task needs Lagoon to do something once it is finished, like update a value in the API, then you need to add an annotation to the pod that runs your task. You can do this using the `lagoon-deployer` token and the `NAMESPACE` and `PODNAME` variables that are injected into your pod.

The annotation must be called `lagoon.sh/taskData`, and it needs to contain a JSON payload that Lagoon will use to do the action.

> NOTE: The annotation value must be base64 encoded.

Lagoon will also need to be modified to hand

### Example

One example is the active/standby switch.

The Lagoon side creates a Task, and then crafts the active/standby CRD.
It injects the created CRD into a custom JSON payload, and then sends this task message to the operator.

The custom task image knows how to take this payload and create the CRD it was given using the `lagoon-deployer` token.

The task pod monitors the status of that created CRD and when the CRD status changes, it annotates itself using the PODNAME with a JSON payload that gets sent back for Lagoon to process.