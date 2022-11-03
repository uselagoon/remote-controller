package v1beta1

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

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"github.com/uselagoon/remote-controller/internal/helpers"
)

var (
	crdVersion string = "v1beta1"
)

const (
	// NotOwnedByControllerMessage is used to describe an error where the controller was unable to start the build because
	// the `lagoon.sh/controller` label does not match this controllers name
	NotOwnedByControllerMessage = `Build was cancelled due to an issue with the build controller.
This issue is related to the deployment system, not the repository or code base changes.
Contact your Lagoon support team for help`
	// MissingLabelsMessage is used to describe an error where the controller was unable to start the build because
	// the `lagoon.sh/controller` label is missing
	MissingLabelsMessage = `"Build was cancelled due to namespace configuration issue. A label or labels are missing on the namespace.
This issue is related to the deployment system, not the repository or code base changes.
Contact your Lagoon support team for help`
)

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
			return fmt.Errorf("There was an error creating the lagoon-deployer servicea account. Error was: %v", err)
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
			return fmt.Errorf("There was an error creating the lagoon-deployer-admin role binding. Error was: %v", err)
		}
	}
	return nil
}

// getOrCreateNamespace will create the namespace if it doesn't exist.
func (r *LagoonBuildReconciler) getOrCreateNamespace(ctx context.Context, namespace *corev1.Namespace, lagoonBuild lagoonv1beta1.LagoonBuild, opLog logr.Logger) error {
	// parse the project/env through the project pattern, or use the default
	ns := helpers.GenerateNamespaceName(
		lagoonBuild.Spec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		lagoonBuild.Spec.Project.Environment,
		lagoonBuild.Spec.Project.Name,
		r.NamespacePrefix,
		r.ControllerNamespace,
		r.RandomNamespacePrefix,
	)
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
		// eventually deprecate 'lagoon.sh/environmentAutoIdle' for 'lagoon.sh/environmentIdlingEnabled'
		nsLabels["lagoon.sh/environmentAutoIdle"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.EnvironmentIdling)
		if *lagoonBuild.Spec.Project.ProjectIdling == 1 {
			nsLabels["lagoon.sh/environmentIdlingEnabled"] = "true"
		} else {
			nsLabels["lagoon.sh/environmentIdlingEnabled"] = "false"
		}
	}
	if lagoonBuild.Spec.Project.ProjectIdling != nil {
		// eventually deprecate 'lagoon.sh/projectAutoIdle' for 'lagoon.sh/projectIdlingEnabled'
		nsLabels["lagoon.sh/projectAutoIdle"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.ProjectIdling)
		if *lagoonBuild.Spec.Project.ProjectIdling == 1 {
			nsLabels["lagoon.sh/projectIdlingEnabled"] = "true"
		} else {
			nsLabels["lagoon.sh/projectIdlingEnabled"] = "false"
		}
	}
	if lagoonBuild.Spec.Project.StorageCalculator != nil {
		if *lagoonBuild.Spec.Project.StorageCalculator == 1 {
			nsLabels["lagoon.sh/storageCalculatorEnabled"] = "true"
		} else {
			nsLabels["lagoon.sh/storageCalculatorEnabled"] = "false"
		}
	}
	// add the required lagoon labels to the namespace when creating
	namespace.ObjectMeta = metav1.ObjectMeta{
		Name:   ns,
		Labels: nsLabels,
	}

	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		if helpers.IgnoreNotFound(err) != nil {
			return fmt.Errorf("There was an error getting the namespace. Error was: %v", err)
		}
	}
	if namespace.Status.Phase == corev1.NamespaceTerminating {
		opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled, the namespace is stuck in terminating state", lagoonBuild.ObjectMeta.Name))
		r.cleanUpUndeployableBuild(ctx, lagoonBuild, "Namespace is currently in terminating status - contact your Lagoon support team for help", opLog, true)
		return fmt.Errorf("%s is currently terminating, aborting build", ns)
	}

	// if the namespace exists, check that the controller label exists and matches this controllers namespace name
	if namespace.Status.Phase == corev1.NamespaceActive {
		if value, ok := namespace.ObjectMeta.Labels["lagoon.sh/controller"]; ok {
			if value != r.ControllerNamespace {
				// if the namespace is deployed by a different controller, fail the build
				opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled, the namespace is owned by a different remote-controller", lagoonBuild.ObjectMeta.Name))
				r.cleanUpUndeployableBuild(ctx, lagoonBuild, NotOwnedByControllerMessage, opLog, true)
				return fmt.Errorf("%s is owned by a different remote-controller, aborting build", ns)
			}
		} else {
			// if the label doesn't exist at all, fail the build
			opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled, the namespace is not a Lagoon project/environment", lagoonBuild.ObjectMeta.Name))
			r.cleanUpUndeployableBuild(ctx, lagoonBuild, MissingLabelsMessage, opLog, true)
			return fmt.Errorf("%s is not a Lagoon project/environment, aborting build", ns)
		}
	}

	// if kubernetes, just create it if it doesn't exist
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		if err := r.Create(ctx, namespace); err != nil {
			return fmt.Errorf("There was an error creating the namespace. Error was: %v", err)
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
		return fmt.Errorf("There was an error patching the namespace. Error was: %v", err)
	}
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		return fmt.Errorf("There was an error getting the namespace. Error was: %v", err)
	}

	// if local/regional harbor is enabled
	if r.LFFHarborEnabled {
		// create the harbor client
		lagoonHarbor, err := harbor.New(r.Harbor)
		if err != nil {
			return fmt.Errorf("Error creating harbor client, check your harbor configuration. Error was: %v", err)
		}
		// create the project in harbor
		robotCreds := &helpers.RegistryCredentials{}
		curVer, err := lagoonHarbor.GetHarborVersion(ctx)
		if err != nil {
			return fmt.Errorf("Error getting harbor version, check your harbor configuration. Error was: %v", err)
		}
		if lagoonHarbor.UseV2Functions(curVer) {
			hProject, err := lagoonHarbor.CreateProjectV2(ctx, lagoonBuild.Spec.Project.Name)
			if err != nil {
				return fmt.Errorf("Error creating harbor project: %v", err)
			}
			// create or refresh the robot credentials
			robotCreds, err = lagoonHarbor.CreateOrRefreshRobotV2(ctx,
				r.Client,
				hProject,
				lagoonBuild.Spec.Project.Environment,
				ns,
				lagoonHarbor.RobotAccountExpiry)
			if err != nil {
				return fmt.Errorf("Error creating harbor robot account: %v", err)
			}
		} else {
			hProject, err := lagoonHarbor.CreateProject(ctx, lagoonBuild.Spec.Project.Name)
			if err != nil {
				return fmt.Errorf("Error creating harbor project: %v", err)
			}
			// create or refresh the robot credentials
			robotCreds, err = lagoonHarbor.CreateOrRefreshRobot(ctx,
				r.Client,
				hProject,
				lagoonBuild.Spec.Project.Environment,
				ns,
				time.Now().Add(lagoonHarbor.RobotAccountExpiry).Unix())
			if err != nil {
				return fmt.Errorf("Error creating harbor robot account: %v", err)
			}
		}
		if robotCreds != nil {
			// if we have robotcredentials to create, do that here
			if err := harbor.UpsertHarborSecret(ctx,
				r.Client,
				ns,
				"lagoon-internal-registry-secret",
				lagoonHarbor.Hostname,
				robotCreds); err != nil {
				return fmt.Errorf("Error upserting harbor robot account secret: %v", err)
			}
		}
	}
	return nil
}

