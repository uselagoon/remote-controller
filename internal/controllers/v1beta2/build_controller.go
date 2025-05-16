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
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/dockerhost"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/messenger"
)

// LagoonBuildReconciler reconciles a LagoonBuild object
type LagoonBuildReconciler struct {
	client.Client
	Log                   logr.Logger
	Scheme                *runtime.Scheme
	EnableMQ              bool
	Messaging             *messenger.Messenger
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
	Harbor                           harbor.Harbor
	LFFQoSEnabled                    bool
	BuildQoS                         BuildQoS
	NativeCronPodMinFrequency        int
	LagoonTargetName                 string
	LagoonFeatureFlags               map[string]string
	LagoonAPIConfiguration           helpers.LagoonAPIConfiguration
	ProxyConfig                      ProxyConfig
	UnauthenticatedRegistry          string
	ImagePullPolicy                  corev1.PullPolicy
	DockerHost                       *dockerhost.DockerHost
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
	buildFinalizer = "finalizer.lagoonbuild.crd.lagoon.sh/v1beta2"
)

// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoonbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoonbuilds/status,verbs=get;update;patch

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)

	// your logic here
	var lagoonBuild lagooncrd.LagoonBuild
	if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonBuild.DeletionTimestamp.IsZero() {
		// if the build isn't being deleted, but the status is cancelled
		// then clean up the undeployable build
		if value, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; ok {
			// if cancelled, handle the cancellation process
			if value == lagooncrd.BuildStatusCancelled.String() {
				if value, ok := lagoonBuild.Labels["lagoon.sh/cancelledByNewBuild"]; ok && value == "true" {
					opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled by new build", lagoonBuild.Name))
					_ = r.cleanUpUndeployableBuild(ctx, lagoonBuild, "This build was cancelled as a newer build was triggered.", opLog, true)
				} else {
					opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled", lagoonBuild.Name))
					_ = r.cleanUpUndeployableBuild(ctx, lagoonBuild, "", opLog, false)
				}
			}
		}
		if r.LFFQoSEnabled {
			// handle QoS builds here
			// if we do have a `lagoon.sh/buildStatus` set as running, then process it
			runningNSBuilds := &lagooncrd.LagoonBuildList{}
			listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{
					"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String(),
					"lagoon.sh/controller":  r.ControllerNamespace,
				}),
			})
			// list any builds that are running
			if err := r.List(ctx, runningNSBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			for _, runningBuild := range runningNSBuilds.Items {
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
			// once running builds are processed, run the qos handler
			return r.qosBuildProcessor(ctx, opLog, lagoonBuild)
		}
		// if qos is not enabled, just process it as a standard build
		return r.standardBuildProcessor(ctx, opLog, lagoonBuild, req)
	}
	// The object is being deleted
	if helpers.ContainsString(lagoonBuild.Finalizers, buildFinalizer) {
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
			opLog.Error(err, "Unable to delete external resources")
			return ctrl.Result{}, err
		}
		// remove our finalizer from the list and update it.
		lagoonBuild.Finalizers = helpers.RemoveString(lagoonBuild.Finalizers, buildFinalizer)
		// use patches to avoid update errors
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"finalizers": lagoonBuild.Finalizers,
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
		For(&lagooncrd.LagoonBuild{}).
		WithEventFilter(BuildPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *LagoonBuildReconciler) createNamespaceBuild(ctx context.Context,
	opLog logr.Logger,
	lagoonBuild lagooncrd.LagoonBuild) (ctrl.Result, error) {

	namespace := &corev1.Namespace{}
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking Namespace exists for: %s", lagoonBuild.Name))
	}
	err := r.getOrCreateNamespace(ctx, namespace, lagoonBuild, opLog)
	if err != nil {
		return ctrl.Result{}, err
	}
	// create the `lagoon-deployer` ServiceAccount
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.Name))
	}
	serviceAccount := &corev1.ServiceAccount{}
	err = r.getOrCreateServiceAccount(ctx, serviceAccount, namespace.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	// ServiceAccount RoleBinding creation
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.Name))
	}
	saRoleBinding := &rbacv1.RoleBinding{}
	err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, namespace.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// copy the build resource into a new resource and set the status to pending
	// create the new resource and the controller will handle it via queue
	opLog.Info(fmt.Sprintf("Creating LagoonBuild in Pending status: %s", lagoonBuild.Name))
	err = r.getOrCreateBuildResource(ctx, &lagoonBuild, namespace.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	// if everything is all good controller will handle the new build resource that gets created as it will have
	// the `lagoon.sh/buildStatus = Pending` now
	err = lagooncrd.CancelExtraBuilds(ctx, r.Client, opLog, namespace.Name, lagooncrd.BuildStatusPending.String())
	if err != nil {
		return ctrl.Result{}, err
	}

	// as this is a new build coming through, check if there are any running builds in the namespace
	// if there are, then check the status of that build. if the build pod is missing then the build running will block
	// if the pod exists, attempt to get the status of it (only if its complete or failed) and ship the status
	runningBuilds := &lagooncrd.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(namespace.Name),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": lagooncrd.BuildStatusRunning.String()}),
	})
	// list all builds in the namespace that have the running buildstatus
	if err := r.List(ctx, runningBuilds, listOption); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	// if there are running builds still, check if the pod exists or if the pod is complete/failed and attempt to get the status
	for _, rBuild := range runningBuilds.Items {
		runningBuild := rBuild.DeepCopy()
		lagoonBuildPod := corev1.Pod{}
		err := r.Get(ctx, types.NamespacedName{
			Namespace: rBuild.Namespace,
			Name:      rBuild.Name,
		}, &lagoonBuildPod)
		buildCondition := lagooncrd.BuildStatusCancelled
		if err != nil {
			// cancel the build as there is no pod available
			opLog.Info(fmt.Sprintf("Setting build %s as cancelled", runningBuild.Name))
			runningBuild.Labels["lagoon.sh/buildStatus"] = buildCondition.String()
		} else {
			// get the status from the pod and update the build
			if lagoonBuildPod.Status.Phase == corev1.PodFailed || lagoonBuildPod.Status.Phase == corev1.PodSucceeded {
				buildCondition = lagooncrd.GetBuildConditionFromPod(lagoonBuildPod.Status.Phase)
				opLog.Info(fmt.Sprintf("Setting build %s as %s", runningBuild.Name, buildCondition.String()))
				runningBuild.Labels["lagoon.sh/buildStatus"] = buildCondition.String()
			} else {
				// drop out, don't do anything else
				continue
			}
		}
		if err := r.Update(ctx, runningBuild); err != nil {
			// log the error and drop out
			opLog.Error(err, fmt.Sprintf("Error setting build %s as cancelled", runningBuild.Name))
			continue
		}
		// send the status change to lagoon
		r.updateDeploymentAndEnvironmentTask(ctx, opLog, runningBuild, false, buildCondition, "cancelled")
		continue
	}
	// handle processing running but no pod/failed pod builds
	return ctrl.Result{}, nil
}

