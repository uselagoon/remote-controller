package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewServer returns a *http.Server serving prometheus metrics in a new
// goroutine.
// Caller should defer Shutdown() for cleanup.
func NewServer(log logr.Logger, addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	s := http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  16 * time.Second,
		WriteTimeout: 16 * time.Second,
	}
	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Error(fmt.Errorf("metrics server did not shut down cleanly"), err.Error())
		}
	}()
	return &s
}

var (
	// general counters for builds
	BuildsRunningGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_running_current",
		Help: "The total number of Lagoon builds running",
	})
	BuildsPendingGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_builds_pending_current",
		Help: "The total number of Lagoon builds pending or queued",
	})
	BuildsStartedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_started_total",
		Help: "The total number of Lagoon builds started",
	})
	BuildsCompletedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_completed_total",
		Help: "The total number of Lagoon builds completed",
	})
	BuildsFailedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_failed_total",
		Help: "The total number of Lagoon builds failed",
	})
	BuildsCancelledCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_builds_cancelled_total",
		Help: "The total number of Lagoon builds cancelled",
	})

	// general counters for tasks
	TasksRunningGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_tasks_running_current",
		Help: "The total number of Lagoon tasks running",
	})
	TasksStartedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_started_total",
		Help: "The total number of Lagoon tasks started",
	})
	TasksCompletedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_completed_total",
		Help: "The total number of Lagoon tasks completed",
	})
	TasksFailedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_failed_total",
		Help: "The total number of Lagoon tasks failed",
	})
	TasksCancelledCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_tasks_cancelled_total",
		Help: "The total number of Lagoon tasks cancelled",
	})

	// buildStatus will count the build transisiton steps
	// when the build step changes, the count is removed and the new step metric is created
	// this is useful to gauge how long particular steps take in a build
	BuildStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
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
	BuildRunningStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_running_status",
		Help: "The duration of running Lagoon builds",
	},
		[]string{
			"build_name",
			"build_namespace",
		},
	)
	TaskRunningStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_task_running_status",
		Help: "The duration of running Lagoon tasks",
	},
		[]string{
			"task_name",
			"task_namespace",
		},
	)
)
