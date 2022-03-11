package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *LagoonBuildReconciler) standardBuildProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagoonv1beta1.LagoonBuild,
	req ctrl.Request) (ctrl.Result, error) {
	// check if we get a lagoonbuild that hasn't got any buildstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon build and then copy the build
	// into the created namespace
	if _, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; !ok {
		return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}

	// if we do have a `lagoon.sh/buildStatus` set, then process as normal
	runningBuilds := &lagoonv1beta1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(req.Namespace),
		client.MatchingLabels(map[string]string{
			"lagoon.sh/buildStatus": string(lagoonv1beta1.BuildStatusRunning),
			"lagoon.sh/controller":  r.ControllerNamespace,
		}),
	})
	// list any builds that are running
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	for _, runningBuild := range runningBuilds.Items {
		// if the running build is the one from this request then process it
		if lagoonBuild.ObjectMeta.Name == runningBuild.ObjectMeta.Name {
			// actually process the build here
			if err := r.processBuild(ctx, opLog, lagoonBuild); err != nil {
				return ctrl.Result{}, err
			}
		} // end check if running build is current LagoonBuild
	} // end loop for running builds

	// if there are no running builds, check if there are any pending builds that can be started
	if len(runningBuilds.Items) == 0 {
		pendingBuilds := &lagoonv1beta1.LagoonBuildList{}
		return ctrl.Result{}, helpers.CancelExtraBuilds(ctx, r.Client, opLog, pendingBuilds, req.Namespace, "Running")
	}
	// The object is not being deleted, so if it does not have our finalizer,
	// then lets add the finalizer and update the object. This is equivalent
	// registering our finalizer.
	if !helpers.ContainsString(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer) {
		lagoonBuild.ObjectMeta.Finalizers = append(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer)
		// use patches to avoid update errors
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"finalizers": lagoonBuild.ObjectMeta.Finalizers,
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}
