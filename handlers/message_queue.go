package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/cheshir/go-mq"
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
	OpenshiftProjectName             string `json:"openshiftProjectName"`
}

type messaging interface {
	Consumer(string)
	Publish(string, []byte)
	GetPendingMessages()
}

// Messaging is used for the config and client information for the messaging queue
type Messaging struct {
	Config mq.Config
	Client client.Client
}

// NewMessaging returns a messaging with config and controller-runtime client.
func NewMessaging(config mq.Config, client client.Client) *Messaging {
	return &Messaging{
		Config: config,
		Client: client,
	}
}

// Consumer handles consuming messages sent to the queue that this operator is connected to and processes them accordingly
func (h *Messaging) Consumer(targetName string) { // error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	messageQueue, err := mq.New(h.Config)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to initialize message queue manager: %v", err))
		return
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
			opLog.Info(fmt.Sprintf("Received builddeploy task for project %s, environment %s", newBuild.Spec.Project.Name, newBuild.Spec.Project.Environment))
			// create it now
			if err := h.Client.Create(context.Background(), newBuild); err != nil {
				fmt.Println(err)
				return
			}
		}
		message.Ack(false)
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
			opLog.Info(fmt.Sprintf("Received remove task for project %s, branch %s - %s", removeTask.ProjectName, removeTask.Branch, removeTask.OpenshiftProjectName))
			namespace := &corev1.Namespace{}
			err := h.Client.Get(context.Background(), types.NamespacedName{
				Name: removeTask.OpenshiftProjectName,
			}, namespace)
			if err != nil {
				opLog.Info(fmt.Sprintf("Unable to get namespace %s for project %s, branch %s: %v", removeTask.OpenshiftProjectName, removeTask.ProjectName, removeTask.Branch, err))
				return
			}
			if err := h.Client.Delete(context.Background(), namespace); err != nil {
				opLog.Info(fmt.Sprintf("Unable to delete namespace %s for project %s, branch %s: %v", removeTask.OpenshiftProjectName, removeTask.ProjectName, removeTask.Branch, err))
				return
			}
			opLog.Info(fmt.Sprintf("Deleted project %s, branch %s - %s", removeTask.ProjectName, removeTask.Branch, removeTask.OpenshiftProjectName))
			operatorMsg := lagoonv1alpha1.LagoonMessage{
				Type:      "remove",
				Namespace: removeTask.OpenshiftProjectName,
				Meta: &lagoonv1alpha1.LagoonLogMeta{
					Project:     removeTask.ProjectName,
					Environment: removeTask.Branch,
				},
			}
			operatorMsgBytes, _ := json.Marshal(operatorMsg)
			h.Publish("lagoon-tasks:operator", operatorMsgBytes)
		}
		message.Ack(false)
	})
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "remove-queue", err))
	}
	<-forever
}

// Publish publishes a message to a given queue
func (h *Messaging) Publish(queue string, message []byte) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
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
	opLog.Info(fmt.Sprintf("Checking pending messages across all namespaces"))
	pendingMsgs := &lagoonv1alpha1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/pendingMessages": "true",
		}),
	})
	if err := h.Client.List(context.Background(), pendingMsgs, listOption); err != nil {
		opLog.Info(fmt.Sprintf("Unable to list LagoonBuilds, there may be none or something went wrong: %v", err))
		return
	}
	for _, build := range pendingMsgs.Items {
		// get the latest resource in case it has been updated since the loop started
		if err := h.Client.Get(context.Background(), types.NamespacedName{
			Name:      build.ObjectMeta.Name,
			Namespace: build.ObjectMeta.Namespace,
		}, &build); err != nil {
			opLog.Info(fmt.Sprintf("Unable to get LagoonBuild, something went wrong: %v", err))
			break
		}
		opLog.Info(fmt.Sprintf("LagoonBuild %s has pending messages, attempting to re-send", build.ObjectMeta.Name))
		statusBytes, _ := json.Marshal(build.StatusMessages.StatusMessage)
		logBytes, _ := json.Marshal(build.StatusMessages.BuildLogMessage)
		envBytes, _ := json.Marshal(build.StatusMessages.EnvironmentMessage)

		// try to re-publish message or break and try the next build with pending message
		if build.StatusMessages.StatusMessage != nil {
			if err := h.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Info(fmt.Sprintf("Unable to publush message: %v", err))
				break
			}
		}
		if build.StatusMessages.BuildLogMessage != nil {
			if err := h.Publish("lagoon-logs", logBytes); err != nil {
				opLog.Info(fmt.Sprintf("Unable to publush message: %v", err))
				break
			}
		}
		if build.StatusMessages.EnvironmentMessage != nil {
			if err := h.Publish("lagoon-tasks:operator", envBytes); err != nil {
				opLog.Info(fmt.Sprintf("Unable to publush message: %v", err))
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
		if err := h.Client.Patch(context.Background(), &build, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Info(fmt.Sprintf("Unable to update status condition: %v", err))
			break
		}
	}
	return
}
