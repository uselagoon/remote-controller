package v1beta2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/metrics"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BuildQoS is use for the quality of service configuration for lagoon builds.
type BuildQoS struct {
	MaxBuilds    int
	DefaultValue int
}

func (r *LagoonBuildReconciler) qosBuildProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagooncrd.LagoonBuild) (ctrl.Result, error) {
	// check if we get a lagoonbuild that hasn't got any buildstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon build and then copy the build
	// into the created namespace
	if _, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; !ok {
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Creating new build %s from message queue", lagoonBuild.ObjectMeta.Name))
		}
		return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}
	if r.EnableDebug {
		opLog.Info("Checking which build next")
	}
	// handle the QoS build process here
	// if the build is already running, then there is no need to check which build can be started next
	if lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"] == lagooncrd.BuildStatusRunning.String() {
		// this is done so that all running state updates don't try to force the queue processor to run unnecessarily
		// downside is that this can lead to queue/state changes being less frequent for queued builds in the api
		// any new builds, or complete/failed/cancelled builds will still force the whichbuildnext processor to run though
		return ctrl.Result{}, nil
	}
	// we only care if a new build can start if one is created, cancelled, failed, or completed
	return ctrl.Result{}, r.whichBuildNext(ctx, opLog)
}

func (r *LagoonBuildReconciler) whichBuildNext(ctx context.Context, opLog logr.Logger) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String(),
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	runningBuilds := &lagooncrd.LagoonBuildList{}
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		return fmt.Errorf("unable to list builds in the cluster, there may be none or something went wrong: %v", err)
	}
	buildsToStart := r.BuildQoS.MaxBuilds - len(runningBuilds.Items)
	if len(runningBuilds.Items) >= r.BuildQoS.MaxBuilds {
		// if the maximum number of builds is hit, then drop out and try again next time
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Currently %v running builds, no room for new builds to be started", len(runningBuilds.Items)))
		}
		go r.processQueue(ctx, opLog, buildsToStart, true)
		return nil
	}
	if buildsToStart > 0 {
		opLog.Info(fmt.Sprintf("Currently %v running builds, room for %v builds to be started", len(runningBuilds.Items), buildsToStart))
		// if there are any free slots to start a build, do that here
		go r.processQueue(ctx, opLog, buildsToStart, false)
	}
	return nil
}

var runningProcessQueue bool

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
	if !runningProcessQueue {
		runningProcessQueue = true
		if r.EnableDebug {
			opLog.Info("Processing queue")
		}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": lagooncrd.BuildStatusPending.String(),
				"lagoon.sh/controller":  r.ControllerNamespace,
			}),
		})
		pendingBuilds := &lagooncrd.LagoonBuildList{}
		if err := r.List(ctx, pendingBuilds, listOption); err != nil {
			runningProcessQueue = false
			return fmt.Errorf("unable to list builds in the cluster, there may be none or something went wrong: %v", err)
		}
		metrics.BuildsPendingGauge.Set(float64(len(pendingBuilds.Items)))
		if len(pendingBuilds.Items) > 0 {
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("There are %v pending builds", len(pendingBuilds.Items)))
			}
			// if we have any pending builds, then grab the latest one and make it running
			// if there are any other pending builds, cancel them so only the latest one runs
			sortBuilds(r.BuildQoS.DefaultValue, pendingBuilds)
			for idx, pBuild := range pendingBuilds.Items {
				// need to +1 to index because 0
				if idx+1 <= buildsToStart && !limitHit {
					if r.EnableDebug {
						opLog.Info(fmt.Sprintf("Checking if build %s can be started", pBuild.ObjectMeta.Name))
					}
					// if we do have a `lagoon.sh/buildStatus` set, then process as normal
					runningNSBuilds := &lagooncrd.LagoonBuildList{}
					listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
						client.InNamespace(pBuild.ObjectMeta.Namespace),
						client.MatchingLabels(map[string]string{
							"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String(),
							"lagoon.sh/controller":  r.ControllerNamespace,
						}),
					})
					// list any builds that are running
					if err := r.List(ctx, runningNSBuilds, listOption); err != nil {
						runningProcessQueue = false
						return fmt.Errorf("unable to list builds in the namespace, there may be none or something went wrong: %v", err)
					}
					// if there are no running builds, check if there are any pending builds that can be started
					if len(runningNSBuilds.Items) == 0 {
						if err := lagooncrd.CancelExtraBuilds(ctx, r.Client, opLog, pBuild.ObjectMeta.Namespace, "Running"); err != nil {
							// only return if there is an error doing this operation
							// continue on otherwise to allow the queued status updater to run
							runningProcessQueue = false
							return err
						}
						// don't handle the queued process for this build, continue to next in the list
						continue
					}
					// The object is not being deleted, so if it does not have our finalizer,
					// then lets add the finalizer and update the object. This is equivalent
					// registering our finalizer.
					if !helpers.ContainsString(pBuild.ObjectMeta.Finalizers, buildFinalizer) {
						pBuild.ObjectMeta.Finalizers = append(pBuild.ObjectMeta.Finalizers, buildFinalizer)
						// use patches to avoid update errors
						mergePatch, _ := json.Marshal(map[string]interface{}{
							"metadata": map[string]interface{}{
								"finalizers": pBuild.ObjectMeta.Finalizers,
							},
						})
						if err := r.Patch(ctx, &pBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
							runningProcessQueue = false
							return err
						}
					}
				}
				// update the build to be queued, and add a log message with the build log with the current position in the queue
				// this position will update as builds are created/processed, so the position of a build could change depending on
				// higher or lower priority builds being created
				if err := r.updateQueuedBuild(ctx, pBuild, (idx + 1), len(pendingBuilds.Items), opLog); err != nil {
					runningProcessQueue = false
					return nil
				}
			}
		}
		runningProcessQueue = false
	}
	return nil
}
