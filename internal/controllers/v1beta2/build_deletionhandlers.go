package v1beta2

// this file is used by the `lagoonbuild` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
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
	err := lagooncrd.DeleteBuildPod(ctx, r.Client, opLog, lagoonBuild, req.NamespacedName, r.ControllerNamespace)
	if err != nil {
		if strings.Contains(err.Error(), "unable to find a build pod for") {
			err = r.updateCancelledDeploymentWithLogs(ctx, req, *lagoonBuild)
			if err != nil {
				opLog.Error(err, "unable to update the lagoon with LagoonBuild result")
			}
		} else {
			return err
		}
	}
	return lagooncrd.DeleteBuildResources(ctx, r.Client, opLog, lagoonBuild, req.NamespacedName, r.ControllerNamespace)
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
		ns := &corev1.Namespace{}
		if err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
			if helpers.IgnoreNotFound(err) != nil {
				opLog.Error(err, "namespace %s not found", namespace)
				return
			}
		}
		envName := ns.ObjectMeta.Labels["lagoon.sh/environment"]
		eID, _ := strconv.Atoi(ns.ObjectMeta.Labels["lagoon.sh/environmentId"])
		envID := helpers.UintPtr(uint(eID))
		projectName := ns.ObjectMeta.Labels["lagoon.sh/project"]
		pID, _ := strconv.Atoi(ns.ObjectMeta.Labels["lagoon.sh/projectId"])
		projectID := helpers.UintPtr(uint(pID))
		msg := schema.LagoonMessage{
			Type:      "build",
			Namespace: namespace,
			Meta: &schema.LagoonLogMeta{
				Environment:   envName,
				Project:       projectName,
				EnvironmentID: envID,
				ProjectID:     projectID,
				BuildStatus:   buildCondition.ToLower(),
				BuildStep:     buildStep,
				BuildName:     lagoonBuild.ObjectMeta.Name,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				RemoteID:      string(lagoonBuild.ObjectMeta.UID),
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
			route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, lagoonBuild.ObjectMeta.Namespace)
			// if we aren't being provided the lagoon config, we can skip adding the routes etc
			if err == nil {
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
		route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, r.Client, lagoonBuild.ObjectMeta.Namespace)
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if err == nil {
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
