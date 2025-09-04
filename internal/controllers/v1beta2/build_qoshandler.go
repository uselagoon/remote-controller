package v1beta2

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/metrics"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// BuildQoS is use for the quality of service configuration for lagoon builds.
type BuildQoS struct {
	MaxContainerBuilds int
	TotalBuilds        int
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
		// return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}
	if r.EnableDebug {
		opLog.Info("Checking which build next")
	}
	// handle the QoS build process here
	// we only care if a new build can start if one is created, cancelled, failed, or completed
	return ctrl.Result{}, r.whichBuildNext(ctx, opLog)
}

func (r *LagoonBuildReconciler) whichBuildNext(ctx context.Context, opLog logr.Logger) error {
	dockerBuilds, err := lagooncrd.RunningDockerBuilds(r.BuildCache.Values())
	if err != nil {
		return fmt.Errorf("unable to determine running docker builds: %v", err)
	}
	buildsToStart := r.BuildQoS.MaxContainerBuilds - len(dockerBuilds)
	runningBuilds := len(r.BuildCache.Values())
	queuedBuilds := len(r.QueueCache.Values())
	metrics.BuildsRunningGauge.Set(float64(r.BuildCache.Len()))
	metrics.BuildsPendingGauge.Set(float64(r.QueueCache.Len()))
	if runningBuilds >= r.BuildQoS.TotalBuilds {
		// if the maximum number of builds is hit, then drop out and try again next time
		opLog.Info(fmt.Sprintf("Currently %v running builds, max build limit reached. %v queued", runningBuilds, queuedBuilds))
		// can't start any builds, so pass 0
		err := r.processQueue(ctx, opLog, 0, true)
		if err != nil {
			opLog.Error(err, "error running queue processor")
		}
	} else {
		opLog.Info(fmt.Sprintf("Currently %v running container build phase builds (%v total builds), room for %v to be started. %v queued", len(dockerBuilds), runningBuilds, buildsToStart, queuedBuilds))
		if buildsToStart > 0 {
			// if there are any free slots to start a build, do that here
			err := r.processQueue(ctx, opLog, buildsToStart, false)
			if err != nil {
				opLog.Error(err, "error running queue processor")
			}
		}
	}
	// update all the queued items as required
	go r.updateQueue(ctx, opLog) //nolint:errcheck
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
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("There are %v pending builds", len(sortedBuilds)))
	}
	// if we have any pending builds, then grab the latest one and make it running
	// if there are any other pending builds, cancel them so only the latest one runs
	if !limitHit {
		startedBuilds := 0
		for _, pBuild := range sortedBuilds {
			if startedBuilds < buildsToStart {
				if r.EnableDebug {
					opLog.Info(fmt.Sprintf("Checking if build %s can be started", pBuild.Name))
				}
				runningNSBuilds, err := lagooncrd.NamespaceRunningBuilds(pBuild.Namespace, r.BuildCache.Values())
				if err != nil {
					return fmt.Errorf("unable to determine running namespace builds: %v", err)
				}
				// if there are no running builds, check if there are any pending builds that can be started
				// avoid exceeding total builds
				if len(runningNSBuilds) == 0 && len(r.BuildCache.Values()) < r.BuildQoS.TotalBuilds {
					if r.EnableDebug {
						opLog.Info("Checking StartBuildOrCancelExtraBuilds")
					}
					startBuild, err := lagooncrd.StartBuildOrCancelExtraBuilds(ctx, r.Client, opLog, r.QueueCache, r.BuildCache, pBuild.Namespace)
					if err != nil {
						// only return if there is an error doing this operation
						// continue on otherwise to allow the queued status updater to run
						return err
					} else {
						// start the build pod immediately
						var lagoonBuild lagooncrd.LagoonBuild
						if err := r.Get(ctx, types.NamespacedName{Namespace: pBuild.Namespace, Name: startBuild}, &lagoonBuild); err != nil {
							return err
						}
						if err := r.processBuild(ctx, opLog, lagoonBuild); err != nil {
							// remove it from the cache if there is a failure to process the build
							r.BuildCache.Remove(lagoonBuild.Name)
							r.QueueCache.Remove(lagoonBuild.Name)
						}
						startedBuilds++
					}
					// don't handle the queued process for this build, continue to next in the list
					continue
				}
			} else {
				break
			}
		}
	}
	return nil
}

// this just updates all currently queued builds as required
func (r *LagoonBuildReconciler) updateQueue(ctx context.Context, opLog logr.Logger) error {
	// this process can be quite heavy when it goes to submit the queue messages to the api
	// the downside of this is that there can be delays with the messages it sends to the actual
	// status of the builds, but build complete/fail/cancel will always win out on the lagoon-core side
	// so this isn't that much of an issue if there are some delays in the messages
	sortedBuilds, _ := lagooncrd.SortQueuedBuilds(r.QueueCache.Values())
	// if we have any pending builds, then grab the latest one and make it running
	// if there are any other pending builds, cancel them so only the latest one runs
	for idx, pBuild := range sortedBuilds {
		// update the build to be queued, and add a log message with the build log with the current position in the queue
		// this position will update as builds are created/processed, so the position of a build could change depending on
		// higher or lower priority builds being created
		qcb := sortedBuilds[idx]
		if qcbs, ok := r.QueueCache.Get(sortedBuilds[idx].Name); ok {
			// get the latest version of the item from the cache before comparing anything
			qcb = lagooncrd.StrToCachedBuildQueueItem(qcbs)
		}
		// only update the queued build if the position or length of the queue changes
		// simply to reduce messages sent
		tNow := time.Now().UTC().Unix()
		// don't send the updated queue position if the last update was sent in the last 15 seconds
		// this prevents spamming the message queue during busy build queue periods
		// delaying this just means the queue status in the api won't be reflected immediately as status changes
		if (tNow-qcb.UpdatedTimestamp) > 15 && (qcb.Position != (idx+1) || qcb.Length != len(sortedBuilds)) {
			// if qcb.Position != (idx+1) || qcb.Length != len(sortedBuilds) {
			qcb.Position = (idx + 1)
			qcb.Length = len(sortedBuilds)
			qcb.UpdatedTimestamp = tNow
			r.QueueCache.Add(pBuild.Name, qcb.String())
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("Updating build %s to queued: %s / %s", pBuild.Name, qcb.Name, fmt.Sprintf("This build is currently queued in position %v/%v", qcb.Position, qcb.Length)))
			}
			if err := r.updateQueuedBuild(ctx, types.NamespacedName{Namespace: pBuild.Namespace, Name: pBuild.Name}, qcb.Position, qcb.Length, opLog); err != nil {
				// don't block other updates, just log the error
				opLog.Info(fmt.Sprintf("error updating queued build: %v", err))
			}
		} else {
			r.QueueCache.Add(pBuild.Name, qcb.String())
		}
	}
	return nil
}
