package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type LagoonServices struct {
	Services []schema.EnvironmentService `json:"services"`
	Volumes  []schema.EnvironmentVolume  `json:"volumes"`
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (m *Messenger) BuildStatusLogsToLagoonLogs(
	ctx context.Context,
	mq bool,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	buildCondition lagooncrd.BuildStatusType,
	targetName, buildStep string,
) {
	if mq {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + buildCondition.ToLower(), // @TODO: this probably needs to be changed to a new task event for the controller
			Meta: &schema.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildStatus: buildCondition.ToLower(), // same as buildstatus label
				BuildName:   lagoonBuild.Name,
				BuildStep:   buildStep,
				LogLink:     lagoonBuild.Spec.Project.UILink,
				Cluster:     targetName,
			},
			Message: fmt.Sprintf("*[%s]* %s Build `%s` %s",
				lagoonBuild.Spec.Project.Name,
				lagoonBuild.Spec.Project.Environment,
				lagoonBuild.Name,
				buildCondition.ToLower(),
			),
		}
		route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, m.Client, lagoonBuild.Namespace)
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
		if err := m.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (m *Messenger) UpdateDeploymentAndEnvironmentTask(
	ctx context.Context,
	mq bool,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	checkLagoonEnv bool,
	buildCondition lagooncrd.BuildStatusType,
	targetName, buildStep string,
) {
	namespace := helpers.GenerateNamespaceName(
		lagoonBuild.Spec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		lagoonBuild.Spec.Project.Environment,
		lagoonBuild.Spec.Project.Name,
		m.NamespacePrefix,
		m.ControllerNamespace,
		m.RandomNamespacePrefix,
	)
	if mq {
		ns := &corev1.Namespace{}
		if err := m.Client.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
			if helpers.IgnoreNotFound(err) != nil {
				opLog.Error(err, "namespace %s not found", namespace)
				return
			}
		}
		envName := ns.Labels["lagoon.sh/environment"]
		eID, _ := strconv.Atoi(ns.Labels["lagoon.sh/environmentId"])
		envID := helpers.UintPtr(uint(eID))
		projectName := ns.Labels["lagoon.sh/project"]
		pID, _ := strconv.Atoi(ns.Labels["lagoon.sh/projectId"])
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
				BuildName:     lagoonBuild.Name,
				LogLink:       lagoonBuild.Spec.Project.UILink,
				RemoteID:      string(lagoonBuild.UID),
				Cluster:       targetName,
			},
		}
		lagoonServices := &corev1.ConfigMap{}
		if err := m.APIReader.Get(ctx, types.NamespacedName{Namespace: lagoonBuild.Namespace, Name: "lagoon-services"}, lagoonServices); err != nil {
			if helpers.IgnoreNotFound(err) != nil {
				opLog.Error(err, "configmap %s not found", "lagoon-services")
				return
			}
		}
		if val, ok := lagoonServices.Data["post-deploy"]; ok {
			serviceConfig := LagoonServices{}
			err := json.Unmarshal([]byte(val), &serviceConfig)
			if err == nil {
				fmt.Println(serviceConfig.Services)
				fmt.Println(serviceConfig.Volumes)
				msg.Meta.EnvironmentServices = serviceConfig.Services
			}
		}
		if checkLagoonEnv {
			route, routes, err := helpers.GetLagoonEnvRoutes(ctx, opLog, m.Client, lagoonBuild.Namespace)
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
		if err := m.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (m *Messenger) BuildLogsToLagoonLogs(
	mq bool,
	opLog logr.Logger,
	lagoonBuild *lagooncrd.LagoonBuild,
	logs []byte,
	buildCondition lagooncrd.BuildStatusType,
	targetName string,
) {
	if mq {
		condition := buildCondition
		buildStep := "queued"
		if condition == lagooncrd.BuildStatusCancelled {
			buildStep = "cancelled"
		}
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.Name,
			Meta: &schema.LagoonLogMeta{
				JobName:     lagoonBuild.Name, // @TODO: remove once lagoon is corrected in controller-handler
				BuildName:   lagoonBuild.Name,
				BuildStatus: buildCondition.ToLower(), // same as buildstatus label
				BuildStep:   buildStep,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				RemoteID:    string(lagoonBuild.UID),
				LogLink:     lagoonBuild.Spec.Project.UILink,
				Cluster:     targetName,
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
		if err := m.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, just return
			return
		}
	}
}
