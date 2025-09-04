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
	"strconv"

	"github.com/go-logr/logr"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/messenger"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TaskMonitorReconciler reconciles a Lagoon Task Pod object
type TaskMonitorReconciler struct {
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
	Cache                 *expirable.LRU[string, string]
	QueueCache            *lru.Cache[string, string]
	TasksCache            *lru.Cache[string, string]
}

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *TaskMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoontaskmonitor", req.NamespacedName)

	var jobPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &jobPod); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	if jobPod.DeletionTimestamp.IsZero() {
		// pod is not being deleted
		return ctrl.Result{}, r.handleTaskMonitor(ctx, opLog, req, jobPod)
	}
	// pod deletion request came through, check if this is an activestandby task, if it is, delete the activestandby role
	if value, ok := jobPod.Labels["lagoon.sh/activeStandby"]; ok {
		isActiveStandby, _ := strconv.ParseBool(value)
		if isActiveStandby {
			var destinationNamespace string
			if value, ok := jobPod.Labels["lagoon.sh/activeStandbyDestinationNamespace"]; ok {
				destinationNamespace = value
			}
			err := r.deleteActiveStandbyRole(ctx, destinationNamespace)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch Pods with an event filter that contains our task label
func (r *TaskMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("taskpod").
		For(&corev1.Pod{}).
		WithEventFilter(TaskPodPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *TaskMonitorReconciler) collectLogs(ctx context.Context, req reconcile.Request, jobPod corev1.Pod) ([]byte, error) {
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

// deleteActiveStandbyRole
func (r *TaskMonitorReconciler) deleteActiveStandbyRole(ctx context.Context, destinationNamespace string) error {
	activeStandbyRoleBinding := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: destinationNamespace,
		Name:      "lagoon-deployer-activestandby",
	}, activeStandbyRoleBinding)
	if err != nil {
		_ = helpers.IgnoreNotFound(err)
	}
	err = r.Delete(ctx, activeStandbyRoleBinding)
	if err != nil {
		return fmt.Errorf("unable to delete lagoon-deployer-activestandby role binding")
	}
	return nil
}
