package v1beta1

// this file is used by the `lagoonmonitor` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
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
	var lagoonBuild lagoonv1beta1.LagoonBuild
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
			opLog.Info(fmt.Sprintf("Attempting to cancel build %s", lagoonBuild.ObjectMeta.Name))
			return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, cancel)
		}
	}
	// check if the build pod is in pending, a container in the pod could be failed in this state
	if jobPod.Status.Phase == corev1.PodPending {
		// check each container in the pod
		for _, container := range jobPod.Status.ContainerStatuses {
			// if the container is a lagoon-build container
			// which currently it will be as only one container is spawned in a build
			if container.Name == "lagoon-build" {
				// check if the state of the pod is one of our failure states
				if container.State.Waiting != nil && helpers.ContainsString(failureStates, container.State.Waiting.Reason) {
					// if we have a failure state, then fail the build and get the logs from the container
					opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
					lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1beta1.BuildStatusFailed)
					if err := r.Update(ctx, &lagoonBuild); err != nil {
						return err
					}
					opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.ObjectMeta.Name, string(lagoonv1beta1.BuildStatusFailed)))
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

					// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
					var lagoonEnv corev1.ConfigMap
					err := r.Get(ctx, types.NamespacedName{Namespace: jobPod.ObjectMeta.Namespace, Name: "lagoon-env"}, &lagoonEnv)
					if err != nil {
						// if there isn't a configmap, just info it and move on
						// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
						if r.EnableDebug {
							opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", jobPod.ObjectMeta.Namespace))
						}
					}
					// send any messages to lagoon message queues
					logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
					return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, []byte(logMsg), false)
				}
			}
		}
		return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, false)
	} else if jobPod.Status.Phase == corev1.PodRunning {
		// if the pod is running and detects a change to the pod (eg, detecting an updated lagoon.sh/buildStep label)
		// then ship or store the logs
		// get the build associated to this pod, the information in the resource is used for shipping the logs
		var lagoonBuild lagoonv1beta1.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
	}
	// if the buildpod status is failed or succeeded
	// mark the build accordingly and ship the information back to lagoon
	if jobPod.Status.Phase == corev1.PodFailed || jobPod.Status.Phase == corev1.PodSucceeded {
		// get the build associated to this pod, we wil need update it at some point
		var lagoonBuild lagoonv1beta1.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
	}
	// send any messages to lagoon message queues
	return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, false)
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonMonitorReconciler) buildLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	jobPod *corev1.Pod,
	namespace *corev1.Namespace,
	condition string,
	logs []byte,
) (bool, lagoonv1beta1.LagoonLog) {
	if r.EnableMQ {
		buildStep := "running"
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		envName := lagoonBuild.Spec.Project.Environment
		envID := lagoonBuild.Spec.Project.EnvironmentID
		projectName := lagoonBuild.Spec.Project.Name
		projectID := lagoonBuild.Spec.Project.ID
		if lagoonBuild == nil {
			envName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			eID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			envID = helpers.UintPtr(uint(eID))
			projectName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			pID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			projectID = helpers.UintPtr(uint(pID))
		}
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  projectName,
			Event:    "build-logs:builddeploy-kubernetes:" + jobPod.ObjectMeta.Name,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				EnvironmentID: envID,
				ProjectID:     projectID,
				BuildName:     jobPod.ObjectMeta.Name,
				BranchName:    envName,
				BuildPhase:    condition, // @TODO: same as buildstatus label, remove once lagoon is corrected in controller-handler
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
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
			// r.updateBuildLogMessage(ctx, lagoonBuild, msg)
			return true, msg
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("build-logs:builddeploy-kubernetes:%s", jobPod.ObjectMeta.Name),
					jobPod.ObjectMeta.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonLog{}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonMonitorReconciler) updateDeploymentAndEnvironmentTask(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap,
	namespace *corev1.Namespace,
	condition string,
) (bool, lagoonv1beta1.LagoonMessage) {
	if r.EnableMQ {
		buildStep := "running"
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			time.AfterFunc(31*time.Second, func() {
				buildRunningStatus.Delete(prometheus.Labels{
					"build_namespace": lagoonBuild.ObjectMeta.Namespace,
					"build_name":      lagoonBuild.ObjectMeta.Name,
				})
			})
		}
		envName := lagoonBuild.Spec.Project.Environment
		envID := lagoonBuild.Spec.Project.EnvironmentID
		projectName := lagoonBuild.Spec.Project.Name
		projectID := lagoonBuild.Spec.Project.ID
		if lagoonBuild == nil {
			envName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			eID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			envID = helpers.UintPtr(uint(eID))
			projectName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			pID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			projectID = helpers.UintPtr(uint(pID))
		}
		msg := lagoonv1beta1.LagoonMessage{
			Type:      "build",
			Namespace: namespace.ObjectMeta.Name,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				Environment:   envName,
				EnvironmentID: envID,
				Project:       projectName,
				ProjectID:     projectID,
				BuildName:     jobPod.ObjectMeta.Name,
				BuildPhase:    condition, // @TODO: same as buildstatus label, remove once lagoon is corrected in controller-handler
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				RemoteID:      string(jobPod.ObjectMeta.UID),
				Cluster:       r.LagoonTargetName,
			},
		}
		labelRequirements1, _ := labels.NewRequirement("lagoon.sh/service", selection.NotIn, []string{"faketest"})
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(jobPod.ObjectMeta.Namespace),
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
			return true, msg
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published build update message for %s to lagoon-tasks:controller queue",
					jobPod.ObjectMeta.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonMessage{}
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) buildStatusLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap,
	namespace *corev1.Namespace,
	condition string,
) (bool, lagoonv1beta1.LagoonLog) {
	if r.EnableMQ {
		buildStep := "running"
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		envName := lagoonBuild.Spec.Project.Environment
		envID := lagoonBuild.Spec.Project.EnvironmentID
		projectName := lagoonBuild.Spec.Project.Name
		projectID := lagoonBuild.Spec.Project.ID
		if lagoonBuild == nil {
			envName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			eID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			envID = helpers.UintPtr(uint(eID))
			projectName = namespace.ObjectMeta.Labels["lagoon.sh/environment"]
			pID, _ := strconv.Atoi(namespace.ObjectMeta.Labels["lagoon.sh/environment"])
			projectID = helpers.UintPtr(uint(pID))
		}
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  projectName,
			Event:    "task:builddeploy-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &lagoonv1beta1.LagoonLogMeta{
				EnvironmentID: envID,
				ProjectID:     projectID,
				ProjectName:   projectName,
				BranchName:    envName,
				BuildName:     jobPod.ObjectMeta.Name,
				BuildPhase:    condition, // @TODO: same as buildstatus label, remove once lagoon is corrected in controller-handler
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
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
			projectName,
			envName,
			jobPod.ObjectMeta.Name,
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
			return true, msg
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("task:builddeploy-kubernetes:%s", condition),
					jobPod.ObjectMeta.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonLog{}
}

// updateDeploymentWithLogs collects logs from the build containers and ships or stores them
func (r *LagoonMonitorReconciler) updateDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagoonv1beta1.LagoonBuild,
	jobPod corev1.Pod,
	logs []byte,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	var buildCondition lagoonv1beta1.BuildStatusType
	switch jobPod.Status.Phase {
	case corev1.PodFailed:
		buildCondition = lagoonv1beta1.BuildStatusFailed
		cancel = false // don't cancel failed builds
	case corev1.PodSucceeded:
		buildCondition = lagoonv1beta1.BuildStatusComplete
		cancel = false // don't cancel complete builds
	case corev1.PodPending:
		buildCondition = lagoonv1beta1.BuildStatusPending
	case corev1.PodRunning:
		buildCondition = lagoonv1beta1.BuildStatusRunning
	}
	collectLogs := true
	if cancel {
		buildCondition = lagoonv1beta1.BuildStatusCancelled
		if _, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/cancelBuildNoPod"]; ok {
			collectLogs = false
		}
	}
	buildStep := "running"
	if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
		buildStep = value
	}

	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobPod.ObjectMeta.Namespace}, namespace); err != nil {
		if helpers.IgnoreNotFound(err) != nil {
			return err
		}
	}
	// if the buildstatus is pending or running, or the cancel flag is provided
	// send the update status to lagoon
	if helpers.ContainsString(
		helpers.BuildRunningPendingStatus,
		lagoonBuild.Labels["lagoon.sh/buildStatus"],
	) || cancel {
		opLog.Info(
			fmt.Sprintf(
				"Updating build status for %s to %v/%v",
				jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
				buildCondition,
				buildStep,
			),
		)
		var allContainerLogs []byte
		var err error
		if logs == nil {
			if collectLogs {
				allContainerLogs, err = r.collectLogs(ctx, req, jobPod)
				if err == nil {
					if cancel {
						cancellationMessage := "Build cancelled"
						if cancellationDetails, ok := jobPod.GetAnnotations()["lagoon.sh/cancelReason"]; ok {
							cancellationMessage = fmt.Sprintf("%v : %v", cancellationMessage, cancellationDetails)
						}
						allContainerLogs = append(allContainerLogs, []byte(fmt.Sprintf(`
========================================
%v
========================================`, cancellationMessage))...)
					}
				} else {
					allContainerLogs = []byte(fmt.Sprintf(`
========================================
Build %s
========================================`, buildCondition))
				}
			}
		} else {
			allContainerLogs = logs
		}

		mergeMap := map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus":  string(buildCondition),
					"lagoon.sh/buildStarted": "true",
				},
			},
		}

		condition := lagoonv1beta1.LagoonBuildConditions{
			Type:               buildCondition,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: time.Now().UTC().Format(time.RFC3339),
		}
		if !helpers.BuildContainsStatus(lagoonBuild.Status.Conditions, condition) {
			lagoonBuild.Status.Conditions = append(lagoonBuild.Status.Conditions, condition)
			mergeMap["status"] = map[string]interface{}{
				"conditions": lagoonBuild.Status.Conditions,
				// don't save build logs in resource anymore
			}
		}

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
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", jobPod.ObjectMeta.Namespace))
			}
		}

		// do any message publishing here, and update any pending messages if needed
		pendingStatus, pendingStatusMessage := r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv, namespace, strings.ToLower(string(buildCondition)))
		pendingEnvironment, pendingEnvironmentMessage := r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, &lagoonEnv, namespace, strings.ToLower(string(buildCondition)))
		var pendingBuildLog bool
		var pendingBuildLogMessage lagoonv1beta1.LagoonLog
		// if the container logs can't be retrieved, we don't want to send any build logs back, as this will nuke
		// any previously received logs
		if !strings.Contains(string(allContainerLogs), "unable to retrieve container logs for containerd") {
			pendingBuildLog, pendingBuildLogMessage = r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, namespace, strings.ToLower(string(buildCondition)), allContainerLogs)
		}
		if pendingStatus || pendingEnvironment || pendingBuildLog {
			mergeMap["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["lagoon.sh/pendingMessages"] = "true"
			if pendingStatus {
				mergeMap["statusMessages"].(map[string]interface{})["statusMessage"] = pendingStatusMessage
			}
			if pendingEnvironment {
				mergeMap["statusMessages"].(map[string]interface{})["environmentMessage"] = pendingEnvironmentMessage
			}
			// if the build log message is too long, don't save it
			if pendingBuildLog && len(pendingBuildLogMessage.Message) > 1048576 {
				mergeMap["statusMessages"].(map[string]interface{})["buildLogMessage"] = pendingBuildLogMessage
			}
		}
		if !pendingStatus && !pendingEnvironment && !pendingBuildLog {
			mergeMap["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["lagoon.sh/pendingMessages"] = nil
			mergeMap["statusMessages"] = nil
		}
		mergePatch, _ := json.Marshal(mergeMap)
		// check if the build exists
		if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err == nil {
			// if it does, try to patch it
			if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update resource"))
			}
		}
		// just delete the pod
		// maybe if we move away from using BASH for the kubectl-build-deploy-dind scripts we could handle cancellations better
		if cancel {
			if err := r.Get(ctx, req.NamespacedName, &jobPod); err == nil {
				if r.EnableDebug {
					opLog.Info(fmt.Sprintf("Build pod exists %s", jobPod.ObjectMeta.Name))
				}
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