// getCreateOrUpdateSSHKeySecret will create or update the ssh key.
func (r *LagoonBuildReconciler) getCreateOrUpdateSSHKeySecret(ctx context.Context,
	sshKey *corev1.Secret,
	spec lagoonv1beta1.LagoonBuildSpec,
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
			return fmt.Errorf("There was an error creating the lagoon-sshkey. Error was: %v", err)
		}
	}
	// if the keys are different, then load in the new key from the spec
	if bytes.Compare(sshKey.Data["ssh-privatekey"], spec.Project.Key) != 0 {
		sshKey.Data = map[string][]byte{
			"ssh-privatekey": spec.Project.Key,
		}
		if err := r.Update(ctx, sshKey); err != nil {
			return fmt.Errorf("There was an error updating the lagoon-sshkey. Error was: %v", err)
		}
	}
	return nil
}

func (r *LagoonBuildReconciler) getOrCreateConfigMap(ctx context.Context, cmName string, configMap *corev1.ConfigMap, ns string) error {
	err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      cmName,
	}, configMap)
	if err != nil {
		configMap.SetNamespace(ns)
		configMap.SetName(cmName)
		//we create it
		if err = r.Create(ctx, configMap); err != nil {
			return fmt.Errorf("There was an error creating the configmap '%v'. Error was: %v", cmName, err)
		}
	}
	return nil
}

