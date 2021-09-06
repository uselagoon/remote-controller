package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/cheshir/go-mq"
	"gopkg.in/matryer/try.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type removeTask struct {
	ProjectName                      string `json:"projectName"`
	Type                             string `json:"type"`
	ForceDeleteProductionEnvironment bool   `json:"forceDeleteProductionEnvironment"`
	PullrequestNumber                string `json:"pullrequestNumber"`
	Branch                           string `json:"branch"`
	BranchName                       string `json:"branchName"`
	OpenshiftProjectName             string `json:"openshiftProjectName"`
}

type messaging interface {
	Consumer(string)
	Publish(string, []byte)
	GetPendingMessages()
}

// Messaging is used for the config and client information for the messaging queue.
type Messaging struct {
	Config                  mq.Config
	Client                  client.Client
	ConnectionAttempts      int
	ConnectionRetryInterval int
	ControllerNamespace     string
	EnableDebug             bool
}

// NewMessaging returns a messaging with config and controller-runtime client.
func NewMessaging(config mq.Config, client client.Client, startupAttempts int, startupInterval int, controllerNamespace string, enableDebug bool) *Messaging {
	return &Messaging{
		Config:                  config,
		Client:                  client,
		ConnectionAttempts:      startupAttempts,
		ConnectionRetryInterval: startupInterval,
		ControllerNamespace:     controllerNamespace,
		EnableDebug:             enableDebug,
	}
}

