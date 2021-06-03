package controllers

import (
	"context"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
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

	// handle the QoS build process here
	return ctrl.Result{}, nil
}
