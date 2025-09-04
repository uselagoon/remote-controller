package messenger

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	failedMsg = "an error occured while trying to create this build - contact your Lagoon support team for help"
)

func (m *Messenger) handleBuildMessage(ctx context.Context, opLog logr.Logger, newBuild *lagoonv1beta2.LagoonBuild) {
	// get the namespace the way a new build would generate it it immediately when the build message is received
	// this reduces the time needed to create the build firstly in the "controller namespace"
	// then re-create it in the correct namespace in a later stage
	ns := &corev1.Namespace{}
	clean, err := lagoonv1beta2.GetOrCreateNamespace(ctx, m.Client, ns, *newBuild, opLog, m.ControllerNamespace, m.NamespacePrefix, m.RandomNamespacePrefix)
	if err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Failed to get or create namespace for project %s, environment %s",
				newBuild.Spec.Project.Name,
				newBuild.Spec.Project.Environment,
			),
		)
		if clean {
			msg, _ := lagoonv1beta2.CleanUpUndeployableBuild(ctx, m.Client, true, *newBuild, failedMsg, opLog, true, m.LagoonTargetName)
			m.BuildStatusLogsToLagoonLogs(ctx, true, opLog, newBuild, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName, "cancelled")
			m.UpdateDeploymentAndEnvironmentTask(ctx, true, opLog, newBuild, true, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName, "cancelled")
			m.BuildLogsToLagoonLogs(true, opLog, newBuild, msg, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName)
		}
		return
	}
	newBuild.Namespace = ns.Name
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/controller":  m.ControllerNamespace,
			"crd.lagoon.sh/version": "v1beta2",
			"lagoon.sh/buildStatus": lagoonv1beta2.BuildStatusPending.String(),
		},
	)
	// add the finalizer to the new build
	newBuild.Finalizers = append(newBuild.Finalizers, lagoonv1beta2.BuildFinalizer)
	// all new builds start as "queued" but will transition to pending or running unless they are actually queued :D
	newBuild.Status.Phase = "Queued"
	// also create the build with a queued buildstep
	newBuild.Status.Conditions = []metav1.Condition{
		{
			Type:               "BuildStep",
			Reason:             "Queued",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now().UTC()),
		},
	}
	opLog.Info(
		fmt.Sprintf(
			"Received builddeploy task for project %s, environment %s",
			newBuild.Spec.Project.Name,
			newBuild.Spec.Project.Environment,
		),
	)
	// if everything is all good controller will handle the new build resource that gets created as it will have
	// the `lagoon.sh/buildStatus = Pending` now
	// this ensures that this build being processed will be the next one that runs in the namespace
	// create it now
	if err := m.Client.Create(ctx, newBuild); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Failed to create builddeploy task for project %s, environment %s",
				newBuild.Spec.Project.Name,
				newBuild.Spec.Project.Environment,
			),
		)
		// send a message back to the API to indicate that this build failed to be created
		msg, _ := lagoonv1beta2.CleanUpUndeployableBuild(ctx, m.Client, true, *newBuild, failedMsg, opLog, true, m.LagoonTargetName)
		m.BuildStatusLogsToLagoonLogs(ctx, true, opLog, newBuild, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName, "cancelled")
		m.UpdateDeploymentAndEnvironmentTask(ctx, true, opLog, newBuild, true, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName, "cancelled")
		m.BuildLogsToLagoonLogs(true, opLog, newBuild, msg, lagoonv1beta2.BuildStatusCancelled, m.LagoonTargetName)
		return
	}
	msg := []byte(`
========================================
Build pending
========================================
Received. Awaiting processing`)
	// send a message back to the API to indicate that this build was received and is waiting further processing
	m.BuildStatusLogsToLagoonLogs(ctx, true, opLog, newBuild, lagoonv1beta2.BuildStatusQueued, m.LagoonTargetName, "waiting for update")
	m.UpdateDeploymentAndEnvironmentTask(ctx, true, opLog, newBuild, true, lagoonv1beta2.BuildStatusQueued, m.LagoonTargetName, "waiting for update")
	m.BuildLogsToLagoonLogs(true, opLog, newBuild, msg, lagoonv1beta2.BuildStatusQueued, m.LagoonTargetName)
	// add to queue cache
	position := len(m.BuildQueueCache.Keys()) + 1
	priority := m.DefaultPriority
	if newBuild.Spec.Build.Priority != nil {
		priority = *newBuild.Spec.Build.Priority
	}
	bc := lagoonv1beta2.NewCachedBuildQueueItem(*newBuild, priority, position, position)
	m.BuildQueueCache.Add(newBuild.Name, bc.String())
}
