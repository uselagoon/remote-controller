package v1beta2

import (
	"context"
	"fmt"

	"github.com/uselagoon/remote-controller/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *BuildMonitorReconciler) calculateBuildMetrics(ctx context.Context) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/jobType":    "build",
			"lagoon.sh/controller": r.ControllerNamespace,
		}),
	})
	buildPods := &corev1.PodList{}
	if err := r.List(ctx, buildPods, listOption); err != nil {
		return fmt.Errorf("unable to list builds in the cluster, there may be none or something went wrong: %v", err)
	}
	runningBuilds := float64(0)
	for _, buildPod := range buildPods.Items {
		if buildPod.Status.Phase == corev1.PodRunning {
			runningBuilds += 1
		}
	}
	metrics.BuildsRunningGauge.Set(runningBuilds)
	return nil
}
