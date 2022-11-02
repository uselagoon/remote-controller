package pruner

import (
	"github.com/uselagoon/remote-controller/internal/utilities/deletions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type pruner interface {
	LagoonBuildCleanup()
	BuildPodCleanup()
	TaskPodCleanup()
}

// Pruner is used for cleaning up old pods or resources.
type Pruner struct {
	Client                client.Client
	BuildsToKeep          int
	TasksToKeep           int
	BuildPodsToKeep       int
	TaskPodsToKeep        int
	ControllerNamespace   string
	NamespacePrefix       string
	RandomNamespacePrefix bool
	DeletionHandler       *deletions.Deletions
	TimeoutForBuildPods   int
	TimeoutForTaskPods    int
	EnableDebug           bool
}

// New returns a pruner with controller-runtime client.
func New(
	client client.Client,
	buildsToKeep int,
	buildPodsToKeep int,
	tasksToKeep int,
	taskPodsToKeep int,
	controllerNamespace string,
	deletionHandler *deletions.Deletions,
	timeoutForBuildPods int,
	timeoutForTaskPods int,
	enableDebug bool) *Pruner {
	return &Pruner{
		Client:              client,
		BuildsToKeep:        buildsToKeep,
		TasksToKeep:         tasksToKeep,
		BuildPodsToKeep:     buildPodsToKeep,
		TaskPodsToKeep:      taskPodsToKeep,
		ControllerNamespace: controllerNamespace,
		DeletionHandler:     deletionHandler,
		TimeoutForBuildPods: timeoutForBuildPods,
		TimeoutForTaskPods:  timeoutForTaskPods,
		EnableDebug:         enableDebug,
	}
}
