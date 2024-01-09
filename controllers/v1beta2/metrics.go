package v1beta2

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// general counters for builds
	buildsRunningGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_running_current",
		Help: "The total number of Lagoon builds running",
	})
	buildsPendingGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_pending_current",
		Help: "The total number of Lagoon builds pending",
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

	// general counters for tasks
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

	// buildStatus will count the build transisiton steps
	// when the build step changes, the count is removed and the new step metric is created
	// this is useful to gauge how long particular steps take in a build
	buildStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
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
	buildRunningStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_running_status",
		Help: "The duration of running Lagoon builds",
	},
		[]string{
			"build_name",
			"build_namespace",
		},
	)
	taskRunningStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_task_running_status",
		Help: "The duration of running Lagoon tasks",
	},
		[]string{
			"task_name",
			"task_namespace",
		},
	)
)
