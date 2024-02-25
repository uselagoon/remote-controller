package v1beta1

// this file is used by the `lagoonbuild` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
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
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	req ctrl.Request,
) error {
	// get any running pods that this build may have already created
	lagoonBuildPod := corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: lagoonBuild.ObjectMeta.Namespace,
		Name:      lagoonBuild.ObjectMeta.Name,
	}, &lagoonBuildPod)
	if err != nil {
		opLog.Info(fmt.Sprintf("Unable to find a build pod for %s, continuing to process build deletion", lagoonBuild.ObjectMeta.Name))
		// handle updating lagoon for a deleted build with no running pod
		// only do it if the build status is Pending or Running though
		err = r.updateCancelledDeploymentWithLogs(ctx, req, *lagoonBuild)
		if err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update the lagoon with LagoonBuild result"))
		}
	} else {
		opLog.Info(fmt.Sprintf("Found build pod for %s, deleting it", lagoonBuild.ObjectMeta.Name))
		// handle updating lagoon for a deleted build with a running pod
		// only do it if the build status is Pending or Running though
		// delete the pod, let the pod deletion handler deal with the cleanup there
		if err := r.Delete(ctx, &lagoonBuildPod); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to delete the the LagoonBuild pod %s", lagoonBuild.ObjectMeta.Name))
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
				opLog.Info(fmt.Sprintf("Pod %s deleted", lagoonBuild.ObjectMeta.Name))
			} else {
				// if the pod still exists wait 5 seconds before trying again
				time.Sleep(5 * time.Second)
				podErr = fmt.Errorf("pod %s still exists", lagoonBuild.ObjectMeta.Name)
				opLog.Info(fmt.Sprintf("Pod %s still exists", lagoonBuild.ObjectMeta.Name))
			}
			return attempt < 12, podErr
		})
		if err != nil {
			return err
		}
	}

	// if the LagoonBuild is deleted, then check if the only running build is the one being deleted
	// or if there are any pending builds that can be started
	runningBuilds := &lagoonv1beta1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusRunning.String(),
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
			newRunningBuilds = helpers.RemoveBuild(newRunningBuilds, runningBuild)
		}
	}
	// if the number of runningBuilds is 0 (excluding the one being deleted)
	if len(newRunningBuilds) == 0 {
		pendingBuilds := &lagoonv1beta1.LagoonBuildList{}
		listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusPending.String(),
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
				newPendingBuilds = helpers.RemoveBuild(newPendingBuilds, pendingBuild)
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
						"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusRunning.String(),
					},
				},
			})
			if err := r.Patch(ctx, pendingBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update pending build to running status"))
				return nil
			}
		} else {
			opLog.Info(fmt.Sprintf("No pending builds"))
		}
	}
	return nil
}

