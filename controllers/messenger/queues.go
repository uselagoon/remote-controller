package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cheshir/go-mq"
	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"gopkg.in/matryer/try.v1"
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
	NamespacePattern                 string `json:"namespacePattern,omitempty"`
}

type messaging interface {
	Consumer(string)
	Publish(string, []byte)
	GetPendingMessages()
}

// Messaging is used for the config and client information for the messaging queue.
type Messaging struct {
	Config                           mq.Config
	Client                           client.Client
	ConnectionAttempts               int
	ConnectionRetryInterval          int
	ControllerNamespace              string
	NamespacePrefix                  string
	RandomNamespacePrefix            bool
	AdvancedTaskSSHKeyInjection      bool
	AdvancedTaskDeployTokenInjection bool
	Harbor                           harbor.Harbor
	CleanupHarborRepositoryOnDelete  bool
	EnableDebug                      bool
}

// NewMessaging returns a messaging with config and controller-runtime client.
func NewMessaging(config mq.Config,
	client client.Client,
	startupAttempts int,
	startupInterval int,
	controllerNamespace,
	namespacePrefix string,
	randomNamespacePrefix,
	advancedTaskSSHKeyInjection bool,
	advancedTaskDeployTokenInjection bool,
	harborConfig harbor.Harbor,
	cleanupHarborOnDelete bool,
	enableDebug bool,
) *Messaging {
	return &Messaging{
		Config:                           config,
		Client:                           client,
		ConnectionAttempts:               startupAttempts,
		ConnectionRetryInterval:          startupInterval,
		ControllerNamespace:              controllerNamespace,
		NamespacePrefix:                  namespacePrefix,
		RandomNamespacePrefix:            randomNamespacePrefix,
		AdvancedTaskSSHKeyInjection:      advancedTaskSSHKeyInjection,
		AdvancedTaskDeployTokenInjection: advancedTaskDeployTokenInjection,
		Harbor:                           harborConfig,
		CleanupHarborRepositoryOnDelete:  cleanupHarborOnDelete,
		EnableDebug:                      enableDebug,
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
	opLog.Info(fmt.Sprintf("Listening for lagoon-controller:%s", targetName))
	err = messageQueue.SetConsumerHandler("controller-queue", func(message mq.Message) {
		if err == nil {
			if err := h.handleLagoonEvent(ctx, opLog, message.Body()); err != nil {
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

	// Handle any tasks that go to the `builddeploy` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:builddeploy", targetName))
	err = messageQueue.SetConsumerHandler("builddeploy-queue", func(message mq.Message) {
		if err == nil {
			if err := h.handleBuildEvent(ctx, opLog, message.Body()); err != nil {
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
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:remove", targetName))
	err = messageQueue.SetConsumerHandler("remove-queue", func(message mq.Message) {
		if err == nil {
			if err := h.handleRemovalEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "remove-queue", err))
	}

	// Handle any tasks that go to the `jobs` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:jobs", targetName))
	err = messageQueue.SetConsumerHandler("jobs-queue", func(message mq.Message) {
		if err == nil {
			if err := h.handleTaskEvent(ctx, opLog, message.Body()); err != nil {
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
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:misc", targetName))
	err = messageQueue.SetConsumerHandler("misc-queue", func(message mq.Message) {
		if err == nil {
			if err := h.handleMiscEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "misc-queue", err))
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
	opLog.Info(fmt.Sprintf("Checking pending build messages across all namespaces"))
	h.pendingBuildLogMessages(ctx, opLog)
	opLog.Info(fmt.Sprintf("Checking pending task messages across all namespaces"))
	h.pendingTaskLogMessages(ctx, opLog)
}

func (h *Messaging) pendingBuildLogMessages(ctx context.Context, opLog logr.Logger) {
	pendingMsgs := &lagoonv1beta1.LagoonBuildList{}
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

		// try to re-publish message or break and try the next build with pending message
		if build.StatusMessages.StatusMessage != nil {
			statusBytes, _ := json.Marshal(build.StatusMessages.StatusMessage)
			if err := h.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.BuildLogMessage != nil {
			logBytes, _ := json.Marshal(build.StatusMessages.BuildLogMessage)
			if err := h.Publish("lagoon-logs", logBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.EnvironmentMessage != nil {
			envBytes, _ := json.Marshal(build.StatusMessages.EnvironmentMessage)
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
		if err := h.Client.Patch(ctx, &build, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
			break
		}
	}
	return
}

func (h *Messaging) pendingTaskLogMessages(ctx context.Context, opLog logr.Logger) {
	pendingMsgs := &lagoonv1beta1.LagoonTaskList{}
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
	for _, task := range pendingMsgs.Items {
		// get the latest resource in case it has been updated since the loop started
		if err := h.Client.Get(ctx, types.NamespacedName{
			Name:      task.ObjectMeta.Name,
			Namespace: task.ObjectMeta.Namespace,
		}, &task); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to get LagoonBuild, something went wrong"))
			break
		}
		opLog.Info(fmt.Sprintf("LagoonTasl %s has pending messages, attempting to re-send", task.ObjectMeta.Name))

		// try to re-publish message or break and try the next build with pending message
		if task.StatusMessages.StatusMessage != nil {
			statusBytes, _ := json.Marshal(task.StatusMessages.StatusMessage)
			if err := h.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if task.StatusMessages.TaskLogMessage != nil {
			taskLogBytes, _ := json.Marshal(task.StatusMessages.TaskLogMessage)
			if err := h.Publish("lagoon-logs", taskLogBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if task.StatusMessages.EnvironmentMessage != nil {
			envBytes, _ := json.Marshal(task.StatusMessages.EnvironmentMessage)
			if err := h.Publish("lagoon-tasks:controller", envBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		// if we managed to send all the pending messages, then update the resource to remove the pending state
		// so we don't send the same message multiple times
		opLog.Info(fmt.Sprintf("Sent pending messages for LagoonTask %s", task.ObjectMeta.Name))
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/pendingMessages": "false",
				},
			},
			"statusMessages": nil,
		})
		if err := h.Client.Patch(ctx, &task, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
			break
		}
	}
	return
}