// getOrCreatePromoteSARoleBinding will create the rolebinding for openshift promotions to be used by the lagoon-deployer service account.
// @TODO: this role binding can be used as a basis for active/standby tasks allowing one ns to work in another ns
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
			return fmt.Errorf("There was an error creating the deployer view role binding. Error was: %v", err)
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
			return fmt.Errorf("There was an error creating the image puller role binding. Error was: %v", err)
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
			return fmt.Errorf("There was an error creating the image puller role binding. Error was: %v", err)
		}
	}
	return nil
}

// processBuild will actually process the build.
func (r *LagoonBuildReconciler) processBuild(ctx context.Context, opLog logr.Logger, lagoonBuild lagoonv1beta1.LagoonBuild) error {
	// we run these steps again just to be sure that it gets updated/created if it hasn't already
	opLog.Info(fmt.Sprintf("Checking and preparing namespace and associated resources for build: %s", lagoonBuild.ObjectMeta.Name))
	// create the lagoon-sshkey secret
	sshKey := &corev1.Secret{}
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-sshkey` Secret exists: %s", lagoonBuild.ObjectMeta.Name))
	}
	err := r.getCreateOrUpdateSSHKeySecret(ctx, sshKey, lagoonBuild.Spec, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	// create the `lagoon-deployer` ServiceAccount
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.ObjectMeta.Name))
	}
	serviceAccount := &corev1.ServiceAccount{}
	err = r.getOrCreateServiceAccount(ctx, serviceAccount, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	// ServiceAccount RoleBinding creation
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.ObjectMeta.Name))
	}
	saRoleBinding := &rbacv1.RoleBinding{}
	err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, lagoonBuild.ObjectMeta.Namespace)
	if err != nil {
		return err
	}

	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` Token exists: %s", lagoonBuild.ObjectMeta.Name))
	}

	var serviceaccountTokenSecret string
	for _, secret := range serviceAccount.Secrets {
		match, _ := regexp.MatchString("^lagoon-deployer-token", secret.Name)
		if match {
			serviceaccountTokenSecret = secret.Name
			break
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
			Value: r.BackupConfig.BackupDefaultSchedule,
		},
		{
			Name:  "MONTHLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupConfig.BackupDefaultMonthlyRetention),
		},
		{
			Name:  "WEEKLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupConfig.BackupDefaultWeeklyRetention),
		},
		{
			Name:  "DAILY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupConfig.BackupDefaultDailyRetention),
		},
		{
			Name:  "HOURLY_BACKUP_DEFAULT_RETENTION",
			Value: strconv.Itoa(r.BackupConfig.BackupDefaultHourlyRetention),
		},
		{
			Name:  "LAGOON_FEATURE_BACKUP_DEV_SCHEDULE",
			Value: r.BackupConfig.BackupDefaultDevelopmentSchedule,
		},
		{
			Name:  "LAGOON_FEATURE_BACKUP_PR_SCHEDULE",
			Value: r.BackupConfig.BackupDefaultPullrequestSchedule,
		},
		{
			Name:  "LAGOON_FEATURE_BACKUP_DEV_RETENTION",
			Value: r.BackupConfig.BackupDefaultDevelopmentRetention,
		},
		{
			Name:  "LAGOON_FEATURE_BACKUP_PR_RETENTION",
			Value: r.BackupConfig.BackupDefaultPullrequestRetention,
		},
		{
			Name:  "K8UP_WEEKLY_RANDOM_FEATURE_FLAG",
			Value: strconv.FormatBool(r.LFFBackupWeeklyRandom),
		},
		{
			Name:  "NATIVE_CRON_POD_MINIMUM_FREQUENCY",
			Value: strconv.Itoa(r.NativeCronPodMinFrequency),
		},
		// add the API and SSH endpoint configuration to environments
		{
			Name:  "LAGOON_CONFIG_API_HOST",
			Value: r.LagoonAPIConfiguration.APIHost,
		},
		{
			Name:  "LAGOON_CONFIG_SSH_HOST",
			Value: r.LagoonAPIConfiguration.SSHHost,
		},
		{
			Name:  "LAGOON_CONFIG_SSH_PORT",
			Value: r.LagoonAPIConfiguration.SSHPort,
		},
		// in the future, the SSH_HOST and SSH_PORT could also have regional variants
	}
	// add proxy variables to builds if they are defined
	if r.ProxyConfig.HTTPProxy != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "HTTP_PROXY",
			Value: r.ProxyConfig.HTTPProxy,
		})
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "http_proxy",
			Value: r.ProxyConfig.HTTPProxy,
		})
	}
	if r.ProxyConfig.HTTPSProxy != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "HTTPS_PROXY",
			Value: r.ProxyConfig.HTTPSProxy,
		})
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "https_proxy",
			Value: r.ProxyConfig.HTTPSProxy,
		})
	}
	if r.ProxyConfig.NoProxy != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: r.ProxyConfig.NoProxy,
		})
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "no_proxy",
			Value: r.ProxyConfig.NoProxy,
		})
	}
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
						helpers.ShortName(lagoonBuild.Spec.Project.Environment),
						-1,
					),
					"${project}",
					helpers.ShortName(lagoonBuild.Spec.Project.Name),
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
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "PR_NUMBER",
			Value: string(lagoonBuild.Spec.Pullrequest.Number),
		})
	}
	if lagoonBuild.Spec.Build.Type == "promote" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "PROMOTION_SOURCE_ENVIRONMENT",
			Value: lagoonBuild.Spec.Promote.SourceEnvironment,
		})
	}
	// if local/regional harbor is enabled
	if r.LFFHarborEnabled {
		// unmarshal the project variables
		lagoonProjectVariables := &[]helpers.LagoonEnvironmentVariable{}
		lagoonEnvironmentVariables := &[]helpers.LagoonEnvironmentVariable{}
		json.Unmarshal(lagoonBuild.Spec.Project.Variables.Project, lagoonProjectVariables)
		json.Unmarshal(lagoonBuild.Spec.Project.Variables.Environment, lagoonEnvironmentVariables)
		// check if INTERNAL_REGISTRY_SOURCE_LAGOON is defined, and if it isn't true
		// if this value is true, then we want to use what is provided by Lagoon
		// if it is false, or not set, then we use what is provided by this controller
		// this allows us to make it so a specific environment or the project entirely
		// can still use whats provided by lagoon
		if !helpers.VariableExists(lagoonProjectVariables, "INTERNAL_REGISTRY_SOURCE_LAGOON", "true") ||
			!helpers.VariableExists(lagoonEnvironmentVariables, "INTERNAL_REGISTRY_SOURCE_LAGOON", "true") {
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
			auths := helpers.Auths{}
			if secretData, ok := robotCredential.Data[".dockerconfigjson"]; ok {
				if err := json.Unmarshal(secretData, &auths); err != nil {
					return fmt.Errorf("Could not unmarshal Harbor RobotAccount credential")
				}
				// if the defined regional harbor key exists using the hostname
				if creds, ok := auths.Registries[r.Harbor.URL]; ok {
					// use the regional harbor in the build
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_URL", r.Harbor.URL, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_USERNAME", creds.Username, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_PASSWORD", creds.Password, "internal_container_registry")
				}
				if creds, ok := auths.Registries[r.Harbor.Hostname]; ok {
					// use the regional harbor in the build
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_URL", r.Harbor.Hostname, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_USERNAME", creds.Username, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_PASSWORD", creds.Password, "internal_container_registry")
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
	if r.LFFForceInsights != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_FORCE_INSIGHTS",
			Value: r.LFFForceInsights,
		})
	}
	if r.LFFDefaultInsights != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_DEFAULT_INSIGHTS",
			Value: r.LFFDefaultInsights,
		})
	}
	if r.LFFForceRWX2RWO != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_FORCE_RWX_TO_RWO",
			Value: r.LFFForceRWX2RWO,
		})
	}
	if r.LFFDefaultRWX2RWO != "" {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_FEATURE_FLAG_DEFAULT_RWX_TO_RWO",
			Value: r.LFFDefaultRWX2RWO,
		})
	}
	// add any LAGOON_FEATURE_FLAG_ variables in the controller into the build pods
	for fName, fValue := range r.LagoonFeatureFlags {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  fName,
			Value: fValue,
		})
	}
	// Use the build image in the controller definition
	buildImage := r.BuildImage
	if lagoonBuild.Spec.Build.Image != "" {
		// otherwise if the build spec contains an image definition, use it instead.
		buildImage = lagoonBuild.Spec.Build.Image
	}
	volumes := []corev1.Volume{
		{
			Name: "lagoon-sshkey",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  "lagoon-sshkey",
					DefaultMode: helpers.IntPtr(420),
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "lagoon-sshkey",
			ReadOnly:  true,
			MountPath: "/var/run/secrets/lagoon/ssh",
		},
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
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lagoonBuild.ObjectMeta.Name,
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Labels: map[string]string{
				"lagoon.sh/jobType":       "build",
				"lagoon.sh/buildName":     lagoonBuild.ObjectMeta.Name,
				"lagoon.sh/controller":    r.ControllerNamespace,
				"lagoon.sh/crdVersion":    crdVersion,
				"lagoon.sh/buildRemoteID": string(lagoonBuild.ObjectMeta.UID),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: fmt.Sprintf("%v", lagoonv1beta1.GroupVersion),
					Kind:       "LagoonBuild",
					Name:       lagoonBuild.ObjectMeta.Name,
					UID:        lagoonBuild.UID,
				},
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "lagoon-deployer",
			RestartPolicy:      "Never",
			Volumes:            volumes,
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
					VolumeMounts:    volumeMounts,
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

	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking build pod for: %s", lagoonBuild.ObjectMeta.Name))
	}
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
		buildRunningStatus.With(prometheus.Labels{
			"build_namespace": lagoonBuild.ObjectMeta.Namespace,
			"build_name":      lagoonBuild.ObjectMeta.Name,
		}).Set(1)
		buildStatus.With(prometheus.Labels{
			"build_namespace": lagoonBuild.ObjectMeta.Namespace,
			"build_name":      lagoonBuild.ObjectMeta.Name,
			"build_step":      "running",
		}).Set(1)
		buildsStartedCounter.Inc()
		// then break out of the build
	}
	opLog.Info(fmt.Sprintf("Build pod already running for: %s", lagoonBuild.ObjectMeta.Name))
	return nil
}

