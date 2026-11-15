package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	buildDurationBuckets = []float64{30, 60, 120, 300, 600, 900, 1200, 1800, 2700, 3600, 5400, 7200}
	stepDurationBuckets  = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600}
	queueWaitBuckets     = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200}
	taskDurationBuckets  = []float64{5, 15, 30, 60, 120, 300, 600, 1800, 3600, 7200, 14400}

	// general gauges for builds
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
	// counters for builds, labelled by build type (branch, pullrequest, promote)
	BuildsStartedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_builds_started_total",
			Help: "The total number of Lagoon builds started",
		},
		[]string{"build_type"},
	)
	BuildsCompletedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_builds_completed_total",
			Help: "The total number of Lagoon builds completed",
		},
		[]string{"build_type"},
	)
	BuildsFailedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_builds_failed_total",
			Help: "The total number of Lagoon builds failed",
		},
		[]string{"build_type"},
	)
	BuildsCancelledCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_builds_cancelled_total",
			Help: "The total number of Lagoon builds cancelled",
		},
		[]string{"build_type"},
	)

	// general gauges for tasks
	TasksRunningGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lagoon_tasks_running_current",
			Help: "The total number of Lagoon tasks running",
		},
	)
	TasksPendingGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lagoon_tasks_pending_current",
			Help: "The total number of Lagoon tasks pending or queued",
		},
	)
	// counters for tasks, labelled by task type (standard, advanced)
	TasksStartedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_started_total",
			Help: "The total number of Lagoon tasks started",
		},
		[]string{"task_type"},
	)
	TasksCompletedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_completed_total",
			Help: "The total number of Lagoon tasks completed",
		},
		[]string{"task_type"},
	)
	TasksFailedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_failed_total",
			Help: "The total number of Lagoon tasks failed",
		},
		[]string{"task_type"},
	)
	TasksCancelledCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lagoon_tasks_cancelled_total",
			Help: "The total number of Lagoon tasks cancelled",
		},
		[]string{"task_type"},
	)

	// histograms use bounded labels only, never build or task names
	BuildDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_build_duration_seconds",
		Help:    "The duration of Lagoon builds from pod start to a terminal state",
		Buckets: buildDurationBuckets,
	}, []string{"outcome", "build_type", "dockerhost"})
	BuildStepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_build_step_duration_seconds",
		Help:    "The duration Lagoon builds spend in each build step",
		Buckets: stepDurationBuckets,
	}, []string{"build_step"})
	BuildQueueWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_build_queue_wait_seconds",
		Help:    "The time Lagoon builds wait between being created and their pod being started",
		Buckets: queueWaitBuckets,
	}, []string{"build_type"})
	TaskDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_task_duration_seconds",
		Help:    "The duration of Lagoon tasks from pod start to a terminal state",
		Buckets: taskDurationBuckets,
	}, []string{"outcome", "task_type"})
	TaskQueueWait = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_task_queue_wait_seconds",
		Help:    "The time Lagoon tasks wait between being created and their pod being started",
		Buckets: queueWaitBuckets,
	}, []string{"task_type"})

	// per-build metrics, these use build name labels so they are removed BuildMetricsRetention after the build ends
	BuildStartTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_start_timestamp_seconds",
		Help: "The unix time a build started. Retained for a short period after the build ends",
	}, buildLabels)
	BuildEndTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_end_timestamp_seconds",
		Help: "The unix time a build ended. Retained for a short period after the build ends",
	}, buildLabels)
	BuildTotalDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_total_duration_seconds",
		Help: "The total duration of a single finished build. Retained for a short period after the build ends",
	}, append([]string{"outcome"}, buildLabels...))
	BuildStepSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_build_step_seconds",
		Help: "The time a single build spent in each step. Retained for a short period after the build ends",
	}, []string{"build_name", "build_namespace", "build_step"})

	stepTracker sync.Map
)

// BuildMetricsRetention is how long per-build series are kept after a build ends.
const BuildMetricsRetention = time.Hour

var buildLabels = []string{"build_name", "build_namespace", "project", "environment", "build_type", "dockerhost"}

// BuildInfo identifies a build for the per-build metrics.
type BuildInfo struct {
	Name        string
	Namespace   string
	Project     string
	Environment string
	BuildType   string
	DockerHost  string
}

func (b BuildInfo) labels() prometheus.Labels {
	return prometheus.Labels{
		"build_name":      b.Name,
		"build_namespace": b.Namespace,
		"project":         b.Project,
		"environment":     b.Environment,
		"build_type":      b.BuildType,
		"dockerhost":      b.DockerHost,
	}
}

// RecordBuildStart sets the start timestamp of a single build.
func RecordBuildStart(info BuildInfo) {
	BuildStartTimestamp.With(info.labels()).Set(float64(time.Now().Unix()))
}

// RecordBuildEnd records the end time and total duration of a single build, then schedules the removal of
// all of the per-build series.
func RecordBuildEnd(info BuildInfo, outcome string, started time.Time) {
	labels := info.labels()
	BuildEndTimestamp.With(labels).Set(float64(time.Now().Unix()))
	if !started.IsZero() {
		withOutcome := prometheus.Labels{"outcome": outcome}
		for key, value := range labels {
			withOutcome[key] = value
		}
		BuildTotalDuration.With(withOutcome).Set(time.Since(started).Seconds())
	}
	time.AfterFunc(BuildMetricsRetention, func() {
		match := prometheus.Labels{"build_name": info.Name, "build_namespace": info.Namespace}
		BuildStartTimestamp.DeletePartialMatch(match)
		BuildEndTimestamp.DeletePartialMatch(match)
		BuildTotalDuration.DeletePartialMatch(match)
		BuildStepSeconds.DeletePartialMatch(match)
	})
}

type stepEntry struct {
	step    string
	started time.Time
}

// ObserveBuildStep records the time spent in the previous step of a build when it moves to a new step.
func ObserveBuildStep(namespace, name, newStep string) {
	key := namespace + "/" + name
	now := time.Now()
	previous, loaded := stepTracker.Swap(key, stepEntry{step: newStep, started: now})
	if !loaded {
		return
	}
	entry := previous.(stepEntry)
	if entry.step != newStep {
		seconds := now.Sub(entry.started).Seconds()
		BuildStepDuration.WithLabelValues(entry.step).Observe(seconds)
		BuildStepSeconds.WithLabelValues(name, namespace, entry.step).Set(seconds)
	} else {
		stepTracker.Store(key, entry)
	}
}

// FinishBuildStep records the time spent in the final step of a build and stops tracking it.
func FinishBuildStep(namespace, name string) {
	if previous, loaded := stepTracker.LoadAndDelete(namespace + "/" + name); loaded {
		entry := previous.(stepEntry)
		seconds := time.Since(entry.started).Seconds()
		BuildStepDuration.WithLabelValues(entry.step).Observe(seconds)
		BuildStepSeconds.WithLabelValues(name, namespace, entry.step).Set(seconds)
	}
}

// ObserveSince observes the seconds since start, ignoring zero start times.
func ObserveSince(observer prometheus.Observer, start time.Time) {
	if start.IsZero() {
		return
	}
	observer.Observe(time.Since(start).Seconds())
}

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
		TasksPendingGauge,
		TasksStartedCounter,
		TasksCompletedCounter,
		TasksFailedCounter,
		TasksCancelledCounter,
		BuildDuration,
		BuildStartTimestamp,
		BuildEndTimestamp,
		BuildTotalDuration,
		BuildStepSeconds,
		BuildStepDuration,
		BuildQueueWait,
		TaskDuration,
		TaskQueueWait,
	)
}
