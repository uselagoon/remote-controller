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
	"regexp"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
)

// LagoonTaskReconciler reconciles a LagoonTask object
type LagoonTaskReconciler struct {
	client.Client
	Log                    logr.Logger
	Scheme                 *runtime.Scheme
	ControllerNamespace    string
	NamespacePrefix        string
	RandomNamespacePrefix  bool
	LagoonAPIConfiguration LagoonAPIConfiguration
	EnableDebug            bool
	LagoonTargetName       string
	ProxyConfig            ProxyConfig
}

// LagoonAPIConfiguration is for the settings for task API/SSH host/ports
type LagoonAPIConfiguration struct {
	APIHost string
	SSHHost string
	SSHPort string
}

var (
	taskFinalizer = "finalizer.lagoontask.crd.lagoon.sh/v1beta1"
)

// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoontasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.lagoon.sh,resources=lagoontasks/status,verbs=get;update;patch

// Reconcile runs when a request comes through
func (r *LagoonTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("lagoontask", req.NamespacedName)

	// your logic here
	var lagoonTask lagoonv1beta1.LagoonTask
	if err := r.Get(ctx, req.NamespacedName, &lagoonTask); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonTask.ObjectMeta.DeletionTimestamp.IsZero() {
		// check if the task that has been recieved is a standard or advanced task
		if lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] == string(lagoonv1beta1.TaskStatusPending) &&
			lagoonTask.ObjectMeta.Labels["lagoon.sh/taskType"] == string(lagoonv1beta1.TaskTypeStandard) {
			return ctrl.Result{}, r.createStandardTask(ctx, &lagoonTask, opLog)
		}
		if lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] == string(lagoonv1beta1.TaskStatusPending) &&
			lagoonTask.ObjectMeta.Labels["lagoon.sh/taskType"] == string(lagoonv1beta1.TaskTypeAdvanced) {
			return ctrl.Result{}, r.createAdvancedTask(ctx, &lagoonTask, opLog)
		}
	} else {
		// The object is being deleted
		if helpers.ContainsString(lagoonTask.ObjectMeta.Finalizers, taskFinalizer) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&lagoonTask, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}
			// remove our finalizer from the list and update it.
			lagoonTask.ObjectMeta.Finalizers = helpers.RemoveString(lagoonTask.ObjectMeta.Finalizers, taskFinalizer)
			// use patches to avoid update errors
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"finalizers": lagoonTask.ObjectMeta.Finalizers,
				},
			})
			if err := r.Patch(ctx, &lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
func (r *LagoonTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lagoonv1beta1.LagoonTask{}).
		WithEventFilter(TaskPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *LagoonTaskReconciler) deleteExternalResources(lagoonTask *lagoonv1beta1.LagoonTask, namespace string) error {
	// delete any external resources if required
	return nil
}

// get the task pod information for kubernetes
func (r *LagoonTaskReconciler) getTaskPodDeployment(ctx context.Context, lagoonTask *lagoonv1beta1.LagoonTask) (*corev1.Pod, error) {
	deployments := &appsv1.DeploymentList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonTask.ObjectMeta.Namespace),
	})
	err := r.List(ctx, deployments, listOption)
	if err != nil {
		return nil, fmt.Errorf(
			"Unable to get deployments for project %s, environment %s: %v",
			lagoonTask.Spec.Project.Name,
			lagoonTask.Spec.Environment.Name,
			err,
		)
	}
	if len(deployments.Items) > 0 {
		hasService := false
		for _, dep := range deployments.Items {
			// grab the deployment that contains the task service we want to use
			if dep.ObjectMeta.Name == lagoonTask.Spec.Task.Service {
				hasService = true
				// grab the container
				for idx, depCon := range dep.Spec.Template.Spec.Containers {
					// --- deprecate these at some point in favor of the `LAGOON_CONFIG_X` variants
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "TASK_API_HOST",
						Value: r.getTaskValue(lagoonTask, "TASK_API_HOST"),
					})
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "TASK_SSH_HOST",
						Value: r.getTaskValue(lagoonTask, "TASK_SSH_HOST"),
					})
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "TASK_SSH_PORT",
						Value: r.getTaskValue(lagoonTask, "TASK_SSH_PORT"),
					})
					// ^^ ---
					// add the API and SSH endpoint configuration to environments
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "LAGOON_CONFIG_API_HOST",
						Value: r.getTaskValue(lagoonTask, "LAGOON_CONFIG_API_HOST"),
					})
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "LAGOON_CONFIG_SSH_HOST",
						Value: r.getTaskValue(lagoonTask, "LAGOON_CONFIG_SSH_HOST"),
					})
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "LAGOON_CONFIG_SSH_PORT",
						Value: r.getTaskValue(lagoonTask, "LAGOON_CONFIG_SSH_PORT"),
					})
					// in the future, the SSH_HOST and SSH_PORT could also have regional variants
					dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
						Name:  "TASK_DATA_ID",
						Value: lagoonTask.Spec.Task.ID,
					})
					// add proxy variables to builds if they are defined
					if r.ProxyConfig.HTTPProxy != "" {
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "HTTP_PROXY",
							Value: r.ProxyConfig.HTTPProxy,
						})
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "http_proxy",
							Value: r.ProxyConfig.HTTPProxy,
						})
					}
					if r.ProxyConfig.HTTPSProxy != "" {
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "HTTPS_PROXY",
							Value: r.ProxyConfig.HTTPSProxy,
						})
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "https_proxy",
							Value: r.ProxyConfig.HTTPSProxy,
						})
					}
					if r.ProxyConfig.NoProxy != "" {
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "NO_PROXY",
							Value: r.ProxyConfig.NoProxy,
						})
						dep.Spec.Template.Spec.Containers[idx].Env = append(dep.Spec.Template.Spec.Containers[idx].Env, corev1.EnvVar{
							Name:  "no_proxy",
							Value: r.ProxyConfig.NoProxy,
						})
					}
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
					taskPod := &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      lagoonTask.ObjectMeta.Name,
							Namespace: lagoonTask.ObjectMeta.Namespace,
							Labels: map[string]string{
								"lagoon.sh/jobType":    "task",
								"lagoon.sh/taskName":   lagoonTask.ObjectMeta.Name,
								"lagoon.sh/crdVersion": crdVersion,
								"lagoon.sh/controller": r.ControllerNamespace,
							},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion: fmt.Sprintf("%v", lagoonv1beta1.GroupVersion),
									Kind:       "LagoonTask",
									Name:       lagoonTask.ObjectMeta.Name,
									UID:        lagoonTask.UID,
								},
							},
						},
						Spec: dep.Spec.Template.Spec,
					}
					return taskPod, nil
				}
			}
		}
		if !hasService {
			return nil, fmt.Errorf(
				"No matching service %s for project %s, environment %s: %v",
				lagoonTask.Spec.Task.Service,
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Environment.Name,
				err,
			)
		}
	}
	// no deployments found return error
	return nil, fmt.Errorf(
		"No deployments %s for project %s, environment %s: %v",
		lagoonTask.ObjectMeta.Namespace,
		lagoonTask.Spec.Project.Name,
		lagoonTask.Spec.Environment.Name,
		err,
	)
}

