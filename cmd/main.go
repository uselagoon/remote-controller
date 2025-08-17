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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cheshir/go-mq/v2"
	str2duration "github.com/xhit/go-str2duration/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/uselagoon/remote-controller/internal/dockerhost"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/utilities/deletions"
	"github.com/uselagoon/remote-controller/internal/utilities/pruner"

	cron "gopkg.in/robfig/cron.v2"

	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"
	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	harborctrl "github.com/uselagoon/remote-controller/internal/controllers/harbor"
	lagoonv1beta2ctrl "github.com/uselagoon/remote-controller/internal/controllers/v1beta2"
	"github.com/uselagoon/remote-controller/internal/messenger"
	k8upv1alpha1 "github.com/vshn/k8up/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme                          = runtime.NewScheme()
	setupLog                        = ctrl.Log.WithName("setup")
	lagoonTargetName                string
	mqUser                          string
	mqPass                          string
	mqHost                          string
	mqTLS                           bool
	mqVerify                        bool
	mqCACert                        string
	mqClientCert                    string
	mqClientKey                     string
	lagoonAPIHost                   string
	lagoonSSHHost                   string
	lagoonSSHPort                   string
	lagoonTokenHost                 string
	lagoonTokenPort                 string
	tlsSkipVerify                   bool
	advancedTaskSSHKeyInjection     bool
	advancedTaskDeployToken         bool
	cleanupHarborRepositoryOnDelete bool
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = lagoonv1beta2.AddToScheme(scheme)
	_ = k8upv1.AddToScheme(scheme)
	_ = k8upv1alpha1.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableMQ bool
	var leaderElectionID string
	var secureMetrics bool
	var enableHTTP2 bool

	var mqWorkers int
	var rabbitRetryInterval int
	var startupConnectionAttempts int
	var startupConnectionInterval int
	var overrideBuildDeployImage string
	var namespacePrefix string
	var randomPrefix bool
	var controllerNamespace string
	var enableDebug bool
	var fastlyServiceID string
	var fastlyWatchStatus bool
	var buildPodRunAsUser uint
	var buildPodRunAsGroup uint
	var buildPodFSGroup uint
	var backupDefaultHourlyRetention int
	var backupDefaultDailyRetention int
	var backupDefaultWeeklyRetention int
	var backupDefaultMonthlyRetention int
	var backupDefaultSchedule string

	var backupDefaultDevelopmentSchedule string
	var backupDefaultPullrequestSchedule string
	var backupDefaultDevelopmentRetention string
	var backupDefaultPullrequestRetention string

	// Lagoon Feature Flags options control features in Lagoon. Default options
	// set a default cluster policy, while Force options enforce a cluster policy
	// and cannot be overridden.
	var lffForceRootlessWorkload string
	var lffDefaultRootlessWorkload string
	var lffForceIsolationNetworkPolicy string
	var lffDefaultIsolationNetworkPolicy string
	var lffForceInsights string
	var lffDefaultInsights string
	var lffForceRWX2RWO string
	var lffDefaultRWX2RWO string
	var buildPodCleanUpEnable bool
	var taskPodCleanUpEnable bool
	var buildsCleanUpEnable bool
	var taskCleanUpEnable bool
	var buildPodCleanUpCron string
	var taskPodCleanUpCron string
	var buildsCleanUpCron string
	var taskCleanUpCron string
	var buildsToKeep int
	var buildPodsToKeep int
	var tasksToKeep int
	var taskPodsToKeep int
	var lffBackupWeeklyRandom bool
	var lffHarborEnabled bool
	var lffSupportK8UPv2 bool
	var harborURL string
	var harborAPI string
	var harborUsername string
	var harborPassword string
	var harborRobotPrefix string
	var harborRobotDeleteDisabled bool
	var harborWebhookAdditionEnabled bool
	var harborExpiryInterval string
	var harborRotateInterval string
	var harborRobotAccountExpiry string
	var harborCredentialCron string
	var harborLagoonWebhook string
	var harborWebhookEventTypes string
	var nativeCronPodMinFrequency int
	var pvcRetryAttempts int
	var pvcRetryInterval int
	var cleanNamespacesEnabled bool
	var cleanNamespacesCron string
	var pruneLongRunningBuildPods bool
	var pruneLongRunningTaskPods bool
	var timeoutForLongRunningBuildPods int
	var timeoutForLongRunningTaskPods int
	var pruneLongRunningPodsCron string

	var lffQoSEnabled bool
	var qosTotalBuilds int
	var qosMaxContainerBuilds int
	var qosDefaultPriority int

	var lffTaskQoSEnabled bool
	var qosMaxTasks int
	var qosMaxNamespaceTasks int

	var taskImagePullPolicy string
	var buildImagePullPolicy string

	var lffRouterURL bool

	var enableDeprecatedAPIs bool

	var httpProxy string
	var httpsProxy string
	var noProxy string
	var enablePodProxy bool
	var podsUseDifferentProxy bool

	var unauthenticatedRegistry string
	var dockerHostNamespace string
	var dockerHostReuseType string

	var clusterAutoscalerEvict bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")

	flag.StringVar(&lagoonTargetName, "lagoon-target-name", "ci-local-control-k8s",
		"The name of the target as it is in lagoon.")
	flag.StringVar(&mqUser, "rabbitmq-username", "guest",
		"The username of the rabbitmq user.")
	flag.StringVar(&mqPass, "rabbitmq-password", "guest",
		"The password for the rabbitmq user.")
	flag.StringVar(&mqHost, "rabbitmq-hostname", "localhost:5672",
		"The hostname:port for the rabbitmq host.")
	flag.BoolVar(&mqTLS, "rabbitmq-tls", false,
		"To use amqps instead of amqp.")
	flag.BoolVar(&mqVerify, "rabbitmq-verify", false,
		"To verify rabbitmq peer connection.")
	flag.StringVar(&mqCACert, "rabbitmq-cacert", "",
		"The path to the ca certificate")
	flag.StringVar(&mqClientCert, "rabbitmq-clientcert", "",
		"The path to the client certificate")
	flag.StringVar(&mqClientKey, "rabbitmq-clientkey", "",
		"The path to the client key")
	flag.IntVar(&mqWorkers, "rabbitmq-queue-workers", 1,
		"The number of workers to start with.")
	flag.IntVar(&rabbitRetryInterval, "rabbitmq-retry-interval", 30,
		"The retry interval for rabbitmq.")
	flag.StringVar(&leaderElectionID, "leader-election-id", "lagoon-builddeploy-leader-election-helper",
		"The ID to use for leader election.")
	flag.String("pending-message-cron", "", "This does nothing and will be removed in a future version.")
	flag.IntVar(&startupConnectionAttempts, "startup-connection-attempts", 10,
		"The number of startup attempts before exiting.")
	flag.IntVar(&startupConnectionInterval, "startup-connection-interval-seconds", 30,
		"The duration between startup attempts.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableMQ, "enable-message-queue", true,
		"Enable message queue to provide updates back to Lagoon.")
	flag.StringVar(&overrideBuildDeployImage, "override-builddeploy-image", "uselagoon/build-deploy-image:latest",
		"The build and deploy image that should be used by builds started by the controller.")
	flag.StringVar(&namespacePrefix, "namespace-prefix", "",
		"The prefix that will be added to all namespaces that are generated, maximum 8 characters. (only used if random-prefix is set false)")
	flag.BoolVar(&randomPrefix, "random-prefix", false,
		"Flag to determine if the all namespaces should be prefixed with 5 random characters.")
	flag.StringVar(&controllerNamespace, "controller-namespace", "",
		"The name of the namespace the controller is deployed in.")
	flag.StringVar(&lagoonAPIHost, "lagoon-api-host", "http://10.0.2.2:3000",
		"The host address for the lagoon API.")
	flag.StringVar(&lagoonSSHHost, "lagoon-ssh-host", "ssh.lagoon.svc",
		"The host address for the Lagoon SSH service, or ssh-portal service.")
	flag.StringVar(&lagoonSSHPort, "lagoon-ssh-port", "2020",
		"The port for the Lagoon SSH service, or ssh-portal service.")
	flag.StringVar(&lagoonTokenHost, "lagoon-token-host", "ssh.lagoon.svc",
		"The host address for the Lagoon Token service.")
	flag.StringVar(&lagoonTokenPort, "lagoon-token-port", "2020",
		"The port for the Lagoon Token service.")
	// @TODO: Nothing uses this at the moment, but could be use in the future by controllers
	flag.BoolVar(&enableDebug, "enable-debug", false,
		"Flag to enable more verbose debugging logs.")
	flag.StringVar(&fastlyServiceID, "fastly-service-id", "",
		"The service ID that should be added to any ingresses to use the lagoon no-cache service for this cluster.")
	flag.BoolVar(&fastlyWatchStatus, "fastly-watch-status", false,
		"Flag to determine if the fastly.amazee.io/watch status should be added to any ingresses to use the lagoon no-cache service for this cluster.")
	flag.UintVar(&buildPodRunAsUser, "build-pod-run-as-user", 0, "The build pod security context runAsUser.")
	flag.UintVar(&buildPodRunAsGroup, "build-pod-run-as-group", 0, "The build pod security context runAsGroup.")
	flag.UintVar(&buildPodFSGroup, "build-pod-fs-group", 0, "The build pod security context fsGroup.")
	flag.StringVar(&backupDefaultSchedule, "backup-default-schedule", "M H(22-2) * * *",
		"The default backup schedule for all projects on this cluster.")
	flag.StringVar(&backupDefaultDevelopmentSchedule, "backup-default-dev-schedule", "",
		"The default backup schedule for all devlopment environments on this cluster.")
	flag.StringVar(&backupDefaultPullrequestSchedule, "backup-default-pr-schedule", "",
		"The default backup schedule for all pullrequest environments on this cluster.")
	flag.StringVar(&backupDefaultDevelopmentRetention, "backup-default-dev-retention", "",
		"The default backup retention for all devlopment environments on this cluster (H:D:W:M).")
	flag.StringVar(&backupDefaultPullrequestRetention, "backup-default-pr-retention", "",
		"The default backup retention for all pullrequest environments on this cluster (H:D:W:M).")
	flag.IntVar(&backupDefaultMonthlyRetention, "backup-default-monthly-retention", 1,
		"The number of monthly backups k8up should retain after a prune operation.")
	flag.IntVar(&backupDefaultWeeklyRetention, "backup-default-weekly-retention", 6,
		"The number of weekly backups k8up should retain after a prune operation.")
	flag.IntVar(&backupDefaultDailyRetention, "backup-default-daily-retention", 7,
		"The number of daily backups k8up should retain after a prune operation.")
	flag.IntVar(&backupDefaultHourlyRetention, "backup-default-hourly-retention", 0,
		"The number of hourly backups k8up should retain after a prune operation.")
	// Lagoon feature flags
	flag.StringVar(&lffForceRootlessWorkload, "lagoon-feature-flag-force-rootless-workload", "",
		"sets the LAGOON_FEATURE_FLAG_FORCE_ROOTLESS_WORKLOAD build environment variable to enforce cluster policy")
	flag.StringVar(&lffDefaultRootlessWorkload, "lagoon-feature-flag-default-rootless-workload", "",
		"sets the LAGOON_FEATURE_FLAG_DEFAULT_ROOTLESS_WORKLOAD build environment variable to control default cluster policy")
	flag.StringVar(&lffForceIsolationNetworkPolicy, "lagoon-feature-flag-force-isolation-network-policy", "",
		"sets the LAGOON_FEATURE_FLAG_FORCE_ISOLATION_NETWORK_POLICY build environment variable to enforce cluster policy")
	flag.StringVar(&lffDefaultIsolationNetworkPolicy, "lagoon-feature-flag-default-isolation-network-policy", "",
		"sets the LAGOON_FEATURE_FLAG_DEFAULT_ISOLATION_NETWORK_POLICY build environment variable to control default cluster policy")
	flag.StringVar(&lffForceInsights, "lagoon-feature-flag-force-insights", "",
		"sets the LAGOON_FEATURE_FLAG_FORCE_INSIGHTS build environment variable to enforce cluster policy")
	flag.StringVar(&lffDefaultInsights, "lagoon-feature-flag-default-insights", "",
		"sets the LAGOON_FEATURE_FLAG_DEFAULT_INSIGHTS build environment variable to control default cluster policy")
	flag.StringVar(&lffForceRWX2RWO, "lagoon-feature-flag-force-rwx-rwo", "",
		"sets the LAGOON_FEATURE_FLAG_FORCE_RWX_TO_RWO build environment variable to enforce cluster policy")
	flag.StringVar(&lffDefaultRWX2RWO, "lagoon-feature-flag-default-rwx-rwo", "",
		"sets the LAGOON_FEATURE_FLAG_DEFAULT_RWX_TO_RWO build environment variable to control default cluster policy")
	flag.BoolVar(&buildPodCleanUpEnable, "enable-build-pod-cleanup", true, "Flag to enable build pod cleanup.")
	flag.StringVar(&buildPodCleanUpCron, "build-pod-cleanup-cron", "0 * * * *",
		"The cron definition for how often to run the build pod cleanup.")
	flag.BoolVar(&buildsCleanUpEnable, "enable-lagoonbuilds-cleanup", true, "Flag to enable lagoonbuild resources cleanup.")
	flag.StringVar(&buildsCleanUpCron, "lagoonbuilds-cleanup-cron", "30 * * * *",
		"The cron definition for how often to run the lagoonbuild resources cleanup.")
	flag.IntVar(&buildsToKeep, "num-builds-to-keep", 1, "The number of lagoonbuild resources to keep per namespace.")
	flag.IntVar(&buildPodsToKeep, "num-build-pods-to-keep", 1, "The number of build pods to keep per namespace.")
	flag.BoolVar(&taskPodCleanUpEnable, "enable-task-pod-cleanup", true, "Flag to enable build pod cleanup.")
	flag.StringVar(&taskPodCleanUpCron, "task-pod-cleanup-cron", "30 * * * *",
		"The cron definition for how often to run the task pod cleanup.")
	flag.BoolVar(&taskCleanUpEnable, "enable-lagoontasks-cleanup", true, "Flag to enable lagoontask resources cleanup.")
	flag.StringVar(&taskCleanUpCron, "lagoontasks-cleanup-cron", "0 * * * *",
		"The cron definition for how often to run the lagoontask resources cleanup.")
	flag.IntVar(&tasksToKeep, "num-tasks-to-keep", 1, "The number of lagoontask resources to keep per namespace.")
	flag.IntVar(&taskPodsToKeep, "num-task-pods-to-keep", 1, "The number of task pods to keep per namespace.")
	flag.BoolVar(&lffBackupWeeklyRandom, "lagoon-feature-flag-backup-weekly-random", false,
		"Tells Lagoon whether or not to use the \"weekly-random\" schedule for k8up backups.")
	flag.BoolVar(&lffSupportK8UPv2, "lagoon-feature-flag-support-k8upv2", false,
		"Tells Lagoon whether or not it can support k8up v2.")

	flag.BoolVar(&tlsSkipVerify, "skip-tls-verify", false, "Flag to skip tls verification for http clients (harbor).")

	// default the sshkey injection to true for now, eventually Lagoon should handle this for tasks that require it
	// these are deprecated now and do nothing as the values are sourced from the lagoon task now
	flag.BoolVar(&advancedTaskSSHKeyInjection, "advanced-task-sshkey-injection", true,
		"DEPRECATED: Flag to specify injecting the sshkey for the environment into any advanced tasks.")
	flag.BoolVar(&advancedTaskDeployToken, "advanced-task-deploytoken-injection", false,
		"DEPRECATED: Flag to specify injecting the deploy token for the environment into any advanced tasks.")

	flag.BoolVar(&cleanupHarborRepositoryOnDelete, "cleanup-harbor-repository-on-delete", false,
		"Flag to specify if when deleting an environment, the associated harbor repository/images should be removed too.")

	flag.IntVar(&nativeCronPodMinFrequency, "native-cron-pod-min-frequency", 15, "The number of lagoontask resources to keep per namespace.")

	// this is enabled by default for now
	// eventually will be disabled by default because support for the generation/modification of this will
	// be handled by lagoon or the builds themselves
	flag.BoolVar(&lffRouterURL, "lagoon-feature-flag-enable-router-url", true,
		"Tells the controller to handle router-url generation or not")

	// harbor configurations
	flag.BoolVar(&lffHarborEnabled, "enable-harbor", false, "Flag to enable this controller to talk to a specific harbor.")
	flag.StringVar(&harborURL, "harbor-url", "harbor.172.17.0.1.nip.io:32080",
		"The URL for harbor, this is where images will be pushed.")
	flag.StringVar(&harborAPI, "harbor-api", "http://harbor.172.17.0.1.nip.io:32080/api/",
		"The URL for harbor API.")
	flag.StringVar(&harborUsername, "harbor-username", "admin",
		"The username for accessing harbor.")
	flag.StringVar(&harborPassword, "harbor-password", "Harbor12345",
		"The password for accessing harbor.")
	flag.StringVar(&harborRobotPrefix, "harbor-robot-prefix", "robot$",
		"The default prefix for robot accounts, will usually be \"robot$\".")
	flag.BoolVar(&harborRobotDeleteDisabled, "harbor-robot-delete-disabled", true,
		"Tells harbor to delete any disabled robot accounts and re-create them if required.")
	flag.StringVar(&harborExpiryInterval, "harbor-expiry-interval", "2d",
		"The number of days or hours (eg 24h or 30d) before expiring credentials to re-fresh.")
	flag.StringVar(&harborRotateInterval, "harbor-rotate-interval", "25d",
		"The number of days or hours (eg 24h or 30d) to force refresh if required.")
	flag.StringVar(&harborRobotAccountExpiry, "harbor-robot-account-expiry", "30d",
		"The number of days or hours (eg 24h or 30d) to set for new robot account expiration.")
	flag.StringVar(&harborCredentialCron, "harbor-credential-cron", "0 1 * * *",
		"Cron definition for how often to run harbor credential rotations")
	flag.BoolVar(&harborWebhookAdditionEnabled, "harbor-enable-project-webhook", false,
		"Tells the controller to add Lagoon webhook policies to harbor projects when creating or updating.")
	flag.StringVar(&harborLagoonWebhook, "harbor-lagoon-webhook", "http://webhook.172.17.0.1.nip.io:32080",
		"The webhook URL to add for Lagoon, this is where events notifications will be posted.")
	flag.StringVar(&harborWebhookEventTypes, "harbor-webhook-eventtypes", "SCANNING_FAILED,SCANNING_COMPLETED",
		"The event types to use for the Lagoon webhook")

	// this is for legacy reasons, and backwards compatability support due to removal of https://github.com/uselagoon/lagoon/pull/3659
	flag.StringVar(&unauthenticatedRegistry, "unauthenticated-registry", "registry.lagoon.svc:5000", "An unauthenticated registry URL that could be used for local testing without a harbor")

	// NS cleanup configuration
	flag.BoolVar(&cleanNamespacesEnabled, "enable-namespace-cleanup", false,
		"Tells the controller to remove namespaces marked for deletion with labels (lagoon.sh/expiration=<unixtimestamp>).")
	flag.StringVar(&cleanNamespacesCron, "namespace-cleanup-cron", "30 * * * *",
		"The cron definition for how often to run the namespace resources cleanup.")

	// LongRuning Worker Pod Timeout config
	flag.StringVar(&pruneLongRunningPodsCron, "longrunning-pod-cleanup-cron", "30 * * * *",
		"The cron definition for how often to run the long running Task/Build cleanup process.")
	flag.BoolVar(&pruneLongRunningBuildPods, "enable-longrunning-build-pod-cleanup", true,
		"Tells the controller to remove Build pods that have been running for too long.")
	flag.BoolVar(&pruneLongRunningTaskPods, "enable-longrunning-task-pod-cleanup", true,
		"Tells the controller to remove Task pods that have been running for too long.")
	flag.IntVar(&timeoutForLongRunningBuildPods, "timeout-longrunning-build-pod-cleanup", 6, "How many hours a build pod should run before forcefully closed.")
	flag.IntVar(&timeoutForLongRunningTaskPods, "timeout-longrunning-task-pod-cleanup", 6, "How many hours a task pod should run before forcefully closed.")

	// Build QoS configuration
	flag.BoolVar(&lffQoSEnabled, "enable-qos", false, "Flag to enable this controller with QoS for builds.")
	// this flag remains the same, the number of max builds flag remains unchanged to be backwards compatible
	flag.IntVar(&qosMaxContainerBuilds, "qos-max-builds", 20, "The total number of builds during the container build phase that can run at any one time.")
	// this new flag is added but defaults to 0, if it is greater than `qos-max-builds` then it will be used, otherwise it will default to the value of `qos-max-builds`
	flag.IntVar(&qosTotalBuilds, "qos-total-builds", 0, "The total number of builds that can run at any one time. Defaults to qos-max-builds if not provided or less than qos-max-builds.")
	flag.IntVar(&qosDefaultPriority, "qos-default", 5, "The default qos priority value to apply if one is not provided.")

	// Task QoS configuration
	flag.BoolVar(&lffTaskQoSEnabled, "enable-task-qos", false, "Flag to enable this controller with QoS for tasks.")
	flag.IntVar(&qosMaxTasks, "qos-max-tasks", 200, "The total number of tasks that can run at any one time.")
	flag.IntVar(&qosMaxNamespaceTasks, "qos-max-namespace-tasks", 20, "The total number of tasks that can run at any one time.")

	// flags to change the image pull policy used for tasks and builds
	// defaults to Always, can change to another option as required. tests use IfNotPresent
	// these flags are used for stability in testing, in actual production use you should never change these.
	flag.StringVar(&taskImagePullPolicy, "task-image-pull-policy", "Always",
		"The image pull policy to use for tasks. Changing this can have a negative impact and result in tasks failing.")
	flag.StringVar(&buildImagePullPolicy, "build-image-pull-policy", "Always",
		"The image pull policy to use for builds. Changing this can have a negative impact and result in builds failing.")

	// If installing this controller from scratch, deprecated APIs should not be configured
	flag.BoolVar(&enableDeprecatedAPIs, "enable-deprecated-apis", false, "Flag to have this controller enable support for deprecated APIs.")

	// Use a different proxy to what this pod is started with
	flag.BoolVar(&enablePodProxy, "enable-pod-proxy", false,
		"Flag to have this controller inject proxy variables to build and task pods.")
	flag.BoolVar(&podsUseDifferentProxy, "pods-use-different-proxy", false,
		"Flag to have this controller provide different proxy configuration to build pods.\nUse LAGOON_HTTP_PROXY, LAGOON_HTTPS_PROXY, and LAGOON_NO_PROXY when using this flag")

	// the number of attempts for cleaning up pvcs in a namespace default 30 attempts, 10 seconds apart (300 seconds total)
	flag.IntVar(&pvcRetryAttempts, "delete-pvc-retry-attempts", 30, "How many attempts to check that PVCs have been removed (default 30).")
	flag.IntVar(&pvcRetryInterval, "delete-pvc-retry-interval", 10, "The number of seconds between each retry attempt (default 10).")

	flag.StringVar(&dockerHostNamespace, "docker-host-namespace", "lagoon", "The name of the docker-host namespace")
	flag.StringVar(&dockerHostReuseType, "docker-host-reuse-type", "namespace",
		`The resource type (namespace, project, organization) to use when assigning a docker-host to a build to preference an already used dockerhost
		eg. If project is defined, all builds from a project will prefer to build on the same docker-host where possible`)

	// Flag to control the setting for the label cluster-autoscaler.kubernetes.io/safe-to-evict on build pods, defaults to false to avoid evicting pods
	flag.BoolVar(&clusterAutoscalerEvict, "enable-cluster-autoscaler-eviction", false,
		"Flag to enable cluster autoscaler eviction on build pods, defaults to false to avoid evicting running builds")

	flag.Parse()

	// get overrides from environment variables
	mqUser = helpers.GetEnv("RABBITMQ_USERNAME", mqUser)
	mqPass = helpers.GetEnv("RABBITMQ_PASSWORD", mqPass)
	mqHost = helpers.GetEnv("RABBITMQ_HOSTNAME", mqHost)
	mqTLS = helpers.GetEnvBool("RABBITMQ_TLS", mqTLS)
	mqCACert = helpers.GetEnv("RABBITMQ_CACERT", mqCACert)
	mqClientCert = helpers.GetEnv("RABBITMQ_CLIENTCERT", mqClientCert)
	mqClientKey = helpers.GetEnv("RABBITMQ_CLIENTKEY", mqClientKey)
	mqVerify = helpers.GetEnvBool("RABBITMQ_VERIFY", mqVerify)
	lagoonTargetName = helpers.GetEnv("LAGOON_TARGET_NAME", lagoonTargetName)
	overrideBuildDeployImage = helpers.GetEnv("OVERRIDE_BUILD_DEPLOY_DIND_IMAGE", overrideBuildDeployImage)
	namespacePrefix = helpers.GetEnv("NAMESPACE_PREFIX", namespacePrefix)
	if len(namespacePrefix) > 8 {
		// truncate the namespace prefix to 8 characters so that a really long prefix
		// does not become a problem, and namespaces are still somewhat identifiable.
		setupLog.Info(fmt.Sprintf("provided namespace prefix exceeds 8 characters, truncating prefix to %s", namespacePrefix[0:8]))
		namespacePrefix = namespacePrefix[0:8]
	}
	// controllerNamespace is used to label all resources created by this controller
	// this is to ensure that if multiple controllers are running, they only watch ones that are labelled accordingly
	// this is also used by the controller as the namespace that resources will be created in initially when received by the queue
	// this can be defined using `valueFrom.fieldRef.fieldPath: metadata.namespace` in any deployments to get the
	// namespace from where the controller is running
	controllerNamespace = helpers.GetEnv("CONTROLLER_NAMESPACE", controllerNamespace)
	if controllerNamespace == "" {
		setupLog.Error(fmt.Errorf("controller-namespace is empty"), "unable to start manager")
		os.Exit(1)
	}
	// LAGOON_CONFIG_X are the supported path now
	lagoonAPIHost = helpers.GetEnv("LAGOON_CONFIG_API_HOST", lagoonAPIHost)
	lagoonTokenHost = helpers.GetEnv("LAGOON_CONFIG_TOKEN_HOST", lagoonTokenHost)
	lagoonTokenPort = helpers.GetEnv("LAGOON_CONFIG_TOKEN_PORT", lagoonTokenPort)
	lagoonSSHHost = helpers.GetEnv("LAGOON_CONFIG_SSH_HOST", lagoonSSHHost)
	lagoonSSHPort = helpers.GetEnv("LAGOON_CONFIG_SSH_PORT", lagoonSSHPort)
	// TODO: deprecate TASK_X variables
	lagoonAPIHost = helpers.GetEnv("TASK_API_HOST", lagoonAPIHost)
	lagoonTokenHost = helpers.GetEnv("TASK_SSH_HOST", lagoonTokenHost)
	lagoonTokenPort = helpers.GetEnv("TASK_SSH_PORT", lagoonTokenPort)

	nativeCronPodMinFrequency = helpers.GetEnvInt("NATIVE_CRON_POD_MINIMUM_FREQUENCY", nativeCronPodMinFrequency)

	unauthenticatedRegistry = helpers.GetEnv("UNAUTHENTICATED_REGISTRY", unauthenticatedRegistry)

	// harbor envvars
	harborURL = helpers.GetEnv("HARBOR_URL", harborURL)
	harborAPI = helpers.GetEnv("HARBOR_API", harborAPI)
	harborUsername = helpers.GetEnv("HARBOR_USERNAME", harborUsername)
	harborPassword = helpers.GetEnv("HARBOR_PASSWORD", harborPassword)
	harborRobotPrefix = helpers.GetEnv("HARBOR_ROBOT_PREFIX", harborRobotPrefix)
	harborWebhookAdditionEnabled = helpers.GetEnvBool("HARBOR_WEBHOOK_ADDITION_ENABLED", harborWebhookAdditionEnabled)
	harborLagoonWebhook = helpers.GetEnv("HARBOR_LAGOON_WEBHOOK", harborLagoonWebhook)
	harborWebhookEventTypes = helpers.GetEnv("HARBOR_WEBHOOK_EVENTTYPES", harborWebhookEventTypes)
	harborRobotDeleteDisabled = helpers.GetEnvBool("HARBOR_ROBOT_DELETE_DISABLED", harborRobotDeleteDisabled)
	harborExpiryInterval = helpers.GetEnv("HARBOR_EXPIRY_INTERVAL", harborExpiryInterval)
	harborRotateInterval = helpers.GetEnv("HARBOR_ROTATE_INTERVAL", harborRotateInterval)
	harborRobotAccountExpiry = helpers.GetEnv("HARBOR_ROTATE_ACCOUNT_EXPIRY", harborRobotAccountExpiry)

	harborExpiryIntervalDuration, err := str2duration.ParseDuration(harborExpiryInterval)
	if err != nil {
		setupLog.Error(fmt.Errorf("harbor-expiry-interval unable to convert to duration"), "unable to start manager")
		os.Exit(1)
	}
	harborRotateIntervalDuration, err := str2duration.ParseDuration(harborRotateInterval)
	if err != nil {
		setupLog.Error(fmt.Errorf("harbor-rotate-interval unable to convert to duration"), "unable to start manager")
		os.Exit(1)
	}
	harborRobotAccountExpiryDuration, err := str2duration.ParseDuration(harborRobotAccountExpiry)
	if err != nil {
		setupLog.Error(fmt.Errorf("harbor-robot-account-expiry unable to convert to duration"), "unable to start manager")
		os.Exit(1)
	}

	// Fastly configuration options
	// the service id should be that for the cluster which will be used as the default no-cache passthrough
	fastlyServiceID = helpers.GetEnv("FASTLY_SERVICE_ID", fastlyServiceID)
	// this is used to control setting the service id into build pods
	fastlyWatchStatus = helpers.GetEnvBool("FASTLY_WATCH_STATUS", fastlyWatchStatus)

	dockerHostNamespace = helpers.GetEnv("DOCKERHOST_NAMESPACE", dockerHostNamespace)
	dockerHostReuseType = helpers.GetEnv("DOCKERHOST_REUSE_TYPE", dockerHostReuseType)

	if enablePodProxy {
		httpProxy = helpers.GetEnv("HTTP_PROXY", httpProxy)
		httpsProxy = helpers.GetEnv("HTTPS_PROXY", httpsProxy)
		noProxy = helpers.GetEnv("NO_PROXY", noProxy)
		if podsUseDifferentProxy {
			httpProxy = helpers.GetEnv("LAGOON_HTTP_PROXY", httpProxy)
			httpsProxy = helpers.GetEnv("LAGOON_HTTPS_PROXY", httpsProxy)
			noProxy = helpers.GetEnv("LAGOON_NO_PROXY", noProxy)
		}
	}

	ctrl.SetLogger(zap.New(func(o *zap.Options) {
		o.Development = true
	}))
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}
	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}
	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		Metrics:          metricsServerOptions,
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: leaderElectionID,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	cacheSize := helpers.GetEnvInt("CACHE_SIZE", 2000)
	// create the cancellation cache
	cache := expirable.NewLRU[string, string](cacheSize, nil, time.Minute*60)
	// create queue cache
	buildsQueueCache, _ := lru.New[string, string](cacheSize)
	// create builds cache
	buildsCache, _ := lru.New[string, string](cacheSize)
	// create tasks queue cache
	tasksQueueCache, _ := lru.New[string, string](cacheSize)
	// create tasks cache
	tasksCache, _ := lru.New[string, string](cacheSize)

	brokerDSN := fmt.Sprintf("amqp://%s:%s@%s", mqUser, mqPass, mqHost)
	if mqTLS {
		verify := "verify_none"
		if mqVerify {
			verify = "verify_peer"
		}
		brokerDSN = fmt.Sprintf("amqps://%s:%s@%s?verify=%s", mqUser, mqPass, mqHost, verify)
		if mqCACert != "" {
			brokerDSN = fmt.Sprintf("%s&cacertfile=%s", brokerDSN, mqCACert)
		}
		if mqClientCert != "" {
			brokerDSN = fmt.Sprintf("%s&certfile=%s", brokerDSN, mqClientCert)
		}
		if mqClientKey != "" {
			brokerDSN = fmt.Sprintf("%s&keyfile=%s", brokerDSN, mqClientKey)
		}
	}
	config := mq.Config{
		ReconnectDelay: time.Duration(rabbitRetryInterval) * time.Second,
		Exchanges: mq.Exchanges{
			{
				Name: "lagoon-tasks",
				Type: "direct",
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
		},
		Consumers: mq.Consumers{
			{
				Name:    "remove-queue",
				Queue:   fmt.Sprintf("lagoon-tasks:%s:remove", lagoonTargetName),
				Workers: mqWorkers,
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:    "builddeploy-queue",
				Queue:   fmt.Sprintf("lagoon-tasks:%s:builddeploy", lagoonTargetName),
				Workers: mqWorkers,
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:    "jobs-queue",
				Queue:   fmt.Sprintf("lagoon-tasks:%s:jobs", lagoonTargetName),
				Workers: mqWorkers,
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:    "misc-queue",
				Queue:   fmt.Sprintf("lagoon-tasks:%s:misc", lagoonTargetName),
				Workers: mqWorkers,
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
		},
		Queues: mq.Queues{
			{
				Name:       fmt.Sprintf("lagoon-tasks:%s:builddeploy", lagoonTargetName),
				Exchange:   "lagoon-tasks",
				RoutingKey: fmt.Sprintf("%s:builddeploy", lagoonTargetName),
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:       fmt.Sprintf("lagoon-tasks:%s:remove", lagoonTargetName),
				Exchange:   "lagoon-tasks",
				RoutingKey: fmt.Sprintf("%s:remove", lagoonTargetName),
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:       fmt.Sprintf("lagoon-tasks:%s:jobs", lagoonTargetName),
				Exchange:   "lagoon-tasks",
				RoutingKey: fmt.Sprintf("%s:jobs", lagoonTargetName),
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			}, {
				Name:       fmt.Sprintf("lagoon-tasks:%s:misc", lagoonTargetName),
				Exchange:   "lagoon-tasks",
				RoutingKey: fmt.Sprintf("%s:misc", lagoonTargetName),
				Options: mq.Options{
					"durable":       true,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
		},
		Producers: mq.Producers{
			{
				Name:     "lagoon-logs",
				Exchange: "lagoon-logs",
				Options: mq.Options{
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
			{
				Name:       "lagoon-tasks:controller",
				Exchange:   "lagoon-tasks",
				RoutingKey: "controller",
				Options: mq.Options{
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
		},
		DSN: brokerDSN,
	}

	harborURLParsed, _ := url.Parse(harborURL)
	harborHostname := harborURLParsed.Host
	if harborURLParsed.Host == "" {
		harborHostname = harborURL
	}
	harborConfig := harbor.Harbor{
		URL:                   harborURL,
		Hostname:              harborHostname,
		API:                   harborAPI,
		Username:              harborUsername,
		Password:              harborPassword,
		RobotPrefix:           harborRobotPrefix,
		ExpiryInterval:        harborExpiryIntervalDuration,
		RotateInterval:        harborRotateIntervalDuration,
		DeleteDisabled:        harborRobotDeleteDisabled,
		RobotAccountExpiry:    harborRobotAccountExpiryDuration,
		WebhookAddition:       harborWebhookAdditionEnabled,
		ControllerNamespace:   controllerNamespace,
		NamespacePrefix:       namespacePrefix,
		RandomNamespacePrefix: randomPrefix,
		WebhookURL:            harborLagoonWebhook,
		LagoonTargetName:      lagoonTargetName,
		WebhookEventTypes:     strings.Split(harborWebhookEventTypes, ","),
		TLSSkipVerify:         tlsSkipVerify,
	}

	deletion := deletions.New(mgr.GetClient(),
		harborConfig,
		deletions.DeleteConfig{
			PVCRetryAttempts: pvcRetryAttempts,
			PVCRetryInterval: pvcRetryInterval,
		},
		cleanupHarborRepositoryOnDelete,
		enableDebug,
	)

	messaging := messenger.New(config,
		mgr.GetClient(),
		mgr.GetAPIReader(),
		startupConnectionAttempts,
		startupConnectionInterval,
		controllerNamespace,
		namespacePrefix,
		randomPrefix,
		advancedTaskSSHKeyInjection,
		advancedTaskDeployToken,
		deletion,
		enableDebug,
		lffSupportK8UPv2,
		cache,
		harborConfig,
		lagoonTargetName,
	)

	reuseCache, _ := lru.New[string, string](cacheSize)
	buildCache, _ := lru.New[string, string](cacheSize)
	dockerhostResuseTypes := map[string]bool{
		"namespace":    true,
		"project":      true,
		"organization": true,
	}
	if !dockerhostResuseTypes[dockerHostReuseType] {
		setupLog.Error(fmt.Errorf("unsupported docker-host reuse type"), "problem configuring docker-host handler")
		os.Exit(1)
	}
	dockerhosts := dockerhost.New(
		mgr.GetClient(),
		ctrl.Log.WithName("dockerhost"),
		dockerHostNamespace,
		dockerHostReuseType,
		reuseCache,
		buildCache,
	)
	c := cron.New()
	// if we are running with MQ support, then start the consumer handler

	if enableMQ {
		setupLog.Info("starting messaging handler")
		go messaging.Consumer(lagoonTargetName)
	}

	// this ensures that the max number of builds is not less than the container builds support
	if qosTotalBuilds < qosMaxContainerBuilds {
		qosTotalBuilds = qosMaxContainerBuilds
	}
	buildQoSConfigv1beta2 := lagoonv1beta2ctrl.BuildQoS{
		TotalBuilds:        qosTotalBuilds,
		MaxContainerBuilds: qosMaxContainerBuilds,
		DefaultPriority:    qosDefaultPriority,
	}

	taskQoSConfigv1beta2 := lagoonv1beta2ctrl.TaskQoS{
		MaxTasks:          qosMaxTasks,
		MaxNamespaceTasks: qosMaxNamespaceTasks,
	}

	tipp := corev1.PullAlways
	switch taskImagePullPolicy {
	case "IfNotPresent":
		tipp = corev1.PullIfNotPresent
	case "Never":
		tipp = corev1.PullNever
	}
	bipp := corev1.PullAlways
	switch buildImagePullPolicy {
	case "IfNotPresent":
		bipp = corev1.PullIfNotPresent
	case "Never":
		bipp = corev1.PullNever
	}

	resourceCleanup := pruner.New(mgr.GetClient(),
		mgr.GetAPIReader(),
		buildsToKeep,
		buildPodsToKeep,
		tasksToKeep,
		taskPodsToKeep,
		controllerNamespace,
		deletion,
		timeoutForLongRunningBuildPods,
		timeoutForLongRunningTaskPods,
		enableDebug,
	)
	// if the lagoonbuild cleanup is enabled, add the cronjob for it
	if buildsCleanUpEnable {
		setupLog.Info("starting LagoonBuild CRD cleanup handler")
		// use cron to run a lagoonbuild cleanup task
		// this will check any Lagoon builds and attempt to delete them
		_, err := c.AddFunc(buildsCleanUpCron, func() {
			lagoonv1beta2.LagoonBuildPruner(context.Background(), mgr.GetClient(), controllerNamespace, buildsToKeep)
		})
		if err != nil {
			setupLog.Error(err, "unable to create LagoonBuild CRD cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}
	// if the build pod cleanup is enabled, add the cronjob for it
	if buildPodCleanUpEnable {
		setupLog.Info("starting build pod cleanup handler")
		// use cron to run a build pod cleanup task
		// this will check any Lagoon build pods and attempt to delete them
		_, err := c.AddFunc(buildPodCleanUpCron, func() {
			lagoonv1beta2.BuildPodPruner(context.Background(), mgr.GetClient(), controllerNamespace, buildPodsToKeep)
		})
		if err != nil {
			setupLog.Error(err, "unable to create build pod cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}
	// if the lagoontask cleanup is enabled, add the cronjob for it
	if taskCleanUpEnable {
		setupLog.Info("starting LagoonTask CRD cleanup handler")
		// use cron to run a lagoontask cleanup task
		// this will check any Lagoon tasks and attempt to delete them
		_, err := c.AddFunc(taskCleanUpCron, func() {
			lagoonv1beta2.LagoonTaskPruner(context.Background(), mgr.GetClient(), controllerNamespace, tasksToKeep)
		})
		if err != nil {
			setupLog.Error(err, "unable to create LagoonTask CRD cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}
	// if the task pod cleanup is enabled, add the cronjob for it
	if taskPodCleanUpEnable {
		setupLog.Info("starting task pod cleanup handler")
		// use cron to run a task pod cleanup task
		// this will check any Lagoon task pods and attempt to delete them
		_, err := c.AddFunc(taskPodCleanUpCron, func() {
			lagoonv1beta2.TaskPodPruner(context.Background(), mgr.GetClient(), controllerNamespace, taskPodsToKeep)
		})
		if err != nil {
			setupLog.Error(err, "unable to create task pod cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}
	// if harbor is enabled, add the cronjob for credential rotation
	if lffHarborEnabled {
		setupLog.Info("starting harbor robot credential rotation task")
		// use cron to run a task pod cleanup task
		// this will check any Lagoon task pods and attempt to delete them
		_, err := c.AddFunc(harborCredentialCron, func() {
			lagoonHarbor, _ := harbor.New(harborConfig)
			lagoonHarbor.RotateRobotCredentials(context.Background(), mgr.GetClient())
		})
		if err != nil {
			setupLog.Error(err, "unable to create harbor robot credential cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}

	// if we've set namespaces to be cleaned up, we run the job periodically
	if cleanNamespacesEnabled {
		setupLog.Info("starting namespace cleanup task")
		_, err := c.AddFunc(taskPodCleanUpCron, func() {
			resourceCleanup.NamespacePruner()
		})
		if err != nil {
			setupLog.Error(err, "unable to create namespace cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}

	if pruneLongRunningTaskPods || pruneLongRunningBuildPods {
		setupLog.Info("starting long running task cleanup task")
		_, err := c.AddFunc(pruneLongRunningPodsCron, func() {
			resourceCleanup.LagoonOldProcPruner(pruneLongRunningBuildPods, pruneLongRunningTaskPods)
		})
		if err != nil {
			setupLog.Error(err, "unable to create long running pod cleanup cronjob", "controller", "LagoonTask")
			os.Exit(1)
		}
	}

	c.Start()

	// pre-seed the queues with the current state of builds
	if err := lagoonv1beta2.SeedBuildStartup(ctrl.GetConfigOrDie(), scheme, controllerNamespace, qosDefaultPriority, buildsCache, buildsQueueCache); err != nil {
		setupLog.Error(err, "unable to seed controller startup state")
	}
	// pre-seed the queues with the current state of tasks
	if err := lagoonv1beta2.SeedTaskStartup(ctrl.GetConfigOrDie(), scheme, controllerNamespace, tasksCache, tasksQueueCache); err != nil {
		setupLog.Error(err, "unable to seed controller startup state")
	}

	setupLog.Info("starting build controller")
	// v1beta2 is the latest version
	if err = (&lagoonv1beta2ctrl.LagoonBuildReconciler{
		Client:                mgr.GetClient(),
		APIReader:             mgr.GetAPIReader(),
		Log:                   ctrl.Log.WithName("v1beta2").WithName("LagoonBuild"),
		Scheme:                mgr.GetScheme(),
		EnableMQ:              enableMQ,
		BuildImage:            overrideBuildDeployImage,
		Messaging:             messaging,
		NamespacePrefix:       namespacePrefix,
		RandomNamespacePrefix: randomPrefix,
		ControllerNamespace:   controllerNamespace,
		EnableDebug:           enableDebug,
		FastlyServiceID:       fastlyServiceID,
		FastlyWatchStatus:     fastlyWatchStatus,
		BuildPodRunAsUser:     int64(buildPodRunAsUser),
		BuildPodRunAsGroup:    int64(buildPodRunAsGroup),
		BuildPodFSGroup:       int64(buildPodFSGroup),
		BackupConfig: lagoonv1beta2ctrl.BackupConfig{
			BackupDefaultSchedule:             backupDefaultSchedule,
			BackupDefaultDevelopmentSchedule:  backupDefaultDevelopmentSchedule,
			BackupDefaultPullrequestSchedule:  backupDefaultPullrequestSchedule,
			BackupDefaultDevelopmentRetention: backupDefaultDevelopmentRetention,
			BackupDefaultPullrequestRetention: backupDefaultPullrequestRetention,
			BackupDefaultMonthlyRetention:     backupDefaultMonthlyRetention,
			BackupDefaultWeeklyRetention:      backupDefaultWeeklyRetention,
			BackupDefaultDailyRetention:       backupDefaultDailyRetention,
			BackupDefaultHourlyRetention:      backupDefaultHourlyRetention,
		},
		// Lagoon feature flags
		LFFForceRootlessWorkload:         lffForceRootlessWorkload,
		LFFDefaultRootlessWorkload:       lffDefaultRootlessWorkload,
		LFFForceIsolationNetworkPolicy:   lffForceIsolationNetworkPolicy,
		LFFDefaultIsolationNetworkPolicy: lffDefaultIsolationNetworkPolicy,
		LFFForceInsights:                 lffForceInsights,
		LFFDefaultInsights:               lffDefaultInsights,
		LFFForceRWX2RWO:                  lffForceRWX2RWO,
		LFFDefaultRWX2RWO:                lffDefaultRWX2RWO,
		LFFBackupWeeklyRandom:            lffBackupWeeklyRandom,
		LFFRouterURL:                     lffRouterURL,
		LFFHarborEnabled:                 lffHarborEnabled,
		Harbor:                           harborConfig,
		LFFQoSEnabled:                    lffQoSEnabled,
		BuildQoS:                         buildQoSConfigv1beta2,
		NativeCronPodMinFrequency:        nativeCronPodMinFrequency,
		LagoonTargetName:                 lagoonTargetName,
		LagoonFeatureFlags:               helpers.GetLagoonFeatureFlags(),
		LagoonAPIConfiguration: helpers.LagoonAPIConfiguration{
			APIHost:   lagoonAPIHost,
			TokenHost: lagoonTokenHost,
			TokenPort: lagoonTokenPort,
			SSHHost:   lagoonSSHHost,
			SSHPort:   lagoonSSHPort,
		},
		ProxyConfig: lagoonv1beta2ctrl.ProxyConfig{
			HTTPProxy:  httpProxy,
			HTTPSProxy: httpsProxy,
			NoProxy:    noProxy,
		},
		UnauthenticatedRegistry: unauthenticatedRegistry,
		ImagePullPolicy:         bipp,
		DockerHost:              dockerhosts,
		QueueCache:              buildsQueueCache,
		BuildCache:              buildsCache,
		ClusterAutoscalerEvict:  clusterAutoscalerEvict,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonBuild")
		os.Exit(1)
	}
	setupLog.Info("starting task controller")
	if err = (&lagoonv1beta2ctrl.LagoonTaskReconciler{
		Client:                mgr.GetClient(),
		Log:                   ctrl.Log.WithName("v1beta2").WithName("LagoonTask"),
		Scheme:                mgr.GetScheme(),
		EnableMQ:              enableMQ,
		Messaging:             messaging,
		ControllerNamespace:   controllerNamespace,
		NamespacePrefix:       namespacePrefix,
		RandomNamespacePrefix: randomPrefix,
		LagoonAPIConfiguration: helpers.LagoonAPIConfiguration{
			APIHost:   lagoonAPIHost,
			TokenHost: lagoonTokenHost,
			TokenPort: lagoonTokenPort,
			SSHHost:   lagoonSSHHost,
			SSHPort:   lagoonSSHPort,
		},
		EnableDebug:      enableDebug,
		LagoonTargetName: lagoonTargetName,
		ProxyConfig: lagoonv1beta2ctrl.ProxyConfig{
			HTTPProxy:  httpProxy,
			HTTPSProxy: httpsProxy,
			NoProxy:    noProxy,
		},
		LFFTaskQoSEnabled:      lffTaskQoSEnabled,
		TaskQoS:                taskQoSConfigv1beta2,
		ImagePullPolicy:        tipp,
		QueueCache:             tasksQueueCache,
		TasksCache:             tasksCache,
		ClusterAutoscalerEvict: clusterAutoscalerEvict,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonTask")
		os.Exit(1)
	}
	setupLog.Info("starting build pod monitor controller")
	if err = (&lagoonv1beta2ctrl.BuildMonitorReconciler{
		Client:                mgr.GetClient(),
		APIReader:             mgr.GetAPIReader(),
		Log:                   ctrl.Log.WithName("v1beta2").WithName("LagoonBuildPodMonitor"),
		Scheme:                mgr.GetScheme(),
		EnableMQ:              enableMQ,
		Messaging:             messaging,
		ControllerNamespace:   controllerNamespace,
		NamespacePrefix:       namespacePrefix,
		RandomNamespacePrefix: randomPrefix,
		EnableDebug:           enableDebug,
		LagoonTargetName:      lagoonTargetName,
		LFFQoSEnabled:         lffQoSEnabled,
		BuildQoS:              buildQoSConfigv1beta2,
		Cache:                 cache,
		DockerHost:            dockerhosts,
		QueueCache:            buildsQueueCache,
		BuildCache:            buildsCache,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonBuildPodMonitor")
		os.Exit(1)
	}
	setupLog.Info("starting task pod monitor controller")
	if err = (&lagoonv1beta2ctrl.TaskMonitorReconciler{
		Client:                mgr.GetClient(),
		Log:                   ctrl.Log.WithName("v1beta2").WithName("LagoonTaskPodMonitor"),
		Scheme:                mgr.GetScheme(),
		EnableMQ:              enableMQ,
		Messaging:             messaging,
		ControllerNamespace:   controllerNamespace,
		NamespacePrefix:       namespacePrefix,
		RandomNamespacePrefix: randomPrefix,
		EnableDebug:           enableDebug,
		LagoonTargetName:      lagoonTargetName,
		Cache:                 cache,
		QueueCache:            tasksQueueCache,
		TasksCache:            tasksCache,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonTaskPodMonitor")
		os.Exit(1)
	}

	// for now the namespace reconciler only needs to run if harbor is enabled so that we can watch the namespace for rotation label events
	if lffHarborEnabled {
		if err = (&harborctrl.HarborCredentialReconciler{
			Client:              mgr.GetClient(),
			APIReader:           mgr.GetAPIReader(),
			Log:                 ctrl.Log.WithName("harbor").WithName("HarborCredentialReconciler"),
			Scheme:              mgr.GetScheme(),
			LFFHarborEnabled:    lffHarborEnabled,
			ControllerNamespace: controllerNamespace,
			Harbor:              harborConfig,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "HarborCredentialReconciler")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
