package v1beta2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	"github.com/matryer/try"
	"github.com/prometheus/client_golang/prometheus"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/metrics"

	dockerconfig "github.com/docker/cli/cli/config/configfile"
)

var (
	crdVersion string = "v1beta2"
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
			return fmt.Errorf("there was an error creating the lagoon-deployer servicea account. Error was: %v", err)
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
			return fmt.Errorf("there was an error creating the lagoon-deployer-admin role binding. Error was: %v", err)
		}
	}
	return nil
}

// getCreateOrUpdateSSHKeySecret will create or update the ssh key.
func (r *LagoonBuildReconciler) getCreateOrUpdateSSHKeySecret(ctx context.Context,
	sshKey *corev1.Secret,
	spec lagooncrd.LagoonBuildSpec,
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
			return fmt.Errorf("there was an error creating the lagoon-sshkey. Error was: %v", err)
		}
	}
	// if the keys are different, then load in the new key from the spec
	if !bytes.Equal(sshKey.Data["ssh-privatekey"], spec.Project.Key) {
		sshKey.Data = map[string][]byte{
			"ssh-privatekey": spec.Project.Key,
		}
		if err := r.Update(ctx, sshKey); err != nil {
			return fmt.Errorf("there was an error updating the lagoon-sshkey. Error was: %v", err)
		}
	}
	return nil
}