func (r *LagoonTaskReconciler) createStandardTask(ctx context.Context, lagoonTask *lagoonv1beta1.LagoonTask, opLog logr.Logger) error {
	newTaskPod := &corev1.Pod{}
	var err error

	newTaskPod, err = r.getTaskPodDeployment(ctx, lagoonTask)
	if err != nil {
		opLog.Info(fmt.Sprintf("%v", err))
		//@TODO: send msg back and update task to failed?
		return nil
	}
	opLog.Info(fmt.Sprintf("Checking task pod for: %s", lagoonTask.ObjectMeta.Name))
	// once the pod spec has been defined, check if it isn't already created
	err = r.Get(ctx, types.NamespacedName{
		Namespace: lagoonTask.ObjectMeta.Namespace,
		Name:      newTaskPod.ObjectMeta.Name,
	}, newTaskPod)
	if err != nil {
		// if it doesn't exist, then create the task pod
		opLog.Info(fmt.Sprintf("Creating task pod for: %s", lagoonTask.ObjectMeta.Name))
		// create the task pod
		if err := r.Create(ctx, newTaskPod); err != nil {
			opLog.Info(
				fmt.Sprintf(
					"Unable to create task pod for project %s, environment %s: %v",
					lagoonTask.Spec.Project.Name,
					lagoonTask.Spec.Environment.Name,
					err,
				),
			)
			//@TODO: send msg back and update task to failed?
			return nil
		}
		taskRunningStatus.With(prometheus.Labels{
			"task_namespace": lagoonTask.ObjectMeta.Namespace,
			"task_name":      lagoonTask.ObjectMeta.Name,
		}).Set(1)
		tasksStartedCounter.Inc()
	} else {
		opLog.Info(fmt.Sprintf("Task pod already running for: %s", lagoonTask.ObjectMeta.Name))
	}
	// The object is not being deleted, so if it does not have our finalizer,
	// then lets add the finalizer and update the object. This is equivalent
	// registering our finalizer.
	if !helpers.ContainsString(lagoonTask.ObjectMeta.Finalizers, taskFinalizer) {
		lagoonTask.ObjectMeta.Finalizers = append(lagoonTask.ObjectMeta.Finalizers, taskFinalizer)
		// use patches to avoid update errors
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"finalizers": lagoonTask.ObjectMeta.Finalizers,
			},
		})
		if err := r.Patch(ctx, lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return err
		}
	}
	return nil
}

