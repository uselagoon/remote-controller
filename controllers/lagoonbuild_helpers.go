package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/matryer/try.v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"

	// Openshift
	projectv1 "github.com/openshift/api/project/v1"
)

// updateBuildStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonBuildReconciler) updateBuildStatusCondition(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	condition lagoonv1alpha1.LagoonBuildConditions,
	log []byte,
) error {
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
		if err := r.Patch(ctx, lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
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
func (r *LagoonBuildReconciler) getOrCreateNamespace(ctx context.Context, namespace *corev1.Namespace, lagoonBuild lagoonv1alpha1.LagoonBuild, opLog logr.Logger) error {
	// parse the project/env through the project pattern, or use the default
	var err error
	nsPattern := lagoonBuild.Spec.Project.NamespacePattern
	if lagoonBuild.Spec.Project.NamespacePattern == "" {
		nsPattern = DefaultNamespacePattern
	}
	// lowercase and dnsify the namespace against the namespace pattern
	ns := makeSafe(
		strings.Replace(
			strings.Replace(
				nsPattern,
				"${environment}",
				lagoonBuild.Spec.Project.Environment,
				-1,
			),
			"${project}",
			lagoonBuild.Spec.Project.Name,
			-1,
		),
	)
	// If there is a namespaceprefix defined, and random prefix is disabled
	// then add the prefix to the namespace
	if r.NamespacePrefix != "" && r.RandomNamespacePrefix == false {
		ns = fmt.Sprintf("%s-%s", r.NamespacePrefix, ns)
	}
	// If the randomprefix is enabled, then add a prefix based on the hash of the controller namespace
	if r.RandomNamespacePrefix {
		ns = fmt.Sprintf("%s-%s", hashString(r.ControllerNamespace)[0:8], ns)
	}
	// Once the namespace is fully calculated, then truncate the generated namespace
	// to 63 characters to not exceed the kubernetes namespace limit
	if len(ns) > 63 {
		ns = fmt.Sprintf("%s-%s", ns[0:58], hashString(ns)[0:4])
	}
	nsLabels := map[string]string{
		"lagoon.sh/project":         lagoonBuild.Spec.Project.Name,
		"lagoon.sh/environment":     lagoonBuild.Spec.Project.Environment,
		"lagoon.sh/environmentType": lagoonBuild.Spec.Project.EnvironmentType,
		"lagoon.sh/controller":      r.ControllerNamespace,
	}
	if lagoonBuild.Spec.Project.ID != nil {
		nsLabels["lagoon.sh/projectId"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.ID)
	}
	if lagoonBuild.Spec.Project.EnvironmentID != nil {
		nsLabels["lagoon.sh/environmentId"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.EnvironmentID)
	}
	// set the auto idling values if they are defined
	if lagoonBuild.Spec.Project.EnvironmentIdling != nil {
		nsLabels["lagoon.sh/environmentAutoIdle"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.EnvironmentIdling)
	}
	if lagoonBuild.Spec.Project.ProjectIdling != nil {
		nsLabels["lagoon.sh/projectAutoIdle"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.ProjectIdling)
	}
	// if it isn't an openshift build, then just create a normal namespace
	// add the required lagoon labels to the namespace when creating
	namespace.ObjectMeta = metav1.ObjectMeta{
		Name:   ns,
		Labels: nsLabels,
	}

	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		if ignoreNotFound(err) != nil {
			return err
		}
	}
	if namespace.Status.Phase == corev1.NamespaceTerminating {
		opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled, the namespace is stuck in terminating state", lagoonBuild.ObjectMeta.Name))
		r.cleanUpUndeployableBuild(ctx, lagoonBuild, "Namespace is currently in terminating status - contact your Lagoon support team for help", opLog)
		return fmt.Errorf("%s is currently terminating, aborting build", ns)
	}

	// this is an openshift build, then we need to create a projectrequest
	// we use projectrequest so that we ensure any openshift specific things can happen.
	if r.IsOpenshift {
		projectRequest := &projectv1.ProjectRequest{}
		projectRequest.ObjectMeta.Name = ns
		projectRequest.DisplayName = fmt.Sprintf(`[%s] %s`, lagoonBuild.Spec.Project.Name, lagoonBuild.Spec.Project.Environment)
		if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
			if err := r.Create(ctx, projectRequest); err != nil {
				return err
			}
		}
		// once the projectrequest is created, we should wait for the namespace to get created
		// this should happen pretty quickly, but if it hasn't happened in a minute it probably failed
		// this namespace check will also run to patch existing namespaces with labels when they are re-deployed
		err = try.Do(func(attempt int) (bool, error) {
			var err error
			if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
				time.Sleep(10 * time.Second) // wait 10 seconds
			}
			return attempt < 6, err
		})
		if err != nil {
			return err
		}
	} else {
		// if kubernetes, just create it if it doesn't exist
		if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
			if err := r.Create(ctx, namespace); err != nil {
				return err
			}
		}
	}
	// once the namespace exists, then we can patch it with our labels
	// this means the labels will always get added or updated if we need to change them or add new labels
	// after the namespace has been created
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": nsLabels,
		},
	})
	if err := r.Patch(ctx, namespace, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return err
	}
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		return err
	}

	// if local/regional harbor is enabled, and this is not an openshift 3 cluster
	if r.LFFHarborEnabled && !r.IsOpenshift {
		// create the harbor client
		lagoonHarbor, err := NewHarbor(r.Harbor)
		if err != nil {
			return err
		}
		// create the project in harbor
		hProject, err := lagoonHarbor.CreateProject(ctx, lagoonBuild.Spec.Project.Name)
		if err != nil {
			return err
		}
		// create or refresh the robot credentials
		robotCreds, err := lagoonHarbor.CreateOrRefreshRobot(ctx,
			r.Client,
			hProject,
			lagoonBuild.Spec.Project.Environment,
			ns,
			time.Now().Add(lagoonHarbor.RobotAccountExpiry).Unix())
		if err != nil {
			return err
		}
		if robotCreds != nil {
			// if we have robotcredentials to create, do that here
			if err := upsertHarborSecret(ctx,
				r.Client,
				ns,
				"lagoon-internal-registry-secret",
				lagoonHarbor.Hostname,
				robotCreds); err != nil {
				return err
			}
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

// processBuild will actually process the build.
func (r *LagoonBuildReconciler) processBuild(ctx context.Context, opLog logr.Logger, lagoonBuild lagoonv1alpha1.LagoonBuild) error {
	// we run these steps again just to be sure that it gets updated/created if it hasn't already
	opLog.Info(fmt.Sprintf("Starting work on build: %s", lagoonBuild.ObjectMeta.Name))
	// create the lagoon-sshkey secret
	sshKey := &corev1.Secret{}
	opLog.Info(fmt.Sprintf("Checking `lagoon-sshkey` Secret exists: %s", lagoonBuild.ObjectMeta.Name))
	err := r.getCreateOrUpdateSSHKeySecret(ctx, sshKey, lagoonBuild.Spec, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	// create the `lagoon-deployer` ServiceAccount
	opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.ObjectMeta.Name))
	serviceAccount := &corev1.ServiceAccount{}
	err = r.getOrCreateServiceAccount(ctx, serviceAccount, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	// ServiceAccount RoleBinding creation
	opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.ObjectMeta.Name))
	saRoleBinding := &rbacv1.RoleBinding{}
	err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	if r.IsOpenshift && lagoonBuild.Spec.Build.Type == "promote" {
		err := r.getOrCreatePromoteSARoleBinding(ctx, lagoonBuild.Spec.Promote.SourceProject, lagoonBuild.ObjectMeta.Namespace)
		if err != nil {
			return err
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
		return fmt.Errorf("Could not find token secret for ServiceAccount lagoon-deployer")
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
			return fmt.Errorf("Could not find ServiceAccount builder")
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
			return fmt.Errorf("Could not find token secret for ServiceAccount builder")
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
		{
			Name:  "DEFAULT_BACKUP_SCHEDULE",
			Value: r.BackupDefaultSchedule,
		},
		{
			Name:  "MONTHLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupDefaultMonthlyRetention),
		},
		{
			Name:  "WEEKLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupDefaultWeeklyRetention),
		},
		{
			Name:  "DAILY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupDefaultDailyRetention),
		},
		{
			Name:  "HOURLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupDefaultHourlyRetention),
		},
		{
			Name:  "K8UP_WEEKLY_RANDOM_FEATURE_FLAG",
			Value: strconv.FormatBool(r.LFFBackupWeeklyRandom),
		},
		{
			Name:  "NATIVE_CRON_POD_MINIMUM_FREQUENCY",
			Value: strconv.Itoa(r.NativeCronPodMinFrequency),
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
		// this is enabled by default for now
		// eventually will be disabled by default because support for the generation/modification of this will
		// be handled by lagoon or the builds themselves
		if r.LFFRouterURL {
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
		}
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
		// this is enabled by default for now
		// eventually will be disabled by default because support for the generation/modification of this will
		// be handled by lagoon or the builds themselves
		if r.LFFRouterURL {
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
			podEnvs = append(podEnvs, corev1.EnvVar{
				Name: "SHORT_ROUTER_URL",
				Value: strings.ToLower(
					strings.Replace(
						strings.Replace(
							lagoonBuild.Spec.Project.RouterPattern,
							"${environment}",
							shortName(lagoonBuild.Spec.Project.Environment),
							-1,
						),
						"${project}",
						shortName(lagoonBuild.Spec.Project.Name),
						-1,
					),
				),
			})
		}
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
		}
	}
	// if local/regional harbor is enabled, and this is not an openshift 3 cluster
	if r.LFFHarborEnabled && !r.IsOpenshift {
		// unmarshal the project variables
		lagoonProjectVariables := &[]LagoonEnvironmentVariable{}
		lagoonEnvironmentVariables := &[]LagoonEnvironmentVariable{}
		json.Unmarshal(lagoonBuild.Spec.Project.Variables.Project, lagoonProjectVariables)
		json.Unmarshal(lagoonBuild.Spec.Project.Variables.Environment, lagoonEnvironmentVariables)
		// check if INTERNAL_REGISTRY_SOURCE_LAGOON is defined, and if it isn't true
		// if this value is true, then we want to use what is provided by Lagoon
		// if it is false, or not set, then we use what is provided by this controller
		// this allows us to make it so a specific environment or the project entirely
		// can still use whats provided by lagoon
		if !variableExists(lagoonProjectVariables, "INTERNAL_REGISTRY_SOURCE_LAGOON", "true") ||
			!variableExists(lagoonEnvironmentVariables, "INTERNAL_REGISTRY_SOURCE_LAGOON", "true") {
			// source the robot credential, and inject it into the lagoon project variables
			// this will overwrite what is provided by lagoon (if lagoon has provided them)
			// or it will add them.
			robotCredential := &corev1.Secret{}
			if err = r.Get(ctx, types.NamespacedName{
				Namespace: lagoonBuild.ObjectMeta.Namespace,
				Name:      "lagoon-internal-registry-secret",
			}, robotCredential); err != nil {
				return fmt.Errorf("Could not find Harbor RobotAccount credential")
			}
			auths := Auths{}
			if secretData, ok := robotCredential.Data[".dockerconfigjson"]; ok {
				if err := json.Unmarshal(secretData, &auths); err != nil {
					return fmt.Errorf("Could not unmarshal Harbor RobotAccount credential")
				}
				// if the defined regional harbor key exists using the hostname
				if creds, ok := auths.Registries[r.Harbor.URL]; ok {
					// use the regional harbor in the build
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_URL", r.Harbor.URL, "internal_container_registry")
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_USERNAME", creds.Username, "internal_container_registry")
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_PASSWORD", creds.Password, "internal_container_registry")
				}
				if creds, ok := auths.Registries[r.Harbor.Hostname]; ok {
					// use the regional harbor in the build
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_URL", r.Harbor.Hostname, "internal_container_registry")
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_USERNAME", creds.Username, "internal_container_registry")
					replaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_PASSWORD", creds.Password, "internal_container_registry")
				}
			}
			// marshal any changes into the project spec on the fly, don't save the spec though
			// these values are being overwritten and injected directly into the build pod to be consumed
			// by the build pod image
			lagoonBuild.Spec.Project.Variables.Project, _ = json.Marshal(lagoonProjectVariables)
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
	// if the fastly watch status is set on the controller, inject the fastly service ID into the build pod to be consumed
	// by the build-depoy-dind image
	if r.FastlyWatchStatus {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FASTLY_NOCACHE_SERVICE_ID",
			Value: r.FastlyServiceID,
		})
	}
	// Set any defined Lagoon feature flags in the build environment.
	if r.LFFForceRootlessWorkload != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_FORCE_ROOTLESS_WORKLOAD",
			Value: r.LFFForceRootlessWorkload,
		})
	}
	if r.LFFDefaultRootlessWorkload != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_DEFAULT_ROOTLESS_WORKLOAD",
			Value: r.LFFDefaultRootlessWorkload,
		})
	}
	if r.LFFForceIsolationNetworkPolicy != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_FORCE_ISOLATION_NETWORK_POLICY",
			Value: r.LFFForceIsolationNetworkPolicy,
		})
	}
	if r.LFFDefaultIsolationNetworkPolicy != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_DEFAULT_ISOLATION_NETWORK_POLICY",
			Value: r.LFFDefaultIsolationNetworkPolicy,
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
				"lagoon.sh/jobType":       "build",
				"lagoon.sh/buildName":     lagoonBuild.ObjectMeta.Name,
				"lagoon.sh/controller":    r.ControllerNamespace,
				"lagoon.sh/buildRemoteID": string(lagoonBuild.ObjectMeta.UID),
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
				{
					Key:      "lagoon.sh/build",
					Effect:   "NoSchedule",
					Operator: "Exists",
				},
				{
					Key:      "lagoon.sh/build",
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

	// set the pod security context, if defined to a non-default value
	if r.BuildPodRunAsUser != 0 || r.BuildPodRunAsGroup != 0 ||
		r.BuildPodFSGroup != 0 {
		newPod.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser:  &r.BuildPodRunAsUser,
			RunAsGroup: &r.BuildPodRunAsGroup,
			FSGroup:    &r.BuildPodFSGroup,
		}
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
			opLog.Error(err, fmt.Sprintf("Unable to create build pod"))
			// log the error and just exit, don't continue to try and do anything
			// @TODO: should update the build to failed
			return nil
		}
		// then break out of the build
	}
	opLog.Info(fmt.Sprintf("Build pod already running for: %s", lagoonBuild.ObjectMeta.Name))
	return nil
}

