package v1beta2

// this file is used by the `lagoonbuild` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
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
	lagoonBuild *lagooncrd.LagoonBuild,
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
			opLog.Error(err, "unable to update the lagoon with LagoonBuild result")
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
	runningBuilds := &lagooncrd.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String(),
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	// list any builds that are running
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		opLog.Error(err, "unable to list builds in the namespace, there may be none or something went wrong")
		// just return nil so the deletion of the resource isn't held up
		return nil
	}
	newRunningBuilds := runningBuilds.Items
	for _, runningBuild := range runningBuilds.Items {
		// if there are any running builds, check if it is the one currently being deleted
		if lagoonBuild.ObjectMeta.Name == runningBuild.ObjectMeta.Name {
			// if the one being deleted is a running one, remove it from the list of running builds
			newRunningBuilds = lagooncrd.RemoveBuild(newRunningBuilds, runningBuild)
		}
	}
	// if the number of runningBuilds is 0 (excluding the one being deleted)
	if len(newRunningBuilds) == 0 {
		pendingBuilds := &lagooncrd.LagoonBuildList{}
		listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(lagoonBuild.ObjectMeta.Namespace),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": lagooncrd.BuildStatusPending.String(),
				"lagoon.sh/controller":  r.ControllerNamespace,
			}),
		})
		if err := r.List(ctx, pendingBuilds, listOption); err != nil {
			opLog.Error(err, "unable to list builds in the namespace, there may be none or something went wrong")
			// just return nil so the deletion of the resource isn't held up
			return nil
		}
		newPendingBuilds := pendingBuilds.Items
		for _, pendingBuild := range pendingBuilds.Items {
			// if there are any pending builds, check if it is the one currently being deleted
			if lagoonBuild.ObjectMeta.Name == pendingBuild.ObjectMeta.Name {
				// if the one being deleted a the pending one, remove it from the list of pending builds
				newPendingBuilds = lagooncrd.RemoveBuild(newPendingBuilds, pendingBuild)
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
						"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String(),
					},
				},
			})
			if err := r.Patch(ctx, pendingBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, "unable to update pending build to running status")
				return nil
			}
		} else {
			opLog.Info("No pending builds")
		}
	}
	return nil
}

func (r *LagoonBuildReconciler) updateCancelledDeploymentWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonBuild lagooncrd.LagoonBuild,
) error {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)
	// if the build status is Pending or Running,
	// then the buildCondition will be set to cancelled when we tell lagoon
	// this is because we are deleting it, so we are basically cancelling it
	// if it was already Failed or Completed, lagoon probably already knows
	// so we don't have to do anything else.
	if helpers.ContainsString(
		lagooncrd.BuildRunningPendingStatus,
		lagoonBuild.Labels["lagoon.sh/buildStatus"],
	) {
		opLog.Info(
			fmt.Sprintf(
				"Updating build status for %s to %v",
				lagoonBuild.ObjectMeta.Name,
				lagoonBuild.Labels["lagoon.sh/buildStatus"],
			),
		)

		// if we get this handler, then it is likely that the build was in a pending or running state with no actual running pod
		// so just set the logs to be cancellation message
		allContainerLogs := []byte(`
========================================
Build cancelled
========================================`)
		buildCondition := lagooncrd.BuildStatusCancelled
		lagoonBuild.Labels["lagoon.sh/buildStatus"] = buildCondition.String()
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus": buildCondition.String(),
				},
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, "unable to update build status")
		}
		// send any messages to lagoon message queues
		// update the deployment with the status of cancelled in lagoon
		r.buildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, lagooncrd.BuildStatusCancelled, "cancelled")
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, true, lagooncrd.BuildStatusCancelled, "cancelled")
		r.buildLogsToLagoonLogs(opLog, &lagoonBuild, allContainerLogs, lagooncrd.BuildStatusCancelled)
	}
	return nil
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonBuildReconciler) buildLogsToLagoonLogs(
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	logs []byte,
	buildCondition lagooncrd.BuildStatusType,
) {
	if r.EnableMQ {
		condition := buildCondition
		buildStep := "queued"
		if condition == lagooncrd.BuildStatusCancelled {
			buildStep = "cancelled"
		}
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &schema.LagoonLogMeta{
				JobName:     lagoonBuild.ObjectMeta.Name, // @TODO: remove once lagoon is corrected in controller-handler
				BuildName:   lagoonBuild.ObjectMeta.Name,
				BuildStatus: buildCondition.ToLower(), // same as buildstatus label
				BuildStep:   buildStep,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				RemoteID:    string(lagoonBuild.ObjectMeta.UID),
				LogLink:     lagoonBuild.Spec.Project.UILink,
				Cluster:     r.LagoonTargetName,
			},
		}
		// add the actual build log message
		msg.Message = string(logs)
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonBuildReconciler) updateDeploymentAndEnvironmentTask(
	ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	checkLagoonEnv bool,
	buildCondition lagooncrd.BuildStatusType,
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
		msg := schema.LagoonMessage{
			Type:      "build",
			Namespace: namespace,
			Meta: &schema.LagoonLogMeta{
				Environment: lagoonBuild.Spec.Project.Environment,
				Project:     lagoonBuild.Spec.Project.Name,
				BuildStatus: buildCondition.ToLower(),
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
		services := []schema.EnvironmentService{}
		if err := r.List(context.TODO(), podList, listOption); err == nil {
			// generate the list of services to add to the environment
			for _, pod := range podList.Items {
				var serviceName, serviceType string
				containers := []schema.ServiceContainer{}
				if name, ok := pod.ObjectMeta.Labels["lagoon.sh/service"]; ok {
					serviceName = name
					serviceNames = append(serviceNames, serviceName)
					for _, container := range pod.Spec.Containers {
						containers = append(containers, schema.ServiceContainer{Name: container.Name})
					}
				}
				if sType, ok := pod.ObjectMeta.Labels["lagoon.sh/service-type"]; ok {
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
		if checkLagoonEnv {
			lagoonEnv, route, routes := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, lagoonBuild.ObjectMeta.Namespace)
			// if we aren't being provided the lagoon config, we can skip adding the routes etc
			if lagoonEnv {
				msg.Meta.Route = route
				msg.Meta.Routes = routes
			}
		}
		if buildCondition.ToLower() == "failed" || buildCondition.ToLower() == "complete" || buildCondition.ToLower() == "cancelled" {
			msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
		if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonBuildReconciler) buildStatusLogsToLagoonLogs(
	ctx context.Context,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	buildCondition lagooncrd.BuildStatusType,
	buildStep string,
) {
	if r.EnableMQ {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + buildCondition.ToLower(), //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &schema.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildStatus: buildCondition.ToLower(), // same as buildstatus label
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
		lagoonEnv, route, routes := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, lagoonBuild.ObjectMeta.Namespace)
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if lagoonEnv {
			msg.Meta.Route = route
			msg.Meta.Routes = routes
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}
