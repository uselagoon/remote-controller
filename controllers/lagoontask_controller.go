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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
)

// LagoonTaskReconciler reconciles a LagoonTask object
type LagoonTaskReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoontasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoontasks/status,verbs=get;update;patch

// Reconcile runs when a request comes through
func (r *LagoonTaskReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("lagoontask", req.NamespacedName)

	// your logic here
	var lagoonTask lagoonv1alpha1.LagoonTask
	if err := r.Get(ctx, req.NamespacedName, &lagoonTask); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	finalizerName := "finalizer.lagoontask.lagoon.amazee.io/v1alpha1"

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonTask.ObjectMeta.DeletionTimestamp.IsZero() {
		if lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] == "Pending" {
			deployments := &appsv1.DeploymentList{}
			listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(lagoonTask.Spec.Environment.OpenshiftProjectName),
			})
			err := r.List(ctx, deployments, listOption)
			if err != nil {
				opLog.Info(
					fmt.Sprintf(
						"Unable to get deployments for project %s, environment %s: %v",
						lagoonTask.Spec.Project.Name,
						lagoonTask.Spec.Environment.Name,
						err,
					),
				)
				//@TODO: send msg back and update task to failed?
				return ctrl.Result{}, nil
			}
			if len(deployments.Items) > 0 {
				hasService := false
				for _, dep := range deployments.Items {
					// grab the deployment that contains the task service we want to use
					if dep.ObjectMeta.Name == lagoonTask.Spec.Task.Service {
						hasService = true
						// grab the container
						for idx, depCon := range dep.Spec.Template.Spec.Containers {
							dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
								Name:  "TASK_API_HOST",
								Value: lagoonTask.Spec.Task.APIHost,
							})
							dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
								Name:  "TASK_SSH_HOST",
								Value: lagoonTask.Spec.Task.SSHHost,
							})
							dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
								Name:  "TASK_SSH_PORT",
								Value: lagoonTask.Spec.Task.SSHPort,
							})
							dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
								Name:  "TASK_DATA_ID",
								Value: lagoonTask.Spec.Task.ID,
							})
							for idx2, env := range depCon.Env {
								// remove any cronjobs from the envvars
								if env.Name == "CRONJOBS" {
									// Shift left one index.
									copy(dep.Spec.Template.Spec.Containers[idx].Env[idx2:], dep.Spec.Template.Spec.Containers[idx].Env[idx2+1:])
									// Erase last element (write zero value).
									dep.Spec.Template.Spec.Containers[idx].Env[len(dep.Spec.Template.Spec.Containers[idx].Env)-1] = corev1.EnvVar{}
									dep.Spec.Template.Spec.Containers[idx].Env = dep.Spec.Template.Spec.Containers[idx].Env[:len(dep.Spec.Template.Spec.Containers[idx].Env)-1]
								}
							}
							dep.Spec.Template.Spec.Containers[idx].Command = []string{"/sbin/tini",
								"--",
								"/lagoon/entrypoints.sh",
								"/bin/sh",
								"-c",
								lagoonTask.Spec.Task.Command,
							}
							dep.Spec.Template.Spec.RestartPolicy = "Never"
							newPod := &corev1.Pod{
								ObjectMeta: metav1.ObjectMeta{
									Name:      lagoonTask.ObjectMeta.Name,
									Namespace: lagoonTask.ObjectMeta.Namespace,
									Labels: map[string]string{
										"lagoon.sh/jobType":  "task",
										"lagoon.sh/taskName": lagoonTask.ObjectMeta.Name,
									},
									OwnerReferences: []metav1.OwnerReference{
										{
											APIVersion: fmt.Sprintf("%v", lagoonv1alpha1.GroupVersion),
											Kind:       "LagoonTask",
											Name:       lagoonTask.ObjectMeta.Name,
											UID:        lagoonTask.UID,
										},
									},
								},
								Spec: dep.Spec.Template.Spec,
							}
							opLog.Info(fmt.Sprintf("Checking task pod for: %s", lagoonTask.ObjectMeta.Name))
							// once the pod spec has been defined, check if it isn't already created
							err = r.Get(ctx, types.NamespacedName{
								Namespace: lagoonTask.ObjectMeta.Namespace,
								Name:      newPod.ObjectMeta.Name,
							}, newPod)
							if err != nil {
								// if it doesn't exist, then create the build pod
								opLog.Info(fmt.Sprintf("Creating task pod for: %s", lagoonTask.ObjectMeta.Name))
								if err := r.Create(ctx, newPod); err != nil {
									opLog.Info(
										fmt.Sprintf(
											"Unable to create task pod for project %s, environment %s: %v",
											lagoonTask.Spec.Project.Name,
											lagoonTask.Spec.Environment.Name,
											err,
										),
									)
									//@TODO: send msg back and update task to failed?
									return ctrl.Result{}, nil
								}
								// then break out of the build
								break
							} else {
								opLog.Info(fmt.Sprintf("Task pod already running for: %s", lagoonTask.ObjectMeta.Name))
							}
						}
					}
				}
				if !hasService {
					opLog.Info(
						fmt.Sprintf(
							"No matching service %s for project %s, environment %s: %v",
							lagoonTask.Spec.Task.Service,
							lagoonTask.Spec.Project.Name,
							lagoonTask.Spec.Environment.Name,
							err,
						),
					)
					//@TODO: send msg back and update task to failed?
					return ctrl.Result{}, nil
				}
			} else {
				opLog.Info(
					fmt.Sprintf(
						"No deployments %s for project %s, environment %s: %v",
						lagoonTask.Spec.Environment.OpenshiftProjectName,
						lagoonTask.Spec.Project.Name,
						lagoonTask.Spec.Environment.Name,
						err,
					),
				)
			}
			// The object is not being deleted, so if it does not have our finalizer,
			// then lets add the finalizer and update the object. This is equivalent
			// registering our finalizer.
			if !containsString(lagoonTask.ObjectMeta.Finalizers, finalizerName) {
				lagoonTask.ObjectMeta.Finalizers = append(lagoonTask.ObjectMeta.Finalizers, finalizerName)
				// use patches to avoid update errors
				mergePatch, _ := json.Marshal(map[string]interface{}{
					"metadata": map[string]interface{}{
						"finalizers": lagoonTask.ObjectMeta.Finalizers,
					},
				})
				if err := r.Patch(ctx, &lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	} else {
		// The object is being deleted
		if containsString(lagoonTask.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&lagoonTask, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}
			// remove our finalizer from the list and update it.
			lagoonTask.ObjectMeta.Finalizers = removeString(lagoonTask.ObjectMeta.Finalizers, finalizerName)
			// use patches to avoid update errors
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"finalizers": lagoonTask.ObjectMeta.Finalizers,
				},
			})
			if err := r.Patch(ctx, &lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
func (r *LagoonTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lagoonv1alpha1.LagoonTask{}).
		Complete(r)
}

func (r *LagoonTaskReconciler) deleteExternalResources(lagoonTask *lagoonv1alpha1.LagoonTask, namespace string) error {
	// delete any external resources if required
	return nil
}
