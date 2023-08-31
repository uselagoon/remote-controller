package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
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
	lagoonBuild lagoonv1beta1.LagoonBuild,
	req ctrl.Request) (ctrl.Result, error) {
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
		opLog.Info(fmt.Sprintf("Checking which build next"))
	}
	// handle the QoS build process here
	return ctrl.Result{}, r.whichBuildNext(ctx, opLog)
}

func (r *LagoonBuildReconciler) whichBuildNext(ctx context.Context, opLog logr.Logger) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusRunning.String(),
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	runningBuilds := &lagoonv1beta1.LagoonBuildList{}
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the cluster, there may be none or something went wrong: %v", err)
	}
	if len(runningBuilds.Items) >= r.BuildQoS.MaxBuilds {
		// if the maximum number of builds is hit, then drop out and try again next time
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Currently %v running builds, no room for new builds to be started", len(runningBuilds.Items)))
		}
		return nil
	}
	buildsToStart := r.BuildQoS.MaxBuilds - len(runningBuilds.Items)
	if buildsToStart > 0 {
		opLog.Info(fmt.Sprintf("Currently %v running builds, room for %v builds to be started", len(runningBuilds.Items), buildsToStart))
		// if there are any free slots to start a build, do that here
		listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusPending.String(),
				"lagoon.sh/controller":  r.ControllerNamespace,
			}),
		})
		pendingBuilds := &lagoonv1beta1.LagoonBuildList{}
		if err := r.List(ctx, pendingBuilds, listOption); err != nil {
			return fmt.Errorf("Unable to list builds in the cluster, there may be none or something went wrong: %v", err)
		}
		if len(pendingBuilds.Items) > 0 {
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("There are %v pending builds", len(pendingBuilds.Items)))
			}
			// if we have any pending builds, then grab the latest one and make it running
			// if there are any other pending builds, cancel them so only the latest one runs
			sort.Slice(pendingBuilds.Items, func(i, j int) bool {
				// sort by priority, then creation timestamp
				iPriority := r.BuildQoS.DefaultValue
				jPriority := r.BuildQoS.DefaultValue
				if ok := pendingBuilds.Items[i].Spec.Build.Priority; ok != nil {
					iPriority = *pendingBuilds.Items[i].Spec.Build.Priority
				}
				if ok := pendingBuilds.Items[j].Spec.Build.Priority; ok != nil {
					jPriority = *pendingBuilds.Items[j].Spec.Build.Priority
				}
				if iPriority < jPriority {
					return false
				}
				if iPriority > jPriority {
					return true
				}
				return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.Before(&pendingBuilds.Items[j].ObjectMeta.CreationTimestamp)
			})
			for idx, pBuild := range pendingBuilds.Items {
				if idx <= buildsToStart {
					if r.EnableDebug {
						opLog.Info(fmt.Sprintf("Checking if build %s can be started", pBuild.ObjectMeta.Name))
					}
					// if we do have a `lagoon.sh/buildStatus` set, then process as normal
					runningNSBuilds := &lagoonv1beta1.LagoonBuildList{}
					listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
						client.InNamespace(pBuild.ObjectMeta.Namespace),
						client.MatchingLabels(map[string]string{
							"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusRunning.String(),
							"lagoon.sh/controller":  r.ControllerNamespace,
						}),
					})
					// list any builds that are running
					if err := r.List(ctx, runningNSBuilds, listOption); err != nil {
						return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
					}
					// if there are no running builds, check if there are any pending builds that can be started
					if len(runningNSBuilds.Items) == 0 {
						pendingNSBuilds := &lagoonv1beta1.LagoonBuildList{}
						if err := helpers.CancelExtraBuilds(ctx, r.Client, opLog, pendingNSBuilds, pBuild.ObjectMeta.Namespace, "Running"); err != nil {
							// only return if there is an error doing this operation
							// continue on otherwise to allow the queued status updater to run
							return err
						}
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
							return err
						}
					}
				}
				// update the build to be queued, and add a log message with the build log with the current position in the queue
				// this position will update as builds are created/processed, so the position of a build could change depending on
				// higher or lower priority builds being created
				r.updateQueuedBuild(ctx, pBuild, (idx + 1), len(pendingBuilds.Items), opLog)
			}
		}
	}
	return nil
}
