package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	lagoonv1alpha1 "github.com/uselagoon/remote-controller/apis/lagoon-old/v1alpha1"
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
	lagoonBuild lagoonv1alpha1.LagoonBuild,
	req ctrl.Request) (ctrl.Result, error) {
	// check if we get a lagoonbuild that hasn't got any buildstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon build and then copy the build
	// into the created namespace
	if _, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; !ok {
		return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}
	// handle the QoS build process here
	return ctrl.Result{}, r.whichBuildNext(ctx, opLog)
}

func (r *LagoonBuildReconciler) whichBuildNext(ctx context.Context, opLog logr.Logger) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusRunning),
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	runningBuilds := &lagoonv1alpha1.LagoonBuildList{}
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the cluster, there may be none or something went wrong: %v", err)
	}
	opLog.Info(fmt.Sprintf("There are %v Running builds", len(runningBuilds.Items)))
	if len(runningBuilds.Items) >= r.BuildQoS.MaxBuilds {
		// if the maximum number of builds is hit, then drop out and try again next time
		return nil
	}
	buildsToStart := r.BuildQoS.MaxBuilds - len(runningBuilds.Items)
	if buildsToStart > 0 {
		opLog.Info(fmt.Sprintf("There is room for %v builds to be started", buildsToStart))
		// if there are any free slots to start a build, do that here
		listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusPending),
				"lagoon.sh/controller":  r.ControllerNamespace,
			}),
		})
		pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
		if err := r.List(ctx, pendingBuilds, listOption); err != nil {
			return fmt.Errorf("Unable to list builds in the cluster, there may be none or something went wrong: %v", err)
		}
		opLog.Info(fmt.Sprintf("There are %v Pending builds", len(runningBuilds.Items)))
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
		if len(pendingBuilds.Items) > 0 {
			for idx, pBuild := range pendingBuilds.Items {
				if idx <= buildsToStart {
					// if we do have a `lagoon.sh/buildStatus` set, then process as normal
					runningNSBuilds := &lagoonv1alpha1.LagoonBuildList{}
					listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
						client.InNamespace(pBuild.ObjectMeta.Namespace),
						client.MatchingLabels(map[string]string{
							"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusRunning),
							"lagoon.sh/controller":  r.ControllerNamespace,
						}),
					})
					// list any builds that are running
					if err := r.List(ctx, runningNSBuilds, listOption); err != nil {
						return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
					}
					// if there are no running builds, check if there are any pending builds that can be started
					if len(runningNSBuilds.Items) == 0 {
						pendingNSBuilds := &lagoonv1alpha1.LagoonBuildList{}
						return cancelExtraBuilds(ctx, r.Client, opLog, pendingNSBuilds, pBuild.ObjectMeta.Namespace, "Running")
					}
					// The object is not being deleted, so if it does not have our finalizer,
					// then lets add the finalizer and update the object. This is equivalent
					// registering our finalizer.
					if !containsString(pBuild.ObjectMeta.Finalizers, buildFinalizer) {
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
			}
		}
	}
	return nil
}
