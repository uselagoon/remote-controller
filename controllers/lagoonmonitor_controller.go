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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
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

	var buildPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &buildPod); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	if buildPod.ObjectMeta.DeletionTimestamp.IsZero() {
		// get the build associated to this pod, we wil need update it at some point
		var lagoonBuild lagoonv1alpha1.LagoonBuild
		err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: buildPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
		if err != nil {
			return ctrl.Result{}, err
		}
		// check if the build pod is in pending, a container in the pod could be failed in this state
		if buildPod.Status.Phase == corev1.PodPending {
			opLog.Info(fmt.Sprintf("Build %s is %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
			// send any messages to lagoon message queues
			r.statusLogsToLagoonLogs(&lagoonBuild, &buildPod, nil)
			r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &buildPod, nil)
			// check each container in the pod
			for _, container := range buildPod.Status.ContainerStatuses {
				// if the container is a lagoon-build container
				// which currently it will be as only one container is spawned in a build
				if container.Name == "lagoon-build" {
					// check if the state of the pod is one of our failure states
					if container.State.Waiting != nil && containsString(failureStates, container.State.Waiting.Reason) {
						// if we have a failure state, then fail the build and get the logs from the container
						opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
						lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.BuildFailed)
						if err := r.Update(ctx, &lagoonBuild); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.ObjectMeta.Name, string(lagoonv1alpha1.BuildFailed)))
						if err := r.Delete(ctx, &buildPod); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Deleted failed build pod: %s", buildPod.ObjectMeta.Name))
						// update the status to failed on the deleted pod
						// and set the terminate time to now, it is used when we update the deployment and environment
						buildPod.Status.Phase = corev1.PodFailed
						state := corev1.ContainerStatus{
							State: corev1.ContainerState{
								Terminated: &corev1.ContainerStateTerminated{
									FinishedAt: metav1.Time{Time: time.Now().UTC()},
								},
							},
						}
						buildPod.Status.ContainerStatuses[0] = state
						r.updateStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
							Type:   lagoonv1alpha1.BuildFailed,
							Status: corev1.ConditionTrue,
						}, []byte(container.State.Waiting.Message))

						// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
						var lagoonEnv corev1.ConfigMap
						err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: "lagoon-env"}, &lagoonEnv)
						if err != nil {
							// if there isn't a configmap, just info it and move on
							// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
							opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", buildPod.ObjectMeta.Namespace))
						}
						// send any messages to lagoon message queues
						r.statusLogsToLagoonLogs(&lagoonBuild, &buildPod, &lagoonEnv)
						r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &buildPod, &lagoonEnv)
						logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
						r.buildLogsToLagoonLogs(&lagoonBuild, &buildPod, []byte(logMsg))
						return ctrl.Result{}, nil
					}
				}
			}
			return ctrl.Result{}, nil
		}
		// if the buildpod status is failed or succeeded
		// mark the build accordingly and ship the information back to lagoon
		if buildPod.Status.Phase == corev1.PodFailed || buildPod.Status.Phase == corev1.PodSucceeded {
			// get the build associated to this pod, we wil need update it at some point
			var lagoonBuild lagoonv1alpha1.LagoonBuild
			err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: buildPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
			if err != nil {
				return ctrl.Result{}, err
			}
			var buildCondition lagoonv1alpha1.BuildConditionType
			switch buildPod.Status.Phase {
			case corev1.PodFailed:
				buildCondition = lagoonv1alpha1.BuildFailed
			case corev1.PodSucceeded:
				buildCondition = lagoonv1alpha1.BuildComplete
			}
			// if the build status doesn't equal the status of the pod
			// then update the build to reflect the current pod status
			// we do this so we don't update the status of the build again
			if lagoonBuild.Labels["lagoon.sh/buildStatus"] != string(buildCondition) {
				opLog.Info(fmt.Sprintf("Build %s %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
				var allContainerLogs []byte
				// grab all the logs from the containers in the build pod and just merge them all together
				// we only have 1 container at the moment in a buildpod anyway so it doesn't matter
				// if we do move to multi container builds, then worry about it
				for _, container := range buildPod.Spec.Containers {
					cLogs, err := getContainerLogs(container.Name, req)
					if err != nil {
						opLog.Info(fmt.Sprintf("LogsErr: %v", err))
						return ctrl.Result{}, nil
					}
					allContainerLogs = append(allContainerLogs, cLogs...)
				}
				// set the status to the build condition
				lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(buildCondition)
				if err := r.Update(ctx, &lagoonBuild); err != nil {
					return ctrl.Result{}, err
				}
				r.updateStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
					Type:   buildCondition,
					Status: corev1.ConditionTrue,
				}, allContainerLogs)

				// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
				var lagoonEnv corev1.ConfigMap
				err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: "lagoon-env"}, &lagoonEnv)
				if err != nil {
					// if there isn't a configmap, just info it and move on
					// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
					opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", buildPod.ObjectMeta.Namespace))
				}
				// send any messages to lagoon message queues
				// update the deployment with the status
				r.statusLogsToLagoonLogs(&lagoonBuild, &buildPod, &lagoonEnv)
				r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &buildPod, &lagoonEnv)
				r.buildLogsToLagoonLogs(&lagoonBuild, &buildPod, allContainerLogs)
			}
			return ctrl.Result{}, nil
		}
		// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
		opLog.Info(fmt.Sprintf("Build %s is %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
		// send any messages to lagoon message queues
		r.statusLogsToLagoonLogs(&lagoonBuild, &buildPod, nil)
		r.updateDeploymentAndEnvironmentTask(&lagoonBuild, &buildPod, nil)
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

// updateStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonMonitorReconciler) updateStatusCondition(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild, condition lagoonv1alpha1.LagoonBuildConditions, log []byte) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !buildContainsStatus(lagoonBuild.Status.Conditions, condition) {
		lagoonBuild.Status.Conditions = append(lagoonBuild.Status.Conditions, condition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": lagoonBuild.Status.Conditions,
				"log":        log,
			},
		})
		if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}

// updateStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateStatusMessage(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild, statusMessage lagoonv1alpha1.LagoonLog) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"statusMessage": statusMessage,
		},
	})
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateEnvironmentMessage(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild, envMessage lagoonv1alpha1.LagoonMessage) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"environmentMessage": envMessage,
		},
	})
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateBuildLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateBuildLogMessage(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild, buildMessage lagoonv1alpha1.LagoonLog) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"buildLogMessage": buildMessage,
		},
	})
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// removePendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonMonitorReconciler) removePendingMessageStatus(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild) error {
	// if we have the pending messages label as true, then we want to remove this label and any pending statusmessages
	// so we can avoid double handling, or an old pending message from being sent after a new pending message
	if val, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/pendingMessages"]; !ok {
		if val == "true" {
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"lagoon.sh/pendingMessages": "false",
					},
				},
				"statusMessages": nil,
			})
			if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}

// statusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) statusLogsToLagoonLogs(lagoonBuild *lagoonv1alpha1.LagoonBuild, buildPod *corev1.Pod, lagoonEnv *corev1.ConfigMap) {
	if r.EnableMQ {
		condition := "pending"
		switch buildPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the operator
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				RemoteID:    string(buildPod.ObjectMeta.UID),
				LogLink:     lagoonBuild.Spec.Project.UILink,
			},
			Message: fmt.Sprintf("*[%s]* %s Build `%s` %s",
				lagoonBuild.Spec.Project.Name,
				lagoonBuild.Spec.Project.Environment,
				lagoonBuild.ObjectMeta.Name,
				string(buildPod.Status.Phase),
			),
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if lagoonEnv != nil {
			msg.Meta.Route = ""
			if route, ok := lagoonEnv.Data["LAGOON_ROUTE"]; ok {
				msg.Meta.Route = route
			}
			msg.Meta.Routes = []string{}
			if routes, ok := lagoonEnv.Data["LAGOON_ROUTES"]; ok {
				msg.Meta.Routes = strings.Split(routes, ",")
			}
			msg.Meta.MonitoringURLs = []string{}
			if monitoringUrls, ok := lagoonEnv.Data["LAGOON_MONITORING_URLS"]; ok {
				msg.Meta.MonitoringURLs = strings.Split(monitoringUrls, ",")
			}
		}
		msgBytes, _ := json.Marshal(msg)
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateStatusMessage(context.Background(), lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removePendingMessageStatus(context.Background(), lagoonBuild)
	}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the operatorhandler message queue in lagoon,