// cleanUpUndeployableBuild will clean up a build if the namespace is being terminated, or some other reason that it can't deploy (or create the pod)
func (r *LagoonBuildReconciler) cleanUpUndeployableBuild(
	ctx context.Context,
	lagoonBuild lagoonv1alpha1.LagoonBuild,
	message string,
	opLog logr.Logger,
) error {
	var allContainerLogs []byte
	// if we get this handler, then it is likely that the build was in a pending or running state with no actual running pod
	// so just set the logs to be cancellation message
	allContainerLogs = []byte(fmt.Sprintf(`
========================================
Build cancelled
========================================
%s`, message))
	var jobCondition lagoonv1alpha1.BuildStatusType
	jobCondition = lagoonv1alpha1.BuildStatusCancelled
	lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(jobCondition)
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/buildStatus": string(jobCondition),
			},
		},
	})
	if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to update build status"))
	}
	// get the configmap for lagoon-env so we can use it for updating the deployment in lagoon
	var lagoonEnv corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{
		Namespace: lagoonBuild.ObjectMeta.Namespace,
		Name:      "lagoon-env",
	},
		&lagoonEnv,
	)
	if err != nil {
		// if there isn't a configmap, just info it and move on
		// the updatedeployment function will see it as nil and not bother doing the bits that require the configmap
		opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", lagoonBuild.ObjectMeta.Namespace))
	}
	// send any messages to lagoon message queues
	// update the deployment with the status
	r.cancelledBuildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &lagoonEnv)
	r.updateCancelledDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &lagoonEnv)
	r.cancelledBuildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, allContainerLogs)
	// delete the build from the lagoon namespace in kubernetes entirely
	err = r.Delete(ctx, &lagoonBuild)
	if err != nil {
		return err
	}
	return nil
}

