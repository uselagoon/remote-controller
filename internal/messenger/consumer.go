package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cheshir/go-mq"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"gopkg.in/matryer/try.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Consumer handles consuming messages sent to the queue that these controllers are connected to and processes them accordingly
func (m *Messenger) Consumer(targetName string) { //error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	ctx := context.Background()
	var messageQueue mq.MQ
	// if no mq is found when the goroutine starts, retry a few times before exiting
	// default is 10 retry with 30 second delay = 5 minutes
	err := try.Do(func(attempt int) (bool, error) {
		var err error
		messageQueue, err = mq.New(m.Config)
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Failed to initialize message queue manager, retrying in %d seconds, attempt %d/%d",
					m.ConnectionRetryInterval,
					attempt,
					m.ConnectionAttempts,
				),
			)
			time.Sleep(time.Duration(m.ConnectionRetryInterval) * time.Second)
		}
		return attempt < m.ConnectionAttempts, err
	})
	if err != nil {
		log.Fatalf("Finally failed to initialize message queue manager: %v", err)
	}
	defer messageQueue.Close()

	go func() {
		count := 0
		for err := range messageQueue.Error() {
			opLog.Info(fmt.Sprintf("Caught error from message queue: %v", err))
			// if there are 5 errors (usually after about 60-120 seconds)
			// fatalf and restart the controller
			if count == 4 {
				log.Fatalf("Terminating controller due to error with message queue: %v", err)
			}
			count++
		}
		count = 0
	}()

	forever := make(chan bool)

	// Handle any tasks that go to the `builddeploy` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":builddeploy")
	err = messageQueue.SetConsumerHandler("builddeploy-queue", func(message mq.Message) {
		if err == nil {
			// unmarshal the body into a lagoonbuild
			newBuild := &lagoonv1beta1.LagoonBuild{}
			json.Unmarshal(message.Body(), newBuild)
			// new builds that come in should initially get created in the controllers own
			// namespace before being handled and re-created in the correct namespace
			// so set the controller namespace to the build namespace here
			newBuild.ObjectMeta.Namespace = m.ControllerNamespace
			newBuild.SetLabels(
				map[string]string{
					"lagoon.sh/controller": m.ControllerNamespace,
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
			if err := m.Client.Create(ctx, newBuild); err != nil {
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
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "builddeploy-queue", err))
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
			// generate the namespace name from the branch and project and any prefixes that the controller may add
			ns := helpers.GenerateNamespaceName(
				removeTask.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
				removeTask.Branch,
				removeTask.ProjectName,
				m.NamespacePrefix,
				m.ControllerNamespace,
				m.RandomNamespacePrefix,
			)
			branch := removeTask.Branch
			project := removeTask.ProjectName
			opLog.WithName("RemoveTask").Info(
				fmt.Sprintf(
					"Received remove task for project %s, branch %s - %s",
					project,
					branch,
					ns,
				),
			)
			namespace := &corev1.Namespace{}
			err := m.Client.Get(ctx, types.NamespacedName{
				Name: ns,
			}, namespace)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					opLog.WithName("RemoveTask").Info(
						fmt.Sprintf(
							"Namespace %s for project %s, branch %s does not exist, marking deleted",
							ns,
							project,
							branch,
						),
					)
					msg := lagoonv1beta1.LagoonMessage{
						Type:      "remove",
						Namespace: ns,
						Meta: &lagoonv1beta1.LagoonLogMeta{
							Project:     project,
							Environment: branch,
						},
					}
					msgBytes, _ := json.Marshal(msg)
					m.Publish("lagoon-tasks:controller", msgBytes)
				} else {
					opLog.WithName("RemoveTask").Info(
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
			// check that the namespace selected for deletion is owned by this controller
			if value, ok := namespace.ObjectMeta.Labels["lagoon.sh/controller"]; ok {
				if value == m.ControllerNamespace {
					// spawn the deletion process for this namespace
					go func() {
						err := m.DeletionHandler.ProcessDeletion(ctx, opLog, namespace)
						if err == nil {
							msg := lagoonv1beta1.LagoonMessage{
								Type:      "remove",
								Namespace: namespace.ObjectMeta.Name,
								Meta: &lagoonv1beta1.LagoonLogMeta{
									Project:     project,
									Environment: branch,
								},
							}
							msgBytes, _ := json.Marshal(msg)
							m.Publish("lagoon-tasks:controller", msgBytes)
						}
					}()
					message.Ack(false) // ack to remove from queue
					return
				}
				// controller label didn't match, log the message
				opLog.WithName("RemoveTask").Info(
					fmt.Sprintf(
						"Selected namespace %s for project %s, branch %s: %v",
						ns,
						project,
						branch,
						fmt.Errorf("The controller label value %s does not match %s for this namespace", value, m.ControllerNamespace),
					),
				)
				message.Ack(false) // ack to remove from queue
				return
			}
			// controller label didn't match, log the message
			opLog.WithName("RemoveTask").Info(
				fmt.Sprintf(
					"Selected namespace %s for project %s, branch %s: %v",
					ns,
					project,
					branch,
					fmt.Errorf("The controller ownership label does not exist on this namespace, nothing will be done for this removal request"),
				),
			)
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "remove-queue", err))
	}

	// Handle any tasks that go to the `jobs` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":jobs")
	err = messageQueue.SetConsumerHandler("jobs-queue", func(message mq.Message) {
		if err == nil {
			// unmarshall the message into a remove task to be processed
			jobSpec := &lagoonv1beta1.LagoonTaskSpec{}
			json.Unmarshal(message.Body(), jobSpec)
			namespace := helpers.GenerateNamespaceName(
				jobSpec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
				jobSpec.Environment.Name,
				jobSpec.Project.Name,
				m.NamespacePrefix,
				m.ControllerNamespace,
				m.RandomNamespacePrefix,
			)
			opLog.Info(
				fmt.Sprintf(
					"Received task for project %s, environment %s - %s",
					jobSpec.Project.Name,
					jobSpec.Environment.Name,
					namespace,
				),
			)
			job := &lagoonv1beta1.LagoonTask{}
			job.Spec = *jobSpec
			// set the namespace to the `openshiftProjectName` from the environment
			job.ObjectMeta.Namespace = namespace
			job.SetLabels(
				map[string]string{
					"lagoon.sh/taskType":   lagoonv1beta1.TaskTypeStandard.String(),
					"lagoon.sh/taskStatus": lagoonv1beta1.TaskStatusPending.String(),
					"lagoon.sh/controller": m.ControllerNamespace,
				},
			)
			job.ObjectMeta.Name = fmt.Sprintf("lagoon-task-%s-%s", job.Spec.Task.ID, helpers.HashString(job.Spec.Task.ID)[0:6])
			if job.Spec.Task.TaskName != "" {
				job.ObjectMeta.Name = job.Spec.Task.TaskName
			}
			if err := m.Client.Create(ctx, job); err != nil {
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
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "jobs-queue", err))
	}

	// Handle any tasks that go to the `misc` queue
	opLog.Info("Listening for lagoon-tasks:" + targetName + ":misc")
	err = messageQueue.SetConsumerHandler("misc-queue", func(message mq.Message) {
		if err == nil {
			opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
			// unmarshall the message into a remove task to be processed
			jobSpec := &lagoonv1beta1.LagoonTaskSpec{}
			json.Unmarshal(message.Body(), jobSpec)
			// check which key has been received
			namespace := helpers.GenerateNamespaceName(
				jobSpec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
				jobSpec.Environment.Name,
				jobSpec.Project.Name,
				m.NamespacePrefix,
				m.ControllerNamespace,
				m.RandomNamespacePrefix,
			)
			switch jobSpec.Key {
			case "kubernetes:build:cancel":
				opLog.Info(
					fmt.Sprintf(
						"Received build cancellation for project %s, environment %s - %s",
						jobSpec.Project.Name,
						jobSpec.Environment.Name,
						namespace,
					),
				)
				err := m.CancelBuild(namespace, jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "kubernetes:task:cancel":
				opLog.Info(
					fmt.Sprintf(
						"Received task cancellation for project %s, environment %s - %s",
						jobSpec.Project.Name,
						jobSpec.Environment.Name,
						namespace,
					),
				)
				err := m.CancelTask(namespace, jobSpec)
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
				err := m.ResticRestore(namespace, jobSpec)
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
				err := m.IngressRouteMigration(namespace, jobSpec)
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
				err := m.IngressRouteMigration(namespace, jobSpec)
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
				err := m.AdvancedTask(namespace, jobSpec)
				if err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			case "deploytarget:task:activestandby":
				opLog.Info(
					fmt.Sprintf(
						"Received activestandy switch for project %s",
						jobSpec.Project.Name,
					),
				)
				err := m.ActiveStandbySwitch(namespace, jobSpec)
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
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "misc-queue", err))
	}
	<-forever
}
