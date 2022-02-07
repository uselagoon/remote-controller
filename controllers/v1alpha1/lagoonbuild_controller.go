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

package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1alpha1 "github.com/uselagoon/remote-controller/apis/lagoon-deprecated/v1alpha1"
	"github.com/uselagoon/remote-controller/handlers"
	// Openshift
)

// LagoonBuildReconciler reconciles a LagoonBuild object
type LagoonBuildReconciler struct {
	client.Client
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EnableMQ              bool
	Messaging             *handlers.Messaging
	BuildImage            string
	IsOpenshift           bool
	NamespacePrefix       string
	RandomNamespacePrefix bool
	ControllerNamespace   string
	EnableDebug           bool
	FastlyServiceID       string
	FastlyWatchStatus     bool
	// BuildPodRunAsUser sets the build pod securityContext.runAsUser value.
	BuildPodRunAsUser int64
	// BuildPodRunAsGroup sets the build pod securityContext.runAsGroup value.
	BuildPodRunAsGroup int64
	// BuildPodFSGroup sets the build pod securityContext.fsGroup value.
	BuildPodFSGroup int64
	// Lagoon feature flags
	LFFForceRootlessWorkload         string
	LFFDefaultRootlessWorkload       string
	LFFForceIsolationNetworkPolicy   string
	LFFDefaultIsolationNetworkPolicy string
	BackupDefaultSchedule            string
	BackupDefaultMonthlyRetention    int
	BackupDefaultWeeklyRetention     int
	BackupDefaultDailyRetention      int
	BackupDefaultHourlyRetention     int
	LFFBackupWeeklyRandom            bool
	LFFRouterURL                     bool
	LFFHarborEnabled                 bool
	LFFQoSEnabled                    bool
	NativeCronPodMinFrequency        int
	LagoonTargetName                 string
}

var (
	buildFinalizer = "finalizer.lagoonbuild.lagoon.amazee.io/v1alpha1"
)

// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoonbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoonbuilds/status,verbs=get;update;patch

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)

	// your logic here
	var lagoonBuild lagoonv1alpha1.LagoonBuild
	if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonBuild.ObjectMeta.DeletionTimestamp.IsZero() {
		// if the build status is complete/failed/cancelled/pending, delete the resource
		// current running builds will continue
		if containsString(
			CompletedCancelledFailedPendingStatus,
			lagoonBuild.Labels["lagoon.sh/buildStatus"],
		) {
			opLog.Info(fmt.Sprintf("%s found in namespace %s is no longer required, removing it. v1alpha1 is deprecated in favor of v1beta1",
				lagoonBuild.ObjectMeta.Name,
				req.NamespacedName.Namespace,
			))
			if err := r.Delete(ctx, &lagoonBuild); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer) {
			// our finalizer is present, so lets handle any external dependency
			// first deleteExternalResources will try and check for any pending builds that it can
			// can change to running to kick off the next pending build
			if err := r.deleteExternalResources(ctx,
				opLog,
				&lagoonBuild,
				req,
			); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				opLog.Error(err, fmt.Sprintf("Unable to delete external resources"))
				return ctrl.Result{}, err
			}
			// remove our finalizer from the list and update it.
			lagoonBuild.ObjectMeta.Finalizers = removeString(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer)
			// use patches to avoid update errors
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"finalizers": lagoonBuild.ObjectMeta.Finalizers,
				},
			})
			if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				return ctrl.Result{}, ignoreNotFound(err)
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch LagoonBuilds
func (r *LagoonBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lagoonv1alpha1.LagoonBuild{}).
		WithEventFilter(BuildPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}
