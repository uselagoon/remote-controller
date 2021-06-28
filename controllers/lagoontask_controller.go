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
	"regexp"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	// openshift
	oappsv1 "github.com/openshift/api/apps/v1"
)

// LagoonTaskReconciler reconciles a LagoonTask object
type LagoonTaskReconciler struct {
	client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	IsOpenshift         bool
	ControllerNamespace string
	TaskSettings        LagoonTaskSettings
	EnableDebug         bool
}

// LagoonTaskSettings is for the settings for task API/SSH host/ports
type LagoonTaskSettings struct {
	APIHost string
	SSHHost string
	SSHPort string
}

var (
	finalizerName = "finalizer.lagoontask.lagoon.amazee.io/v1alpha1"
)

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

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonTask.ObjectMeta.DeletionTimestamp.IsZero() {
		// check if the task that has been recieved is a standard or advanced task
		if lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] == "Pending" && lagoonTask.ObjectMeta.Labels["lagoon.sh/taskType"] == "standard" {
			return ctrl.Result{}, r.createStandardTask(ctx, &lagoonTask, opLog)
		}
		if lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] == "Pending" && lagoonTask.ObjectMeta.Labels["lagoon.sh/taskType"] == "advanced" {
			return ctrl.Result{}, r.createAdvancedTask(ctx, &lagoonTask, opLog)
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
		WithEventFilter(TaskPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}

func (r *LagoonTaskReconciler) deleteExternalResources(lagoonTask *lagoonv1alpha1.LagoonTask, namespace string) error {
	// delete any external resources if required
	return nil
}

// get the task pod information for openshift
func (r *LagoonTaskReconciler) getTaskPodDeploymentConfig(ctx context.Context, taskPod *corev1.Pod, lagoonTask *lagoonv1alpha1.LagoonTask) error {
	deployments := &oappsv1.DeploymentConfigList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonTask.Spec.Environment.OpenshiftProjectName),
	})
	err := r.List(ctx, deployments, listOption)
	if err != nil {
		return fmt.Errorf(
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
					taskPod = &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      lagoonTask.ObjectMeta.Name,
							Namespace: lagoonTask.ObjectMeta.Namespace,
							Labels: map[string]string{
								"lagoon.sh/jobType":    "task",
								"lagoon.sh/taskName":   lagoonTask.ObjectMeta.Name,
								"lagoon.sh/controller": r.ControllerNamespace,
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
					return nil
				}
			}
		}
		if !hasService {
			return fmt.Errorf(
				"No matching service %s for project %s, environment %s: %v",
				lagoonTask.Spec.Task.Service,
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Environment.Name,
				err,
			)
		}
	}
	// no deployments found return error
	return fmt.Errorf(
		"No deployments %s for project %s, environment %s: %v",
		lagoonTask.Spec.Environment.OpenshiftProjectName,
		lagoonTask.Spec.Project.Name,
		lagoonTask.Spec.Environment.Name,
		err,
	)
}

// get the task pod information for kubernetes
func (r *LagoonTaskReconciler) getTaskPodDeployment(ctx context.Context, taskPod *corev1.Pod, lagoonTask *lagoonv1alpha1.LagoonTask) error {
	deployments := &appsv1.DeploymentList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(lagoonTask.Spec.Environment.OpenshiftProjectName),
	})
	err := r.List(ctx, deployments, listOption)
	if err != nil {
		return fmt.Errorf(
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
					taskPod = &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      lagoonTask.ObjectMeta.Name,
							Namespace: lagoonTask.ObjectMeta.Namespace,
							Labels: map[string]string{
								"lagoon.sh/jobType":    "task",
								"lagoon.sh/taskName":   lagoonTask.ObjectMeta.Name,
								"lagoon.sh/controller": r.ControllerNamespace,
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
					return nil
				}
			}
		}
		if !hasService {
			return fmt.Errorf(
				"No matching service %s for project %s, environment %s: %v",
				lagoonTask.Spec.Task.Service,
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Environment.Name,
				err,
			)
		}
	}
	// no deployments found return error
	return fmt.Errorf(
		"No deployments %s for project %s, environment %s: %v",
		lagoonTask.Spec.Environment.OpenshiftProjectName,
		lagoonTask.Spec.Project.Name,
		lagoonTask.Spec.Environment.Name,
		err,
	)
}

