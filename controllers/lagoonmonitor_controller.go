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
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/handlers"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// LagoonMonitorReconciler reconciles a LagoonBuild object
type LagoonMonitorReconciler struct {
	client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	EnableMQ            bool
	Messaging           *handlers.Messaging
	ControllerNamespace string
	EnableDebug         bool
}

// slice of the different failure states of pods that we care about
// if we observe these on a pending pod, fail the build and get the logs
var failureStates = []string{
	"CrashLoopBackOff",
	"ImagePullBackOff",
}

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonMonitorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)

	var jobPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &jobPod); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	// if this is a lagoon task, then run the handle task monitoring process
	if jobPod.ObjectMeta.Labels["lagoon.sh/jobType"] == "task" {
		if jobPod.ObjectMeta.DeletionTimestamp.IsZero() {
			// pod is not being deleted
			return ctrl.Result{}, r.handleTaskMonitor(ctx, opLog, req, jobPod)
		}
	}
	// if this is a lagoon build, then run the handle build monitoring process
	if jobPod.ObjectMeta.Labels["lagoon.sh/jobType"] == "build" {
		if jobPod.ObjectMeta.DeletionTimestamp.IsZero() {
			// pod is not being deleted
			return ctrl.Result{}, r.handleBuildMonitor(ctx, opLog, req, jobPod)
		}

		// a pod deletion request came through
		// first try and clean up the pod and capture the logs and update
		// the lagoonbuild that owns it with the status
		var lagoonBuild lagoonv1alpha1.LagoonBuild
		err := r.Get(ctx, types.NamespacedName{
			Namespace: jobPod.ObjectMeta.Namespace,
			Name:      jobPod.ObjectMeta.Labels["lagoon.sh/buildName"],
		}, &lagoonBuild)
		if err != nil {
			opLog.Info(fmt.Sprintf("The build that started this pod may have been deleted or not started yet, continuing with cancellation if required."))
			err = r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, true)
			if err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update the LagoonBuild."))
			}
		} else {
			opLog.Info(fmt.Sprintf("Attempting to update the LagoonBuild with cancellation if required."))
			// this will update the deployment back to lagoon if it can do so
			// and should only update if the LagoonBuild is Pending or Running
			err = r.updateDeploymentWithLogs(ctx, req, lagoonBuild, jobPod, true)
			if err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update the LagoonBuild."))
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
		opLog.Info(fmt.Sprintf("Checking for any pending builds."))
		pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
		runningBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(req.Namespace),
			client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Running"}),
		})
		// list all builds in the namespace that have the running buildstatus
		if err := r.List(ctx, runningBuilds, listOption); err != nil {
			return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
		}
		// if we have no running builds, then check for any pending builds
		if len(runningBuilds.Items) == 0 {
			listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Pending"}),
			})
			if err := r.List(ctx, pendingBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			opLog.Info(fmt.Sprintf("There are %v Pending builds", len(pendingBuilds.Items)))
			// if we have any pending builds, then grab the first one from the items as they are sorted by oldest pending first
			if len(pendingBuilds.Items) > 0 {
				// sort the pending builds by creation timestamp
				sort.Slice(pendingBuilds.Items, func(i, j int) bool {
					return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.Before(&pendingBuilds.Items[j].ObjectMeta.CreationTimestamp)
				})
				opLog.Info(fmt.Sprintf("Next build is: %s", pendingBuilds.Items[0].ObjectMeta.Name))
				pendingBuild := pendingBuilds.Items[0].DeepCopy()
				pendingBuild.Labels["lagoon.sh/buildStatus"] = "Running"
				if err := r.Update(ctx, pendingBuild); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch Pods with an event filter that contains our build label
func (r *LagoonMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(PodPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

// getContainerLogs grabs the logs from a given container
func getContainerLogs(containerName string, request ctrl.Request) ([]byte, error) {
	restCfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create client: %v", err)
	}
	req := clientset.CoreV1().Pods(request.Namespace).GetLogs(request.Name, &corev1.PodLogOptions{Container: containerName})
	podLogs, err := req.Stream()
	if err != nil {
		return nil, fmt.Errorf("error in opening stream: %v", err)
	}
	defer podLogs.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return nil, fmt.Errorf("error in copy information from podLogs to buffer: %v", err)
	}
	return buf.Bytes(), nil
}
