package v1beta2

import (
	"context"
	"fmt"

	"github.com/uselagoon/remote-controller/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TaskMonitorReconciler) calculateTaskMetrics(ctx context.Context) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/jobType":    "task",
			"lagoon.sh/controller": r.ControllerNamespace,
		}),
	})
	taskPods := &corev1.PodList{}
	if err := r.List(ctx, taskPods, listOption); err != nil {
		return fmt.Errorf("unable to list tasks in the cluster, there may be none or something went wrong: %v", err)
	}
	runningTasks := float64(0)
	for _, taskPod := range taskPods.Items {
		if taskPod.Status.Phase == corev1.PodRunning {
			runningTasks += 1
		}
	}
	metrics.TasksRunningGauge.Set(runningTasks)
	return nil
}