func (r *LagoonTaskReconciler) createStandardTask(ctx context.Context, lagoonTask *lagoonv1alpha1.LagoonTask, opLog logr.Logger) error {
	newTaskPod := &corev1.Pod{}
	// get the podspec from openshift or kubernetes, then get or create a new pod to run the task in
	if r.IsOpenshift {
		err := r.getTaskPodDeploymentConfig(ctx, newTaskPod, lagoonTask)
		if err != nil {
			opLog.Info(fmt.Sprintf("%v", err))
			//@TODO: send msg back and update task to failed?
			return nil
		}
	} else {
		err := r.getTaskPodDeployment(ctx, newTaskPod, lagoonTask)
		if err != nil {
			opLog.Info(fmt.Sprintf("%v", err))
			//@TODO: send msg back and update task to failed?
			return nil
		}
	}
	opLog.Info(fmt.Sprintf("Checking task pod for: %s", lagoonTask.ObjectMeta.Name))
	// once the pod spec has been defined, check if it isn't already created
	err := r.Get(ctx, types.NamespacedName{
		Namespace: lagoonTask.ObjectMeta.Namespace,
		Name:      newTaskPod.ObjectMeta.Name,
	}, newTaskPod)
	if err != nil {
		// if it doesn't exist, then create the task pod
		token, err := r.getDeployerToken(ctx, lagoonTask)
		if err != nil {
			return fmt.Errorf("Task failed getting the deployer token, error was: %v", err)
		}
		config := &rest.Config{
			BearerToken: token,
			Host:        "https://kubernetes.default.svc",
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: true,
			},
		}
		// create the client using the rest config.
		c, err := client.New(config, client.Options{})
		if err != nil {
			return fmt.Errorf("Task failed creating the client, error was: %v", err)
		}
		opLog.Info(fmt.Sprintf("Creating task pod for: %s", lagoonTask.ObjectMeta.Name))
		// use the client that was created with the deployer-token to create the task pod
		if err := c.Create(ctx, newTaskPod); err != nil {
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
	} else {
		opLog.Info(fmt.Sprintf("Task pod already running for: %s", lagoonTask.ObjectMeta.Name))
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
		if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return err
		}
	}
	return nil
}

