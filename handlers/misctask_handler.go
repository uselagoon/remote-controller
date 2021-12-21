package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	lagoonv1alpha1 "github.com/uselagoon/remote-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// CancelDeployment handles cancelling running deployments.
func (h *Messaging) CancelDeployment(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	var jobPod corev1.Pod
	if err := h.Client.Get(context.Background(), types.NamespacedName{
		Name:      jobSpec.Misc.Name,
		Namespace: jobSpec.Environment.OpenshiftProjectName,
	}, &jobPod); err != nil {
		// since there was no build pod, check for the lagoon build resource
		var lagoonBuild lagoonv1alpha1.LagoonBuild
		if err := h.Client.Get(context.Background(), types.NamespacedName{
			Name:      jobSpec.Misc.Name,
			Namespace: jobSpec.Environment.OpenshiftProjectName,
		}, &lagoonBuild); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find build %s to cancel it. Sending response to Lagoon to update the build to cancelled.",
				jobSpec.Misc.Name,
			))
			// if there is no pod or build, update the build in Lagoon to cancelled
			h.updateLagoonBuild(opLog, *jobSpec)
			return nil
		}
		// as there is no build pod, but there is a lagoon build resource
		// update it to cancelled so that the controller doesn't try to run it
		lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.BuildStatusCancelled)
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
		h.updateLagoonBuild(opLog, *jobSpec)
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

func (h *Messaging) updateLagoonBuild(opLog logr.Logger, jobSpec lagoonv1alpha1.LagoonTaskSpec) {
	// if the build isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the build to cancelled
	// this allows us to update builds in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	msg := lagoonv1alpha1.LagoonMessage{
		Type:      "build",
		Namespace: jobSpec.Environment.OpenshiftProjectName,
		Meta: &lagoonv1alpha1.LagoonLogMeta{
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

// ResticRestore handles creating the restic restore jobs.
func (h *Messaging) ResticRestore(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
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
	restore.SetNamespace(jobSpec.Environment.OpenshiftProjectName)
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
func (h *Messaging) IngressRouteMigration(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
	return createAdvancedTask(jobSpec, h)
}

// AdvancedTask handles running the ingress migrations.
func (h *Messaging) AdvancedTask(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
	return createAdvancedTask(jobSpec, h)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func createAdvancedTask(jobSpec *lagoonv1alpha1.LagoonTaskSpec, h *Messaging) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// create the advanced task
	task := lagoonv1alpha1.LagoonTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lagoon-advanced-task-" + randString(6),
			Namespace: jobSpec.Environment.OpenshiftProjectName,
			Labels: map[string]string{
				"lagoon.sh/taskType":   string(lagoonv1alpha1.TaskTypeAdvanced),
				"lagoon.sh/taskStatus": string(lagoonv1alpha1.TaskStatusPending),
				"lagoon.sh/controller": h.ControllerNamespace,
			},
		},
		Spec: *jobSpec,
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