// this is for the operatorhandler to process.
func (r *LagoonMonitorReconciler) updateDeploymentAndEnvironmentTask(lagoonBuild *lagoonv1alpha1.LagoonBuild, buildPod *corev1.Pod, lagoonEnv *corev1.ConfigMap) {
	if r.EnableMQ {
		condition := "pending"
		switch buildPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		operatorMsg := lagoonv1alpha1.LagoonMessage{
			Type:      "build",
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				Environment: lagoonBuild.Spec.Project.Environment,
				Project:     lagoonBuild.Spec.Project.Name,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				RemoteID:    string(buildPod.ObjectMeta.UID),
				LogLink:     lagoonBuild.Spec.Project.UILink,
			},
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if lagoonEnv != nil {
			operatorMsg.Meta.Route = ""
			if route, ok := lagoonEnv.Data["LAGOON_ROUTE"]; ok {
				operatorMsg.Meta.Route = route
			}
			operatorMsg.Meta.Routes = []string{}
			if routes, ok := lagoonEnv.Data["LAGOON_ROUTES"]; ok {
				operatorMsg.Meta.Routes = strings.Split(routes, ",")
			}
			operatorMsg.Meta.MonitoringURLs = []string{}
			if monitoringUrls, ok := lagoonEnv.Data["LAGOON_MONITORING_URLS"]; ok {
				operatorMsg.Meta.MonitoringURLs = strings.Split(monitoringUrls, ",")
			}
		}
		// we can add the build start time here
		if buildPod.Status.StartTime != nil {
			operatorMsg.Meta.StartTime = buildPod.Status.StartTime.Time.UTC().Format("2006-01-02 15:04:05")
		}
		// and then once the pod is terminated we can add the terminated time here
		if buildPod.Status.ContainerStatuses != nil {
			if buildPod.Status.ContainerStatuses[0].State.Terminated != nil {
				operatorMsg.Meta.EndTime = buildPod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time.UTC().Format("2006-01-02 15:04:05")
			}
		}
		operatorMsgBytes, _ := json.Marshal(operatorMsg)
		if err := r.Messaging.Publish("lagoon-tasks:operator", operatorMsgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateEnvironmentMessage(context.Background(), lagoonBuild, operatorMsg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removePendingMessageStatus(context.Background(), lagoonBuild)
	}
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
func (r *LagoonMonitorReconciler) buildLogsToLagoonLogs(lagoonBuild *lagoonv1alpha1.LagoonBuild, buildPod *corev1.Pod, logs []byte) {
	if r.EnableMQ {
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				JobName:    lagoonBuild.ObjectMeta.Name,
				BranchName: lagoonBuild.Spec.Project.Environment,
				BuildPhase: string(buildPod.Status.Phase),
				RemoteID:   string(buildPod.ObjectMeta.UID),
				LogLink:    lagoonBuild.Spec.Project.UILink,
			},
			Message: fmt.Sprintf("%s", logs),
		}
		msgBytes, _ := json.Marshal(msg)
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateBuildLogMessage(context.Background(), lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removePendingMessageStatus(context.Background(), lagoonBuild)
	}
}