// createAdvancedTask allows running of more advanced tasks than the standard lagoon tasks
// see notes in the docs for infomration about advanced tasks
func (r *LagoonTaskReconciler) createAdvancedTask(ctx context.Context, lagoonTask *lagoonv1beta1.LagoonTask, opLog logr.Logger) error {
	// handle the volumes for sshkey
	sshKeyVolume := corev1.Volume{
		Name: "lagoon-sshkey",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName:  "lagoon-sshkey",
				DefaultMode: helpers.IntPtr(420),
			},
		},
	}
	sshKeyVolumeMount := corev1.VolumeMount{
		Name:      "lagoon-sshkey",
		ReadOnly:  true,
		MountPath: "/var/run/secrets/lagoon/ssh",
	}
	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}
	if lagoonTask.Spec.AdvancedTask.SSHKey {
		volumes = append(volumes, sshKeyVolume)
		volumeMounts = append(volumeMounts, sshKeyVolumeMount)
	}
	if lagoonTask.Spec.AdvancedTask.DeployerToken {
		// if this advanced task can access kubernetes, mount the token in
		serviceAccount := &corev1.ServiceAccount{}
		err := r.getServiceAccount(ctx, serviceAccount, lagoonTask.ObjectMeta.Namespace)
		if err != nil {
			return err
		}
		var serviceaccountTokenSecret string
		for _, secret := range serviceAccount.Secrets {
			match, _ := regexp.MatchString("^lagoon-deployer-token", secret.Name)
			if match {
				serviceaccountTokenSecret = secret.Name
				break
			}
		}
		// if the existing token exists, mount it
		if serviceaccountTokenSecret != "" {
			volumes = append(volumes, corev1.Volume{
				Name: serviceaccountTokenSecret,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  serviceaccountTokenSecret,
						DefaultMode: helpers.IntPtr(420),
					},
				},
			})
			// legacy tokens are mounted /var/run/secrets/lagoon/deployer
			// new tokens using volume projection are mounted /var/run/secrets/kubernetes.io/serviceaccount/token
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      serviceaccountTokenSecret,
				ReadOnly:  true,
				MountPath: "/var/run/secrets/lagoon/deployer",
			})
		}
	}
	opLog.Info(fmt.Sprintf("Checking advanced task pod for: %s", lagoonTask.ObjectMeta.Name))
	// once the pod spec has been defined, check if it isn't already created

	newPod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: lagoonTask.ObjectMeta.Namespace,
		Name:      lagoonTask.ObjectMeta.Name,
	}, newPod)
	if err != nil {
		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      lagoonTask.ObjectMeta.Name,
				Namespace: lagoonTask.ObjectMeta.Namespace,
				Labels: map[string]string{
					"lagoon.sh/jobType":    "task",
					"lagoon.sh/taskName":   lagoonTask.ObjectMeta.Name,
					"lagoon.sh/crdVersion": crdVersion,
					"lagoon.sh/controller": r.ControllerNamespace,
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: fmt.Sprintf("%v", lagoonv1beta1.GroupVersion),
						Kind:       "LagoonTask",
						Name:       lagoonTask.ObjectMeta.Name,
						UID:        lagoonTask.UID,
					},
				},
			},
			Spec: corev1.PodSpec{
				RestartPolicy: "Never",
				Volumes:       volumes,
				Containers: []corev1.Container{
					{
						Name:            "lagoon-task",
						Image:           lagoonTask.Spec.AdvancedTask.RunnerImage,
						ImagePullPolicy: "Always",
						EnvFrom: []corev1.EnvFromSource{
							{
								ConfigMapRef: &corev1.ConfigMapEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "lagoon-env",
									},
								},
							},
						},
						Env: []corev1.EnvVar{
							{
								Name:  "JSON_PAYLOAD",
								Value: string(lagoonTask.Spec.AdvancedTask.JSONPayload),
							},
							{
								Name:  "NAMESPACE",
								Value: lagoonTask.ObjectMeta.Namespace,
							},
							{
								Name:  "PODNAME",
								Value: lagoonTask.ObjectMeta.Name,
							},
							{
								Name:  "LAGOON_PROJECT",
								Value: lagoonTask.Spec.Project.Name,
							},
							{
								Name:  "LAGOON_GIT_BRANCH",
								Value: lagoonTask.Spec.Environment.Name,
							},
							{
								Name:  "TASK_API_HOST",
								Value: r.getTaskValue(lagoonTask, "TASK_API_HOST"),
							},
							{
								Name:  "TASK_SSH_HOST",
								Value: r.getTaskValue(lagoonTask, "TASK_SSH_HOST"),
							},
							{
								Name:  "TASK_SSH_PORT",
								Value: r.getTaskValue(lagoonTask, "TASK_SSH_PORT"),
							},
							{
								Name:  "TASK_DATA_ID",
								Value: lagoonTask.Spec.Task.ID,
							},
						},
						VolumeMounts: volumeMounts,
					},
				},
			},
		}
		if lagoonTask.Spec.AdvancedTask.DeployerToken {
			// start this with the serviceaccount so that it gets the token mounted into it
			newPod.Spec.ServiceAccountName = "lagoon-deployer"
		}
		opLog.Info(fmt.Sprintf("Creating advanced task pod for: %s", lagoonTask.ObjectMeta.Name))
		if err := r.Create(ctx, newPod); err != nil {
			opLog.Info(
				fmt.Sprintf(
					"Unable to create advanced task pod for project %s, environment %s: %v",
					lagoonTask.Spec.Project.Name,
					lagoonTask.Spec.Environment.Name,
					err,
				),
			)
			return err
		}
		taskRunningStatus.With(prometheus.Labels{
			"task_namespace": lagoonTask.ObjectMeta.Namespace,
			"task_name":      lagoonTask.ObjectMeta.Name,
		}).Set(1)
		tasksStartedCounter.Inc()
	} else {
		opLog.Info(fmt.Sprintf("Advanced task pod already running for: %s", lagoonTask.ObjectMeta.Name))
	}
	return nil
}

