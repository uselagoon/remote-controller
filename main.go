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
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/controllers"
	"github.com/amazeeio/lagoon-kbd/handlers"
	"github.com/cheshir/go-mq"
	str2duration "github.com/xhit/go-str2duration/v2"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Openshift
	oappsv1 "github.com/openshift/api/apps/v1"
	projectv1 "github.com/openshift/api/project/v1"

	"gopkg.in/robfig/cron.v2"
	// +kubebuilder:scaffold:imports
)

var (
	scheme           = runtime.NewScheme()
	setupLog         = ctrl.Log.WithName("setup")
	lagoonAppID      string
	lagoonTargetName string
	mqUser           string
	mqPass           string
	mqHost           string
	lagoonAPIHost    string
	lagoonSSHHost    string
	lagoonSSHPort    string
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = lagoonv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
	_ = projectv1.AddToScheme(scheme)
	_ = oappsv1.AddToScheme(scheme)
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableMQ bool
	var leaderElectionID string
	var pendingMessageCron string
	var mqWorkers int
	var rabbitRetryInterval int
	var startupConnectionAttempts int
	var startupConnectionInterval int
	var overrideBuildDeployImage string
	var namespacePrefix string
	var randomPrefix bool
	var isOpenshift bool
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
	// Lagoon Feature Flags options control features in Lagoon. Default options
	// set a default cluster policy, while Force options enforce a cluster policy
	// and cannot be overridden.
	var lffForceRootlessWorkload string
	var lffDefaultRootlessWorkload string
	var lffForceIsolationNetworkPolicy string
	var lffDefaultIsolationNetworkPolicy string
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

	var lffQoSEnabled bool
	var qosMaxBuilds int
	var qosDefaultValue int

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&lagoonTargetName, "lagoon-target-name", "ci-local-control-k8s",
		"The name of the target as it is in lagoon.")
	flag.StringVar(&lagoonAppID, "lagoon-app-id", "builddeploymonitor",
		"The appID to use that will be sent with messages.")
	flag.StringVar(&mqUser, "rabbitmq-username", "guest",
		"The username of the rabbitmq user.")
	flag.StringVar(&mqPass, "rabbitmq-password", "guest",
		"The password for the rabbitmq user.")
	flag.StringVar(&mqHost, "rabbitmq-hostname", "localhost:5672",
		"The hostname:port for the rabbitmq host.")
	flag.IntVar(&mqWorkers, "rabbitmq-queue-workers", 1,
		"The number of workers to start with.")
	flag.IntVar(&rabbitRetryInterval, "rabbitmq-retry-interval", 30,
		"The retry interval for rabbitmq.")
	flag.StringVar(&leaderElectionID, "leader-election-id", "lagoon-builddeploy-leader-election-helper",
		"The ID to use for leader election.")
	flag.StringVar(&pendingMessageCron, "pending-message-cron", "*/5 * * * *",
		"The cron definition for pending messages.")
	flag.IntVar(&startupConnectionAttempts, "startup-connection-attempts", 10,
		"The number of startup attempts before exiting.")
	flag.IntVar(&startupConnectionInterval, "startup-connection-interval-seconds", 30,
		"The duration between startup attempts.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableMQ, "enable-message-queue", true,
		"Enable message queue to provide updates back to Lagoon.")
	flag.StringVar(&overrideBuildDeployImage, "override-builddeploy-image", "uselagoon/kubectl-build-deploy-dind:latest",
		"The build and deploy image that should be used by builds started by the controller.")
	flag.BoolVar(&isOpenshift, "is-openshift", false,
		"Flag to determine if the controller is running in an openshift.")
	flag.StringVar(&namespacePrefix, "namespace-prefix", "",
		"The prefix that will be added to all namespaces that are generated, maximum 8 characters. (only used if random-prefix is set false)")
	flag.BoolVar(&randomPrefix, "random-prefix", false,
		"Flag to determine if the all namespaces should be prefixed with 5 random characters.")
	flag.StringVar(&controllerNamespace, "controller-namespace", "",
		"The name of the namespace the controller is deployed in.")
	flag.StringVar(&lagoonAPIHost, "lagoon-api-host", "http://10.0.2.2:3000",
		"The host address for the lagoon API.")
	flag.StringVar(&lagoonSSHHost, "lagoon-ssh-host", "ssh.lagoon.svc",
		"The host address for the Lagoon SSH service.")
	flag.StringVar(&lagoonSSHPort, "lagoon-ssh-port", "2020",
		"The port for the Lagoon SSH service.")
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
	flag.BoolVar(&buildPodCleanUpEnable, "enable-build-pod-cleanup", true, "Flag to enable build pod cleanup.")
	flag.StringVar(&buildPodCleanUpCron, "build-pod-cleanup-cron", "0 * * * *",
		"The cron definition for how often to run the build pod cleanup.")
	flag.BoolVar(&buildsCleanUpEnable, "enable-lagoonbuilds-cleanup", true, "Flag to enable lagoonbuild resources cleanup.")
	flag.StringVar(&buildsCleanUpCron, "lagoonbuilds-cleanup-cron", "0 * * * *",
		"The cron definition for how often to run the lagoonbuild resources cleanup.")
	flag.IntVar(&buildsToKeep, "num-builds-to-keep", 5, "The number of lagoonbuild resources to keep per namespace.")
	flag.IntVar(&buildPodsToKeep, "num-build-pods-to-keep", 1, "The number of build pods to keep per namespace.")
	flag.BoolVar(&taskPodCleanUpEnable, "enable-task-pod-cleanup", true, "Flag to enable build pod cleanup.")
	flag.StringVar(&taskPodCleanUpCron, "task-pod-cleanup-cron", "30 * * * *",
		"The cron definition for how often to run the task pod cleanup.")
	flag.BoolVar(&taskCleanUpEnable, "enable-lagoontasks-cleanup", true, "Flag to enable lagoontask resources cleanup.")
	flag.StringVar(&taskCleanUpCron, "lagoontasks-cleanup-cron", "0 * * * *",
		"The cron definition for how often to run the lagoontask resources cleanup.")
	flag.IntVar(&tasksToKeep, "num-tasks-to-keep", 5, "The number of lagoontask resources to keep per namespace.")
	flag.IntVar(&taskPodsToKeep, "num-task-pods-to-keep", 1, "The number of task pods to keep per namespace.")
	flag.BoolVar(&lffBackupWeeklyRandom, "lffBackupWeeklyRandom", false,
		"Tells Lagoon whether or not to use the \"weekly-random\" schedule for k8up backups.")

	flag.IntVar(&nativeCronPodMinFrequency, "native-cron-pod-min-frequency", 15, "The number of lagoontask resources to keep per namespace.")

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
	flag.StringVar(&harborRotateInterval, "harbor-rotate-interval", "30d",
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

	// QoS configuration
	flag.BoolVar(&lffQoSEnabled, "enable-qos", false, "Flag to enable this controller with QoS for builds.")
	flag.IntVar(&qosMaxBuilds, "qos-max-builds", 20, "The number of builds that can run at any one time.")
	flag.IntVar(&qosDefaultValue, "qos-default", 5, "The default qos value to apply if one is not provided.")

	flag.Parse()

	// get overrides from environment variables
	mqUser = getEnv("RABBITMQ_USERNAME", mqUser)
	mqPass = getEnv("RABBITMQ_PASSWORD", mqPass)
	mqHost = getEnv("RABBITMQ_HOSTNAME", mqHost)
	lagoonTargetName = getEnv("LAGOON_TARGET_NAME", lagoonTargetName)
	lagoonAppID = getEnv("LAGOON_APP_ID", lagoonAppID)
	pendingMessageCron = getEnv("PENDING_MESSAGE_CRON", pendingMessageCron)
	overrideBuildDeployImage = getEnv("OVERRIDE_BUILD_DEPLOY_DIND_IMAGE", overrideBuildDeployImage)
	namespacePrefix = getEnv("NAMESPACE_PREFIX", namespacePrefix)
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
	controllerNamespace = getEnv("CONTROLLER_NAMESPACE", controllerNamespace)
	if controllerNamespace == "" {
		setupLog.Error(fmt.Errorf("controller-namespace is empty"), "unable to start manager")
		os.Exit(1)
	}
	lagoonAPIHost = getEnv("TASK_API_HOST", lagoonAPIHost)
	lagoonSSHHost = getEnv("TASK_SSH_HOST", lagoonSSHHost)
	lagoonSSHPort = getEnv("TASK_SSH_PORT", lagoonSSHPort)

	nativeCronPodMinFrequency = getEnvInt("NATIVE_CRON_POD_MINIMUM_FREQUENCY", nativeCronPodMinFrequency)

	// harbor envvars
	harborURL = getEnv("HARBOR_URL", harborURL)
	harborAPI = getEnv("HARBOR_API", harborAPI)
	harborUsername = getEnv("HARBOR_USERNAME", harborUsername)
	harborPassword = getEnv("HARBOR_PASSWORD", harborPassword)
	harborRobotPrefix = getEnv("HARBOR_ROBOT_PREFIX", harborRobotPrefix)
	harborWebhookAdditionEnabled = getEnvBool("HARBOR_WEBHOOK_ADDITION_ENABLED", harborWebhookAdditionEnabled)
	harborLagoonWebhook = getEnv("HARBOR_LAGOON_WEBHOOK", harborLagoonWebhook)
	harborWebhookEventTypes = getEnv("HARBOR_WEBHOOK_EVENTTYPES", harborWebhookEventTypes)
	harborRobotDeleteDisabled = getEnvBool("HARBOR_ROBOT_DELETE_DISABLED", harborRobotDeleteDisabled)
	harborExpiryInterval = getEnv("HARBOR_EXPIRY_INTERVAL", harborExpiryInterval)
	harborRotateInterval = getEnv("HARBOR_ROTATE_INTERVAL", harborRotateInterval)
	harborRobotAccountExpiry = getEnv("HARBOR_ROTATE_ACCOUNT_EXPIRY", harborRobotAccountExpiry)
	harborExpiryIntervalDuration := 2 * 24 * time.Hour
	harborRotateIntervalDuration := 30 * 24 * time.Hour
	harborRobotAccountExpiryDuration := 30 * 24 * time.Hour
	if lffHarborEnabled {
		var err error
		harborExpiryIntervalDuration, err = str2duration.ParseDuration(harborExpiryInterval)
		if err != nil {
			setupLog.Error(fmt.Errorf("harbor-expiry-interval unable to convert to duration"), "unable to start manager")
			os.Exit(1)
		}
		harborRotateIntervalDuration, err = str2duration.ParseDuration(harborRotateInterval)
		if err != nil {
			setupLog.Error(fmt.Errorf("harbor-rotate-interval unable to convert to duration"), "unable to start manager")
			os.Exit(1)
		}
		harborRobotAccountExpiryDuration, err = str2duration.ParseDuration(harborRobotAccountExpiry)
		if err != nil {
			setupLog.Error(fmt.Errorf("harbor-robot-account-expiry unable to convert to duration"), "unable to start manager")
			os.Exit(1)
		}
	}

	// Fastly configuration options
	// the service id should be that for the cluster which will be used as the default no-cache passthrough
	fastlyServiceID = getEnv("FASTLY_SERVICE_ID", fastlyServiceID)
	// this is used to control setting the service id into build pods
	fastlyWatchStatus = getEnvBool("FASTLY_WATCH_STATUS", fastlyWatchStatus)

	ctrl.SetLogger(zap.New(func(o *zap.Options) {
		o.Development = true
	}))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   leaderElectionID,
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
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
					"app_id":        lagoonAppID,
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
					"app_id":        lagoonAppID,
					"delivery_mode": "2",
					"headers":       "",
					"content_type":  "",
				},
			},
		},
		DSN: fmt.Sprintf("amqp://%s:%s@%s/", mqUser, mqPass, mqHost),
	}
	messaging := handlers.NewMessaging(config,
		mgr.GetClient(),
		startupConnectionAttempts,
		startupConnectionInterval,
		controllerNamespace,
		enableDebug,
	)
	c := cron.New()
	// if we are running with MQ support, then start the consumer handler
	if enableMQ {
		setupLog.Info("starting messaging handler")
		go messaging.Consumer(lagoonTargetName)

		// use cron to run a pending message task
		// this will check any `LagoonBuild` resources for the pendingMessages label
		// and attempt to re-publish them
		c.AddFunc(pendingMessageCron, func() {
			messaging.GetPendingMessages()
		})
	}

	harborURLParsed, _ := url.Parse(harborURL)
	harborHostname := harborURLParsed.Host
	if harborURLParsed.Host == "" {
		harborHostname = harborURL
	}
	harborConfig := controllers.Harbor{
		URL:                 harborURL,
		Hostname:            harborHostname,
		API:                 harborAPI,
		Username:            harborUsername,
		Password:            harborPassword,
		RobotPrefix:         harborRobotPrefix,
		ExpiryInterval:      harborExpiryIntervalDuration,
		RotateInterval:      harborRotateIntervalDuration,
		DeleteDisabled:      harborRobotDeleteDisabled,
		RobotAccountExpiry:  harborRobotAccountExpiryDuration,
		WebhookAddition:     harborWebhookAdditionEnabled,
		ControllerNamespace: controllerNamespace,
		WebhookURL:          harborLagoonWebhook,
		WebhookEventTypes:   strings.Split(harborWebhookEventTypes, ","),
	}

	buildQoSConfig := controllers.BuildQoS{
		MaxBuilds:    qosMaxBuilds,
		DefaultValue: qosDefaultValue,
	}

	resourceCleanup := handlers.NewCleanup(mgr.GetClient(),
		buildsToKeep,
		buildPodsToKeep,
		tasksToKeep,
		taskPodsToKeep,
		controllerNamespace,
		enableDebug,
	)
	// if the lagoonbuild cleanup is enabled, add the cronjob for it
	if buildsCleanUpEnable {
		setupLog.Info("starting LagoonBuild CRD cleanup handler")
		// use cron to run a lagoonbuild cleanup task
		// this will check any Lagoon builds and attempt to delete them
		c.AddFunc(buildsCleanUpCron, func() {
			resourceCleanup.LagoonBuildCleanup()
		})
	}
	// if the build pod cleanup is enabled, add the cronjob for it
	if buildPodCleanUpEnable {
		setupLog.Info("starting build pod cleanup handler")
		// use cron to run a build pod cleanup task
		// this will check any Lagoon build pods and attempt to delete them
		c.AddFunc(buildPodCleanUpCron, func() {
			resourceCleanup.BuildPodCleanup()
		})
	}
	// if the lagoontask cleanup is enabled, add the cronjob for it
	if taskCleanUpEnable {
		setupLog.Info("starting LagoonTask CRD cleanup handler")
		// use cron to run a lagoontask cleanup task
		// this will check any Lagoon tasks and attempt to delete them
		c.AddFunc(taskCleanUpCron, func() {
			resourceCleanup.LagoonTaskCleanup()
		})
	}
	// if the task pod cleanup is enabled, add the cronjob for it
	if taskPodCleanUpEnable {
		setupLog.Info("starting task pod cleanup handler")
		// use cron to run a task pod cleanup task
		// this will check any Lagoon task pods and attempt to delete them
		c.AddFunc(taskPodCleanUpCron, func() {
			resourceCleanup.TaskPodCleanup()
		})
	}
	// if harbor is enabled, add the cronjob for credential rotation
	if lffHarborEnabled {
		setupLog.Info("starting harbor robot credential rotation task")
		// use cron to run a task pod cleanup task
		// this will check any Lagoon task pods and attempt to delete them
		c.AddFunc(harborCredentialCron, func() {
			lagoonHarbor, _ := controllers.NewHarbor(harborConfig)
			lagoonHarbor.RotateRobotCredentials(context.Background(), mgr.GetClient())
		})
	}
	c.Start()

	setupLog.Info("starting controllers")
	if err = (&controllers.LagoonBuildReconciler{
		Client:                        mgr.GetClient(),
		Log:                           ctrl.Log.WithName("controllers").WithName("LagoonBuild"),
		Scheme:                        mgr.GetScheme(),
		EnableMQ:                      enableMQ,
		BuildImage:                    overrideBuildDeployImage,
		Messaging:                     messaging,
		IsOpenshift:                   isOpenshift,
		NamespacePrefix:               namespacePrefix,
		RandomNamespacePrefix:         randomPrefix,
		ControllerNamespace:           controllerNamespace,
		EnableDebug:                   enableDebug,
		FastlyServiceID:               fastlyServiceID,
		FastlyWatchStatus:             fastlyWatchStatus,
		BuildPodRunAsUser:             int64(buildPodRunAsUser),
		BuildPodRunAsGroup:            int64(buildPodRunAsGroup),
		BuildPodFSGroup:               int64(buildPodFSGroup),
		BackupDefaultSchedule:         backupDefaultSchedule,
		BackupDefaultMonthlyRetention: backupDefaultMonthlyRetention,
		BackupDefaultWeeklyRetention:  backupDefaultWeeklyRetention,
		BackupDefaultDailyRetention:   backupDefaultDailyRetention,
		BackupDefaultHourlyRetention:  backupDefaultHourlyRetention,
		// Lagoon feature flags
		LFFForceRootlessWorkload:         lffForceRootlessWorkload,
		LFFDefaultRootlessWorkload:       lffDefaultRootlessWorkload,
		LFFForceIsolationNetworkPolicy:   lffForceIsolationNetworkPolicy,
		LFFDefaultIsolationNetworkPolicy: lffDefaultIsolationNetworkPolicy,
		LFFBackupWeeklyRandom:            lffBackupWeeklyRandom,
		LFFHarborEnabled:                 lffHarborEnabled,
		Harbor:                           harborConfig,
		LFFQoSEnabled:                    lffQoSEnabled,
		BuildQoS:                         buildQoSConfig,
		NativeCronPodMinFrequency:        nativeCronPodMinFrequency,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonBuild")
		os.Exit(1)
	}
	if err = (&controllers.LagoonMonitorReconciler{
		Client:              mgr.GetClient(),
		Log:                 ctrl.Log.WithName("controllers").WithName("LagoonMonitor"),
		Scheme:              mgr.GetScheme(),
		EnableMQ:            enableMQ,
		Messaging:           messaging,
		ControllerNamespace: controllerNamespace,
		EnableDebug:         enableDebug,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonMonitor")
		os.Exit(1)
	}
	if err = (&controllers.LagoonTaskReconciler{
		Client:              mgr.GetClient(),
		Log:                 ctrl.Log.WithName("controllers").WithName("LagoonTask"),
		Scheme:              mgr.GetScheme(),
		IsOpenshift:         isOpenshift,
		ControllerNamespace: controllerNamespace,
		TaskSettings: controllers.LagoonTaskSettings{
			APIHost: lagoonAPIHost,
			SSHHost: lagoonSSHHost,
			SSHPort: lagoonSSHPort,
		},
		EnableDebug: enableDebug,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonTask")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		valueInt, e := strconv.Atoi(value)
		if e == nil {
			return valueInt
		}
	}
	return fallback
}

// accepts fallback values 1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False
// anything else is false.
func getEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		rVal, _ := strconv.ParseBool(value)
		return rVal
	}
	return fallback
}
