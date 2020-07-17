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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
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
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete

func (r *LagoonMonitorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)

	var buildPod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &buildPod); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	if buildPod.ObjectMeta.DeletionTimestamp.IsZero() {
		if buildPod.Status.Phase == corev1.PodPending {
			opLog.Info(fmt.Sprintf("Build %s is %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
			for _, container := range buildPod.Status.ContainerStatuses {
				if container.Name == "lagoon-build" {
					// check if the state of the pod is one of our failure states
					if container.State.Waiting != nil && containsString(failureStates, container.State.Waiting.Reason) {
						opLog.Info(fmt.Sprintf("Build failed, container exit reason was: %v", container.State.Waiting.Reason))
						var lagoonBuild lagoonv1alpha1.LagoonBuild
						err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: buildPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
						if err != nil {
							return ctrl.Result{}, err
						}
						lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.BuildFailed)
						if err := r.Update(ctx, &lagoonBuild); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Marked build %s as %s", lagoonBuild.ObjectMeta.Name, string(lagoonv1alpha1.BuildFailed)))
						if err := r.Delete(ctx, &buildPod); err != nil {
							return ctrl.Result{}, err
						}
						opLog.Info(fmt.Sprintf("Deleted failed build pod: %s", buildPod.ObjectMeta.Name))
						r.updateStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
							Type:   lagoonv1alpha1.BuildFailed,
							Status: corev1.ConditionTrue,
						}, []byte(container.State.Waiting.Message))
						return ctrl.Result{}, nil
					}
				}
			}
			return ctrl.Result{}, nil
		}
		if buildPod.Status.Phase == corev1.PodFailed || buildPod.Status.Phase == corev1.PodSucceeded {
			var lagoonBuild lagoonv1alpha1.LagoonBuild
			err := r.Get(ctx, types.NamespacedName{Namespace: buildPod.ObjectMeta.Namespace, Name: buildPod.ObjectMeta.Labels["lagoon.sh/buildName"]}, &lagoonBuild)
			if err != nil {
				return ctrl.Result{}, err
			}
			var condition lagoonv1alpha1.BuildConditionType
			switch buildPod.Status.Phase {
			case corev1.PodFailed:
				condition = lagoonv1alpha1.BuildFailed
			case corev1.PodSucceeded:
				condition = lagoonv1alpha1.BuildComplete
			}
			if lagoonBuild.Labels["lagoon.sh/buildStatus"] != string(condition) {
				opLog.Info(fmt.Sprintf("Build %s %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
				var allLogs []byte
				for _, container := range buildPod.Spec.Containers {
					cLogs, err := getPodLogs(container.Name, req)
					if err != nil {
						opLog.Info(fmt.Sprintf("LogsErr: %v", err))
						return ctrl.Result{}, nil
					}
					allLogs = append(allLogs, cLogs...)
				}
				lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(condition)
				if err := r.Update(ctx, &lagoonBuild); err != nil {
					return ctrl.Result{}, err
				}
				r.updateStatusCondition(ctx, &lagoonBuild, lagoonv1alpha1.LagoonBuildConditions{
					Type:   condition,
					Status: corev1.ConditionTrue,
				}, allLogs)
			}
			return ctrl.Result{}, nil
		}
		opLog.Info(fmt.Sprintf("Build %s is %v", buildPod.ObjectMeta.Labels["lagoon.sh/buildName"], buildPod.Status.Phase))
	} else {
		// if any pods are removed, run a pending build check
		opLog.Info(fmt.Sprintf("Checking for any pending builds"))
		pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
		readyBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(req.Namespace),
			client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Running"}),
		})
		if err := r.List(ctx, readyBuilds, listOption); err != nil {
			return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
		}
		if len(readyBuilds.Items) == 0 {
			listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Pending"}),
			})
			if err := r.List(ctx, pendingBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			// sort the pending builds by creation timestamp
			sort.Slice(pendingBuilds.Items, func(i, j int) bool {
				return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.Before(&pendingBuilds.Items[j].ObjectMeta.CreationTimestamp)
			})
			opLog.Info(fmt.Sprintf("There are %v Pending builds", len(pendingBuilds.Items)))
			if len(pendingBuilds.Items) > 0 {
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

func (r *LagoonMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(PodPredicates{}).
		Complete(r)
}

func getPodLogs(containerName string, request ctrl.Request) ([]byte, error) {
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

func (r *LagoonMonitorReconciler) updateStatusCondition(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild, condition lagoonv1alpha1.LagoonBuildConditions, log []byte) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !buildContainsStatus(lagoonBuild.Status.Conditions, condition) {
		lagoonBuild.Status.Conditions = append(lagoonBuild.Status.Conditions, condition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": lagoonBuild.Status.Conditions,
				"log":        log,
			},
		})
		if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}
