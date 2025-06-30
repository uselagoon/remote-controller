package v1beta2

import (
	"context"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *LagoonBuildReconciler) standardBuildProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagooncrd.LagoonBuild,
	req ctrl.Request) (ctrl.Result, error) {
	// check if we get a lagoonbuild that hasn't got any buildstatus
	// this means it was created by the message queue handler
	// so we should do the steps required for a lagoon build and then copy the build
	// into the created namespace
	if _, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; !ok {
		return r.createNamespaceBuild(ctx, opLog, lagoonBuild)
	}
	runningNSBuilds, _ := lagooncrd.NamespaceRunningBuilds(req.Namespace, r.BuildCache.Values())
	for _, runningBuild := range runningNSBuilds {
		// if the running build is the one from this request then process it
		if lagoonBuild.Name == runningBuild.Name {
			// actually process the build here
			if _, ok := lagoonBuild.Labels["lagoon.sh/buildStarted"]; !ok {
				if err := r.processBuild(ctx, opLog, lagoonBuild); err != nil {
					return ctrl.Result{}, err
				}
			}
		} // end check if running build is current LagoonBuild
	} // end loop for running builds

	// if there are no running builds, check if there are any pending builds that can be started
	if len(runningNSBuilds) == 0 {
		return ctrl.Result{}, lagooncrd.CancelExtraBuilds(ctx, r.Client, opLog, r.QueueCache, r.BuildCache, req.Namespace, "Running")
	}
	return ctrl.Result{}, nil
}
