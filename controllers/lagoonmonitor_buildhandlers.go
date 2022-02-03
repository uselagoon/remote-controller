package controllers

// this file is used by the `lagoonmonitor` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	lagoonv1alpha1 "github.com/uselagoon/remote-controller/apis/lagoon-old/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *LagoonMonitorReconciler) handleBuildMonitor(ctx context.Context,
	opLog logr.Logger,
	req ctrl.Request,
	jobPod corev1.Pod,
) error {
	// get the build associated to this pod, we wil need update it at some point
	var lagoonBuild lagoonv1alpha1.LagoonBuild
	err := r.Get(ctx, types.NamespacedName{
		Namespace: jobPod.ObjectMeta.Namespace,
		Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
	}, &lagoonBuild)
	if err != nil {
		return err
	}
	if cancelBuild, ok := jobPod.ObjectMeta.Labels["lagoon.sh/cancelBuild"]; ok {
		cancel, _ := strconv.ParseBool(cancelBuild)
		if cancel {
			return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, cancel)
		}
	}
	// check if the build pod is in pending, a container in the pod could be failed in this state
	if jobPod.Status.Phase == corev1.PodPending {
		opLog.Info(fmt.Sprintf("Build %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
		// send any messages to lagoon message queues
		r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, nil)
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, nil)
		// check each container in the pod
		for _, container := range jobPod.Status.ContainerStatuses {
			// if the container is a lagoon-build container
			// which currently it will be as only one container is spawned in a build
			if container.Name == "lagoon-build" {
				// check if the state of the pod is one of our failure states
				if container.State.Waiting != nil && containsString(failureStates, container.State.Waiting.Reason) {
					// if we have a failure state, then fail the build and get the logs from the container
					opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
					lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.BuildStatusFailed)
					if err := r.Update(ctx, &lagoonBuild); err != nil {
						return err
					}
					opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.ObjectMeta.Name, string(lagoonv1alpha1.BuildStatusFailed)))
					if err := r.Delete(ctx, &jobPod); err != nil {
						return err
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
					r.updateBuildStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
						Type:   lagoonv1alpha1.BuildStatusFailed,
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
					r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv)
					r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv)
					logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
					r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, []byte(logMsg))
					return nil
				}
			}
		}
		return nil
	} else if jobPod.Status.Phase == corev1.PodRunning {
		// if the pod is running and detects a change to the pod (eg, detecting an updated lagoon.sh/buildStep label)
		// then ship or store the logs
		opLog.Info(fmt.Sprintf("Build %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
		// get the build associated to this pod, the information in the resource is used for shipping the logs
		var lagoonBuild lagoonv1alpha1.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
		// actually run the log collection and shipping function
		r.updateRunningDeploymentBuildLogs(ctx, req, lagoonBuild, jobPod)
	}
	// if the buildpod status is failed or succeeded
	// mark the build accordingly and ship the information back to lagoon
	if jobPod.Status.Phase == corev1.PodFailed || jobPod.Status.Phase == corev1.PodSucceeded {
		// get the build associated to this pod, we wil need update it at some point
		var lagoonBuild lagoonv1alpha1.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
		return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, false)
	}
	// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
	opLog.Info(fmt.Sprintf("Build %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/buildName"], jobPod.Status.Phase))
	// send any messages to lagoon message queues
	r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, nil)
	r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, nil)
	return nil
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonMonitorReconciler) buildLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	jobPod *corev1.Pod,
	logs []byte,
) {
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		if bStatus, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; ok {
			if bStatus == string(lagoonv1alpha1.BuildStatusCancelled) {
				condition = "cancelled"
			}
		}
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				EnvironmentID: lagoonBuild.Spec.Project.EnvironmentID,
				ProjectID:     lagoonBuild.Spec.Project.ID,
				JobName:       lagoonBuild.ObjectMeta.Name,
				BranchName:    lagoonBuild.Spec.Project.Environment,
				BuildPhase:    condition,
				RemoteID:      string(jobPod.ObjectMeta.UID),
				LogLink:       lagoonBuild.Spec.Project.UILink,
				Cluster:       r.LagoonTargetName,
			},
		}
		// add the actual build log message
		msg.Message = fmt.Sprintf(`========================================
Logs on pod %s
========================================
%s`, jobPod.ObjectMeta.Name, logs)
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateBuildLogMessage(ctx, lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(ctx, lagoonBuild)
	}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonMonitorReconciler) updateDeploymentAndEnvironmentTask(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap,
) {
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		if bStatus, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; ok {
			if bStatus == string(lagoonv1alpha1.BuildStatusCancelled) {
				condition = "cancelled"
			}
		}
		msg := lagoonv1alpha1.LagoonMessage{
			Type:      "build",
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				Environment:   lagoonBuild.Spec.Project.Environment,
				EnvironmentID: lagoonBuild.Spec.Project.EnvironmentID,
				Project:       lagoonBuild.Spec.Project.Name,
				ProjectID:     lagoonBuild.Spec.Project.ID,
				BuildPhase:    condition,
				BuildName:     lagoonBuild.ObjectMeta.Name,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				RemoteID:      string(jobPod.ObjectMeta.UID),
				Cluster:       r.LagoonTargetName,
			},
		}
		labelRequirements1, _ := labels.NewRequirement("lagoon.sh/service", selection.NotIn, []string{"faketest"})
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
			client.MatchingLabelsSelector{
				Selector: labels.NewSelector().Add(*labelRequirements1),
			},
		})
		podList := &corev1.PodList{}
		serviceNames := []string{}
		if err := r.List(context.TODO(), podList, listOption); err == nil {
			// generate the list of services to add to the environment
			for _, pod := range podList.Items {
				if _, ok := pod.ObjectMeta.Labels["lagoon.sh/service"]; ok {
					for _, container := range pod.Spec.Containers {
						serviceNames = append(serviceNames, container.Name)
					}
				}
				if _, ok := pod.ObjectMeta.Labels["service"]; ok {
					for _, container := range pod.Spec.Containers {
						serviceNames = append(serviceNames, container.Name)
					}
				}
			}
			msg.Meta.Services = serviceNames
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
		// we can add the build start time here
		if jobPod.Status.StartTime != nil {
			msg.Meta.StartTime = jobPod.Status.StartTime.Time.UTC().Format("2006-01-02 15:04:05")
		}
		if condition == "cancelled" {
			// if the build has been canclled, the pod termination time may not exist yet.
			// use the current time first, it will get overwritten if there is a pod termination time later.
			msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
		}
		// and then once the pod is terminated we can add the terminated time here
		if jobPod.Status.ContainerStatuses != nil {
			if jobPod.Status.ContainerStatuses[0].State.Terminated != nil {
				msg.Meta.EndTime = jobPod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time.UTC().Format("2006-01-02 15:04:05")
			}
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateEnvironmentMessage(ctx, lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(ctx, lagoonBuild)
	}
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) buildStatusLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap) {
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		if bStatus, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; ok {
			if bStatus == string(lagoonv1alpha1.BuildStatusCancelled) {
				condition = "cancelled"
			}
		}
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				EnvironmentID: lagoonBuild.Spec.Project.EnvironmentID,
				ProjectID:     lagoonBuild.Spec.Project.ID,
				ProjectName:   lagoonBuild.Spec.Project.Name,
				BranchName:    lagoonBuild.Spec.Project.Environment,
				BuildPhase:    condition,
				BuildName:     lagoonBuild.ObjectMeta.Name,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				Cluster:       r.LagoonTargetName,
			},
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		var addRoute, addRoutes string
		if lagoonEnv != nil {
			msg.Meta.Route = ""
			if route, ok := lagoonEnv.Data["LAGOON_ROUTE"]; ok {
				msg.Meta.Route = route
				addRoute = fmt.Sprintf("\n%s", route)
			}
			msg.Meta.Routes = []string{}
			if routes, ok := lagoonEnv.Data["LAGOON_ROUTES"]; ok {
				msg.Meta.Routes = strings.Split(routes, ",")
				addRoutes = fmt.Sprintf("\n%s", strings.Join(strings.Split(routes, ","), "\n"))
			}
			msg.Meta.MonitoringURLs = []string{}
			if monitoringUrls, ok := lagoonEnv.Data["LAGOON_MONITORING_URLS"]; ok {
				msg.Meta.MonitoringURLs = strings.Split(monitoringUrls, ",")
			}
		}
		msg.Message = fmt.Sprintf("*[%s]* `%s` Build `%s` %s <%s|Logs>%s%s",
			lagoonBuild.Spec.Project.Name,
			lagoonBuild.Spec.Project.Environment,
			lagoonBuild.ObjectMeta.Name,
			string(jobPod.Status.Phase),
			lagoonBuild.Spec.Project.UILink,
			addRoute,
			addRoutes,
		)
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateBuildStatusMessage(ctx, lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(ctx, lagoonBuild)
	}
}

// updateBuildStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonMonitorReconciler) updateBuildStatusCondition(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	condition lagoonv1alpha1.LagoonBuildConditions,
	log []byte,
) error {
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
		if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}

// updateBuildStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateBuildStatusMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	statusMessage lagoonv1alpha1.LagoonLog,
) error {
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
	if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateEnvironmentMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	envMessage lagoonv1alpha1.LagoonMessage,
) error {
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
	if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateBuildLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateBuildLogMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	buildMessage lagoonv1alpha1.LagoonLog,
) error {
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
	if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// removeBuildPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonMonitorReconciler) removeBuildPendingMessageStatus(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
) error {
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
			if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}

// updateRunningDeploymentBuildLogs collects logs from running build containers and ships or stores them
func (r *LagoonMonitorReconciler) updateRunningDeploymentBuildLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagoonv1alpha1.LagoonBuild,
	jobPod corev1.Pod,
) {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	var allContainerLogs []byte
	// grab all the logs from the containers in the build pod and just merge them all together
	// we only have 1 container at the moment in a buildpod anyway so it doesn't matter
	// if we do move to multi container builds, then worry about it
	for _, container := range jobPod.Spec.Containers {
		cLogs, err := getContainerLogs(ctx, container.Name, req)
		if err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to retrieve logs from build pod"))
			// log the error, but just continue
		}
		allContainerLogs = append(allContainerLogs, cLogs...)
	}
	// send any messages to lagoon message queues
	r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, allContainerLogs)
}

