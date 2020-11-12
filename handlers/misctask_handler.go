package handlers

import (
	"context"
	"fmt"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
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
		opLog.Info(
			fmt.Sprintf(
				"Unable to get job %s: %v",
				jobSpec.Misc.Name,
				err,
			),
		)
		return err
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
	if err := h.Client.Update(context.Background(), &jobPod); err != nil {
		opLog.Info(
			fmt.Sprintf(
				"Unable to update job %s: %v",
				jobSpec.Misc.Name,
				err,
			),
		)
		return err
	}
	return nil
}

// ResticRestore handles creating the restic restore jobs.
func (h *Messaging) ResticRestore(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	restore := unstructured.Unstructured{}
	if err := restore.UnmarshalJSON(jobSpec.Misc.MiscResource); err != nil {
		opLog.Info(
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s: %v",
				jobSpec.Misc.Name,
				err,
			),
		)
		return err
	}
	restore.SetNamespace(jobSpec.Environment.OpenshiftProjectName)
	if err := h.Client.Create(context.Background(), &restore); err != nil {
		opLog.Info(
			fmt.Sprintf(
				"Unable to create backup for job %s: %v",
				jobSpec.Misc.Name,
				err,
			),
		)
		return err
	}
	return nil
}

// IngressRouteMigration handles running the ingress migrations.
func (h *Messaging) IngressRouteMigration(jobSpec *lagoonv1alpha1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// create the advanced task
	task := lagoonv1alpha1.LagoonTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lagoon-advanced-task-" + randString(6),
			Namespace: jobSpec.Environment.OpenshiftProjectName,
			Labels: map[string]string{
				"lagoon.sh/taskType":   "advanced",
				"lagoon.sh/taskStatus": "Pending",
				"lagoon.sh/controller": h.ControllerNamespace,
			},
		},
		Spec: *jobSpec,
	}
	if err := h.Client.Create(context.Background(), &task); err != nil {
		opLog.Info(
			fmt.Sprintf(
				"Unable to create task for job %s: %v",
				jobSpec.Misc.Name,
				err,
			),
		)
		return err
	}
	return nil
}
