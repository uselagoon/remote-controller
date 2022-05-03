package v1beta1

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
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
)
