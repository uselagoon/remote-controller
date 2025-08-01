package v1beta2

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
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *BuildMonitorReconciler) handleBuildMonitor(ctx context.Context,
	opLog logr.Logger,
	req ctrl.Request,
	jobPod corev1.Pod,
) error {
	// get the build associated to this pod, we wil need update it at some point
	var lagoonBuild lagooncrd.LagoonBuild
	err := r.Get(ctx, types.NamespacedName{
		Namespace: jobPod.Namespace,
		Name:      jobPod.Labels["lagoon.sh/buildName"],
	}, &lagoonBuild)
	if err != nil {
		return err
	}
	// ensure the build is not in the queue
	r.QueueCache.Remove(jobPod.Name)
	cancel := false
	if cancelBuild, ok := jobPod.Labels["lagoon.sh/cancelBuild"]; ok {
		cancel, _ = strconv.ParseBool(cancelBuild)
	}
	_, ok := r.Cache.Get(lagoonBuild.Name)
	if ok {
		opLog.Info(fmt.Sprintf("Cached cancellation exists for: %s", lagoonBuild.Name))
		// this object exists in the cache meaning the task has been cancelled, set cancel to true and remove from cache
		r.Cache.Remove(lagoonBuild.Name)
		cancel = true
	}
	if cancel {
		opLog.Info(fmt.Sprintf("Attempting to cancel build %s", lagoonBuild.Name))
		return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, cancel)
	}
	dockerBuild := true //nolint:staticcheck
	if imagesComplete, ok := jobPod.Labels["build.lagoon.sh/images-complete"]; ok && imagesComplete == "true" {
		dockerBuild = false
	}
	// brand new builds won't have this label/value so it will be empty
	if jobPod.Labels["lagoon.sh/buildStep"] == "" {
		dockerBuild = true
	}
	bc := lagooncrd.NewCachedBuildItem(lagoonBuild, string(jobPod.Status.Phase), dockerBuild)
	r.BuildCache.Add(lagoonBuild.Name, bc.String())
	// check if the build pod is in pending, a container in the pod could be failed in this state
	switch jobPod.Status.Phase {
	case corev1.PodPending:
		// check each container in the pod
		for _, container := range jobPod.Status.ContainerStatuses {
			// if the container is a lagoon-build container
			// which currently it will be as only one container is spawned in a build
			if container.Name == "lagoon-build" {
				// check if the state of the pod is one of our failure states
				if container.State.Waiting != nil && helpers.ContainsString(failureStates, container.State.Waiting.Reason) {
					// if we have a failure state, then fail the build and get the logs from the container
					opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
					lagoonBuild.Labels["lagoon.sh/buildStatus"] = lagooncrd.BuildStatusFailed.String()
					if err := r.Update(ctx, &lagoonBuild); err != nil {
						return err
					}
					opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.Name, lagooncrd.BuildStatusFailed.String()))
					if err := r.Delete(ctx, &jobPod); err != nil {
						return err
					}
					opLog.Info(fmt.Sprintf("Deleted failed build pod: %s", jobPod.Name))
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
					// remove the build from the build cache
					r.BuildCache.Remove(jobPod.Name)
					// send any messages to lagoon message queues
					logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
					return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, []byte(logMsg), false)
				}
			}
		}
		return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, false)
	case corev1.PodRunning:
		// if the pod is running and detects a change to the pod (eg, detecting an updated lagoon.sh/buildStep label)
		// then ship or store the logs
		// get the build associated to this pod, the information in the resource is used for shipping the logs
		var lagoonBuild lagooncrd.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.Namespace,
				Name:      jobPod.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
		// if the buildpod status is failed or succeeded
		// mark the build accordingly and ship the information back to lagoon
	case corev1.PodFailed, corev1.PodSucceeded:
		// get the build associated to this pod, we wil need update it at some point
		var lagoonBuild lagooncrd.LagoonBuild
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.Namespace,
				Name:      jobPod.Labels["lagoon.sh/buildName"],
			}, &lagoonBuild)
		if err != nil {
			return err
		}
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Build %s reached %s", lagoonBuild.Name, jobPod.Status.Phase))
		}
		// remove the build from the build cache
		r.BuildCache.Remove(jobPod.Name)
	}

	// send any messages to lagoon message queues
	return r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, false)
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *BuildMonitorReconciler) buildLogsToLagoonLogs(
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	jobPod *corev1.Pod,
	namespace *corev1.Namespace,
	condition string,
	logs []byte,
) error {
	if r.EnableMQ {
		buildStep := "pending"
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			// set build step to anything other than running if the condition isn't running
			buildStep = condition
		}
		// then check the resource to see if the buildstep exists, this bit we care about so we can see where it maybe failed if its available
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		envName := namespace.Labels["lagoon.sh/environment"]
		eID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/environmentId"])
		envID := helpers.UintPtr(uint(eID))
		projectName := namespace.Labels["lagoon.sh/project"]
		pID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/projectId"])
		projectID := helpers.UintPtr(uint(pID))
		remoteId := string(jobPod.UID)
		if value, ok := jobPod.Labels["lagoon.sh/buildRemoteID"]; ok {
			remoteId = value
		}
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  projectName,
			Event:    "build-logs:builddeploy-kubernetes:" + jobPod.Name,
			Meta: &schema.LagoonLogMeta{
				EnvironmentID: envID,
				ProjectID:     projectID,
				BuildName:     jobPod.Name,
				BranchName:    envName,
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
				RemoteID:      remoteId,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				Cluster:       r.LagoonTargetName,
			},
		}
		// add the actual build log message
		if jobPod.Spec.NodeName != "" {
			msg.Message = fmt.Sprintf(`================================================================================
Logs on pod %s, assigned to node %s on cluster %s
================================================================================
%s`, jobPod.Name, jobPod.Spec.NodeName, r.LagoonTargetName, logs)
		} else {
			msg.Message = fmt.Sprintf(`========================================
Logs on pod %s, assigned to cluster %s
========================================
%s`, jobPod.Name, r.LagoonTargetName, logs)
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			// r.updateBuildLogMessage(ctx, lagoonBuild, msg)
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("build-logs:builddeploy-kubernetes:%s", jobPod.Name),
					jobPod.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *BuildMonitorReconciler) updateDeploymentAndEnvironmentTask(
	ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	jobPod *corev1.Pod,
	namespace *corev1.Namespace,
	condition string,
) error {
	if r.EnableMQ {
		buildStep := "pending"
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			// set build step to anything other than running if the condition isn't running
			buildStep = condition
		}
		// then check the resource to see if the buildstep exists, this bit we care about so we can see where it maybe failed if its available
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			time.AfterFunc(31*time.Second, func() {
				metrics.BuildRunningStatus.Delete(prometheus.Labels{
					"build_namespace": jobPod.Namespace,
					"build_name":      jobPod.Name,
				})
			})
			// remove the build from the buildcache
			_ = r.DockerHost.BuildCache.Remove(jobPod.Name)
			time.Sleep(2 * time.Second) // smol sleep to reduce race of final messages with previous messages
		}
		if condition == "running" {
			if dockerHost, ok := jobPod.Labels["dockerhost.lagoon.sh/name"]; ok {
				// always ensure that a running pod populates the buildcache
				_ = r.DockerHost.BuildCache.Add(jobPod.Name, dockerHost)
			}
		}
		envName := namespace.Labels["lagoon.sh/environment"]
		eID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/environmentId"])
		envID := helpers.UintPtr(uint(eID))
		projectName := namespace.Labels["lagoon.sh/project"]
		pID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/projectId"])
		projectID := helpers.UintPtr(uint(pID))
		remoteId := string(jobPod.UID)
		if value, ok := jobPod.Labels["lagoon.sh/buildRemoteID"]; ok {
			remoteId = value
		}
		msg := schema.LagoonMessage{
			Type:      "build",
			Namespace: namespace.Name,
			Meta: &schema.LagoonLogMeta{
				Environment:   envName,
				EnvironmentID: envID,
				Project:       projectName,
				ProjectID:     projectID,
				BuildName:     jobPod.Name,
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				RemoteID:      remoteId,
				Cluster:       r.LagoonTargetName,
			},
		}
		labelRequirements1, _ := labels.NewRequirement("lagoon.sh/service", selection.NotIn, []string{"faketest"})
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(jobPod.Namespace),
			client.MatchingLabelsSelector{
				Selector: labels.NewSelector().Add(*labelRequirements1),
			},
		})
		depList := &appsv1.DeploymentList{}
		serviceNames := []string{}
		services := []schema.EnvironmentService{}
		if err := r.List(context.TODO(), depList, listOption); err == nil {
			// generate the list of services to add or update to the environment
			for _, deployment := range depList.Items {
				var serviceName, serviceType string
				containers := []schema.ServiceContainer{}
				if name, ok := deployment.Labels["lagoon.sh/service"]; ok {
					serviceName = name
					serviceNames = append(serviceNames, serviceName)
					for _, container := range deployment.Spec.Template.Spec.Containers {
						containers = append(containers, schema.ServiceContainer{Name: container.Name})
					}
				}
				if sType, ok := deployment.Labels["lagoon.sh/service-type"]; ok {
					serviceType = sType
				}
				// probably need to collect dbaas consumers too at some stage
				services = append(services, schema.EnvironmentService{
					Name:       serviceName,
					Type:       serviceType,
					Containers: containers,
				})
			}
			msg.Meta.Services = serviceNames
			msg.Meta.EnvironmentServices = services
		}
		route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, namespace.Name)
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if err == nil {
			msg.Meta.Route = route
			msg.Meta.Routes = routes
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
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published build update message for %s to lagoon-tasks:controller queue",
					jobPod.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *BuildMonitorReconciler) buildStatusLogsToLagoonLogs(
	ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	jobPod *corev1.Pod,
	namespace *corev1.Namespace,
	condition string,
) error {
	if r.EnableMQ {
		buildStep := "pending"
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			// set build step to anything other than running if the condition isn't running
			buildStep = condition
		}
		// then check the resource to see if the buildstep exists, this bit we care about so we can see where it maybe failed if its available
		if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
			buildStep = value
		}
		envName := namespace.Labels["lagoon.sh/environment"]
		eID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/environmentId"])
		envID := helpers.UintPtr(uint(eID))
		projectName := namespace.Labels["lagoon.sh/project"]
		pID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/projectId"])
		projectID := helpers.UintPtr(uint(pID))
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  projectName,
			Event:    "task:builddeploy-kubernetes:" + condition, // @TODO: this probably needs to be changed to a new task event for the controller
			Meta: &schema.LagoonLogMeta{
				EnvironmentID: envID,
				ProjectID:     projectID,
				ProjectName:   projectName,
				BranchName:    envName,
				BuildName:     jobPod.Name,
				BuildStatus:   condition, // same as buildstatus label
				BuildStep:     buildStep,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				Cluster:       r.LagoonTargetName,
			},
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		var addRoute, addRoutes string
		route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, namespace.Name)
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if err == nil {
			msg.Meta.Route = route
			addRoute = fmt.Sprintf("\n%s", route)
			msg.Meta.Routes = routes
			addRoutes = fmt.Sprintf("\n%s", strings.Join(routes, "\n"))
		}
		msg.Message = fmt.Sprintf("*[%s]* `%s` Build `%s` %s <%s|Logs>%s%s",
			projectName,
			envName,
			jobPod.Name,
			string(jobPod.Status.Phase),
			lagoonBuild.Spec.Project.UILink,
			addRoute,
			addRoutes,
		)
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("task:builddeploy-kubernetes:%s", condition),
					jobPod.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// updateDeploymentWithLogs collects logs from the build containers and ships or stores them
