package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// LagoonEvent defines a Lagoon event type
type LagoonEvent struct {
	EventType string      `json:"eventType"`
	Payload   interface{} `json:"payload"`
}

const (
	lagoonBuild  = "lagoon:build"
	lagoonTask   = "lagoon:task"
	lagoonMisc   = "lagoon:misc"
	lagoonRemval = "lagoon:removal"
)

func (h *Messaging) handleLagoonEvent(ctx context.Context, opLog logr.Logger, body []byte) error {
	// unmarshal the body of the message into a lagoonevent
	lEvent := &LagoonEvent{}
	err := json.Unmarshal(body, lEvent)
	if err != nil {
		return err
	}

	// turn the payload back into bytes to be processed by the handlers
	payloadBytes, err := json.Marshal(lEvent.Payload)
	if err != nil {
		return err
	}
	switch lEvent.EventType {
	case lagoonBuild:
		h.handleBuildEvent(ctx, opLog, payloadBytes)
	case lagoonTask:
		h.handleTaskEvent(ctx, opLog, payloadBytes)
	case lagoonMisc:
		h.handleMiscEvent(ctx, opLog, payloadBytes)
	case lagoonRemval:
		h.handleRemovalEvent(ctx, opLog, payloadBytes)
	}
	return nil
}

func (h *Messaging) handleBuildEvent(ctx context.Context, opLog logr.Logger, payload []byte) error {
	// unmarshal the body into a lagoonbuild
	newBuild := &lagoonv1beta1.LagoonBuild{}
	json.Unmarshal(payload, newBuild)
	// new builds that come in should initially get created in the controllers own
	// namespace before being handled and re-created in the correct namespace
	// so set the controller namespace to the build namespace here
	newBuild.ObjectMeta.Namespace = h.ControllerNamespace
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/controller": h.ControllerNamespace,
		},
	)
	opLog.Info(
		fmt.Sprintf(
			"Received builddeploy task for project %s, environment %s",
			newBuild.Spec.Project.Name,
			newBuild.Spec.Project.Environment,
		),
	)
	// create it now
	if err := h.Client.Create(ctx, newBuild); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Failed to create builddeploy task for project %s, environment %s",
				newBuild.Spec.Project.Name,
				newBuild.Spec.Project.Environment,
			),
		)
		//@TODO: send msg back to lagoon and update task to failed?
		return err
	}
	return nil
}

func (h *Messaging) handleTaskEvent(ctx context.Context, opLog logr.Logger, payload []byte) error {
	// unmarshall the message into a remove task to be processed
	jobSpec := &lagoonv1beta1.LagoonTaskSpec{}
	json.Unmarshal(payload, jobSpec)
	namespace := helpers.GenerateNamespaceName(
		jobSpec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		jobSpec.Environment.Name,
		jobSpec.Project.Name,
		h.NamespacePrefix,
		h.ControllerNamespace,
		h.RandomNamespacePrefix,
	)
	opLog.Info(
		fmt.Sprintf(
			"Received task for project %s, environment %s - %s",
			jobSpec.Project.Name,
			jobSpec.Environment.Name,
			namespace,
		),
	)
	job := &lagoonv1beta1.LagoonTask{}
	job.Spec = *jobSpec
	// set the namespace to the `openshiftProjectName` from the environment
	job.ObjectMeta.Namespace = namespace
	job.SetLabels(
		map[string]string{
			"lagoon.sh/taskType":   string(lagoonv1beta1.TaskTypeStandard),
			"lagoon.sh/taskStatus": string(lagoonv1beta1.TaskStatusPending),
			"lagoon.sh/controller": h.ControllerNamespace,
		},
	)
	job.ObjectMeta.Name = fmt.Sprintf("lagoon-task-%s-%s", job.Spec.Task.ID, helpers.HashString(job.Spec.Task.ID)[0:6])
	if job.Spec.Task.TaskName != "" {
		job.ObjectMeta.Name = job.Spec.Task.TaskName
	}
	if err := h.Client.Create(ctx, job); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create job task for project %s, environment %s",
				job.Spec.Project.Name,
				job.Spec.Environment.Name,
			),
		)
		return err
	}
	return nil
}