// getOrCreateBuildResource will deepcopy the lagoon build into a new resource and push it to the new namespace
// then clean up the old one.
func (r *LagoonBuildReconciler) getOrCreateBuildResource(ctx context.Context, lagoonBuild *lagooncrd.LagoonBuild, ns string) error {
	newBuild := lagoonBuild.DeepCopy()
	newBuild.SetNamespace(ns)
	newBuild.SetResourceVersion("")
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/buildStatus": lagooncrd.BuildStatusPending.String(),
			"lagoon.sh/controller":  r.ControllerNamespace,
			"crd.lagoon.sh/version": crdVersion,
		},
	)
	// add the finalizer to the new build
	newBuild.Finalizers = append(newBuild.Finalizers, buildFinalizer)
	// all new builds start as "queued" but will transition to pending or running fairly quickly
	// unless they are actually queued :D
	newBuild.Status.Phase = "Queued"
	// also create the build with a queued buildstep
	newBuild.Status.Conditions = []metav1.Condition{
		{
			Type: "BuildStep",
			// Reason needs to be CamelCase not camelCase. Would need to update the `build-deploy-tool` to use CamelCase
			// to eventually remove the need for `cases`
			Reason:             "Queued",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now().UTC()),
		},
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      newBuild.Name,
	}, newBuild)
	if err != nil {
		if err := r.Create(ctx, newBuild); err != nil {
			return err
		}
	}
	err = r.Delete(ctx, lagoonBuild)
	if err != nil {
		return err
	}
	return nil
}