func (r *LagoonBuildReconciler) cancelExtraBuilds(ctx context.Context, opLog logr.Logger, pendingBuilds *lagoonv1alpha1.LagoonBuildList, ns string, status string) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": string(lagoonv1alpha1.BuildStatusPending)}),
	})
	if err := r.List(ctx, pendingBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	opLog.Info(fmt.Sprintf("There are %v Pending builds", len(pendingBuilds.Items)))
	// if we have any pending builds, then grab the latest one and make it running
	// if there are any other pending builds, cancel them so only the latest one runs
	sort.Slice(pendingBuilds.Items, func(i, j int) bool {
		return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.After(pendingBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
	})
	if len(pendingBuilds.Items) > 0 {
		for idx, pBuild := range pendingBuilds.Items {
			pendingBuild := pBuild.DeepCopy()
			if idx == 0 {
				pendingBuild.Labels["lagoon.sh/buildStatus"] = status
			} else {
				// cancel any other pending builds
				opLog.Info(fmt.Sprintf("Attempting to cancel build %s", pendingBuild.ObjectMeta.Name))
				pendingBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1alpha1.BuildStatusCancelled)
			}
			if err := r.Update(ctx, pendingBuild); err != nil {
				return err
			}
			var lagoonBuild lagoonv1alpha1.LagoonBuild
			if err := r.Get(ctx, types.NamespacedName{
				Namespace: pendingBuild.ObjectMeta.Namespace,
				Name:      pendingBuild.ObjectMeta.Name,
			}, &lagoonBuild); err != nil {
				return ignoreNotFound(err)
			}
			opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled", lagoonBuild.ObjectMeta.Name))
			r.cleanUpUndeployableBuild(ctx, lagoonBuild, "This build was cancelled, as a new one was deployed before this one could start.", opLog)
		}
	}
	return nil
}
