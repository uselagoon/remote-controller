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

package controllers

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

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/handlers"
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
	Harbor                           Harbor
	LFFQoSEnabled                    bool
	BuildQoS                         BuildQoS
	NativeCronPodMinFrequency        int
}

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

	finalizerName := "finalizer.lagoonbuild.lagoon.amazee.io/v1alpha1"

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonBuild.ObjectMeta.DeletionTimestamp.IsZero() {
		// if the build isn't being deleted, but the status is cancelled
		// then clean up the undeployable build
		if value, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; ok {
			if value == string(lagoonv1alpha1.BuildStatusCancelled) {
				opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled", lagoonBuild.ObjectMeta.Name))
				r.cleanUpUndeployableBuild(ctx, lagoonBuild, "This build was cancelled as a newer build was triggered.", opLog)
			}
		}
		if r.LFFQoSEnabled {
			// handle QoS builds here
			// if we do have a `lagoon.sh/buildStatus` set as running, then process it
			runningNSBuilds := &lagoonv1alpha1.LagoonBuildList{}
			listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{
					"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusRunning),
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
	if containsString(lagoonBuild.ObjectMeta.Finalizers, finalizerName) {
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
		lagoonBuild.ObjectMeta.Finalizers = removeString(lagoonBuild.ObjectMeta.Finalizers, finalizerName)
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

func (r *LagoonBuildReconciler) createNamespaceBuild(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagoonv1alpha1.LagoonBuild) (ctrl.Result, error) {

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
	// create the service account role binding for openshift to allow promotions in the openshift 3.11 clusters
	if r.IsOpenshift && lagoonBuild.Spec.Build.Type == "promote" {
		err := r.getOrCreatePromoteSARoleBinding(ctx, lagoonBuild.Spec.Promote.SourceProject, namespace.ObjectMeta.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
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
	pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
	return ctrl.Result{}, cancelExtraBuilds(ctx, r.Client, opLog, pendingBuilds, namespace.ObjectMeta.Name, string(lagoonv1alpha1.BuildStatusPending))
}

// getOrCreateBuildResource will deepcopy the lagoon build into a new resource and push it to the new namespace
// then clean up the old one.
func (r *LagoonBuildReconciler) getOrCreateBuildResource(ctx context.Context, build *lagoonv1alpha1.LagoonBuild, ns string) error {
	newBuild := build.DeepCopy()
	newBuild.SetNamespace(ns)
	newBuild.SetResourceVersion("")
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusPending),
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