func (h *Messaging) handleMiscEvent(ctx context.Context, opLog logr.Logger, payload []byte) error {
	// unmarshall the message into a remove task to be processed
	jobSpec := &lagoonv1beta1.LagoonTaskSpec{}
	json.Unmarshal(payload, jobSpec)
	// check which key has been received
	namespace := helpers.GenerateNamespaceName(
		jobSpec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		jobSpec.Environment.Name,
		jobSpec.Project.Name,
		h.NamespacePrefix,
		h.ControllerNamespace,
		h.RandomNamespacePrefix,
	)
	switch jobSpec.Key {
	case "kubernetes:build:cancel", "deploytarget:build:cancel":
		opLog.Info(
			fmt.Sprintf(
				"Received build cancellation for project %s, environment %s - %s",
				jobSpec.Project.Name,
				jobSpec.Environment.Name,
				namespace,
			),
		)
		err := h.CancelBuild(namespace, jobSpec)
		if err != nil {
			return err
		}
	case "kubernetes:task:cancel", "deploytarget:task:cancel":
		opLog.Info(
			fmt.Sprintf(
				"Received task cancellation for project %s, environment %s - %s",
				jobSpec.Project.Name,
				jobSpec.Environment.Name,
				namespace,
			),
		)
		err := h.CancelTask(namespace, jobSpec)
		if err != nil {
			return err
		}
	case "kubernetes:restic:backup:restore", "deploytarget:backup:restore":
		opLog.Info(
			fmt.Sprintf(
				"Received backup restoration for project %s, environment %s",
				jobSpec.Project.Name,
				jobSpec.Environment.Name,
			),
		)
		err := h.ResticRestore(namespace, jobSpec)
		if err != nil {
			return err
		}
	case "kubernetes:route:migrate", "deploytarget:ingress:migrate":
		opLog.Info(
			fmt.Sprintf(
				"Received ingress migration for project %s",
				jobSpec.Project.Name,
			),
		)
		err := h.IngressRouteMigration(namespace, jobSpec)
		if err != nil {
			return err
		}
	case "kubernetes:task:advanced", "deploytarget:task:advanced":
		opLog.Info(
			fmt.Sprintf(
				"Received advanced task for project %s",
				jobSpec.Project.Name,
			),
		)
		err := h.AdvancedTask(namespace, jobSpec)
		if err != nil {
			return err
		}
	default:
		// if we get something that we don't know about, spit out the entire message
		opLog.Info(
			fmt.Sprintf(
				"Received unknown message: %s",
				string(payload),
			),
		)
	}
	return nil
}

