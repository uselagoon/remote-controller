package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// every exported collector in this package must be registered, otherwise it is silently never exposed
func TestAllCollectorsRegistered(t *testing.T) {
	collectors := map[string]prometheus.Collector{
		"BuildsRunningGauge": BuildsRunningGauge, "BuildsPendingGauge": BuildsPendingGauge,
		"BuildsStartedCounter": BuildsStartedCounter, "BuildsCompletedCounter": BuildsCompletedCounter,
		"BuildsFailedCounter": BuildsFailedCounter, "BuildsCancelledCounter": BuildsCancelledCounter,
		"TasksRunningGauge": TasksRunningGauge, "TasksPendingGauge": TasksPendingGauge,
		"TasksStartedCounter": TasksStartedCounter, "TasksCompletedCounter": TasksCompletedCounter,
		"TasksFailedCounter": TasksFailedCounter, "TasksCancelledCounter": TasksCancelledCounter,
		"BuildDuration": BuildDuration, "BuildStepDuration": BuildStepDuration, "BuildQueueWait": BuildQueueWait,
		"TaskDuration": TaskDuration, "TaskQueueWait": TaskQueueWait,
		"BuildStartTimestamp": BuildStartTimestamp, "BuildEndTimestamp": BuildEndTimestamp,
		"BuildTotalDuration": BuildTotalDuration, "BuildStepSeconds": BuildStepSeconds,
		"MessagesReceived": MessagesReceived, "MessageHandlingDuration": MessageHandlingDuration,
		"MessagesPublished": MessagesPublished, "MQConnected": MQConnected, "MQDisconnects": MQDisconnects,
		"MQReconnects": MQReconnects, "MQErrors": MQErrors, "MQLastMessageReceived": MQLastMessageReceived,
		"DockerHostBuildsAssigned": DockerHostBuildsAssigned, "DockerHostBuildsInUse": DockerHostBuildsInUse,
		"DockerHostsAvailable": DockerHostsAvailable, "DockerHostSelectionIssues": DockerHostSelectionIssues,
		"HarborRequests": HarborRequests, "HarborRequestDuration": HarborRequestDuration,
		"NamespaceDeletions": NamespaceDeletions, "NamespaceDeletionDuration": NamespaceDeletionDuration,
		"ControllerInfo": ControllerInfo,
	}
	for name, collector := range collectors {
		if err := metrics.Registry.Register(collector); err == nil {
			t.Errorf("%s is not registered with the controller-runtime registry", name)
		} else if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}
