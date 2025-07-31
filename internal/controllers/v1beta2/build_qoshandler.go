package v1beta2

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/metrics"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// BuildQoS is use for the quality of service configuration for lagoon builds.
type BuildQoS struct {
	MaxContainerBuilds int
	MaxBuilds          int
	DefaultPriority    int
}

func (r *LagoonBuildReconciler) qosBuildProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagooncrd.LagoonBuild) (ctrl.Result, error) {
	// check if we get a lagoonbuild that hasn't got any buildstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon build and then copy the build
	// into the created namespace
	if _, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; !ok {
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Creating new build %s from message queue", lagoonBuild.Name))
		}
		return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}
	if r.EnableDebug {
		opLog.Info("Checking which build next")
	}
	// handle the QoS build process here
	// we only care if a new build can start if one is created, cancelled, failed, or completed
	return ctrl.Result{}, r.whichBuildNext(ctx, opLog)
}

func (r *LagoonBuildReconciler) whichBuildNext(ctx context.Context, opLog logr.Logger) error {
	dockerBuilds, _ := lagooncrd.RunningDockerBuilds(r.BuildCache.Values())
	buildsToStart := r.BuildQoS.MaxContainerBuilds - len(dockerBuilds)
	// opLog.Info(fmt.Sprintf("Currently running builds. Total: %v, Docker: %v, Start: %v", len(r.BuildCache.Values()), len(dockerBuilds), buildsToStart))
	if len(r.BuildCache.Values()) >= r.BuildQoS.MaxBuilds {
		// if the maximum number of builds is hit, then drop out and try again next time
		opLog.Info(fmt.Sprintf("Currently %v running builds, max build limit reached.", len(r.BuildCache.Values())))
		// can't start any builds, so pass 0
		go r.processQueue(ctx, opLog, 0, true) //nolint:errcheck
	} else {
		opLog.Info(fmt.Sprintf("Currently %v running container build phase builds, room for %v to be started.", len(dockerBuilds), buildsToStart))
		if buildsToStart > 0 {
			// if there are any free slots to start a build, do that here
			// just start 1 build per reconcile event to ensure that the number builds doesn't exceed the max builds count
			go r.processQueue(ctx, opLog, 1, false) //nolint:errcheck
		}
	}
	return nil
}

// this is a processor for any builds that are currently `queued` status. all normal build activity will still be performed
// this just allows the controller to update any builds that are in the queue periodically
// if this ran on every single event, it would flood the queue with messages, so it is restricted using `runningProcessQueue` global
// to only run the process at any one time til it is complete
// buildsToStart is the number of builds that can be started at the time the process is called
// limitHit is used to determine if the build limit has been hit, this is used to prevent new builds from being started inside this process
func (r *LagoonBuildReconciler) processQueue(ctx context.Context, opLog logr.Logger, buildsToStart int, limitHit bool) error {
	// this should only ever be able to run one instance of at a time within a single controller
	// this is because this process is quite heavy when it goes to submit the queue messages to the api
	// the downside of this is that there can be delays with the messages it sends to the actual
	// status of the builds, but build complete/fail/cancel will always win out on the lagoon-core side
	// so this isn't that much of an issue if there are some delays in the messages
	opLog = opLog.WithName("QueueProcessor")
	if r.EnableDebug {
		opLog.Info("Processing queue")
	}
	sortedBuilds, _ := lagooncrd.SortQueuedBuilds(r.QueueCache.Values())
	metrics.BuildsPendingGauge.Set(float64(len(sortedBuilds)))
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("There are %v pending builds", len(sortedBuilds)))
	}
	// if we have any pending builds, then grab the latest one and make it running
	// if there are any other pending builds, cancel them so only the latest one runs
	for idx, pBuild := range sortedBuilds {
		if idx < buildsToStart && !limitHit {
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("Checking if build %s can be started", pBuild.Name))
			}
			runningNSBuilds, _ := lagooncrd.NamespaceRunningBuilds(pBuild.Namespace, r.BuildCache.Values())
			// if there are no running builds, check if there are any pending builds that can be started
			if len(runningNSBuilds) == 0 {
				opLog.Info("Checking CancelExtraBuilds")
				if err := lagooncrd.CancelExtraBuilds(ctx, r.Client, opLog, r.QueueCache, r.BuildCache, pBuild.Namespace, "Running"); err != nil {
					// only return if there is an error doing this operation
					// continue on otherwise to allow the queued status updater to run
					return err
				}
				// don't handle the queued process for this build, continue to next in the list
				continue
			}
		}
		// update the build to be queued, and add a log message with the build log with the current position in the queue
		// this position will update as builds are created/processed, so the position of a build could change depending on
		// higher or lower priority builds being created
		qcb := sortedBuilds[idx]
		// only update the queued build if the position or length of the queue changes
		// simply to reduce messages sent
		if qcb.Position != (idx+1) || qcb.Length != len(sortedBuilds) {
			qcb.Position = (idx + 1)
			qcb.Length = len(sortedBuilds)
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("Updating build %s to queued: %s", pBuild.Name, fmt.Sprintf("This build is currently queued in position %v/%v", qcb.Position, qcb.Length)))
			}
			if err := r.updateQueuedBuild(ctx, types.NamespacedName{Namespace: pBuild.Namespace, Name: pBuild.Name}, qcb.Position, qcb.Length, opLog); err != nil {
				return nil
			}
		}
		r.QueueCache.Add(pBuild.Name, qcb.String())
	}
	return nil
}