func (h *Messaging) handleRemovalEvent(ctx context.Context, opLog logr.Logger, payload []byte) error {
	// unmarshall the message into a remove task to be processed
	removeTask := &removeTask{}
	json.Unmarshal(payload, removeTask)
	// webhooks2tasks sends the `branch` field, but deletion from the API (UI/CLI) does not
	// the tasks system crafts a field `branchName` which is passed through
	// since webhooks2tasks uses the same underlying mechanism, we can still consume branchName even if branch is populated
	if removeTask.Type == "pullrequest" {
		removeTask.Branch = removeTask.BranchName
	}
	// generate the namespace name from the branch and project and any prefixes that the controller may add
	ns := helpers.GenerateNamespaceName(
		removeTask.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		removeTask.Branch,
		removeTask.ProjectName,
		h.NamespacePrefix,
		h.ControllerNamespace,
		h.RandomNamespacePrefix,
	)
	branch := removeTask.Branch
	project := removeTask.ProjectName
	opLog.WithName("RemoveTask").Info(
		fmt.Sprintf(
			"Received remove task for project %s, branch %s - %s",
			project,
			branch,
			ns,
		),
	)
	namespace := &corev1.Namespace{}
	err := h.Client.Get(ctx, types.NamespacedName{
		Name: ns,
	}, namespace)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			opLog.WithName("RemoveTask").Info(
				fmt.Sprintf(
					"Namespace %s for project %s, branch %s does not exist, marking deleted",
					ns,
					project,
					branch,
				),
			)
			msg := lagoonv1beta1.LagoonMessage{
				Type:      "remove",
				Namespace: ns,
				Meta: &lagoonv1beta1.LagoonLogMeta{
					Project:     project,
					Environment: branch,
				},
			}
			msgBytes, _ := json.Marshal(msg)
			h.Publish("lagoon-tasks:controller", msgBytes)
		} else {
			opLog.WithName("RemoveTask").Info(
				fmt.Sprintf(
					"Unable to get namespace %s for project %s, branch %s: %v",
					ns,
					project,
					branch,
					err,
				),
			)
		}
		//@TODO: send msg back to lagoon and update task to failed?
		return nil

	}
	// check that the namespace selected for deletion is owned by this controller
	if value, ok := namespace.ObjectMeta.Labels["lagoon.sh/controller"]; ok {
		if value == h.ControllerNamespace {
			/*
				get any deployments/statefulsets/daemonsets
				then delete them
			*/
			if h.CleanupHarborRepositoryOnDelete {
				lagoonHarbor, err := harbor.NewHarbor(h.Harbor)
				if err != nil {
					return err
				}
				curVer, err := lagoonHarbor.GetHarborVersion(ctx)
				if err != nil {
					return err
				}
				if lagoonHarbor.UseV2Functions(curVer) {
					lagoonHarbor.DeleteRepository(ctx, project, branch)
				}
			}
			if del := h.DeleteLagoonTasks(ctx, opLog.WithName("DeleteLagoonTasks"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeleteLagoonBuilds(ctx, opLog.WithName("DeleteLagoonBuilds"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeleteDeployments(ctx, opLog.WithName("DeleteDeployments"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeleteStatefulSets(ctx, opLog.WithName("DeleteStatefulSets"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeleteDaemonSets(ctx, opLog.WithName("DeleteDaemonSets"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeleteJobs(ctx, opLog.WithName("DeleteJobs"), ns, project, branch); del == false {
				return nil
			}
			if del := h.DeletePVCs(ctx, opLog.WithName("DeletePVCs"), ns, project, branch); del == false {
				return nil
			}
			/*
				then delete the namespace
			*/
			if del := h.DeleteNamespace(ctx, opLog.WithName("DeleteNamespace"), namespace, project, branch); del == false {
				return nil
			}
			opLog.WithName("DeleteNamespace").Info(
				fmt.Sprintf(
					"Deleted namespace %s for project %s, branch %s",
					ns,
					project,
					branch,
				),
			)
			msg := lagoonv1beta1.LagoonMessage{
				Type:      "remove",
				Namespace: ns,
				Meta: &lagoonv1beta1.LagoonLogMeta{
					Project:     project,
					Environment: branch,
				},
			}
			msgBytes, _ := json.Marshal(msg)
			h.Publish("lagoon-tasks:controller", msgBytes)
			return nil
		}
		// controller label didn't match, log the message
		opLog.WithName("RemoveTask").Info(
			fmt.Sprintf(
				"Selected namespace %s for project %s, branch %s: %v",
				ns,
				project,
				branch,
				fmt.Errorf("The controller label value %s does not match %s for this namespace", value, h.ControllerNamespace),
			),
		)
		return nil
	}
	// controller label didn't match, log the message
	opLog.WithName("RemoveTask").Info(
		fmt.Sprintf(
			"Selected namespace %s for project %s, branch %s: %v",
			ns,
			project,
			branch,
			fmt.Errorf("The controller ownership label does not exist on this namespace, nothing will be done for this removal request"),
		),
	)
	return nil
}