// processBuild will actually process the build.
func (r *LagoonBuildReconciler) processBuild(ctx context.Context, opLog logr.Logger, lagoonBuild lagooncrd.LagoonBuild) error {
	// we run these steps again just to be sure that it gets updated/created if it hasn't already
	opLog.Info(fmt.Sprintf("Checking and preparing namespace and associated resources for build: %s", lagoonBuild.Name))
	// create the lagoon-sshkey secret
	sshKey := &corev1.Secret{}
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-sshkey` Secret exists: %s", lagoonBuild.Name))
	}
	err := r.getCreateOrUpdateSSHKeySecret(ctx, sshKey, lagoonBuild.Spec, lagoonBuild.Namespace)
	if err != nil {
		return err
	}

	// create the `lagoon-deployer` ServiceAccount
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` ServiceAccount exists: %s", lagoonBuild.Name))
	}
	serviceAccount := &corev1.ServiceAccount{}
	err = r.getOrCreateServiceAccount(ctx, serviceAccount, lagoonBuild.Namespace)
	if err != nil {
		return err
	}

	// ServiceAccount RoleBinding creation
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer-admin` RoleBinding exists: %s", lagoonBuild.Name))
	}
	saRoleBinding := &rbacv1.RoleBinding{}
	err = r.getOrCreateSARoleBinding(ctx, saRoleBinding, lagoonBuild.Namespace)
	if err != nil {
		return err
	}

	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Checking `lagoon-deployer` Token exists: %s", lagoonBuild.Name))
	}

	var serviceaccountTokenSecret string
	var regexComp = regexp.MustCompile("^lagoon-deployer-token")
	for _, secret := range serviceAccount.Secrets {
		match := regexComp.MatchString(secret.Name)
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
			Value: helpers.GetAPIValues(r.LagoonAPIConfiguration, "LAGOON_CONFIG_API_HOST"),
		},
		{
			Name:  "LAGOON_CONFIG_TOKEN_HOST",
			Value: helpers.GetAPIValues(r.LagoonAPIConfiguration, "LAGOON_CONFIG_TOKEN_HOST"),
		},
		{
			Name:  "LAGOON_CONFIG_TOKEN_PORT",
			Value: helpers.GetAPIValues(r.LagoonAPIConfiguration, "LAGOON_CONFIG_TOKEN_PORT"),
		},
		{
			Name:  "LAGOON_CONFIG_SSH_HOST",
			Value: helpers.GetAPIValues(r.LagoonAPIConfiguration, "LAGOON_CONFIG_SSH_HOST"),
		},
		{
			Name:  "LAGOON_CONFIG_SSH_PORT",
			Value: helpers.GetAPIValues(r.LagoonAPIConfiguration, "LAGOON_CONFIG_SSH_PORT"),
		},
	}
	if lagoonBuild.Spec.Project.EnvironmentID != nil {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "ENVIRONMENT_ID",
			Value: strconv.Itoa(int(*lagoonBuild.Spec.Project.EnvironmentID)),
		})
	}
	if lagoonBuild.Spec.Project.ID != nil {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "PROJECT_ID",
			Value: strconv.Itoa(int(*lagoonBuild.Spec.Project.ID)),
		})
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
		Value: r.UnauthenticatedRegistry,
	})
	// this is enabled by default for now
	// eventually will be disabled by default because support for the generation/modification of this will
	// be handled by lagoon or the builds themselves
	if r.LFFRouterURL {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name: "ROUTER_URL",
			Value: strings.ToLower(
				strings.ReplaceAll(
					strings.ReplaceAll(
						lagoonBuild.Spec.Project.RouterPattern,
						"${environment}",
						lagoonBuild.Spec.Project.Environment,
					),
					"${project}",
					lagoonBuild.Spec.Project.Name,
				),
			),
		})
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name: "SHORT_ROUTER_URL",
			Value: strings.ToLower(
				strings.ReplaceAll(
					strings.ReplaceAll(
						lagoonBuild.Spec.Project.RouterPattern,
						"${environment}",
						helpers.ShortName(lagoonBuild.Spec.Project.Environment),
					),
					"${project}",
					helpers.ShortName(lagoonBuild.Spec.Project.Name),
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
		// handle rotating the credential for harbor before the build starts as required
		namespace := &corev1.Namespace{}
		err = r.Get(ctx, types.NamespacedName{
			Name: lagoonBuild.Namespace,
		}, namespace)
		if err != nil {
			return fmt.Errorf("error getting namespace. Error was: %v", err)
		}
		// create the harbor client
		rotated, err := r.Harbor.RotateRobotCredential(ctx, r.Client, *namespace, false)
		if err != nil {
			opLog.Error(err, "error rotating robot credential")
		}
		if rotated {
			opLog.Info(fmt.Sprintf("Robot credentials rotated for %s", lagoonBuild.Namespace))
		}

		// unmarshal the project variables
		lagoonProjectVariables := &[]helpers.LagoonEnvironmentVariable{}
		lagoonEnvironmentVariables := &[]helpers.LagoonEnvironmentVariable{}
		_ = json.Unmarshal(lagoonBuild.Spec.Project.Variables.Project, lagoonProjectVariables)
		_ = json.Unmarshal(lagoonBuild.Spec.Project.Variables.Environment, lagoonEnvironmentVariables)
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
			try.MaxRetries = 12
			err = try.Do(func(attempt int) (bool, error) {
				var secretErr error
				err := r.Get(ctx, types.NamespacedName{
					Namespace: lagoonBuild.Namespace,
					Name:      "lagoon-internal-registry-secret",
				}, robotCredential)
				if err != nil {
					// if the secret doesn't exist wait 5 seconds before trying again
					time.Sleep(5 * time.Second)
					secretErr = fmt.Errorf("secret %s doesn't exist", "lagoon-internal-registry-secret")
				} else {
					// the secret exists, exit the retry
					secretErr = nil
				}
				return attempt < 12, secretErr
			})
			if err != nil {
				return fmt.Errorf("could not find Harbor RobotAccount credential")
			}
			auths := dockerconfig.ConfigFile{}
			if secretData, ok := robotCredential.Data[".dockerconfigjson"]; ok {
				if err := json.Unmarshal(secretData, &auths); err != nil {
					return fmt.Errorf("could not unmarshal Harbor RobotAccount credential")
				}
				// if the defined regional harbor key exists using the hostname
				if creds, ok := auths.AuthConfigs[r.Harbor.URL]; ok {
					// use the regional harbor in the build
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_URL", r.Harbor.URL, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_USERNAME", creds.Username, "internal_container_registry")
					helpers.ReplaceOrAddVariable(lagoonProjectVariables, "INTERNAL_REGISTRY_PASSWORD", creds.Password, "internal_container_registry")
				}
				if creds, ok := auths.AuthConfigs[r.Harbor.Hostname]; ok {
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

	// set the organization variables into the build pod so they're available to builds for consumption
	if lagoonBuild.Spec.Project.Organization != nil {
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_ORGANIZATION_ID",
			Value: fmt.Sprintf("%d", *lagoonBuild.Spec.Project.Organization.ID),
		})
		podEnvs = append(podEnvs, corev1.EnvVar{
			Name:  "LAGOON_ORGANIZATION_NAME",
			Value: lagoonBuild.Spec.Project.Organization.Name,
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
					DefaultMode: helpers.Int32Ptr(420),
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
					DefaultMode: helpers.Int32Ptr(420),
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
			Name:      lagoonBuild.Name,
			Namespace: lagoonBuild.Namespace,
			Labels: map[string]string{
				"lagoon.sh/jobType":       "build",
				"lagoon.sh/buildName":     lagoonBuild.Name,
				"lagoon.sh/controller":    r.ControllerNamespace,
				"crd.lagoon.sh/version":   crdVersion,
				"lagoon.sh/buildRemoteID": string(lagoonBuild.UID),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: fmt.Sprintf("%v", lagooncrd.GroupVersion),
					Kind:       "LagoonBuild",
					Name:       lagoonBuild.Name,
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
					ImagePullPolicy: r.ImagePullPolicy,
					Env:             podEnvs,
					VolumeMounts:    volumeMounts,
				},
			},
		},
	}

	// set the organization labels on build pods
	if lagoonBuild.Spec.Project.Organization != nil {
		newPod.Labels["organization.lagoon.sh/id"] = fmt.Sprintf("%d", *lagoonBuild.Spec.Project.Organization.ID)
		newPod.Labels["organization.lagoon.sh/name"] = lagoonBuild.Spec.Project.Organization.Name
	}

	if !r.ClusterAutoscalerEvict {
		// try to prevent build pods from being evicted by cluster autoscaler
		newPod.Labels["cluster-autoscaler.kubernetes.io/safe-to-evict"] = "false"
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
		opLog.Info(fmt.Sprintf("Checking build pod for: %s", lagoonBuild.Name))
	}
	// once the pod spec has been defined, check if it isn't already created
	err = r.Get(ctx, types.NamespacedName{
		Namespace: lagoonBuild.Namespace,
		Name:      newPod.Name,
	}, newPod)
	if err != nil {
		// check the dockerhosts and assign as required
		reuseType := lagoonBuild.Namespace
		switch r.DockerHost.ReuseType {
		case "project":
			reuseType = lagoonBuild.Spec.Project.Name
		case "organization":
			if lagoonBuild.Spec.Project.Organization != nil {
				reuseType = lagoonBuild.Spec.Project.Organization.Name
			} else {
				// fall back to project name if organization not found
				reuseType = lagoonBuild.Spec.Project.Name
			}
		}
		dockerHost := r.DockerHost.AssignDockerHost(
			lagoonBuild.Name,
			reuseType,
			r.BuildQoS.MaxContainerBuilds,
		)
		dockerHostEnvVar := corev1.EnvVar{
			Name:  "DOCKER_HOST",
			Value: dockerHost,
		}
		newPod.Annotations = map[string]string{
			"dockerhost.lagoon.sh/name": dockerHost,
		}
		newPod.Spec.Containers[0].Env = append(newPod.Spec.Containers[0].Env, dockerHostEnvVar)
		opLog.Info(fmt.Sprintf("Assigning build %s to dockerhost %s", lagoonBuild.Name, dockerHost))
		// if it doesn't exist, then create the build pod
		opLog.Info(fmt.Sprintf("Creating build pod for: %s", lagoonBuild.Name))
		if err := r.Create(ctx, newPod); err != nil {
			opLog.Error(err, "Unable to create build pod")
			// log the error and just exit, don't continue to try and do anything
			// @TODO: should update the build to failed
			return nil
		}
		metrics.BuildRunningStatus.With(prometheus.Labels{
			"build_namespace":  lagoonBuild.Namespace,
			"build_name":       lagoonBuild.Name,
			"build_dockerhost": dockerHost,
		}).Set(1)
		metrics.BuildStatus.With(prometheus.Labels{
			"build_namespace": lagoonBuild.Namespace,
			"build_name":      lagoonBuild.Name,
			"build_step":      "running",
		}).Set(1)
		metrics.BuildsStartedCounter.Inc()
		// then break out of the build
	} else {
		opLog.Info(fmt.Sprintf("Build pod already running for: %s", lagoonBuild.Name))
	}
	return nil
}

// updateQueuedBuild will update a build if it is queued
func (r *LagoonBuildReconciler) updateQueuedBuild(
	ctx context.Context,
	buildReq types.NamespacedName,
	queuePosition, queueLength int,
	opLog logr.Logger,
) error {
	var lagoonBuild lagooncrd.LagoonBuild
	if err := r.Get(ctx, buildReq, &lagoonBuild); err != nil {
		return helpers.IgnoreNotFound(err)
	}
	// if we get this handler, then it is likely that the build was in a pending or running state with no actual running pod
	// so just set the logs to be cancellation message
	allContainerLogs := []byte(fmt.Sprintf(`
========================================
%s
========================================
`, fmt.Sprintf("This build is currently queued in position %v/%v", queuePosition, queueLength)))
	// send any messages to lagoon message queues
	r.Messaging.BuildStatusLogsToLagoonLogs(ctx, r.EnableMQ, opLog, &lagoonBuild, lagooncrd.BuildStatusQueued, r.LagoonTargetName, fmt.Sprintf("queued %v/%v", queuePosition, queueLength))
	r.Messaging.UpdateDeploymentAndEnvironmentTask(ctx, r.EnableMQ, opLog, &lagoonBuild, true, lagooncrd.BuildStatusQueued, r.LagoonTargetName, fmt.Sprintf("queued %v/%v", queuePosition, queueLength))
	r.Messaging.BuildLogsToLagoonLogs(r.EnableMQ, opLog, &lagoonBuild, allContainerLogs, lagooncrd.BuildStatusQueued, r.LagoonTargetName)
	return nil
}