// createAdvancedTask allows running of more advanced tasks than the standard lagoon tasks
// see notes in the docs for infomration about advanced tasks
func (r *LagoonTaskReconciler) createAdvancedTask(ctx context.Context, lagoonTask *lagoonv1alpha1.LagoonTask, opLog logr.Logger) error {
	serviceAccount := &corev1.ServiceAccount{}
	// get the service account from the namespace, this can be used by services in the custom task to perform work in kubernetes
	err := r.getServiceAccount(ctx, serviceAccount, lagoonTask.Spec.Environment.OpenshiftProjectName)
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
	if serviceaccountTokenSecret == "" {
		return fmt.Errorf("Could not find token secret for ServiceAccount lagoon-deployer")
	}
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lagoonTask.ObjectMeta.Name,
			Namespace: lagoonTask.Spec.Environment.OpenshiftProjectName,
			Labels: map[string]string{
				"lagoon.sh/jobType":    "task",
				"lagoon.sh/taskName":   lagoonTask.ObjectMeta.Name,
				"lagoon.sh/controller": r.ControllerNamespace,
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
		Spec: corev1.PodSpec{
			RestartPolicy: "Never",
			Volumes: []corev1.Volume{
				{
					Name: serviceaccountTokenSecret,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  serviceaccountTokenSecret,
							DefaultMode: intPtr(420),
						},
					},
				},
				{
					Name: "lagoon-sshkey",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  "lagoon-sshkey",
							DefaultMode: intPtr(420),
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "lagoon-task",
					Image:           lagoonTask.Spec.AdvancedTask.RunnerImage,
					ImagePullPolicy: "Always",
					Env: []corev1.EnvVar{
						{
							Name:  "JSON_PAYLOAD",
							Value: string(lagoonTask.Spec.AdvancedTask.JSONPayload),
						},
						{
							Name:  "NAMESPACE",
							Value: lagoonTask.Spec.Environment.OpenshiftProjectName,
						},
						{
							Name:  "PODNAME",
							Value: lagoonTask.ObjectMeta.Name,
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
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      serviceaccountTokenSecret,
							ReadOnly:  true,
							MountPath: "/var/run/secrets/lagoon/deployer",
						},
						{
							Name:      "lagoon-sshkey",
							ReadOnly:  true,
							MountPath: "/var/run/secrets/lagoon/ssh",
						},
					},
				},
			},
		},
	}
	token, err := r.getDeployerToken(ctx, lagoonTask)
	if err != nil {
		return fmt.Errorf("Task failed getting the deployer token, error was: %v", err)
	}
	config := &rest.Config{
		BearerToken: token,
		Host:        "https://kubernetes.default.svc",
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}
	// create the client using the rest config.
	c, err := client.New(config, client.Options{})
	if err != nil {
		return fmt.Errorf("Task failed creating the client, error was: %v", err)
	}
	// use the client that was created with the deployer-token to create the task pod
	if err := c.Create(ctx, newPod); err != nil {
		return err
	}
	return nil
}

// check for the API/SSH settings in the task spec first, if nothing there use the one from our settings.
func (r *LagoonTaskReconciler) getTaskValue(lagoonTask *lagoonv1alpha1.LagoonTask, value string) string {
	switch value {
	case "TASK_API_HOST":
		if lagoonTask.Spec.Task.APIHost == "" {
			return r.TaskSettings.APIHost
		}
		return lagoonTask.Spec.Task.APIHost
	case "TASK_SSH_HOST":
		if lagoonTask.Spec.Task.SSHHost == "" {
			return r.TaskSettings.SSHHost
		}
		return lagoonTask.Spec.Task.SSHHost
	case "TASK_SSH_PORT":
		if lagoonTask.Spec.Task.SSHPort == "" {
			return r.TaskSettings.SSHPort
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

// getDeployerToken will get the deployer token from the service account if it exists
func (r *LagoonTaskReconciler) getDeployerToken(ctx context.Context, lagoonTask *lagoonv1alpha1.LagoonTask) (string, error) {
	serviceAccount := &corev1.ServiceAccount{}
	// get the service account from the namespace, this can be used by services in the custom task to perform work in kubernetes
	err := r.getServiceAccount(ctx, serviceAccount, lagoonTask.Spec.Environment.OpenshiftProjectName)
	if err != nil {
		return "", err
	}
	var serviceaccountTokenSecret string
	for _, secret := range serviceAccount.Secrets {
		match, _ := regexp.MatchString("^lagoon-deployer-token", secret.Name)
		if match {
			serviceaccountTokenSecret = secret.Name
			break
		}
	}
	if serviceaccountTokenSecret == "" {
		return "", fmt.Errorf("Could not find token secret for ServiceAccount lagoon-deployer")
	}
	saSecret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: lagoonTask.ObjectMeta.Namespace,
		Name:      serviceaccountTokenSecret,
	}, saSecret)
	if err != nil {
		return "", fmt.Errorf("Task failed getting the deployer token, error was: %v", err)
	}
	if string(saSecret.Data["token"]) == "" {
		return "", fmt.Errorf("There is no token field in the deployer token secret")
	}
	return string(saSecret.Data["token"]), nil
}
