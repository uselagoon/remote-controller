package controllers

// this file is used by the `lagoonbuild` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"
	"gopkg.in/matryer/try.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// handle deleting any external resources here
func (r *LagoonBuildReconciler) deleteExternalResources(
	ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	req ctrl.Request,
) error {
	// get any running pods that this build may have already created
	lagoonBuildPod := corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: lagoonBuild.ObjectMeta.Namespace,
		Name:      lagoonBuild.ObjectMeta.Name,
	}, &lagoonBuildPod)
	if err != nil {
		opLog.Info(fmt.Sprintf("Unable to find a build pod associated to this build, continuing to process build deletion"))
		// handle updating lagoon for a deleted build with no running pod
		// only do it if the build status is Pending or Running though
		err = r.updateDeploymentWithLogs(ctx, req, *lagoonBuild)
		if err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update the lagoon with LagoonBuild result"))
		}
	} else {
		opLog.Info(fmt.Sprintf("Found build pod, deleting it"))
		// handle updating lagoon for a deleted build with a running pod
		// only do it if the build status is Pending or Running though
		// delete the pod, let the pod deletion handler deal with the cleanup there
		if err := r.Delete(ctx, &lagoonBuildPod); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to delete the the LagoonBuild pod"))
		}
		// check that the pod is deleted before continuing, this allows the pod deletion to happen
		// and the pod deletion process in the LagoonMonitor controller to be able to send what it needs back to lagoon
		// this 1 minute timeout will just hold up the deletion of `LagoonBuild` resources only if a build pod exists
		// if the 1 minute timeout is reached the worst that happens is a deployment will show as running
		// but cancelling the deployment in lagoon will force it to go to a cancelling state in the lagoon api
		// @TODO: we could use finalizers on the build pods, but to avoid holding up other processes we can just give up after waiting for a minute
		try.MaxRetries = 12
		err = try.Do(func(attempt int) (bool, error) {
			var podErr error
			err := r.Get(ctx, types.NamespacedName{
				Namespace: lagoonBuild.ObjectMeta.Namespace,
				Name:      lagoonBuild.ObjectMeta.Name,
			}, &lagoonBuildPod)
			if err != nil {
				// the pod doesn't exist anymore, so exit the retry
				podErr = nil
				opLog.Info(fmt.Sprintf("Pod deleted"))
			} else {
				// if the pod still exists wait 5 seconds before trying again
				time.Sleep(5 * time.Second)
				podErr = fmt.Errorf("pod still exists")
				opLog.Info(fmt.Sprintf("Pod still exists"))
			}
			return attempt < 12, podErr
		})
		if err != nil {
			return err
		}
	}

	// if the LagoonBuild is deleted, then check if the only running build is the one being deleted
	// or if there are any pending builds that can be started
	runningBuilds := &lagoonv1alpha1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": "Running",
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	// list any builds that are running
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list builds in the namespace, there may be none or something went wrong"))
		// just return nil so the deletion of the resource isn't held up
		return nil
	}
	newRunningBuilds := runningBuilds.Items
	for _, runningBuild := range runningBuilds.Items {
		// if there are any running builds, check if it is the one currently being deleted
		if lagoonBuild.ObjectMeta.Name == runningBuild.ObjectMeta.Name {
			// if the one being deleted is a running one, remove it from the list of running builds
			newRunningBuilds = removeBuild(newRunningBuilds, runningBuild)
		}
	}
	// if the number of runningBuilds is 0 (excluding the one being deleted)
	if len(newRunningBuilds) == 0 {
		pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": "Pending",
				"lagoon.sh/controller":  r.ControllerNamespace,
			}),
		})
		if err := r.List(ctx, pendingBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list builds in the namespace, there may be none or something went wrong"))
			// just return nil so the deletion of the resource isn't held up
			return nil
		}
		newPendingBuilds := pendingBuilds.Items
		for _, pendingBuild := range pendingBuilds.Items {
			// if there are any pending builds, check if it is the one currently being deleted
			if lagoonBuild.ObjectMeta.Name == pendingBuild.ObjectMeta.Name {
				// if the one being deleted a the pending one, remove it from the list of pending builds
				newPendingBuilds = removeBuild(newPendingBuilds, pendingBuild)
			}
		}
		// sort the pending builds by creation timestamp
		sort.Slice(newPendingBuilds, func(i, j int) bool {
			return newPendingBuilds[i].ObjectMeta.CreationTimestamp.Before(&newPendingBuilds[j].ObjectMeta.CreationTimestamp)
		})
		// if there are more than 1 pending builds (excluding the one being deleted), update the oldest one to running
		if len(newPendingBuilds) > 0 {
			pendingBuild := pendingBuilds.Items[0].DeepCopy()
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"lagoon.sh/buildStatus": "Running",
					},
				},
			})
			if err := r.Patch(ctx, pendingBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update pending build to running status"))
				return nil
			}
		} else {
			opLog.Info(fmt.Sprintf("No pending builds"))
		}
	}
	return nil
}

