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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/handlers"

	"gopkg.in/matryer/try.v1"

	// Openshift
	projectv1 "github.com/openshift/api/project/v1"
)

// LagoonBuildReconciler reconciles a LagoonBuild object
type LagoonBuildReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	EnableMQ    bool
	Messaging   *handlers.Messaging
	BuildImage  string
	IsOpenshift bool
}

// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoonbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lagoon.amazee.io,resources=lagoonbuilds/status,verbs=get;update;patch

// @TODO: all the things for now, review later
// +kubebuilder:rbac:groups="*",resources="*",verbs="*"

// Reconcile runs when a request comes through
func (r *LagoonBuildReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	opLog := r.Log.WithValues("lagoonbuild", req.NamespacedName)

	// your logic here
	var lagoonBuild lagoonv1alpha1.LagoonBuild
	if err := r.Get(ctx, req.NamespacedName, &lagoonBuild); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	finalizerName := "finalizer.lagoonbuild.lagoon.amazee.io/v1alpha1"

	// examine DeletionTimestamp to determine if object is under deletion
	if lagoonBuild.ObjectMeta.DeletionTimestamp.IsZero() {
		// check if we get a lagoonbuild that hasn't got any buildstatus
		// this means it was created by the message queue handler
		// so we should do the steps required for a lagoon build and then copy the build
		// into the created namespace
		if _, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; !ok {
			// Namesapce creation
			namespace := &corev1.Namespace{}
			opLog.Info(fmt.Sprintf("Checking Namespace exists for: %s", lagoonBuild.ObjectMeta.Name))
			err := r.getOrCreateNamespace(ctx, namespace, lagoonBuild.Spec)
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
			return ctrl.Result{}, nil
		}
		// if we do have a `lagoon.sh/buildStatus` set, then process as normal
		runningBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(req.Namespace),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/buildStatus": "Running",
			}),
		})
		// list any builds that are running
		if err := r.List(ctx, runningBuilds, listOption); err != nil {
			return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
		}
		for _, runningBuild := range runningBuilds.Items {
			// if the running build is the one from this request then process it
			if lagoonBuild.ObjectMeta.Name == runningBuild.ObjectMeta.Name {
				// we run these steps again just to be sure that it gets updated/created if it hasn't already
				opLog.Info(fmt.Sprintf("Starting work on build: %s", lagoonBuild.ObjectMeta.Name))
				// create the lagoon-sshkey secret
				sshKey := &corev1.Secret{}
				opLog.Info(fmt.Sprintf("Checking `lagoon-sshkey` Secret exists: %s", lagoonBuild.ObjectMeta.Name))
				err := r.getCreateOrUpdateSSHKeySecret(ctx, sshKey, lagoonBuild.Spec, lagoonBuild.ObjectMeta.Namespace)
				if err != nil {
					return ctrl.Result{}, err
				}

				// create the `lagoon-deployer` ServiceAccount
				opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.ObjectMeta.Name))
				serviceAccount := &corev1.ServiceAccount{}
				err = r.getOrCreateServiceAccount(ctx, serviceAccount, lagoonBuild.ObjectMeta.Namespace)
				if err != nil {
					return ctrl.Result{}, err
				}

				// ServiceAccount RoleBinding creation
				opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.ObjectMeta.Name))
				saRoleBinding := &rbacv1.RoleBinding{}
				err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, lagoonBuild.ObjectMeta.Namespace)
				if err != nil {
					return ctrl.Result{}, err
				}

				if r.IsOpenshift && lagoonBuild.Spec.Build.Type == "promote" {
					err := r.getOrCreatePromoteSARoleBinding(ctx, lagoonBuild.Spec.Promote.SourceProject, lagoonBuild.ObjectMeta.Namespace)
					if err != nil {
						return ctrl.Result{}, err
					}
				}

				opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` Token exists: %s", lagoonBuild.ObjectMeta.Name))
				var serviceaccountTokenSecret string
				for _, secret := range serviceAccount.Secrets {
					match, _ := regexp.MatchString("^lagoon-deployer-token", secret.Name)
					if match {
						serviceaccountTokenSecret = secret.Name
						break
					}
				}
				if serviceaccountTokenSecret == "" {
					return ctrl.Result{}, fmt.Errorf("Could not find token secret for ServiceAccount lagoon-deployer")
				}

				// openshift uses a builder service account to be able to push images to the openshift registry
				// lets load this in exactly the same way an openshift build would
				var builderServiceaccountTokenSecret string
				if r.IsOpenshift {
					builderAccount := &corev1.ServiceAccount{}
					err := r.Get(ctx, types.NamespacedName{
						Namespace: lagoonBuild.ObjectMeta.Namespace,
						Name:      "builder",
					}, builderAccount)
					if err != nil {
						return ctrl.Result{}, fmt.Errorf("Could not find ServiceAccount builder")
					}
					opLog.Info(fmt.Sprintf("Checking `builder` Token exists: %s", lagoonBuild.ObjectMeta.Name))
					for _, secret := range builderAccount.Secrets {
						match, _ := regexp.MatchString("^builder-token", secret.Name)
						if match {
							builderServiceaccountTokenSecret = secret.Name
							break
						}
					}
					if builderServiceaccountTokenSecret == "" {
						return ctrl.Result{}, fmt.Errorf("Could not find token secret for ServiceAccount builder")
					}
				}

				// create the Pod that will do the work
				podEnvs := []corev1.EnvVar{
					{
						Name:  "SOURCE_REPOSITORY",
						Value: lagoonBuild.Spec.Project.GitURL,
					},
					{
						Name:  "GIT_REF",
						Value: lagoonBuild.Spec.GitReference,
					},
					{
						Name:  "SUBFOLDER",
						Value: lagoonBuild.Spec.Project.SubFolder,
					},
					{
						Name:  "BRANCH",
						Value: lagoonBuild.Spec.Branch.Name,
					},
					{
						Name:  "PROJECT",
						Value: lagoonBuild.Spec.Project.Name,
					},
					{
						Name:  "ENVIRONMENT_TYPE",
						Value: lagoonBuild.Spec.Project.EnvironmentType,
					},
					{
						Name:  "ACTIVE_ENVIRONMENT",
						Value: lagoonBuild.Spec.Project.ProductionEnvironment,
					},
					{
						Name:  "STANDBY_ENVIRONMENT",
						Value: lagoonBuild.Spec.Project.StandbyEnvironment,
					},
					{
						Name:  "PROJECT_SECRET",
						Value: lagoonBuild.Spec.Project.ProjectSecret,
					},
					{
						Name:  "MONITORING_ALERTCONTACT",
						Value: lagoonBuild.Spec.Project.Monitoring.Contact,
					},
				}
				if r.IsOpenshift {
					// openshift builds have different names for some things, and also additional values to add
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "TYPE",
						Value: lagoonBuild.Spec.Build.Type,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "SAFE_BRANCH",
						Value: lagoonBuild.Spec.Project.Environment,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "SAFE_PROJECT",
						Value: makeSafe(lagoonBuild.Spec.Project.Name),
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "OPENSHIFT_NAME",
						Value: lagoonBuild.Spec.Project.DeployTarget,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name: "ROUTER_URL",
						Value: strings.ToLower(
							strings.Replace(
								strings.Replace(
									lagoonBuild.Spec.Project.RouterPattern,
									"${branch}",
									lagoonBuild.Spec.Project.Environment,
									-1,
								),
								"${project}",
								lagoonBuild.Spec.Project.Name,
								-1,
							),
						),
					})
				} else {
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "BUILD_TYPE",
						Value: lagoonBuild.Spec.Build.Type,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "ENVIRONMENT",
						Value: lagoonBuild.Spec.Project.Environment,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "KUBERNETES",
						Value: lagoonBuild.Spec.Project.DeployTarget,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "REGISTRY",
						Value: lagoonBuild.Spec.Project.Registry,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name: "ROUTER_URL",
						Value: strings.ToLower(
							strings.Replace(
								strings.Replace(
									lagoonBuild.Spec.Project.RouterPattern,
									"${environment}",
									lagoonBuild.Spec.Project.Environment,
									-1,
								),
								"${project}",
								lagoonBuild.Spec.Project.Name,
								-1,
							),
						),
					})
				}
				if lagoonBuild.Spec.Build.CI != "" {
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "CI",
						Value: lagoonBuild.Spec.Build.CI,
					})
				}
				if lagoonBuild.Spec.Build.Type == "pullrequest" {
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PR_HEAD_BRANCH",
						Value: lagoonBuild.Spec.Pullrequest.HeadBranch,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PR_HEAD_SHA",
						Value: lagoonBuild.Spec.Pullrequest.HeadSha,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PR_BASE_BRANCH",
						Value: lagoonBuild.Spec.Pullrequest.BaseBranch,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PR_BASE_SHA",
						Value: lagoonBuild.Spec.Pullrequest.BaseSha,
					})
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PR_TITLE",
						Value: lagoonBuild.Spec.Pullrequest.Title,
					})
					if !r.IsOpenshift {
						// we don't use PR_NUMBER in openshift builds
						podEnvs = append(podEnvs, corev1.EnvVar{
							Name:  "PR_NUMBER",
							Value: string(lagoonBuild.Spec.Pullrequest.Number),
						})
					}
				}
				if lagoonBuild.Spec.Build.Type == "promote" {
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "PROMOTION_SOURCE_ENVIRONMENT",
						Value: lagoonBuild.Spec.Promote.SourceEnvironment,
					})
					if r.IsOpenshift {
						// openshift does promotions differently
						podEnvs = append(podEnvs, corev1.EnvVar{
							Name:  "PROMOTION_SOURCE_OPENSHIFT_PROJECT",
							Value: lagoonBuild.Spec.Promote.SourceProject,
						})
					} else {
						podEnvs = append(podEnvs, corev1.EnvVar{
							Name:  "PROMOTION_SOURCE_NAMESPACE",
							Value: lagoonBuild.Spec.Promote.SourceProject,
						})
					}
				}
				if lagoonBuild.Spec.Project.Variables.Project != nil {
					// if this is 2 bytes long, then it means its just an empty json array
					// we only want to add it if it is more than 2 bytes
					if len(lagoonBuild.Spec.Project.Variables.Project) > 2 {
						podEnvs = append(podEnvs, corev1.EnvVar{
							Name:  "LAGOON_PROJECT_VARIABLES",
							Value: string(lagoonBuild.Spec.Project.Variables.Project),
						})
					}
				}
				if lagoonBuild.Spec.Project.Variables.Environment != nil {
					// if this is 2 bytes long, then it means its just an empty json array
					// we only want to add it if it is more than 2 bytes
					if len(lagoonBuild.Spec.Project.Variables.Environment) > 2 {
						podEnvs = append(podEnvs, corev1.EnvVar{
							Name:  "LAGOON_ENVIRONMENT_VARIABLES",
							Value: string(lagoonBuild.Spec.Project.Variables.Environment),
						})
					}
				}
				if lagoonBuild.Spec.Project.Monitoring.StatuspageID != "" {
					podEnvs = append(podEnvs, corev1.EnvVar{
						Name:  "MONITORING_STATUSPAGEID",
						Value: lagoonBuild.Spec.Project.Monitoring.StatuspageID,
					})
				}
				// Use the build image in the controller definition
				buildImage := r.BuildImage
				if lagoonBuild.Spec.Build.Image != "" {
					// otherwise if the build spec contains an image definition, use it instead.
					buildImage = lagoonBuild.Spec.Build.Image
				}
				newPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      lagoonBuild.ObjectMeta.Name,
						Namespace: lagoonBuild.ObjectMeta.Namespace,
						Labels: map[string]string{
							"lagoon.sh/jobType":   "build",
							"lagoon.sh/buildName": lagoonBuild.ObjectMeta.Name,
						},
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: fmt.Sprintf("%v", lagoonv1alpha1.GroupVersion),
								Kind:       "LagoonBuild",
								Name:       lagoonBuild.ObjectMeta.Name,
								UID:        lagoonBuild.UID,
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
						Tolerations: []corev1.Toleration{
							{
								Key:      "lagoon/build",
								Effect:   "NoSchedule",
								Operator: "Exists",
							},
							{
								Key:      "lagoon/build",
								Effect:   "PreferNoSchedule",
								Operator: "Exists",
							},
						},
						Containers: []corev1.Container{
							{
								Name:            "lagoon-build",
								Image:           buildImage,
								ImagePullPolicy: "Always",
								Env:             podEnvs,
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

				// openshift uses a builder service account to be able to push images to the openshift registry
				// load that into the podspec here
				if r.IsOpenshift {
					newPod.Spec.ServiceAccountName = "builder"
					builderToken := corev1.VolumeMount{
						Name:      builderServiceaccountTokenSecret,
						ReadOnly:  true,
						MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
					}
					builderVolume := corev1.Volume{
						Name: builderServiceaccountTokenSecret,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								SecretName:  builderServiceaccountTokenSecret,
								DefaultMode: intPtr(420),
							},
						},
					}
					newPod.Spec.Volumes = append(newPod.Spec.Volumes, builderVolume)
					newPod.Spec.Containers[0].VolumeMounts = append(newPod.Spec.Containers[0].VolumeMounts, builderToken)
				}
				opLog.Info(fmt.Sprintf("Checking build pod for: %s", lagoonBuild.ObjectMeta.Name))
				// once the pod spec has been defined, check if it isn't already created
				err = r.Get(ctx, types.NamespacedName{
					Namespace: lagoonBuild.ObjectMeta.Namespace,
					Name:      newPod.ObjectMeta.Name,
				}, newPod)
				if err != nil {
					// if it doesn't exist, then create the build pod
					opLog.Info(fmt.Sprintf("Creating build pod for: %s", lagoonBuild.ObjectMeta.Name))
					if err := r.Create(ctx, newPod); err != nil {
						return ctrl.Result{}, err
					}
					// then break out of the build
					break
				} else {
					opLog.Info(fmt.Sprintf("Build pod already running for: %s", lagoonBuild.ObjectMeta.Name))
				}
			} // end check if running build is current LagoonBuild
		} // end loop for running builds

		// if there are no running builds, check if there are any pending builds
		if len(runningBuilds.Items) == 0 {
			pendingBuilds := &lagoonv1alpha1.LagoonBuildList{}
			listOption = (&client.ListOptions{}).ApplyOptions([]client.ListOption{
				client.InNamespace(req.Namespace),
				client.MatchingLabels(map[string]string{
					"lagoon.sh/buildStatus": "Pending",
				}),
			})
			if err := r.List(ctx, pendingBuilds, listOption); err != nil {
				return ctrl.Result{}, fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
			}
			// sort the pending builds by creation timestamp
			sort.Slice(pendingBuilds.Items, func(i, j int) bool {
				return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.Before(&pendingBuilds.Items[j].ObjectMeta.CreationTimestamp)
			})
			if len(pendingBuilds.Items) > 0 {
				pendingBuild := pendingBuilds.Items[0].DeepCopy()
				pendingBuild.Labels["lagoon.sh/buildStatus"] = "Running"
				if err := r.Update(ctx, pendingBuild); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(lagoonBuild.ObjectMeta.Finalizers, finalizerName) {
			lagoonBuild.ObjectMeta.Finalizers = append(lagoonBuild.ObjectMeta.Finalizers, finalizerName)
			// use patches to avoid update errors
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"finalizers": lagoonBuild.ObjectMeta.Finalizers,
				},
			})
			if err := r.Patch(ctx, &lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if containsString(lagoonBuild.ObjectMeta.Finalizers, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&lagoonBuild, req.NamespacedName.Namespace); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
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
			if err := r.Patch(ctx, &lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the given manager
// and we set it to watch LagoonBuilds
func (r *LagoonBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lagoonv1alpha1.LagoonBuild{}).
		Complete(r)
}

func (r *LagoonBuildReconciler) deleteExternalResources(lagoonBuild *lagoonv1alpha1.LagoonBuild, namespace string) error {
	// delete any external resources if required
	return nil
}

// updateStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonBuildReconciler) updateStatusCondition(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	condition lagoonv1alpha1.LagoonConditions,
	log string) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !jobContainsStatus(lagoonBuild.Status.Conditions, condition) {
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

// getOrCreateServiceAccount will create the lagoon-deployer service account if it doesn't exist.
func (r *LagoonBuildReconciler) getOrCreateServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount, ns string) error {
	serviceAccount.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-deployer",
		Namespace: ns,
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      "lagoon-deployer",
	}, serviceAccount)
	if err != nil {
		if err := r.Create(ctx, serviceAccount); err != nil {
			return err
		}
	}
	return nil
}

// getOrCreateSARoleBinding will create the rolebinding for the lagoon-deployer if it doesn't exist.
func (r *LagoonBuildReconciler) getOrCreateSARoleBinding(ctx context.Context, saRoleBinding *rbacv1.RoleBinding, ns string) error {
	saRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-deployer-admin",
		Namespace: ns,
	}
	saRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "admin",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	saRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: ns,
		},
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      "lagoon-deployer-admin",
	}, saRoleBinding)
	if err != nil {
		if err := r.Create(ctx, saRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

// getOrCreateNamespace will create the namespace if it doesn't exist.
func (r *LagoonBuildReconciler) getOrCreateNamespace(ctx context.Context, namespace *corev1.Namespace, spec lagoonv1alpha1.LagoonBuildSpec) error {
	// parse the project/env through the project pattern, or use the default
	nsPattern := spec.Project.NamespacePattern
	if spec.Project.NamespacePattern == "" {
		nsPattern = DefaultNamespacePattern
	}
	// lowercase and dnsify the namespace against the namespace pattern
	ns := makeSafe(
		strings.Replace(
			strings.Replace(
				nsPattern,
				"${environment}",
				spec.Project.Environment,
				-1,
			),
			"${project}",
			spec.Project.Name,
			-1,
		),
	)
	nsLabels := map[string]string{
		"lagoon.sh/project":         spec.Project.Name,
		"lagoon.sh/environment":     spec.Project.Environment,
		"lagoon.sh/environmentType": spec.Project.EnvironmentType,
	}
	// if it isn't an openshift build, then just create a normal namespace
	// add the required lagoon labels to the namespace when creating
	namespace.ObjectMeta = metav1.ObjectMeta{
		Name:   ns,
		Labels: nsLabels,
	}
	// this is an openshift build, then we need to create a projectrequest
	// we use projectrequest so that we ensure any openshift specific things can happen.
	if r.IsOpenshift {
		projectRequest := &projectv1.ProjectRequest{}
		projectRequest.ObjectMeta.Name = ns
		projectRequest.DisplayName = fmt.Sprintf(`[%s] %s`, spec.Project.Name, spec.Project.Environment)
		err := r.Get(ctx, types.NamespacedName{
			Name: ns,
		}, namespace)
		if err != nil {
			if err := r.Create(ctx, projectRequest); err != nil {
				return err
			}
		}
		// once the projectrequest is created, we should wait for the namespace to get created
		// this should happen pretty quickly, but if it hasn't happened in a minute it probably failed
		// this namespace check will also run to patch existing namespaces with labels when they are re-deployed
		err = try.Do(func(attempt int) (bool, error) {
			var err error
			err = r.Get(ctx, types.NamespacedName{
				Name: ns,
			}, namespace)
			if err != nil {
				time.Sleep(10 * time.Second) // wait 10 seconds
			}
			return attempt < 6, err
		})
		if err != nil {
			return err
		}
		// once the namespace exists, then we can patch it with our labels
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": nsLabels,
			},
		})
		if err := r.Patch(ctx, namespace, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return err
		}
		return nil
	}
	err := r.Get(ctx, types.NamespacedName{
		Name: ns,
	}, namespace)
	if err != nil {
		if err := r.Create(ctx, namespace); err != nil {
			return err
		}
	}
	return nil
}

// getCreateOrUpdateSSHKeySecret will create or update the ssh key.
func (r *LagoonBuildReconciler) getCreateOrUpdateSSHKeySecret(ctx context.Context,
	sshKey *corev1.Secret,
	spec lagoonv1alpha1.LagoonBuildSpec,
	ns string) error {
	sshKey.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-sshkey",
		Namespace: ns,
	}
	sshKey.Type = "kubernetes.io/ssh-auth"
	sshKey.Data = map[string][]byte{
		"ssh-privatekey": spec.Project.Key,
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      "lagoon-sshkey",
	}, sshKey)
	if err != nil {
		if err := r.Create(ctx, sshKey); err != nil {
			return err
		}
	}
	// if the keys are different, then load in the new key from the spec
	if bytes.Compare(sshKey.Data["ssh-privatekey"], spec.Project.Key) != 0 {
		sshKey.Data = map[string][]byte{
			"ssh-privatekey": spec.Project.Key,
		}
		if err := r.Update(ctx, sshKey); err != nil {
			return err
		}
	}
	return nil
}

// getOrCreateBuildResource will deepcopy the lagoon build into a new resource and push it to the new namespace
// then clean up the old one.
func (r *LagoonBuildReconciler) getOrCreateBuildResource(ctx context.Context, build *lagoonv1alpha1.LagoonBuild, ns string) error {
	newBuild := build.DeepCopy()
	newBuild.SetNamespace(ns)
	newBuild.SetResourceVersion("")
	newBuild.SetLabels(
		map[string]string{
			"lagoon.sh/buildStatus": "Pending",
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

// getOrCreatePromoteSARoleBinding will create the rolebinding for openshift promotions to be used by the lagoon-deployer service account.
func (r *LagoonBuildReconciler) getOrCreatePromoteSARoleBinding(ctx context.Context, sourcens string, ns string) error {
	viewRoleBinding := &rbacv1.RoleBinding{}
	viewRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-lagoon-deployer-view", ns),
		Namespace: sourcens,
	}
	viewRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "view",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	viewRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: ns,
		},
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: sourcens,
		Name:      fmt.Sprintf("%s-lagoon-deployer-view", ns),
	}, viewRoleBinding)
	if err != nil {
		if err := r.Create(ctx, viewRoleBinding); err != nil {
			return err
		}
	}
	imagePullRoleBinding := &rbacv1.RoleBinding{}
	imagePullRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-lagoon-deployer-image-puller", ns),
		Namespace: sourcens,
	}
	imagePullRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "system:image-puller",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	imagePullRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: ns,
		},
	}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: sourcens,
		Name:      fmt.Sprintf("%s-lagoon-deployer-image-puller", ns),
	}, imagePullRoleBinding)
	if err != nil {
		if err := r.Create(ctx, imagePullRoleBinding); err != nil {
			return err
		}
	}
	defaultImagePullRoleBinding := &rbacv1.RoleBinding{}
	defaultImagePullRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-lagoon-deployer-default-image-puller", ns),
		Namespace: sourcens,
	}
	defaultImagePullRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "system:image-puller",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	defaultImagePullRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: ns,
		},
	}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: sourcens,
		Name:      fmt.Sprintf("%s-lagoon-deployer-default-image-puller", ns),
	}, defaultImagePullRoleBinding)
	if err != nil {
		if err := r.Create(ctx, defaultImagePullRoleBinding); err != nil {
			return err
		}
	}
	return nil
}