// updateDeploymentWithLogs collects logs from the build containers and ships or stores them
func (r *LagoonMonitorReconciler) updateDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagoonv1alpha1.LagoonBuild,
	jobPod corev1.Pod,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	var jobCondition lagoonv1alpha1.BuildStatusType
	switch jobPod.Status.Phase {
	case corev1.PodFailed:
		jobCondition = lagoonv1alpha1.BuildStatusFailed
	case corev1.PodSucceeded:
		jobCondition = lagoonv1alpha1.BuildStatusComplete
	}
	if cancel {
		jobCondition = lagoonv1alpha1.BuildStatusCancelled
	}
	// if the build status is Pending or Running
	// then the jobCondition is Failed, Complete, or Cancelled
	// then update the build to reflect the current pod status
	// we do this so we don't update the status of the build again
	if containsString(
		RunningPendingStatus,
		lagoonBuild.Labels["lagoon.sh/buildStatus"],
	) {
		opLog.Info(
			fmt.Sprintf(
				"Updating build status for %s to %v",
				jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
				jobPod.Status.Phase,
			),
		)
		var allContainerLogs []byte
		// grab all the logs from the containers in the build pod and just merge them all together
		// we only have 1 container at the moment in a buildpod anyway so it doesn't matter
		// if we do move to multi container builds, then worry about it
		for _, container := range jobPod.Spec.Containers {
			cLogs, err := getContainerLogs(ctx, container.Name, req)
			if err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to retrieve logs from build pod"))
				// log the error, but just continue
			}
			allContainerLogs = append(allContainerLogs, cLogs...)
		}
		if cancel {
			allContainerLogs = append(allContainerLogs, []byte(fmt.Sprintf(`
========================================
Build cancelled
========================================`))...)
		}
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus": string(jobCondition),
				},
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update resource"))
		}
		r.updateBuildStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
			Type:   jobCondition,
			Status: corev1.ConditionTrue,
		}, allContainerLogs)

		// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
		var lagoonEnv corev1.ConfigMap
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: jobPod.ObjectMeta.Namespace,
			Name:      "lagoon-env",
		},
			&lagoonEnv,
		); err != nil {
			// if there isn't a configmap, just info it and move on
			// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
			opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", jobPod.ObjectMeta.Namespace))
		}
		// send any messages to lagoon message queues
		// update the deployment with the status
		r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv)
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv)
		r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, allContainerLogs)
		// just delete the pod
		// maybe if we move away from using BASH for the kubectl-build-deploy-dind scripts we could handle cancellations better
		if cancel {
			if err := r.Delete(ctx, &jobPod); err != nil {
				return err
			}
		}
	}
	return nil
}
