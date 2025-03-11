/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta2

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/hashicorp/golang-lru/v2/expirable"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/messenger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// BuildMonitorReconciler reconciles a Lagoon Build Pod object
type BuildMonitorReconciler struct {
	client.Client
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EnableMQ              bool
	Messaging             *messenger.Messenger
	ControllerNamespace   string
	NamespacePrefix       string
	RandomNamespacePrefix bool
	EnableDebug           bool
	LagoonTargetName      string
	LFFQoSEnabled         bool
	BuildQoS              BuildQoS
	Cache                 *expirable.LRU[string, string]
}

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *BuildMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoonbuildmonitor", req.NamespacedName)

	var jobPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &jobPod); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	err := r.calculateBuildMetrics(ctx)
	if err != nil {
		opLog.Error(err, "Unable to generate metrics.")
	}
	if jobPod.ObjectMeta.DeletionTimestamp.IsZero() {
		// pod is not being deleted
		return ctrl.Result{}, r.handleBuildMonitor(ctx, opLog, req, jobPod)
	}

	// a pod deletion request came through
	// first try and clean up the pod and capture the logs and update
	// the lagoonbuild that owns it with the status
	var lagoonBuild lagooncrd.LagoonBuild
	err = r.Get(ctx, types.NamespacedName{
		Namespace: jobPod.ObjectMeta.Namespace,
		Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
	}, &lagoonBuild)
	if err != nil {
		opLog.Info("The build that started this pod may have been deleted or not started yet, continuing with cancellation if required.")
		err = r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, true)
		if err != nil {
			opLog.Error(err, "Unable to update the LagoonBuild.")
		}
	} else {
		if helpers.ContainsString(
			lagooncrd.BuildRunningPendingStatus,
			lagoonBuild.Labels["lagoon.sh/buildStatus"],
		) {
			opLog.Info("Attempting to update the LagoonBuild with cancellation if required.")
			// this will update the deployment back to lagoon if it can do so
			// and should only update if the LagoonBuild is Pending or Running
			err = r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, nil, true)
			if err != nil {
				opLog.Error(err, "Unable to update the LagoonBuild.")
			}
		}
	}
	// if the update is successful or not, it will just continue on to check for pending builds
	// in the event pending builds are not processed and the build pod itself has been deleted
	// then manually patching the `LagoonBuild` with the label
	// "lagoon.sh/buildStatus=Cancelled"
	// should be enough to get things rolling again if no pending builds are being picked up

	// if we got any pending builds come through while one is running
	// they will be processed here when any pods are cleaned up
	// we check all `LagoonBuild` in the requested namespace
	// if there are no running jobs, we check for any pending jobs
	// sorted by their creation timestamp and set the first to running
	if !r.LFFQoSEnabled {
		// if qos is not enabled, then handle the check for pending builds here
		opLog.Info("Checking for any pending builds.")
		runningBuilds := &lagooncrd.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(req.Namespace),
			client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String()}),
		})
		// list all builds in the namespace that have the running buildstatus
		if err := r.List(ctx, runningBuilds, listOption); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to list builds in the namespace, there may be none or something went wrong: %v", err)
		}
		// if we have no running builds, then check for any pending builds
		if len(runningBuilds.Items) == 0 {
			return ctrl.Result{}, lagooncrd.CancelExtraBuilds(ctx, r.Client, opLog, req.Namespace, "Running")
		}
	} else {
		// since qos handles pending build checks as part of its own operations, we can skip the running pod check step with no-op
		if r.EnableDebug {
			opLog.Info("No pending build check in namespaces when QoS is enabled")
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch Pods with an event filter that contains our build label
func (r *BuildMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("buildpod").
		For(&corev1.Pod{}).
		WithEventFilter(BuildPodPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *BuildMonitorReconciler) collectLogs(ctx context.Context, req reconcile.Request, jobPod corev1.Pod) ([]byte, error) {
	var allContainerLogs []byte
	// grab all the logs from the containers in the task pod and just merge them all together
	// we only have 1 container at the moment in a taskpod anyway so it doesn't matter
	// if we do move to multi container tasks, then worry about it
	for _, container := range jobPod.Spec.Containers {
		cLogs, err := getContainerLogs(ctx, container.Name, req)
		if err != nil {
			return nil, fmt.Errorf("unable to retrieve logs from pod: %v", err)
		}
		allContainerLogs = append(allContainerLogs, cLogs...)
	}
	return allContainerLogs, nil
}
