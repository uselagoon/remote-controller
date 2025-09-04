package v1beta2

// this file is used by the `lagoonbuild` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
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
				opLog.Info(fmt.Sprintf("unable to update the lagoon with LagoonBuild result: %v", err))
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
				lagoonBuild.Name,
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
		r.Messaging.BuildStatusLogsToLagoonLogs(ctx, r.EnableMQ, opLog, &lagoonBuild, lagooncrd.BuildStatusCancelled, r.LagoonTargetName, "cancelled")
		r.Messaging.UpdateDeploymentAndEnvironmentTask(ctx, r.EnableMQ, opLog, &lagoonBuild, true, lagooncrd.BuildStatusCancelled, r.LagoonTargetName, "cancelled")
		r.Messaging.BuildLogsToLagoonLogs(r.EnableMQ, opLog, &lagoonBuild, allContainerLogs, lagooncrd.BuildStatusCancelled, r.LagoonTargetName)
	}
	return nil
}
