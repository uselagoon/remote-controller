package v1beta1

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	buildsRunningGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_running_current",
		Help: "The total number of Lagoon builds running",
	})
	buildsStartedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_started_total",
		Help: "The total number of Lagoon builds started",
	})
	buildsCompletedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_completed_total",
		Help: "The total number of Lagoon builds completed",
	})
	buildsFailedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_failed_total",
		Help: "The total number of Lagoon builds failed",
	})
	buildsCancelledCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_cancelled_total",
		Help: "The total number of Lagoon builds cancelled",
	})

	tasksRunningGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_tasks_running_current",
		Help: "The total number of Lagoon tasks running",
	})
	tasksStartedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_started_total",
		Help: "The total number of Lagoon tasks started",
	})
	tasksCompletedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_completed_total",
		Help: "The total number of Lagoon tasks completed",
	})
	tasksFailedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_failed_total",
		Help: "The total number of Lagoon tasks failed",
	})
	tasksCancelledCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_cancelled_total",
		Help: "The total number of Lagoon tasks cancelled",
	})

	buildsInitialSetup = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_initial_setup_step",
		Help: "The total number of Lagoon builds at the initial setup step",
	})
	buildsConfigureVars = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_configure_vars_step",
		Help: "The total number of Lagoon builds at the configure vars step",
	})
	buildsImageBuildComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_image_build_complete_step",
		Help: "The total number of Lagoon builds at the image build complete step",
	})
	buildsPreRolloutsCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_pre_rollouts_complete_step",
		Help: "The total number of Lagoon builds at the pre rollouts complete step",
	})
	buildsServiceConfigurationComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_service_configuration_complete_step",
		Help: "The total number of Lagoon builds at the service configuration step",
	})
	buildsServiceConfiguration2Complete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_service_configuration2_complete_step",
		Help: "The total number of Lagoon builds at the second service configuration step",
	})
	buildsRouteConfigurationComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_route_configuration_complete_step",
		Help: "The total number of Lagoon builds at the route configuration complete step",
	})
	buildsBackupConfigurationComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_backup_configuration_complete_step",
		Help: "The total number of Lagoon builds at the backup configuration complete step",
	})
	buildsImagePushComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_image_push_complete_step",
		Help: "The total number of Lagoon builds at the image push complete step",
	})
	buildsDeploymentTemplatingComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_deployment_template_complete_step",
		Help: "The total number of Lagoon builds at the deployment templating complete step",
	})
	buildsDeploymentApplyComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_deployment_apply_complete_step",
		Help: "The total number of Lagoon builds at the deployment template apply complete step",
	})
	buildsCronjobCleanupComplete = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_cronjob_cleanup_complete_step",
		Help: "The total number of Lagoon builds at the cronjob cleanup complete step",
	})
	buildsPostRolloutsCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_post_rollouts_complete_step",
		Help: "The total number of Lagoon builds at the post rollouts complete step",
	})
	buildsDeployCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_deploy_complete_step",
		Help: "The total number of Lagoon builds at the deploy complete step",
	})
	buildsInsightsCompleted = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_insights_complete_step",
		Help: "The total number of Lagoon builds at the insights complete step",
	})
)