// cleanUpUndeployableBuild will clean up a build if the namespace is being terminated, or some other reason that it can't deploy (or create the pod)
func (r *LagoonBuildReconciler) cleanUpUndeployableBuild(
	ctx context.Context,
	lagoonBuild lagoonv1beta1.LagoonBuild,
	message string,
	opLog logr.Logger,
	cancelled bool,
) error {
	var allContainerLogs []byte
	if cancelled {
		// if we get this handler, then it is likely that the build was in a pending or running state with no actual running pod
		// so just set the logs to be cancellation message
		allContainerLogs = []byte(fmt.Sprintf(`
========================================
Build cancelled
========================================
%s`, message))
		var buildCondition lagoonv1beta1.BuildStatusType
		buildCondition = lagoonv1beta1.BuildStatusCancelled
		lagoonBuild.Labels["lagoon.sh/buildStatus"] = string(buildCondition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/buildStatus": string(buildCondition),
				},
			},
		})
		if err := r.Patch(ctx, &lagoonBuild, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update build status"))
		}
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
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("There is no configmap %s in namespace %s ", "lagoon-env", lagoonBuild.ObjectMeta.Namespace))
		}
	}
	// send any messages to lagoon message queues
	// update the deployment with the status
	r.cancelledBuildStatusLogsToLagoonLogs(ctx, opLog, &lagoonBuild, &lagoonEnv)
	r.updateCancelledDeploymentAndEnvironmentTask(ctx, opLog, &lagoonBuild, &lagoonEnv)
	if cancelled {
		r.cancelledBuildLogsToLagoonLogs(ctx, opLog, &lagoonBuild, allContainerLogs)
	}
	// delete the build from the lagoon namespace in kubernetes entirely
	err = r.Delete(ctx, &lagoonBuild)
	if err != nil {
		return fmt.Errorf("There was an error deleting the lagoon build. Error was: %v", err)
	}
	return nil
}

