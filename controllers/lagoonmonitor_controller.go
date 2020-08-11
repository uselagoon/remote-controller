/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/handlers"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// LagoonMonitorReconciler reconciles a LagoonBuild object
type LagoonMonitorReconciler struct {
	client.Client
	Log       logr.Logger
	Scheme    *runtime.Scheme
	EnableMQ  bool
	Messaging *handlers.Messaging
}

// slice of the different failure states of pods that we care about
// if we observe these on a pending pod, fail the build and get the logs
var failureStates = []string{
	"CrashLoopBackOff",
	"ImagePullBackOff",
}

// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonMonitorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)

	var jobPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &jobPod); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	if jobPod.ObjectMeta.Labels["lagoon.sh/jobType"] == "task" {
		if jobPod.ObjectMeta.DeletionTimestamp.IsZero() {
			// get the task associated to this pod, we wil need update it at some point
			var lagoonTask lagoonv1alpha1.LagoonTask
			err := r.Get(ctx, types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/taskName"],
			}, &lagoonTask)
			if err != nil {
				return ctrl.Result{}, err
			}
			if jobPod.Status.Phase == corev1.PodPending {
				opLog.Info(fmt.Sprintf("Task %s is %v", jobPod.ObjectMeta.Name, jobPod.Status.Phase))
				for _, container := range jobPod.Status.ContainerStatuses {
					fmt.Println(container.Name)
					if container.State.Waiting != nil && containsString(failureStates, container.State.Waiting.Reason) {
						// if we have a failure state, then fail the build and get the logs from the container
						opLog.Info(fmt.Sprintf("Task failed, container exit reason was: %v", container.State.Waiting.Reason))
						lagoonTask.Labels["lagoon.sh/taskStatus"] = string(lagoonv1alpha1.JobFailed)
						if err := r.Update(ctx, &lagoonTask); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Marked task %s as %s", lagoonTask.ObjectMeta.Name, string(lagoonv1alpha1.JobFailed)))
						if err := r.Delete(ctx, &jobPod); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Deleted failed task pod: %s", jobPod.ObjectMeta.Name))
						// update the status to failed on the deleted pod
						// and set the terminate time to now, it is used when we update the deployment and environment
						jobPod.Status.Phase = corev1.PodFailed
						state := corev1.ContainerStatus{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: time.Now().UTC()},
								},
							},
						}
						jobPod.Status.ContainerStatuses[0] = state
						r.updateTaskStatusCondition(ctx, &lagoonTask, lagoonv1alpha1.LagoonConditions{
							Type:   lagoonv1alpha1.JobFailed,
							Status: corev1.ConditionTrue,
						}, []byte(container.State.Waiting.Message))
						// send any messages to lagoon message queues
						r.taskStatusLogsToLagoonLogs(&lagoonTask, &jobPod, nil)
						r.updateLagoonTask(&lagoonTask, &jobPod, nil)
						logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
						r.taskLogsToLagoonLogs(&lagoonTask, &jobPod, []byte(logMsg))
						return ctrl.Result{}, nil
					}
				}
			}
			if jobPod.Status.Phase == corev1.PodFailed || jobPod.Status.Phase == corev1.PodSucceeded {
				// get the task associated to this pod, we wil need update it at some point
				var lagoonTask lagoonv1alpha1.LagoonTask
				err := r.Get(ctx, types.NamespacedName{
					Namespace: jobPod.ObjectMeta.Namespace,
					Name:      jobPod.ObjectMeta.Labels["lagoon.sh/taskName"],
				}, &lagoonTask)
				if err != nil {
					return ctrl.Result{}, err
				}
				var jobCondition lagoonv1alpha1.JobConditionType
				switch jobPod.Status.Phase {
				case corev1.PodFailed:
					jobCondition = lagoonv1alpha1.JobFailed
				case corev1.PodSucceeded:
					jobCondition = lagoonv1alpha1.JobComplete
				}
				// if the build status doesn't equal the status of the pod
				// then update the build to reflect the current pod status
				// we do this so we don't update the status of the build again
				if lagoonTask.Labels["lagoon.sh/taskStatus"] != string(jobCondition) {
					opLog.Info(fmt.Sprintf("Task %s %v", jobPod.ObjectMeta.Labels["lagoon.sh/taskName"], jobPod.Status.Phase))
					var allContainerLogs []byte
					// grab all the logs from the containers in the build pod and just merge them all together
					// we only have 1 container at the moment in a buildpod anyway so it doesn't matter
					// if we do move to multi container builds, then worry about it
					for _, container := range jobPod.Spec.Containers {
						cLogs, err := getContainerLogs(container.Name, req)
						if err != nil {
							opLog.Info(fmt.Sprintf("LogsErr: %v", err))
							return ctrl.Result{}, nil
						}
						allContainerLogs = append(allContainerLogs, cLogs...)
					}
					// set the status to the build condition
					lagoonTask.Labels["lagoon.sh/taskStatus"] = string(jobCondition)
					if err := r.Update(ctx, &lagoonTask); err != nil {
						return ctrl.Result{}, err
					}
					r.updateTaskStatusCondition(ctx, &lagoonTask, lagoonv1alpha1.LagoonConditions{
						Type:   jobCondition,
						Status: corev1.ConditionTrue,
					}, allContainerLogs)
					// send any messages to lagoon message queues
					// update the deployment with the status
					r.taskStatusLogsToLagoonLogs(&lagoonTask, &jobPod, nil)
					r.updateLagoonTask(&lagoonTask, &jobPod, nil)
					r.taskLogsToLagoonLogs(&lagoonTask, &jobPod, allContainerLogs)
				}
				return ctrl.Result{}, nil
			}
			// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
			opLog.Info(fmt.Sprintf("Task %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/taskName"], jobPod.Status.Phase))
			// send any messages to lagoon message queues
			r.taskStatusLogsToLagoonLogs(&lagoonTask, &jobPod, nil)
			r.updateLagoonTask(&lagoonTask, &jobPod, nil)
		}
	}
	// if this is a lagoon build, then handle that here
	if jobPod.ObjectMeta.Labels["lagoon.sh/jobType"] == "build" {
		if jobPod.ObjectMeta.DeletionTimestamp.IsZero() {
			// get the build associated to this pod, we wil need update it at some point
			var lagoonBuild lagoonv1alpha1.LagoonBuild
			err := r.Get(ctx, types.NamespacedName{Namespace: jobPod.ObjectMeta.Namespace, Name: jobPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
			if err != nil {
				return ctrl.Result{}, err
			}
			// check if the build pod is in pending, a container in the pod could be failed in this state
			if jobPod.Status.Phase == corev1.PodPending {
				opLog.Info(fmt.Sprintf("Build %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
				// send any messages to lagoon message queues
				r.buildStatusLogsToLagoonLogs(&lagoonBuild, &jobPod, nil)
				r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &jobPod, nil)
				// check each container in the pod
				for _, container := range jobPod.Status.ContainerStatuses {
					// if the container is a lagoon-build container
					// which currently it will be as only one container is spawned in a build
					if container.Name == "lagoon-build" {
						// check if the state of the pod is one of our failure states
						if container.State.Waiting != nil && containsString(failureStates, container.State.Waiting.Reason) {
							// if we have a failure state, then fail the build and get the logs from the container
							opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
							lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.JobFailed)
							if err := r.Update(ctx, &lagoonBuild); err != nil {
								return ctrl.Result{}, err
							}
							opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.ObjectMeta.Name, string(lagoonv1alpha1.JobFailed)))
							if err := r.Delete(ctx, &jobPod); err != nil {
								return ctrl.Result{}, err
							}
							opLog.Info(fmt.Sprintf("Deleted failed build pod: %s", jobPod.ObjectMeta.Name))
							// update the status to failed on the deleted pod
							// and set the terminate time to now, it is used when we update the deployment and environment
							jobPod.Status.Phase = corev1.PodFailed
							state := corev1.ContainerStatus{
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										FinishedAt: metav1.Time{Time: time.Now().UTC()},
									},
								},
							}
							jobPod.Status.ContainerStatuses[0] = state
							r.updateBuildStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonConditions{
								Type:   lagoonv1alpha1.JobFailed,
								Status: corev1.ConditionTrue,
							}, []byte(container.State.Waiting.Message))

							// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
							var lagoonEnv corev1.ConfigMap
							err := r.Get(ctx, types.NamespacedName{Namespace: jobPod.ObjectMeta.Namespace, Name: "lagoon-env"}, &lagoonEnv)
							if err != nil {
								// if there isn't a configmap, just info it and move on
								// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
								opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", jobPod.ObjectMeta.Namespace))
							}
							// send any messages to lagoon message queues
							r.buildStatusLogsToLagoonLogs(&lagoonBuild, &jobPod, &lagoonEnv)
							r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &jobPod, &lagoonEnv)
							logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
							r.buildLogsToLagoonLogs(&lagoonBuild, &jobPod, []byte(logMsg))
							return ctrl.Result{}, nil
						}
					}
				}
				return ctrl.Result{}, nil
			}
			// if the buildpod status is failed or succeeded
			// mark the build accordingly and ship the information back to lagoon
			if jobPod.Status.Phase == corev1.PodFailed || jobPod.Status.Phase == corev1.PodSucceeded {
				// get the build associated to this pod, we wil need update it at some point
				var lagoonBuild lagoonv1alpha1.LagoonBuild
				err := r.Get(ctx, types.NamespacedName{Namespace: jobPod.ObjectMeta.Namespace, Name: jobPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
				if err != nil {
					return ctrl.Result{}, err
				}
				var jobCondition lagoonv1alpha1.JobConditionType
				switch jobPod.Status.Phase {
				case corev1.PodFailed:
					jobCondition = lagoonv1alpha1.JobFailed
				case corev1.PodSucceeded:
					jobCondition = lagoonv1alpha1.JobComplete
				}
				// if the build status doesn't equal the status of the pod
				// then update the build to reflect the current pod status
				// we do this so we don't update the status of the build again
				if lagoonBuild.Labels["lagoon.sh/buildStatus"] != string(jobCondition) {
					opLog.Info(fmt.Sprintf("Build %s %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
					var allContainerLogs []byte
					// grab all the logs from the containers in the build pod and just merge them all together
					// we only have 1 container at the moment in a buildpod anyway so it doesn't matter
					// if we do move to multi container builds, then worry about it
					for _, container := range jobPod.Spec.Containers {
						cLogs, err := getContainerLogs(container.Name, req)
						if err != nil {
							opLog.Info(fmt.Sprintf("LogsErr: %v", err))
							return ctrl.Result{}, nil
						}
						allContainerLogs = append(allContainerLogs, cLogs...)
					}
					// set the status to the build condition
					lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(jobCondition)
					if err := r.Update(ctx, &lagoonBuild); err != nil {
						return ctrl.Result{}, err
					}
					r.updateBuildStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonConditions{
						Type:   jobCondition,
						Status: corev1.ConditionTrue,
					}, allContainerLogs)

					// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
					var lagoonEnv corev1.ConfigMap
					err := r.Get(ctx, types.NamespacedName{Namespace: jobPod.ObjectMeta.Namespace, Name: "lagoon-env"}, &lagoonEnv)
					if err != nil {
						// if there isn't a configmap, just info it and move on
						// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
						opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", jobPod.ObjectMeta.Namespace))
					}
					// send any messages to lagoon message queues
					// update the deployment with the status
					r.buildStatusLogsToLagoonLogs(&lagoonBuild, &jobPod, &lagoonEnv)
					r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &jobPod, &lagoonEnv)
					r.buildLogsToLagoonLogs(&lagoonBuild, &jobPod, allContainerLogs)
				}
				return ctrl.Result{}, nil
			}
			// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
			opLog.Info(fmt.Sprintf("Build %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
			// send any messages to lagoon message queues
			r.buildStatusLogsToLagoonLogs(&lagoonBuild, &jobPod, nil)
			r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &jobPod, nil)
		} else {
			// a pod deletion request came through

			// if we got any pending builds come through while one is running
			// they will be processed here when any pods are cleaned up
			// we check all `LagoonBuild` in the requested namespace
			// if there are no running jobs, we check for any pending jobs
			// sorted by their creation timestamp and set the first to running
			opLog.Info(fmt.Sprintf("Checking for any pending builds"))
			pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
			runningBuilds := &lagoonv1alpha1.LagoonBuildList{}
			listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Running"}),
			})
			// list all builds in the namespace that have the running buildstatus
			if err := r.List(ctx, runningBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			// if we have no running builds, then check for any pending builds
			if len(runningBuilds.Items) == 0 {
				listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
					client.InNamespace(req.Namespace),
					client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Pending"}),
				})
				if err := r.List(ctx, pendingBuilds, listOption); err != nil {
					return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
				}
				opLog.Info(fmt.Sprintf("There are %v Pending builds", len(pendingBuilds.Items)))
				// if we have any pending builds, then grab the first one from the items as they are sorted by oldest pending first
				if len(pendingBuilds.Items) > 0 {
					// sort the pending builds by creation timestamp
					sort.Slice(pendingBuilds.Items, func(i, j int) bool {
						return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.Before(&pendingBuilds.Items[j].ObjectMeta.CreationTimestamp)
					})
					opLog.Info(fmt.Sprintf("Next build is: %s", pendingBuilds.Items[0].ObjectMeta.Name))
					pendingBuild := pendingBuilds.Items[0].DeepCopy()
					pendingBuild.Labels["lagoon.sh/buildStatus"] = "Running"
					if err := r.Update(ctx, pendingBuild); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch Pods with an event filter that contains our build label
func (r *LagoonMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(PodPredicates{}).
		Complete(r)
}

// getContainerLogs grabs the logs from a given container
func getContainerLogs(containerName string, request ctrl.Request) ([]byte, error) {
	restCfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create client: %v", err)
	}
	req := clientset.CoreV1().Pods(request.Namespace).GetLogs(request.Name, &corev1.PodLogOptions{Container: containerName})
	podLogs, err := req.Stream()
	if err != nil {
		return nil, fmt.Errorf("error in opening stream: %v", err)
	}
	defer podLogs.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return nil, fmt.Errorf("error in copy information from podLogs to buffer: %v", err)
	}
	return buf.Bytes(), nil
}
