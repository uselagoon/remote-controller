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

package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/handlers"
	"github.com/uselagoon/remote-controller/internal/helpers"
)

// LagoonBuildReconciler reconciles a LagoonBuild object
type LagoonBuildReconciler struct {
	client.Client
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EnableMQ              bool
	Messaging             *handlers.Messaging
	BuildImage            string
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
	LFFForceInsights                 string
	LFFDefaultInsights               string
	LFFForceRWX2RWO                  string
	LFFDefaultRWX2RWO                string
	LFFBackupWeeklyRandom            bool
	LFFRouterURL                     bool
	LFFHarborEnabled                 bool
	BackupConfig                     BackupConfig
	Harbor                           Harbor
	LFFQoSEnabled                    bool
	BuildQoS                         BuildQoS
	NativeCronPodMinFrequency        int
	LagoonTargetName                 string
	ProxyConfig                      ProxyConfig
}

// BackupConfig holds all the backup configuration settings
type BackupConfig struct {
	BackupDefaultSchedule         string
	BackupDefaultMonthlyRetention int
	BackupDefaultWeeklyRetention  int
	BackupDefaultDailyRetention   int
	BackupDefaultHourlyRetention  int

	BackupDefaultDevelopmentSchedule  string
	BackupDefaultPullrequestSchedule  string
	BackupDefaultDevelopmentRetention string
	BackupDefaultPullrequestRetention string
}

// ProxyConfig is used for proxy configuration.
type ProxyConfig struct {
	HTTPProxy  string
	HTTPSProxy string
	NoProxy    string
}

var (
	buildFinalizer = "finalizer.lagoonbuild.crd.lagoon.sh/v1beta1"
)

// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoonbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoonbuilds/status,verbs=get;update;patch

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)

	// your logic here
	var lagoonBuild lagoonv1beta1.LagoonBuild
	if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonBuild.ObjectMeta.DeletionTimestamp.IsZero() {
		// if the build isn't being deleted, but the status is cancelled
		// then clean up the undeployable build
		if value, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; ok {
			if value == string(lagoonv1beta1.BuildStatusCancelled) {
				opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled", lagoonBuild.ObjectMeta.Name))
				if value, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/cancelledByNewBuild"]; ok {
					if value == "true" {
						r.cleanUpUndeployableBuild(ctx, lagoonBuild, "This build was cancelled as a newer build was triggered.", opLog, true)
					} else {
						r.cleanUpUndeployableBuild(ctx, lagoonBuild, "", opLog, false)
					}
				}
			}
		}
		if r.LFFQoSEnabled {
			// handle QoS builds here
			// if we do have a `lagoon.sh/buildStatus` set as running, then process it
			runningNSBuilds := &lagoonv1beta1.LagoonBuildList{}
			listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{
					"lagoon.sh/buildStatus": string(lagoonv1beta1.BuildStatusRunning),
					"lagoon.sh/controller":  r.ControllerNamespace,
				}),
			})
			// list any builds that are running
			if err := r.List(ctx, runningNSBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			for _, runningBuild := range runningNSBuilds.Items {
				// if the running build is the one from this request then process it
				if lagoonBuild.ObjectMeta.Name == runningBuild.ObjectMeta.Name {
					// actually process the build here
					if err := r.processBuild(ctx, opLog, lagoonBuild); err != nil {
						return ctrl.Result{}, err
					}
				} // end check if running build is current LagoonBuild
			} // end loop for running builds
			// once running builds are processed, run the qos handler
			return r.qosBuildProcessor(ctx, opLog, lagoonBuild, req)
		}
		// if qos is not enabled, just process it as a standard build
		return r.standardBuildProcessor(ctx, opLog, lagoonBuild, req)
	}
	// The object is being deleted
	if helpers.ContainsString(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer) {
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
		lagoonBuild.ObjectMeta.Finalizers = helpers.RemoveString(lagoonBuild.ObjectMeta.Finalizers, buildFinalizer)
		// use patches to avoid update errors
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"finalizers": lagoonBuild.ObjectMeta.Finalizers,
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return ctrl.Result{}, helpers.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch LagoonBuilds
func (r *LagoonBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lagoonv1beta1.LagoonBuild{}).
		WithEventFilter(BuildPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *LagoonBuildReconciler) createNamespaceBuild(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagoonv1beta1.LagoonBuild) (ctrl.Result, error) {

	namespace := &corev1.Namespace{}
	opLog.Info(fmt.Sprintf("Checking Namespace exists for: %s", lagoonBuild.ObjectMeta.Name))
	err := r.getOrCreateNamespace(ctx, namespace, lagoonBuild, opLog)
	if err != nil {
		return ctrl.Result{}, err
	}
	// create the `lagoon-deployer` ServiceAccount
	opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.ObjectMeta.Name))
	serviceAccount := &corev1.ServiceAccount{}
	err = r.getOrCreateServiceAccount(ctx, serviceAccount, namespace.ObjectMeta.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	// ServiceAccount RoleBinding creation
	opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.ObjectMeta.Name))
	saRoleBinding := &rbacv1.RoleBinding{}
	err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, namespace.ObjectMeta.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// copy the build resource into a new resource and set the status to pending
	// create the new resource and the controller will handle it via queue
	opLog.Info(fmt.Sprintf("Creating LagoonBuild in Pending status: %s", lagoonBuild.ObjectMeta.Name))
	err = r.getOrCreateBuildResource(ctx, &lagoonBuild, namespace.ObjectMeta.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// if everything is all good controller will handle the new build resource that gets created as it will have
	// the `lagoon.sh/buildStatus = Pending` now
	// so end this reconcile process
	pendingBuilds := &lagoonv1beta1.LagoonBuildList{}
	return ctrl.Result{}, helpers.CancelExtraBuilds(ctx, r.Client, opLog, pendingBuilds, namespace.ObjectMeta.Name, string(lagoonv1beta1.BuildStatusPending))
}

// getOrCreateBuildResource will deepcopy the lagoon build into a new resource and push it to the new namespace
// then clean up the old one.
func (r *LagoonBuildReconciler) getOrCreateBuildResource(ctx context.Context, build *lagoonv1beta1.LagoonBuild, ns string) error {
	newBuild := build.DeepCopy()
	newBuild.SetNamespace(ns)
	newBuild.SetResourceVersion("")
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/buildStatus": string(lagoonv1beta1.BuildStatusPending),
			"lagoon.sh/controller":  r.ControllerNamespace,
		},
	)
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      newBuild.ObjectMeta.Name,
	}, newBuild)
	if err != nil {
		if err := r.Create(ctx, newBuild); err != nil {
			return err
		}
	}
	err = r.Delete(ctx, build)
	if err != nil {
		return err
	}
	return nil
}
