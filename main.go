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
	"flag"
	"fmt"
	"os"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/amazeeio/lagoon-kbd/controllers"
	"github.com/amazeeio/lagoon-kbd/handlers"
	"github.com/cheshir/go-mq"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Openshift
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
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = lagoonv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
	_ = projectv1.AddToScheme(scheme)
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
	var isOpenshift bool

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&lagoonTargetName, "lagoon-target-name", "ci-local-kubernetes",
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
		"The hostname:port for the rabbitmq host.")
	flag.IntVar(&rabbitRetryInterval, "rabbitmq-retry-interval", 30,
		"The hostname:port for the rabbitmq host.")
	flag.StringVar(&leaderElectionID, "leader-election-id", "lagoon-builddeploy-leader-election-helper",
		"The ID to use for leader election.")
	flag.StringVar(&pendingMessageCron, "pending-message-cron", "*/5 * * * *",
		"The hostname:port for the rabbitmq host.")
	flag.IntVar(&startupConnectionAttempts, "startup-connection-attempts", 10,
		"The hostname:port for the rabbitmq host.")
	flag.IntVar(&startupConnectionInterval, "startup-connection-interval-seconds", 30,
		"The hostname:port for the rabbitmq host.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableMQ, "enable-message-queue", true,
		"Enable message queue to provide updates back to Lagoon.")
	flag.StringVar(&overrideBuildDeployImage, "override-builddeploy-image", "uselagoon/kubectl-build-deploy-dind:latest",
		"The build and deploy image that should be used by builds started by the controller.")
	flag.BoolVar(&isOpenshift, "is-openshift", false,
		"Flag to determine if the controller is running in an openshift")
	flag.Parse()

	// get overrides from environment variables
	mqUser = getEnv("RABBITMQ_USERNAME", mqUser)
	mqPass = getEnv("RABBITMQ_PASSWORD", mqPass)
	mqHost = getEnv("RABBITMQ_HOSTNAME", mqHost)
	lagoonTargetName = getEnv("LAGOON_TARGET_NAME", lagoonTargetName)
	lagoonAppID = getEnv("LAGOON_APP_ID", lagoonAppID)
	pendingMessageCron = getEnv("PENDING_MESSAGE_CRON", pendingMessageCron)
	overrideBuildDeployImage = getEnv("OVERRIDE_BUILD_DEPLOY_DIND_IMAGE", overrideBuildDeployImage)

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
	messaging := handlers.NewMessaging(config, mgr.GetClient(), startupConnectionAttempts, startupConnectionInterval)
	// if we are running with MQ support, then start the consumer handler
	if enableMQ {
		setupLog.Info("starting messaging handler")
		go messaging.Consumer(lagoonTargetName)

		// use cron to run a pending message task
		// this will check any `LagoonBuild` resources for the pendingMessages label
		// and attempt to re-publish them
		c := cron.New()
		c.AddFunc(pendingMessageCron, func() {
			messaging.GetPendingMessages()
		})
		c.Start()
	}

	setupLog.Info("starting controllers")
	if err = (&controllers.LagoonBuildReconciler{
		Client:      mgr.GetClient(),
		Log:         ctrl.Log.WithName("controllers").WithName("LagoonBuild"),
		Scheme:      mgr.GetScheme(),
		EnableMQ:    enableMQ,
		BuildImage:  overrideBuildDeployImage,
		IsOpenshift: isOpenshift,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonBuild")
		os.Exit(1)
	}
	if err = (&controllers.LagoonMonitorReconciler{
		Client:    mgr.GetClient(),
		Log:       ctrl.Log.WithName("controllers").WithName("LagoonMonitor"),
		Scheme:    mgr.GetScheme(),
		EnableMQ:  enableMQ,
		Messaging: messaging,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "LagoonMonitor")
		os.Exit(1)
	}
	if err = (&controllers.LagoonTaskReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("LagoonTask"),
		Scheme: mgr.GetScheme(),
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
