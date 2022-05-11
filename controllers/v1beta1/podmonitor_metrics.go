package v1beta1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *LagoonMonitorReconciler) calculateBuildMetrics(ctx context.Context) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/jobType":    "build",
			"lagoon.sh/controller": r.ControllerNamespace,
		}),
	})
	buildPods := &corev1.PodList{}
	if err := r.List(ctx, buildPods, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the cluster, there may be none or something went wrong: %v", err)
	}
	runningBuilds := float64(0)
	for _, buildPod := range buildPods.Items {
		if buildPod.Status.Phase == corev1.PodRunning {
			runningBuilds = runningBuilds + 1
		}
	}
	buildsRunningGauge.Set(runningBuilds)
	return nil
}

func (r *LagoonMonitorReconciler) calculateTaskMetrics(ctx context.Context) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/jobType":    "task",
			"lagoon.sh/controller": r.ControllerNamespace,
		}),
	})
	taskPods := &corev1.PodList{}
	if err := r.List(ctx, taskPods, listOption); err != nil {
		return fmt.Errorf("Unable to list tasks in the cluster, there may be none or something went wrong: %v", err)
	}
	runningTasks := float64(0)
	for _, taskPod := range taskPods.Items {
		if taskPod.Status.Phase == corev1.PodRunning {
			runningTasks = runningTasks + 1
		}
	}
	tasksRunningGauge.Set(runningTasks)
	return nil
}
