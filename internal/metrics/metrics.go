package metrics

import (
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
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

	// messaging metrics
	MessagesReceived = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_mq_messages_received_total",
		Help: "The total number of messages received from the Lagoon message queues",
	}, []string{"queue"})
	MessageHandlingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_mq_message_handling_duration_seconds",
		Help:    "The time taken to handle a message received from the Lagoon message queues",
		Buckets: []float64{.005, .01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"queue"})
	MessagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_mq_messages_published_total",
		Help: "The total number of messages published back to Lagoon, result is ok or error",
	}, []string{"queue", "result"})
	MQConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_mq_connected",
		Help: "1 if the controller is connected to the message queue broker, otherwise 0",
	})
	MQDisconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_mq_disconnects_total",
		Help: "The number of times the message queue connection was observed to be lost",
	})
	MQReconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_mq_reconnects_total",
		Help: "The number of times the message queue connection was observed to recover",
	})
	MQErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "lagoon_mq_errors_total",
		Help: "The number of errors reported by the message queue client",
	})
	MQLastMessageReceived = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_mq_last_message_received_timestamp_seconds",
		Help: "The unix time the last message was received from the Lagoon message queues",
	})

	// dockerhost metrics
	DockerHostBuildsAssigned = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_dockerhost_builds_assigned_total",
		Help: "The total number of builds assigned to each dockerhost",
	}, []string{"dockerhost"})
	DockerHostBuildsInUse = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_dockerhost_builds_in_use",
		Help: "The number of builds the controller currently has assigned to each dockerhost",
	}, []string{"dockerhost"})
	DockerHostsAvailable = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "lagoon_dockerhosts_available",
		Help: "The number of dedicated dockerhosts that responded to the last availability check",
	})
	DockerHostSelectionIssues = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_dockerhost_selection_issues_total",
		Help: "Dockerhost selections that were not ideal, reason is list_error, none_available or all_busy",
	}, []string{"reason"})

	// harbor metrics
	HarborRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_harbor_api_requests_total",
		Help: "The total number of requests made to the Harbor API, code is the HTTP status or error",
	}, []string{"method", "code"})
	HarborRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "lagoon_harbor_api_request_duration_seconds",
		Help:    "The duration of requests made to the Harbor API",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	}, []string{"method"})

	// namespace deletion metrics, result is ok or error
	NamespaceDeletions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lagoon_namespace_deletions_total",
		Help: "The total number of environment namespace deletions processed, from remove messages and the pruner",
	}, []string{"result"})
	NamespaceDeletionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "lagoon_namespace_deletion_duration_seconds",
		Help:    "The time taken to process an environment namespace deletion",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600},
	})

	// operational metrics
	ControllerInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "lagoon_remote_controller_info",
		Help: "Information about the running remote-controller, the value is always 1",
	}, []string{"version", "commit", "go_version", "target"})

	stepTracker sync.Map
)

// Version and Commit can be set at build time with -ldflags "-X github.com/uselagoon/remote-controller/internal/metrics.Version=..."
var (
	Version = ""
	Commit  = ""
)

// SetControllerInfo publishes the controller info metric.
func SetControllerInfo(target string) {
	version, commit := Version, Commit
	if info, ok := debug.ReadBuildInfo(); ok {
		if version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		if commit == "" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value
				}
			}
		}
	}
	if version == "" {
		version = "unknown"
	}
	if commit == "" {
		commit = "unknown"
	}
	ControllerInfo.WithLabelValues(version, commit, runtime.Version(), target).Set(1)
}

// InstrumentedTransport records request metrics for outbound Harbor API calls.
type InstrumentedTransport struct {
	Base http.RoundTripper
}

func (t InstrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	HarborRequestDuration.WithLabelValues(req.Method).Observe(time.Since(start).Seconds())
	code := "error"
	if err == nil {
		code = strconv.Itoa(resp.StatusCode)
	}
	HarborRequests.WithLabelValues(req.Method, code).Inc()
	return resp, err
}

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
		MessagesReceived,
		MessageHandlingDuration,
		MessagesPublished,
		MQConnected,
		MQDisconnects,
		MQReconnects,
		MQErrors,
		MQLastMessageReceived,
		DockerHostBuildsAssigned,
		DockerHostBuildsInUse,
		DockerHostsAvailable,
		DockerHostSelectionIssues,
		HarborRequests,
		HarborRequestDuration,
		NamespaceDeletions,
		NamespaceDeletionDuration,
		ControllerInfo,
	)
}
