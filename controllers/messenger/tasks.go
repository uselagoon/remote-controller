package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ActiveStandbyPayload struct {
	SourceNamespace      string `json:"sourceNamespace"`
	DestinationNamespace string `json:"destinationNamespace"`
}

// CancelBuild handles cancelling builds or handling if a build no longer exists.
func (h *Messaging) CancelBuild(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	var jobPod corev1.Pod
	if err := h.Client.Get(context.Background(), types.NamespacedName{
		Name:      jobSpec.Misc.Name,
		Namespace: namespace,
	}, &jobPod); err != nil {
		opLog.Info(fmt.Sprintf(
			"Unable to find build pod %s to cancel it. Checking to see if LagoonBuild exists.",
			jobSpec.Misc.Name,
		))
		// since there was no build pod, check for the lagoon build resource
		var lagoonBuild lagoonv1beta1.LagoonBuild
		if err := h.Client.Get(context.Background(), types.NamespacedName{
			Name:      jobSpec.Misc.Name,
			Namespace: namespace,
		}, &lagoonBuild); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find build %s to cancel it. Sending response to Lagoon to update the build to cancelled.",
				jobSpec.Misc.Name,
			))
			// if there is no pod or build, update the build in Lagoon to cancelled
			h.updateLagoonBuild(opLog, namespace, *jobSpec)
			return nil
		}
		// as there is no build pod, but there is a lagoon build resource
		// update it to cancelled so that the controller doesn't try to run it
		lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"] = string(lagoonv1beta1.BuildStatusCancelled)
		lagoonBuild.ObjectMeta.Labels["lagoon.sh/cancelBuildNoPod"] = "true"
		if err := h.Client.Update(context.Background(), &lagoonBuild); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update build %s to cancel it.",
					jobSpec.Misc.Name,
				),
			)
			return err
		}
		// and then send the response back to lagoon to say it was cancelled.
		h.updateLagoonBuild(opLog, namespace, *jobSpec)
		return nil
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
	if err := h.Client.Update(context.Background(), &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update build %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

// CancelTask handles cancelling tasks or handling if a tasks no longer exists.
func (h *Messaging) CancelTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	var jobPod corev1.Pod
	//@TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", jobSpec.Task.ID, helpers.HashString(jobSpec.Task.ID)[0:6])
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	if err := h.Client.Get(context.Background(), types.NamespacedName{
		Name:      taskName,
		Namespace: namespace,
	}, &jobPod); err != nil {
		// since there was no task pod, check for the lagoon task resource
		var lagoonTask lagoonv1beta1.LagoonTask
		if err := h.Client.Get(context.Background(), types.NamespacedName{
			Name:      taskName,
			Namespace: namespace,
		}, &lagoonTask); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find task %s to cancel it. Sending response to Lagoon to update the task to cancelled.",
				taskName,
			))
			// if there is no pod or task, update the task in Lagoon to cancelled
			h.updateLagoonTask(opLog, namespace, *jobSpec)
			return nil
		}
		// as there is no task pod, but there is a lagoon task resource
		// update it to cancelled so that the controller doesn't try to run it
		lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] = string(lagoonv1beta1.TaskStatusCancelled)
		if err := h.Client.Update(context.Background(), &lagoonTask); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update task %s to cancel it.",
					taskName,
				),
			)
			return err
		}
		// and then send the response back to lagoon to say it was cancelled.
		h.updateLagoonTask(opLog, namespace, *jobSpec)
		return nil
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelTask"] = "true"
	if err := h.Client.Update(context.Background(), &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update task %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

func (h *Messaging) updateLagoonBuild(opLog logr.Logger, namespace string, jobSpec lagoonv1beta1.LagoonTaskSpec) {
	// if the build isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the build to cancelled
	// this allows us to update builds in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	msg := lagoonv1beta1.LagoonMessage{
		Type:      "build",
		Namespace: namespace,
		Meta: &lagoonv1beta1.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			BuildPhase:  "cancelled",
			BuildName:   jobSpec.Misc.Name,
		},
	}
	// if the build isn't found at all, then set the start/end time to be now
	// to stop the duration counter in the ui
	msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		opLog.Error(err, "Unable to encode message as JSON")
	}
	// publish the cancellation result back to lagoon
	if err := h.Publish("lagoon-tasks:controller", msgBytes); err != nil {
		opLog.Error(err, "Unable to publish message.")
	}
}