func (r *LagoonBuildReconciler) updateCancelledDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagoonv1beta1.LagoonBuild,
) error {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)
	// if the build status is Pending or Running,
	// then the buildCondition will be set to cancelled when we tell lagoon
	// this is because we are deleting it, so we are basically cancelling it
	// if it was already Failed or Completed, lagoon probably already knows
	// so we don't have to do anything else.
	if helpers.ContainsString(
		helpers.BuildRunningPendingStatus,
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
		var buildCondition lagoonv1beta1.BuildStatusType
		buildCondition = lagoonv1beta1.BuildStatusCancelled
		lagoonBuild.Labels["lagoon.sh/buildStatus"] = buildCondition.String()
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus": buildCondition.String(),
				},
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
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
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", lagoonBuild.ObjectMeta.Namespace))
			}
		}
		// send any messages to lagoon message queues
		// update the deployment with the status of cancelled in lagoon
		r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &lagoonEnv, lagoonv1beta1.BuildStatusCancelled, "cancelled")
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &lagoonEnv, lagoonv1beta1.BuildStatusCancelled, "cancelled")
		r.buildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, allContainerLogs, lagoonv1beta1.BuildStatusCancelled)
	}
	return nil
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonBuildReconciler) buildLogsToLagoonLogs(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	logs []byte,
	buildCondition lagoonv1beta1.BuildStatusType,
) {
	if r.EnableMQ {
		condition := buildCondition
		buildStep := "queued"
		if condition == lagoonv1beta1.BuildStatusCancelled {
			buildStep = "cancelled"
		}
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				JobName:     lagoonBuild.ObjectMeta.Name, // @TODO: remove once lagoon is corrected in controller-handler
				BuildName:   lagoonBuild.ObjectMeta.Name,
				BuildPhase:  buildCondition.ToLower(), // @TODO: same as buildstatus label, remove once lagoon is corrected in controller-handler
				BuildStatus: buildCondition.ToLower(), // same as buildstatus label
				BuildStep:   buildStep,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				RemoteID:    string(lagoonBuild.ObjectMeta.UID),
				LogLink:     lagoonBuild.Spec.Project.UILink,
				Cluster:     r.LagoonTargetName,
			},
		}
		// add the actual build log message
		msg.Message = fmt.Sprintf("%s", logs)
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
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	lagoonEnv *corev1.ConfigMap,
	buildCondition lagoonv1beta1.BuildStatusType,
	buildStep string,
) {
	namespace := helpers.GenerateNamespaceName(
		lagoonBuild.Spec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		lagoonBuild.Spec.Project.Environment,
		lagoonBuild.Spec.Project.Name,
		r.NamespacePrefix,
		r.ControllerNamespace,
		r.RandomNamespacePrefix,
	)
	if r.EnableMQ {
		msg := lagoonv1beta1.LagoonMessage{
			Type:      "build",
			Namespace: namespace,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				Environment: lagoonBuild.Spec.Project.Environment,
				Project:     lagoonBuild.Spec.Project.Name,
				BuildPhase:  buildCondition.ToLower(),
				BuildStep:   buildStep,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				LogLink:     lagoonBuild.Spec.Project.UILink,
				RemoteID:    string(lagoonBuild.ObjectMeta.UID),
				Cluster:     r.LagoonTargetName,
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
		services := []lagoonv1beta1.LagoonService{}
		if err := r.List(context.TODO(), podList, listOption); err == nil {
			// generate the list of services to add to the environment
			for _, pod := range podList.Items {
				var serviceName, serviceType string
				containers := []lagoonv1beta1.EnvironmentContainer{}
				if name, ok := pod.ObjectMeta.Labels["lagoon.sh/service"]; ok {
					serviceName = name
					serviceNames = append(serviceNames, serviceName)
					for _, container := range pod.Spec.Containers {
						containers = append(containers, lagoonv1beta1.EnvironmentContainer{Name: container.Name})
					}
				}
				if sType, ok := pod.ObjectMeta.Labels["lagoon.sh/service-type"]; ok {
					serviceType = sType
				}
				// probably need to collect dbaas consumers too at some stage
				services = append(services, lagoonv1beta1.LagoonService{
					Name:       serviceName,
					Type:       serviceType,
					Containers: containers,
				})
			}
			msg.Meta.Services = serviceNames
			msg.Meta.EnvironmentServices = services
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
		}
		if buildCondition.ToLower() == "failed" || buildCondition.ToLower() == "complete" || buildCondition.ToLower() == "cancelled" {
			msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
		}
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
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	lagoonEnv *corev1.ConfigMap,
	buildCondition lagoonv1beta1.BuildStatusType,
	buildStep string,
) {
	if r.EnableMQ {
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + buildCondition.ToLower(), //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &lagoonv1beta1.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildPhase:  buildCondition.ToLower(),
				BuildName:   lagoonBuild.ObjectMeta.Name,
				BuildStep:   buildStep,
				LogLink:     lagoonBuild.Spec.Project.UILink,
				Cluster:     r.LagoonTargetName,
			},
			Message: fmt.Sprintf("*[%s]* %s Build `%s` %s",
				lagoonBuild.Spec.Project.Name,
				lagoonBuild.Spec.Project.Environment,
				lagoonBuild.ObjectMeta.Name,
				buildCondition.ToLower(),
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
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	envMessage lagoonv1beta1.LagoonMessage,
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

// updateBuildStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonBuildReconciler) updateBuildStatusMessage(ctx context.Context,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	statusMessage lagoonv1beta1.LagoonLog,
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

// removeBuildPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonBuildReconciler) removeBuildPendingMessageStatus(ctx context.Context,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
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

// updateBuildLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonBuildReconciler) updateBuildLogMessage(ctx context.Context,
	lagoonBuild *lagoonv1beta1.LagoonBuild,
	buildMessage lagoonv1beta1.LagoonLog,
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
