package messenger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetPendingMessages will get any pending messages from the queue and attempt to publish them if possible
func (m *Messenger) GetPendingMessages() {
	opLog := ctrl.Log.WithName("handlers").WithName("PendingMessages")
	ctx := context.Background()
	opLog.Info(fmt.Sprintf("Checking pending build messages across all namespaces"))
	m.pendingBuildLogMessages(ctx, opLog)
	opLog.Info(fmt.Sprintf("Checking pending task messages across all namespaces"))
	m.pendingTaskLogMessages(ctx, opLog)
}

func (m *Messenger) pendingBuildLogMessages(ctx context.Context, opLog logr.Logger) {
	pendingMsgs := &lagoonv1beta1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/pendingMessages": "true",
			"lagoon.sh/controller":      m.ControllerNamespace,
		}),
	})
	if err := m.Client.List(ctx, pendingMsgs, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuilds, there may be none or something went wrong"))
		return
	}
	for _, build := range pendingMsgs.Items {
		// get the latest resource in case it has been updated since the loop started
		if err := m.Client.Get(ctx, types.NamespacedName{
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
			if err := m.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.BuildLogMessage != nil {
			logBytes, _ := json.Marshal(build.StatusMessages.BuildLogMessage)
			if err := m.Publish("lagoon-logs", logBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if build.StatusMessages.EnvironmentMessage != nil {
			envBytes, _ := json.Marshal(build.StatusMessages.EnvironmentMessage)
			if err := m.Publish("lagoon-tasks:controller", envBytes); err != nil {
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
		if err := m.Client.Patch(ctx, &build, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
			break
		}
	}
	return
}

func (m *Messenger) pendingTaskLogMessages(ctx context.Context, opLog logr.Logger) {
	pendingMsgs := &lagoonv1beta1.LagoonTaskList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/pendingMessages": "true",
			"lagoon.sh/controller":      m.ControllerNamespace,
		}),
	})
	if err := m.Client.List(ctx, pendingMsgs, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuilds, there may be none or something went wrong"))
		return
	}
	for _, task := range pendingMsgs.Items {
		// get the latest resource in case it has been updated since the loop started
		if err := m.Client.Get(ctx, types.NamespacedName{
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
			if err := m.Publish("lagoon-logs", statusBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if task.StatusMessages.TaskLogMessage != nil {
			taskLogBytes, _ := json.Marshal(task.StatusMessages.TaskLogMessage)
			if err := m.Publish("lagoon-logs", taskLogBytes); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to publush message"))
				break
			}
		}
		if task.StatusMessages.EnvironmentMessage != nil {
			envBytes, _ := json.Marshal(task.StatusMessages.EnvironmentMessage)
			if err := m.Publish("lagoon-tasks:controller", envBytes); err != nil {
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
		if err := m.Client.Patch(ctx, &task, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
			break
		}
	}
	return
}