func (r *BuildMonitorReconciler) updateDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagooncrd.LagoonBuild,
	jobPod corev1.Pod,
	logs []byte,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	buildCondition := lagooncrd.GetBuildConditionFromPod(jobPod.Status.Phase)
	collectLogs := true
	if cancel {
		// only set the status to cancelled if the pod is running/pending/queued
		// otherwise send the existing status of complete/failed/cancelled
		if helpers.ContainsString(
			lagooncrd.BuildRunningPendingStatus,
			lagoonBuild.Labels["lagoon.sh/buildStatus"],
		) {
			buildCondition = lagooncrd.BuildStatusCancelled
		}
		if _, ok := lagoonBuild.Labels["lagoon.sh/cancelBuildNoPod"]; ok {
			collectLogs = false
		}
	}
	buildStep := "pending"
	if value, ok := jobPod.Labels["lagoon.sh/buildStep"]; ok {
		buildStep = value
	}

	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobPod.Namespace}, namespace); err != nil {
		if helpers.IgnoreNotFound(err) != nil {
			return err
		}
	}
	// if the buildstatus is pending or running, or the cancel flag is provided
	// send the update status to lagoon
	if helpers.ContainsString(
		lagooncrd.BuildRunningPendingStatus,
		lagoonBuild.Labels["lagoon.sh/buildStatus"],
	) || cancel {
		opLog.Info(
			fmt.Sprintf(
				"Updating build status for %s to %v/%v",
				jobPod.Labels["lagoon.sh/buildName"],
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
					"lagoon.sh/buildStatus":  buildCondition.String(),
					"lagoon.sh/buildStarted": "true",
				},
			},
		}
		condition1, condition2 := helpers.BuildStepToStatusConditions(buildStep, buildCondition.String(), time.Now().UTC())
		_ = meta.SetStatusCondition(&lagoonBuild.Status.Conditions, condition1)
		_ = meta.SetStatusCondition(&lagoonBuild.Status.Conditions, condition2)
		mergeMap["status"] = map[string]interface{}{
			"conditions": lagoonBuild.Status.Conditions,
			"phase":      buildCondition.String(),
		}

		// do any message publishing here, and update any pending messages if needed
		if err = r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &jobPod, namespace, buildCondition.ToLower()); err != nil {
			opLog.Error(err, "unable to publish build status logs")
		}
		if err = r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &jobPod, namespace, buildCondition.ToLower()); err != nil {
			opLog.Error(err, "unable to publish build update")
		}
		// if the container logs can't be retrieved, we don't want to send any build logs back, as this will nuke
		// any previously received logs
		if !strings.Contains(string(allContainerLogs), "unable to retrieve container logs for containerd") {
			if err = r.buildLogsToLagoonLogs(opLog, &lagoonBuild, &jobPod, namespace, buildCondition.ToLower(), allContainerLogs); err != nil {
				opLog.Error(err, "unable to publish build logs")
			}
		}

		mergePatch, _ := json.Marshal(mergeMap)
		// check if the build exists
		if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err == nil {
			// if it does, try to patch it
			if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, "unable to update resource")
			}
		}
		// just delete the pod
		// maybe if we move away from using BASH for the kubectl-build-deploy-dind scripts we could handle cancellations better
		if cancel {
			if err := r.Get(ctx, req.NamespacedName, &jobPod); err == nil {
				if r.EnableDebug {
					opLog.Info(fmt.Sprintf("Build pod exists %s", jobPod.Name))
				}
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
