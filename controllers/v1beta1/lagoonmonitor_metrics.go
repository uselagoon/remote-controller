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
	initialSetup := float64(0)
	configureVars := float64(0)
	imageBuildComplete := float64(0)
	preRolloutsCompleted := float64(0)
	serviceConfigurationComplete := float64(0)
	serviceConfiguration2Complete := float64(0)
	routeConfigurationComplete := float64(0)
	backupConfigurationComplete := float64(0)
	imagePushComplete := float64(0)
	deploymentTemplatingComplete := float64(0)
	deploymentApplyComplete := float64(0)
	cronjobCleanupComplete := float64(0)
	postRolloutsCompleted := float64(0)
	deployCompleted := float64(0)
	insightsCompleted := float64(0)
	for _, buildPod := range buildPods.Items {
		if buildPod.Status.Phase == corev1.PodRunning {
			runningBuilds = runningBuilds + 1
			if buildStep, ok := buildPod.ObjectMeta.Labels["lagoon.sh/buildStep"]; ok {
				switch buildStep {
				case "initialSetup":
					initialSetup = initialSetup + 1
				case "configureVars":
					configureVars = configureVars + 1
				case "imageBuildComplete":
					imageBuildComplete = imageBuildComplete + 1
				case "preRolloutsCompleted":
					preRolloutsCompleted = preRolloutsCompleted + 1
				case "serviceConfigurationComplete":
					serviceConfigurationComplete = serviceConfigurationComplete + 1
				case "serviceConfiguration2Complete":
					serviceConfiguration2Complete = serviceConfiguration2Complete + 1
				case "routeConfigurationComplete":
					routeConfigurationComplete = routeConfigurationComplete + 1
				case "backupConfigurationComplete":
					backupConfigurationComplete = backupConfigurationComplete + 1
				case "imagePushComplete":
					imagePushComplete = imagePushComplete + 1
				case "deploymentTemplatingComplete":
					deploymentTemplatingComplete = deploymentTemplatingComplete + 1
				case "deploymentApplyComplete":
					deploymentApplyComplete = deploymentApplyComplete + 1
				case "cronjobCleanupComplete":
					cronjobCleanupComplete = cronjobCleanupComplete + 1
				case "postRolloutsCompleted":
					postRolloutsCompleted = postRolloutsCompleted + 1
				case "deployCompleted":
					deployCompleted = deployCompleted + 1
				case "insightsCompleted":
					insightsCompleted = insightsCompleted + 1
				}
			}
		}
	}
	buildsRunningGauge.Set(runningBuilds)
	buildsInitialSetup.Set(initialSetup)
	buildsConfigureVars.Set(configureVars)
	buildsImageBuildComplete.Set(imageBuildComplete)
	buildsPreRolloutsCompleted.Set(preRolloutsCompleted)
	buildsServiceConfigurationComplete.Set(serviceConfigurationComplete)
	buildsServiceConfiguration2Complete.Set(serviceConfiguration2Complete)
	buildsRouteConfigurationComplete.Set(routeConfigurationComplete)
	buildsBackupConfigurationComplete.Set(backupConfigurationComplete)
	buildsImagePushComplete.Set(imagePushComplete)
	buildsDeploymentTemplatingComplete.Set(deploymentTemplatingComplete)
	buildsDeploymentApplyComplete.Set(deploymentApplyComplete)
	buildsCronjobCleanupComplete.Set(cronjobCleanupComplete)
	buildsPostRolloutsCompleted.Set(postRolloutsCompleted)
	buildsDeployCompleted.Set(deployCompleted)
	buildsInsightsCompleted.Set(insightsCompleted)
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
