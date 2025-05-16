package v1beta2

import (
	"context"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *LagoonTaskReconciler) standardTaskProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonTask lagooncrd.LagoonTask) (ctrl.Result, error) {
	// check if we get a lagoontask that hasn't got any taskstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon task and then copy the task
	// into the created namespace
	if _, ok := lagoonTask.Labels["lagoon.sh/taskStarted"]; !ok {
		if lagoonTask.Labels["lagoon.sh/taskStatus"] == lagooncrd.TaskStatusPending.String() &&
			lagoonTask.Labels["lagoon.sh/taskType"] == lagooncrd.TaskTypeStandard.String() {
			return ctrl.Result{}, r.createStandardTask(ctx, &lagoonTask, opLog)
		}
		if lagoonTask.Labels["lagoon.sh/taskStatus"] == lagooncrd.TaskStatusPending.String() &&
			lagoonTask.Labels["lagoon.sh/taskType"] == lagooncrd.TaskTypeAdvanced.String() {
			return ctrl.Result{}, r.createAdvancedTask(ctx, &lagoonTask, opLog)
		}
	}
	return ctrl.Result{}, nil
}