func (r *LagoonBuildReconciler) updateDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagoonv1alpha1.LagoonBuild,
) error {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)
	// if the build status is Pending or Running,
	// then the jobCondition will be set to cancelled when we tell lagoon
	// this is because we are deleting it, so we are basically cancelling it
	// if it was already Failed or Completed, lagoon probably already knows
	// so we don't have to do anything else.
	if containsString(
		[]string{
			"Pending",
			"Running",
		},
		lagoonBuild.Labels["lagoon.sh/buildStatus"],
	) {
		opLog.Info(
			fmt.Sprintf(
				"Updating build status for %s to %v",
				lagoonBuild.ObjectMeta.Name,
				lagoonBuild.Labels["lagoon.sh/buildStatus"],
			),
		)

		var allContainerLogs []byte
		// if we get this handler, then it is likely that the build was in a pending or running state with no actual running pod
		// so just set the logs to be cancellation message
		allContainerLogs = []byte(fmt.Sprintf(`
========================================
Build cancelled
========================================`))
		var jobCondition lagoonv1alpha1.JobConditionType
		jobCondition = lagoonv1alpha1.JobCancelled
		lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(jobCondition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus": string(jobCondition),
				},
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update build status"))
		}
		// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
		var lagoonEnv corev1.ConfigMap
		err := r.Get(ctx, types.NamespacedName{
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Name:      "lagoon-env",
		},
			&lagoonEnv,
		)
		if err != nil {
			// if there isn't a configmap, just info it and move on
			// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
			opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", lagoonBuild.ObjectMeta.Namespace))
		}
		// send any messages to lagoon message queues
		// update the deployment with the status
		r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &lagoonEnv)
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &lagoonEnv)
		r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, allContainerLogs)
	}
	return nil
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonBuildReconciler) buildLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	logs []byte,
) {
	if r.EnableMQ {
		condition := "cancelled"
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				JobName:    lagoonBuild.ObjectMeta.Name,
				BranchName: lagoonBuild.Spec.Project.Environment,
				BuildPhase: condition,
				RemoteID:   string(lagoonBuild.ObjectMeta.UID),
				LogLink:    lagoonBuild.Spec.Project.UILink,
			},
		}
		// add the actual build log message
		msg.Message = fmt.Sprintf(`========================================
Logs on pod %s
========================================
%s`, lagoonBuild.ObjectMeta.Name, logs)
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
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
func (r *LagoonBuildReconciler) updateDeploymentAndEnvironmentTask(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	lagoonEnv *corev1.ConfigMap,
) {
	if r.EnableMQ {
		condition := "cancelled"
		msg := lagoonv1alpha1.LagoonMessage{
			Type:      "build",
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				Environment: lagoonBuild.Spec.Project.Environment,
				Project:     lagoonBuild.Spec.Project.Name,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				LogLink:     lagoonBuild.Spec.Project.UILink,
				RemoteID:    string(lagoonBuild.ObjectMeta.UID),
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
		msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
		msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
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
func (r *LagoonBuildReconciler) buildStatusLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	lagoonEnv *corev1.ConfigMap) {
	if r.EnableMQ {
		condition := "cancelled"
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				LogLink:     lagoonBuild.Spec.Project.UILink,
			},
			Message: fmt.Sprintf("*[%s]* %s Build `%s` %s",
				lagoonBuild.Spec.Project.Name,
				lagoonBuild.Spec.Project.Environment,
				lagoonBuild.ObjectMeta.Name,
				"cancelled",
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
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
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

// updateEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonBuildReconciler) updateEnvironmentMessage(ctx context.Context,
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
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateBuildStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonBuildReconciler) updateBuildStatusMessage(ctx context.Context,
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
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// removeBuildPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonBuildReconciler) removeBuildPendingMessageStatus(ctx context.Context,
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
			if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}

// updateBuildLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonBuildReconciler) updateBuildLogMessage(ctx context.Context,
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
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}