func (r *LagoonBuildReconciler) cancelExtraBuilds(ctx context.Context, opLog logr.Logger, pendingBuilds *lagoonv1beta1.LagoonBuildList, ns string, status string) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": string(lagoonv1beta1.BuildStatusPending)}),
	})
	if err := r.List(ctx, pendingBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	if len(pendingBuilds.Items) > 0 {
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("There are %v pending builds", len(pendingBuilds.Items)))
		}
		// if we have any pending builds, then grab the latest one and make it running
		// if there are any other pending builds, cancel them so only the latest one runs
		sort.Slice(pendingBuilds.Items, func(i, j int) bool {
			return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.After(pendingBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		for idx, pBuild := range pendingBuilds.Items {
			pendingBuild := pBuild.DeepCopy()
			if idx == 0 {
				pendingBuild.Labels["lagoon.sh/buildStatus"] = status
			} else {
				// cancel any other pending builds
				opLog.Info(fmt.Sprintf("Attempting to cancel build %s", pendingBuild.ObjectMeta.Name))
				pendingBuild.Labels["lagoon.sh/buildStatus"] = string(lagoonv1beta1.BuildStatusCancelled)
			}
			if err := r.Update(ctx, pendingBuild); err != nil {
				return fmt.Errorf("There was an error updating the pending build. Error was: %v", err)
			}
			var lagoonBuild lagoonv1beta1.LagoonBuild
			if err := r.Get(ctx, types.NamespacedName{
				Namespace: pendingBuild.ObjectMeta.Namespace,
				Name:      pendingBuild.ObjectMeta.Name,
			}, &lagoonBuild); err != nil {
				return helpers.IgnoreNotFound(err)
			}
			opLog.Info(fmt.Sprintf("Cleaning up build %s as cancelled extra build", lagoonBuild.ObjectMeta.Name))
			r.cleanUpUndeployableBuild(ctx, lagoonBuild, "This build was cancelled as a newer build was triggered.", opLog, true)
		}
	}
	return nil
}
