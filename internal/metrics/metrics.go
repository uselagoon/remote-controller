package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// general counters for builds
	BuildsRunningGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lagoon_builds_running_current",
			Help: "The total number of Lagoon builds running",
		},
	)
	BuildsPendingGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lagoon_builds_pending_current",
			Help: "The total number of Lagoon builds pending or queued",
		},
	)
	BuildsStartedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_builds_started_total",
			Help: "The total number of Lagoon builds started",
		},
	)
	BuildsCompletedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_builds_completed_total",
			Help: "The total number of Lagoon builds completed",
		},
	)
	BuildsFailedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_builds_failed_total",
			Help: "The total number of Lagoon builds failed",
		},
	)
	BuildsCancelledCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_builds_cancelled_total",
			Help: "The total number of Lagoon builds cancelled",
		},
	)

	// general counters for tasks
	TasksRunningGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lagoon_tasks_running_current",
			Help: "The total number of Lagoon tasks running",
		},
	)
	TasksStartedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_started_total",
			Help: "The total number of Lagoon tasks started",
		},
	)
	TasksCompletedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_completed_total",
			Help: "The total number of Lagoon tasks completed",
		},
	)
	TasksFailedCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_failed_total",
			Help: "The total number of Lagoon tasks failed",
		},
	)
	TasksCancelledCounter = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_cancelled_total",
			Help: "The total number of Lagoon tasks cancelled",
		},
	)

	// buildStatus will count the build transisiton steps
	// when the build step changes, the count is removed and the new step metric is created
	// this is useful to gauge how long particular steps take in a build
	BuildStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_status",
		Help: "The status of running Lagoon builds",
	},
		[]string{
			"build_name",
			"build_namespace",
			"build_step",
		},
	)

	// RunningStatus will count when a build or task is running
	// when the build or task is complete, the count is removed
	// this is useful to gauge how long a build or task runs for
	BuildRunningStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_running_status",
		Help: "The duration of running Lagoon builds",
	},
		[]string{
			"build_name",
			"build_namespace",
			"build_dockerhost",
		},
	)
	TaskRunningStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_task_running_status",
		Help: "The duration of running Lagoon tasks",
	},
		[]string{
			"task_name",
			"task_namespace",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(
		BuildsRunningGauge,
		BuildsPendingGauge,
		BuildsStartedCounter,
		BuildsCompletedCounter,
		BuildsFailedCounter,
		BuildsCancelledCounter,
		TasksRunningGauge,
		TasksStartedCounter,
		TasksCompletedCounter,
		TasksFailedCounter,
		TasksCancelledCounter,
		BuildStatus,
		BuildRunningStatus,
		TaskRunningStatus,
	)
}