// Consumer handles consuming messages sent to the queue that these controllers are connected to and processes them accordingly
func (h *Messaging) Consumer(targetName string) { //error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	ctx := context.Background()
	var messageQueue mq.MQ
	// if no mq is found when the goroutine starts, retry a few times before exiting
	// default is 10 retry with 30 second delay = 5 minutes
	err := try.Do(func(attempt int) (bool, error) {
		var err error
		messageQueue, err = mq.New(h.Config)
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Failed to initialize message queue manager, retrying in %d seconds, attempt %d/%d",
					h.ConnectionRetryInterval,
					attempt,
					h.ConnectionAttempts,
				),
			)
			time.Sleep(time.Duration(h.ConnectionRetryInterval) * time.Second)
		}
		return attempt < h.ConnectionAttempts, err
	})
	if err != nil {
		log.Fatalf("Finally failed to initialize message queue manager: %v", err)
	}
	defer messageQueue.Close()

	go func() {
		for err := range messageQueue.Error() {
			opLog.Info(fmt.Sprintf("Caught error from message queue: %v", err))
		}
	}()

	forever := make(chan bool)

	// Handle any tasks that go to the `builddeploy` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":builddeploy")
	err = messageQueue.SetConsumerHandler("builddeploy-queue", func(message mq.Message) {
		if err == nil {
			// unmarshal the body into a lagoonbuild
			newBuild := &lagoonv1alpha1.LagoonBuild{}
			json.Unmarshal(message.Body(), newBuild)
			// new builds that come in should initially get created in the controllers own
			// namespace before being handled and re-created in the correct namespace
			// so set the controller namespace to the build namespace here
			newBuild.ObjectMeta.Namespace = h.ControllerNamespace
			newBuild.SetLabels(
				map[string]string{
					"lagoon.sh/controller": h.ControllerNamespace,
				},
			)
			opLog.Info(
				fmt.Sprintf(
					"Received builddeploy task for project %s, environment %s",
					newBuild.Spec.Project.Name,
					newBuild.Spec.Project.Environment,
				),
			)
			// create it now
			if err := h.Client.Create(ctx, newBuild); err != nil {
				opLog.Error(err,
					fmt.Sprintf(
						"Failed to create builddeploy task for project %s, environment %s",
						newBuild.Spec.Project.Name,
						newBuild.Spec.Project.Environment,
					),
				)
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "builddeploy-queue", err))
	}

	// Handle any tasks that go to the `remove` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":remove")
	err = messageQueue.SetConsumerHandler("remove-queue", func(message mq.Message) {
		if err == nil {
			// unmarshall the message into a remove task to be processed
			removeTask := &removeTask{}
			json.Unmarshal(message.Body(), removeTask)
			// webhooks2tasks sends the `branch` field, but deletion from the API (UI/CLI) does not
			// the tasks system crafts a field `branchName` which is passed through
			// since webhooks2tasks uses the same underlying mechanism, we can still consume branchName even if branch is populated
			if removeTask.Type == "pullrequest" {
				removeTask.Branch = removeTask.BranchName
			}
			ns := removeTask.OpenshiftProjectName
			branch := removeTask.Branch
			project := removeTask.ProjectName
			opLog.WithName("Deletion").Info(
				fmt.Sprintf(
					"Received remove task for project %s, branch %s - %s",
					project,
					branch,
					ns,
				),
			)
			namespace := &corev1.Namespace{}
			err := h.Client.Get(ctx, types.NamespacedName{
				Name: ns,
			}, namespace)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					opLog.WithName("Deletion").Info(
						fmt.Sprintf(
							"Namespace %s for project %s, branch %s does not exist, marking deleted",
							ns,
							project,
							branch,
						),
					)
					msg := lagoonv1alpha1.LagoonMessage{
						Type:      "remove",
						Namespace: ns,
						Meta: &lagoonv1alpha1.LagoonLogMeta{
							Project:     project,
							Environment: branch,
						},
					}
					msgBytes, _ := json.Marshal(msg)
					h.Publish("lagoon-tasks:controller", msgBytes)
				} else {
					opLog.WithName("Deletion").Info(
						fmt.Sprintf(
							"Unable to get namespace %s for project %s, branch %s: %v",
							ns,
							project,
							branch,
							err,
						),
					)

				}
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return

			}
			// @TODO
			/*
				get any deployments/statefulsets/daemonsets
				then delete them
			*/
			if del := h.DeleteDeployments(ctx, opLog.WithName("Deletion"), ns, project, branch); del == false {
				message.Ack(false) // ack to remove from queue
				return
			}
			if del := h.DeleteStatefulSets(ctx, opLog.WithName("Deletion"), ns, project, branch); del == false {
				message.Ack(false) // ack to remove from queue
				return
			}
			if del := h.DeleteDaemonSets(ctx, opLog.WithName("Deletion"), ns, project, branch); del == false {
				message.Ack(false) // ack to remove from queue
				return
			}
			if del := h.DeletePVCs(ctx, opLog.WithName("Deletion"), ns, project, branch); del == false {
				message.Ack(false) // ack to remove from queue
				return
			}
			/*
				then delete the namespace
			*/
			if del := h.DeleteNamespace(ctx, opLog.WithName("Deletion"), namespace, project, branch); del == false {
				message.Ack(false) // ack to remove from queue
				return
			}
			opLog.WithName("Deletion").Info(
				fmt.Sprintf(
					"Deleted namespace %s for project %s, branch %s",
					ns,
					project,
					branch,
				),
			)
			msg := lagoonv1alpha1.LagoonMessage{
				Type:      "remove",
				Namespace: ns,
				Meta: &lagoonv1alpha1.LagoonLogMeta{
					Project:     project,
					Environment: branch,
				},
			}
			msgBytes, _ := json.Marshal(msg)
			h.Publish("lagoon-tasks:controller", msgBytes)
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "remove-queue", err))
	}

	// Handle any tasks that go to the `jobs` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":jobs")
	err = messageQueue.SetConsumerHandler("jobs-queue", func(message mq.Message) {
		if err == nil {
			// unmarshall the message into a remove task to be processed
			jobSpec := &lagoonv1alpha1.LagoonTaskSpec{}
			json.Unmarshal(message.Body(), jobSpec)
			opLog.Info(
				fmt.Sprintf(
					"Received task for project %s, environment %s - %s",
					jobSpec.Project.Name,
					jobSpec.Environment.Name,
					jobSpec.Environment.OpenshiftProjectName,
				),
			)
			job := &lagoonv1alpha1.LagoonTask{}
			job.Spec = *jobSpec
			// set the namespace to the `openshiftProjectName` from the environment
			job.ObjectMeta.Namespace = job.Spec.Environment.OpenshiftProjectName
			job.SetLabels(
				map[string]string{
					"lagoon.sh/taskType":   "standard",
					"lagoon.sh/taskStatus": "Pending",
					"lagoon.sh/controller": h.ControllerNamespace,
				},
			)
			job.ObjectMeta.Name = fmt.Sprintf("lagoon-task-%s-%s", job.Spec.Task.ID, randString(6))
			if err := h.Client.Create(ctx, job); err != nil {
				opLog.Error(err,
					fmt.Sprintf(
						"Unable to create job task for project %s, environment %s",
						job.Spec.Project.Name,
						job.Spec.Environment.Name,
					),
				)
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "jobs-queue", err))
	}

	// Handle any tasks that go to the `misc` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":misc")
	err = messageQueue.SetConsumerHandler("misc-queue", func(message mq.Message) {
		if err == nil {
			opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
			// unmarshall the message into a remove task to be processed
			jobSpec := &lagoonv1alpha1.LagoonTaskSpec{}
			json.Unmarshal(message.Body(), jobSpec)
			// check which key has been received
			switch jobSpec.Key {
			case "kubernetes:build:cancel":
				opLog.Info(
					fmt.Sprintf(
						"Received build cancellation for project %s, environment %s - %s",
						jobSpec.Project.Name,
						jobSpec.Environment.Name,
						jobSpec.Environment.OpenshiftProjectName,
					),
				)
				err := h.CancelDeployment(jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "kubernetes:restic:backup:restore":
				opLog.Info(
					fmt.Sprintf(
						"Received backup restoration for project %s, environment %s",
						jobSpec.Project.Name,
						jobSpec.Environment.Name,
					),
				)
				err := h.ResticRestore(jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "kubernetes:route:migrate":
				opLog.Info(
					fmt.Sprintf(
						"Received ingress migration for project %s",
						jobSpec.Project.Name,
					),
				)
				err := h.IngressRouteMigration(jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "openshift:route:migrate":
				opLog.Info(
					fmt.Sprintf(
						"Received route migration for project %s",
						jobSpec.Project.Name,
					),
				)
				err := h.IngressRouteMigration(jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "kubernetes:task:advanced":
				opLog.Info(
					fmt.Sprintf(
						"Received advanced task for project %s",
						jobSpec.Project.Name,
					),
				)
				err := h.AdvancedTask(jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			default:
				// if we get something that we don't know about, spit out the entire message
				opLog.Info(
					fmt.Sprintf(
						"Received unknown message: %s",
						string(message.Body()),
					),
				)
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "misc-queue", err))
	}
	<-forever
}

// Publish publishes a message to a given queue
func (h *Messaging) Publish(queue string, message []byte) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// no need to re-try here, this is on a cron schedule and the error is returned, cron will try again whenever it is set to run next
	messageQueue, err := mq.New(h.Config)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to initialize message queue manager: %v", err))
		return err
	}
	defer messageQueue.Close()

	producer, err := messageQueue.AsyncProducer(queue)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to get async producer: %v", err))
		return err
	}
	producer.Produce([]byte(fmt.Sprintf("%s", message)))
	return nil
}

// GetPendingMessages will get any pending messages from the queue and attempt to publish them if possible
func (h *Messaging) GetPendingMessages() {
	opLog := ctrl.Log.WithName("handlers").WithName("PendingMessages")
	ctx := context.Background()
	opLog.Info(fmt.Sprintf("Checking pending messages across all namespaces"))
	pendingMsgs := &lagoonv1alpha1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/pendingMessages": "true",
			"lagoon.sh/controller":      h.ControllerNamespace,
		}),
	})
	if err := h.Client.List(ctx, pendingMsgs, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuilds, there may be none or something went wrong"))
		return
	}
	for _, build := range pendingMsgs.Items {
		// get the latest resource in case it has been updated since the loop started
		if err := h.Client.Get(ctx, types.NamespacedName{
			Name:      build.ObjectMeta.Name,
			Namespace: build.ObjectMeta.Namespace,
		}, &build); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to get LagoonBuild, something went wrong"))
			break
		}
		opLog.Info(fmt.Sprintf("LagoonBuild %s has pending messages, attempting to re-send", build.ObjectMeta.Name))
		statusBytes, _ := json.Marshal(build.StatusMessages.StatusMessage)
		logBytes, _ := json.Marshal(build.StatusMessages.BuildLogMessage)
		envBytes, _ := json.Marshal(build.StatusMessages.EnvironmentMessage)

		// try to re-publish message or break and try the next build with pending message
		if build.StatusMessages.StatusMessage != nil {
			if err := h.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.BuildLogMessage != nil {
			if err := h.Publish("lagoon-logs", logBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.EnvironmentMessage != nil {
			if err := h.Publish("lagoon-tasks:controller", envBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		// if we managed to send all the pending messages, then update the resource to remove the pending state
		// so we don't send the same message multiple times
		opLog.Info(fmt.Sprintf("Sent pending messages for LagoonBuild %s", build.ObjectMeta.Name))
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/pendingMessages": "false",
				},
			},
			"statusMessages": nil,
		})
		if err := h.Client.Patch(ctx, &build, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
			break
		}
	}
	return
}
