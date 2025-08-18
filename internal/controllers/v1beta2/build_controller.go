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

	"github.com/go-logr/logr"
	lru "github.com/hashicorp/golang-lru/v2"
	corev1 "k8s.io/api/core/v1"
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
	APIReader             client.Reader
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
	Harbor                           *harbor.Harbor
	BuildQoS                         BuildQoS
	NativeCronPodMinFrequency        int
	LagoonTargetName                 string
	LagoonFeatureFlags               map[string]string
	LagoonAPIConfiguration           helpers.LagoonAPIConfiguration
	ProxyConfig                      ProxyConfig
	UnauthenticatedRegistry          string
	ImagePullPolicy                  corev1.PullPolicy
	DockerHost                       *dockerhost.DockerHost
	QueueCache                       *lru.Cache[string, string]
	BuildCache                       *lru.Cache[string, string]
	ClusterAutoscalerEvict           bool
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
				var msg []byte
				if value, ok := lagoonBuild.Labels["lagoon.sh/cancelledByNewBuild"]; ok && value == "true" {
					opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled by new build", lagoonBuild.Name))
					msg, _ = lagooncrd.CleanUpUndeployableBuild(ctx, r.Client, r.EnableMQ, lagoonBuild, "This build was cancelled as a newer build was triggered.", opLog, true, r.LagoonTargetName)
				} else {
					opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled", lagoonBuild.Name))
					msg, _ = lagooncrd.CleanUpUndeployableBuild(ctx, r.Client, r.EnableMQ, lagoonBuild, "", opLog, true, r.LagoonTargetName)
				}
				r.Messaging.BuildStatusLogsToLagoonLogs(ctx, r.EnableMQ, opLog, &lagoonBuild, lagooncrd.BuildStatusCancelled, r.LagoonTargetName, "cancelled")
				r.Messaging.UpdateDeploymentAndEnvironmentTask(ctx, r.EnableMQ, opLog, &lagoonBuild, true, lagooncrd.BuildStatusCancelled, r.LagoonTargetName, "cancelled")
				r.Messaging.BuildLogsToLagoonLogs(r.EnableMQ, opLog, &lagoonBuild, msg, lagooncrd.BuildStatusCancelled, r.LagoonTargetName)
				// ensure the build is removed from any queues when it is cancelled
				r.BuildCache.Remove(lagoonBuild.Name)
				r.QueueCache.Remove(lagoonBuild.Name)
			}
		}
		if status, ok := lagoonBuild.Labels["lagoon.sh/buildStatus"]; ok && status == lagooncrd.BuildStatusPending.String() {
			priority := r.BuildQoS.DefaultPriority
			if lagoonBuild.Spec.Build.Priority != nil {
				priority = *lagoonBuild.Spec.Build.Priority
			}
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("Adding build %s to queue (%s)", lagoonBuild.Name, status))
			}
			// add the build to the cache when it is received
			qc := lagooncrd.NewCachedBuildQueueItem(lagoonBuild, priority, 0, 0)
			r.QueueCache.Add(lagoonBuild.Name, qc.String())
		}
		// handle QoS builds here
		return r.qosBuildProcessor(ctx, opLog, lagoonBuild)
	}
	// The object is being deleted
	if helpers.ContainsString(lagoonBuild.Finalizers, lagooncrd.BuildFinalizer) {
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
		lagoonBuild.Finalizers = helpers.RemoveString(lagoonBuild.Finalizers, lagooncrd.BuildFinalizer)
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
	// remove the build from the cache when the build itself is removed
	r.BuildCache.Remove(lagoonBuild.Name)
	r.QueueCache.Remove(lagoonBuild.Name)
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