func (h *Messaging) updateLagoonTask(opLog logr.Logger, namespace string, jobSpec lagoonv1beta1.LagoonTaskSpec) {
	//@TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", jobSpec.Task.ID, helpers.HashString(jobSpec.Task.ID)[0:6])
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	// if the task isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the task to cancelled
	// this allows us to update tasks in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	msg := lagoonv1beta1.LagoonMessage{
		Type:      "task",
		Namespace: namespace,
		Meta: &lagoonv1beta1.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			JobName:     taskName,
			JobStatus:   "cancelled",
		},
	}
	// if the task isn't found at all, then set the start/end time to be now
	// to stop the duration counter in the ui
	msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		opLog.Error(err, "Unable to encode message as JSON")
	}
	// publish the cancellation result back to lagoon
	if err := h.Publish("lagoon-tasks:controller", msgBytes); err != nil {
		opLog.Error(err, "Unable to publish message.")
	}
}

// ResticRestore handles creating the restic restore jobs.
func (h *Messaging) ResticRestore(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	restore := unstructured.Unstructured{}
	if err := restore.UnmarshalJSON(jobSpec.Misc.MiscResource); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				jobSpec.Misc.Name,
			),
		)
		// just log the error then return
		return nil
	}
	restore.SetNamespace(namespace)
	if err := h.Client.Create(context.Background(), &restore); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create backup for job %s.",
				jobSpec.Misc.Name,
			),
		)
		// just log the error then return
		return nil
	}
	return nil
}

// IngressRouteMigration handles running the ingress migrations.
func (h *Messaging) IngressRouteMigration(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	return h.createAdvancedTask(namespace, jobSpec, nil)
}

// ActiveStandbySwitch handles running the active standby switch setup advanced task.
func (h *Messaging) ActiveStandbySwitch(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	asPayload := &ActiveStandbyPayload{}
	err := json.Unmarshal([]byte(jobSpec.AdvancedTask.JSONPayload), asPayload)
	if err != nil {
		return fmt.Errorf("Unable to unmarshal json payload: %v", err)
	}
	return h.createAdvancedTask(namespace, jobSpec, map[string]string{
		"lagoon.sh/activeStandby":                     "true",
		"lagoon.sh/activeStandbyDestinationNamespace": asPayload.DestinationNamespace,
		"lagoon.sh/activeStandbySourceNamespace":      asPayload.SourceNamespace,
	})
}

// AdvancedTask handles running the ingress migrations.
func (h *Messaging) AdvancedTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	if h.AdvancedTaskSSHKeyInjection {
		jobSpec.AdvancedTask.SSHKey = true
	}
	if h.AdvancedTaskDeployTokenInjection {
		jobSpec.AdvancedTask.DeployerToken = true
	}
	return h.createAdvancedTask(namespace, jobSpec, nil)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func (h *Messaging) createAdvancedTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec, additionalLabels map[string]string) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// create the advanced task
	task := lagoonv1beta1.LagoonTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lagoon-advanced-task-" + helpers.RandString(6),
			Namespace: namespace,
			Labels: map[string]string{
				"lagoon.sh/taskType":   string(lagoonv1beta1.TaskTypeAdvanced),
				"lagoon.sh/taskStatus": string(lagoonv1beta1.TaskStatusPending),
				"lagoon.sh/controller": h.ControllerNamespace,
			},
		},
		Spec: *jobSpec,
	}
	// add additional labels if required
	for key, value := range additionalLabels {
		task.ObjectMeta.Labels[key] = value
	}
	if err := h.Client.Create(context.Background(), &task); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create task for job %s.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}
