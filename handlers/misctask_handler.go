package handlers

import (
	"context"
	"fmt"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