// check for the API/SSH settings in the task spec first, if nothing there use the one from our settings.
func (r *LagoonTaskReconciler) getTaskValue(lagoonTask *lagoonv1beta1.LagoonTask, value string) string {
	switch value {
	case "TASK_API_HOST":
		if lagoonTask.Spec.Task.APIHost == "" {
			return r.LagoonAPIConfiguration.APIHost
		}
		return lagoonTask.Spec.Task.APIHost
	case "TASK_SSH_HOST":
		if lagoonTask.Spec.Task.SSHHost == "" {
			return r.LagoonAPIConfiguration.SSHHost
		}
		return lagoonTask.Spec.Task.SSHHost
	case "TASK_SSH_PORT":
		if lagoonTask.Spec.Task.SSHPort == "" {
			return r.LagoonAPIConfiguration.SSHPort
		}
		return lagoonTask.Spec.Task.SSHPort
	case "LAGOON_CONFIG_API_HOST":
		if lagoonTask.Spec.Task.APIHost == "" {
			return r.LagoonAPIConfiguration.APIHost
		}
		return lagoonTask.Spec.Task.APIHost
	case "LAGOON_CONFIG_SSH_HOST":
		if lagoonTask.Spec.Task.SSHHost == "" {
			return r.LagoonAPIConfiguration.SSHHost
		}
		return lagoonTask.Spec.Task.SSHHost
	case "LAGOON_CONFIG_SSH_PORT":
		if lagoonTask.Spec.Task.SSHPort == "" {
			return r.LagoonAPIConfiguration.SSHPort
		}
		return lagoonTask.Spec.Task.SSHPort
	}
	return ""
}

// getServiceAccount will get the service account if it exists
func (r *LagoonTaskReconciler) getServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount, ns string) error {
	serviceAccount.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-deployer",
		Namespace: ns,
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      "lagoon-deployer",
	}, serviceAccount)
	if err != nil {
		return err
	}
	return nil
}
