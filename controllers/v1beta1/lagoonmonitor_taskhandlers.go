package v1beta1

// this file is used by the `lagoonmonitor` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *LagoonMonitorReconciler) handleTaskMonitor(ctx context.Context, opLog logr.Logger, req ctrl.Request, jobPod corev1.Pod) error {
	// get the task associated to this pod, we wil need update it at some point
	var lagoonTask lagoonv1beta1.LagoonTask
	err := r.Get(ctx, types.NamespacedName{
		Namespace: jobPod.ObjectMeta.Namespace,
		Name:      jobPod.ObjectMeta.Labels["lagoon.sh/taskName"],
	}, &lagoonTask)
	if err != nil {
		return err
	}
	if cancelTask, ok := jobPod.ObjectMeta.Labels["lagoon.sh/cancelTask"]; ok {
		cancel, _ := strconv.ParseBool(cancelTask)
		if cancel {
			return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, cancel)
		}
	}
	if jobPod.Status.Phase == corev1.PodPending {
		opLog.Info(fmt.Sprintf("Task %s is %v", jobPod.ObjectMeta.Name, jobPod.Status.Phase))
		for _, container := range jobPod.Status.ContainerStatuses {
			if container.State.Waiting != nil && helpers.ContainsString(failureStates, container.State.Waiting.Reason) {
				// if we have a failure state, then fail the task and get the logs from the container
				opLog.Info(fmt.Sprintf("Task failed, container exit reason was: %v", container.State.Waiting.Reason))
				lagoonTask.Labels["lagoon.sh/taskStatus"] = string(lagoonv1beta1.TaskStatusFailed)
				if err := r.Update(ctx, &lagoonTask); err != nil {
					return err
				}
				opLog.Info(fmt.Sprintf("Marked task %s as %s", lagoonTask.ObjectMeta.Name, string(lagoonv1beta1.TaskStatusFailed)))
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
				opLog.Info(fmt.Sprintf("Deleted failed task pod: %s", jobPod.ObjectMeta.Name))
				// update the status to failed on the deleted pod
				// and set the terminate time to now, it is used when we update the deployment and environment
				jobPod.Status.Phase = corev1.PodFailed
				state := corev1.ContainerStatus{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							FinishedAt: metav1.Time{Time: time.Now().UTC()},
						},
					},
				}
				jobPod.Status.ContainerStatuses[0] = state
				r.updateTaskStatusCondition(ctx, &lagoonTask, lagoonv1beta1.LagoonTaskConditions{
					Type:   lagoonv1beta1.TaskStatusFailed,
					Status: corev1.ConditionTrue,
				}, []byte(container.State.Waiting.Message))
				// send any messages to lagoon message queues
				r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, &jobPod)
				r.updateLagoonTask(opLog, &lagoonTask, &jobPod)
				logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
				r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, []byte(logMsg))
				return nil
			}
		}
	}
	if jobPod.Status.Phase == corev1.PodFailed || jobPod.Status.Phase == corev1.PodSucceeded {
		// get the task associated to this pod, we wil need update it at some point
		var lagoonTask lagoonv1beta1.LagoonTask
		err := r.Get(ctx, types.NamespacedName{
			Namespace: jobPod.ObjectMeta.Namespace,
			Name:      jobPod.ObjectMeta.Labels["lagoon.sh/taskName"],
		}, &lagoonTask)
		if err != nil {
			return err
		}
		var jobCondition lagoonv1beta1.TaskStatusType
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			jobCondition = lagoonv1beta1.TaskStatusFailed
		case corev1.PodSucceeded:
			jobCondition = lagoonv1beta1.TaskStatusComplete
		}
		if value, ok := lagoonTask.Labels["lagoon.sh/taskStatus"]; ok {
			if value == string(lagoonv1beta1.TaskStatusCancelled) {
				// jobCondition = lagoonv1beta1.TaskStatusCancelled
				jobCondition = lagoonv1beta1.TaskStatusFailed
			}
		}
		// if the task status doesn't equal the status of the pod
		// then update the task to reflect the current pod status
		// we do this so we don't update the status of the task again
		if lagoonTask.Labels["lagoon.sh/taskStatus"] != string(jobCondition) {
			opLog.Info(fmt.Sprintf("Task %s %v", jobPod.ObjectMeta.Labels["lagoon.sh/taskName"], jobPod.Status.Phase))
			// set the status to the task condition
			lagoonTask.Labels["lagoon.sh/taskStatus"] = string(jobCondition)
			if err := r.Update(ctx, &lagoonTask); err != nil {
				return err
			}
			allContainerLogs := r.collectLogs(ctx, req, jobPod)
			r.updateTaskStatusCondition(ctx, &lagoonTask,
				lagoonv1beta1.LagoonTaskConditions{
					Type:   jobCondition,
					Status: corev1.ConditionTrue,
				},
				allContainerLogs,
			)
			// send any messages to lagoon message queues
			// update the deployment with the status
			r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, &jobPod)
			r.updateLagoonTask(opLog, &lagoonTask, &jobPod)
			r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, allContainerLogs)
		}
		return nil
	}
	// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
	opLog.Info(fmt.Sprintf("Task %s is %v", jobPod.ObjectMeta.Labels["lagoon.sh/taskName"], jobPod.Status.Phase))
	// send any messages to lagoon message queues
	r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, &jobPod)
	r.updateLagoonTask(opLog, &lagoonTask, &jobPod)
	// send the logs to lagoon so that tasks can receive possibly more frequent logs like builds
	r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, r.collectLogs(ctx, req, jobPod))
	return nil
}

// taskLogsToLagoonLogs sends the task logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonMonitorReconciler) taskLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
	logs []byte,
) {
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "completed"
		}
		if value, ok := lagoonTask.Labels["lagoon.sh/taskStatus"]; ok {
			if value == string(lagoonv1beta1.TaskStatusCancelled) {
				// condition = "cancelled"
				condition = "cancelled"
			}
		}
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task-logs:job-kubernetes:" + lagoonTask.ObjectMeta.Name,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				Task:        &lagoonTask.Spec.Task,
				Environment: lagoonTask.Spec.Environment.Name,
				JobName:     lagoonTask.ObjectMeta.Name,
				JobStatus:   condition,
				RemoteID:    string(jobPod.ObjectMeta.UID),
				Key:         lagoonTask.Spec.Key,
				Cluster:     r.LagoonTargetName,
			},
			Message: fmt.Sprintf(`========================================
Logs on pod %s
========================================
%s`, jobPod.ObjectMeta.Name, logs),
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateTaskStatusMessage(context.Background(), lagoonTask, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeTaskPendingMessageStatus(context.Background(), lagoonTask)
	}
}

// updateLagoonTask sends the status of the task and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonMonitorReconciler) updateLagoonTask(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
) {
	namespace := helpers.GenerateNamespaceName(
		lagoonTask.Spec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		lagoonTask.Spec.Environment.Name,
		lagoonTask.Spec.Project.Name,
		r.NamespacePrefix,
		r.ControllerNamespace,
		r.RandomNamespacePrefix,
	)
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		if value, ok := lagoonTask.Labels["lagoon.sh/taskStatus"]; ok {
			if value == string(lagoonv1beta1.TaskStatusCancelled) {
				condition = "cancelled"
			}
		}
		msg := lagoonv1beta1.LagoonMessage{
			Type:      "task",
			Namespace: namespace,
			Meta: &lagoonv1beta1.LagoonLogMeta{
				Task:          &lagoonTask.Spec.Task,
				Environment:   lagoonTask.Spec.Environment.Name,
				Project:       lagoonTask.Spec.Project.Name,
				EnvironmentID: helpers.StringToUintPtr(lagoonTask.Spec.Environment.ID),
				ProjectID:     helpers.StringToUintPtr(lagoonTask.Spec.Project.ID),
				JobName:       lagoonTask.ObjectMeta.Name,
				JobStatus:     condition,
				RemoteID:      string(jobPod.ObjectMeta.UID),
				Key:           lagoonTask.Spec.Key,
				Cluster:       r.LagoonTargetName,
			},
		}
		if _, ok := jobPod.ObjectMeta.Annotations["lagoon.sh/taskData"]; ok {
			// if the task contains `taskData` annotation, this is used to send data back to lagoon
			// lagoon will use the data to perform an action against the api or something else
			// the data in taskData should be base64 encoded
			msg.Meta.AdvancedData = jobPod.ObjectMeta.Annotations["lagoon.sh/taskData"]
		}
		// we can add the task start time here
		if jobPod.Status.StartTime != nil {
			msg.Meta.StartTime = jobPod.Status.StartTime.Time.UTC().Format("2006-01-02 15:04:05")
		}
		// and then once the pod is terminated we can add the terminated time here
		if jobPod.Status.ContainerStatuses != nil {
			if jobPod.Status.ContainerStatuses[0].State.Terminated != nil {
				msg.Meta.EndTime = jobPod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time.UTC().Format("2006-01-02 15:04:05")
			}
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateTaskEnvironmentMessage(context.Background(), lagoonTask, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeTaskPendingMessageStatus(context.Background(), lagoonTask)
	}
}

// taskStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) taskStatusLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
) {
	if r.EnableMQ {
		condition := "pending"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "running"
		case corev1.PodSucceeded:
			condition = "complete"
		}
		if value, ok := lagoonTask.Labels["lagoon.sh/taskStatus"]; ok {
			if value == string(lagoonv1beta1.TaskStatusCancelled) {
				condition = "cancelled"
			}
		}
		msg := lagoonv1beta1.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task:job-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the controller
			Meta: &lagoonv1beta1.LagoonLogMeta{
				Task:          &lagoonTask.Spec.Task,
				ProjectName:   lagoonTask.Spec.Project.Name,
				Environment:   lagoonTask.Spec.Environment.Name,
				EnvironmentID: helpers.StringToUintPtr(lagoonTask.Spec.Environment.ID),
				ProjectID:     helpers.StringToUintPtr(lagoonTask.Spec.Project.ID),
				JobName:       lagoonTask.ObjectMeta.Name,
				JobStatus:     condition,
				RemoteID:      string(jobPod.ObjectMeta.UID),
				Key:           lagoonTask.Spec.Key,
				Cluster:       r.LagoonTargetName,
			},
			Message: fmt.Sprintf("*[%s]* Task `%s` *%s* %s",
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Task.ID,
				lagoonTask.Spec.Task.Name,
				condition,
			),
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateTaskStatusMessage(context.Background(), lagoonTask, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeTaskPendingMessageStatus(context.Background(), lagoonTask)
	}
}

// updateTaskStatusCondition is used to patch the lagoon task with the status conditions for the task, plus any logs
func (r *LagoonMonitorReconciler) updateTaskStatusCondition(ctx context.Context,
	lagoonTask *lagoonv1beta1.LagoonTask,
	condition lagoonv1beta1.LagoonTaskConditions, log []byte) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !helpers.TaskContainsStatus(lagoonTask.Status.Conditions, condition) {
		lagoonTask.Status.Conditions = append(lagoonTask.Status.Conditions, condition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": lagoonTask.Status.Conditions,
				"log":        log,
			},
		})
		if err := r.Patch(ctx, lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}

// updateTaskEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon task
func (r *LagoonMonitorReconciler) updateTaskEnvironmentMessage(ctx context.Context,
	lagoonTask *lagoonv1beta1.LagoonTask,
	envMessage lagoonv1beta1.LagoonMessage) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"environmentMessage": envMessage,
		},
	})
	if err := r.Patch(ctx, lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateTaskStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon task
func (r *LagoonMonitorReconciler) updateTaskStatusMessage(ctx context.Context,
	lagoonTask *lagoonv1beta1.LagoonTask,
	statusMessage lagoonv1beta1.LagoonLog) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"statusMessage": statusMessage,
		},
	})
	if err := r.Patch(ctx, lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// removeTaskPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonMonitorReconciler) removeTaskPendingMessageStatus(ctx context.Context, lagoonTask *lagoonv1beta1.LagoonTask) error {
	// if we have the pending messages label as true, then we want to remove this label and any pending statusmessages
	// so we can avoid double handling, or an old pending message from being sent after a new pending message
	if val, ok := lagoonTask.ObjectMeta.Labels["lagoon.sh/pendingMessages"]; !ok {
		if val == "true" {
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"lagoon.sh/pendingMessages": "false",
					},
				},
				"statusMessages": nil,
			})
			if err := r.Patch(ctx, lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}

// updateTaskWithLogs collects logs from the task containers and ships or stores them
func (r *LagoonMonitorReconciler) updateTaskWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonTask lagoonv1beta1.LagoonTask,
	jobPod corev1.Pod,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	var jobCondition lagoonv1beta1.TaskStatusType
	switch jobPod.Status.Phase {
	case corev1.PodFailed:
		jobCondition = lagoonv1beta1.TaskStatusFailed
	case corev1.PodSucceeded:
		jobCondition = lagoonv1beta1.TaskStatusComplete
	}
	if cancel {
		jobCondition = lagoonv1beta1.TaskStatusCancelled
	}
	// if the task status is Pending or Running
	// then the jobCondition is Failed, Complete, or Cancelled
	// then update the task to reflect the current pod status
	// we do this so we don't update the status of the task again
	if helpers.ContainsString(
		helpers.TaskRunningPendingStatus,
		lagoonTask.Labels["lagoon.sh/taskStatus"],
	) {
		opLog.Info(
			fmt.Sprintf(
				"Updating task status for %s to %v",
				jobPod.ObjectMeta.Name,
				jobCondition,
			),
		)
		// grab all the logs from the containers in the task pod and just merge them all together
		// we only have 1 container at the moment in a taskpod anyway so it doesn't matter
		// if we do move to multi container tasks, then worry about it
		allContainerLogs := r.collectLogs(ctx, req, jobPod)
		if cancel {
			allContainerLogs = append(allContainerLogs, []byte(fmt.Sprintf(`
========================================
Task cancelled
========================================`))...)
		}
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/taskStatus": string(jobCondition),
				},
			},
		})
		lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] = string(jobCondition)
		if err := r.Patch(ctx, &lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to update resource"))
		}
		r.updateTaskStatusCondition(ctx, &lagoonTask, lagoonv1beta1.LagoonTaskConditions{
			Type:   jobCondition,
			Status: corev1.ConditionTrue,
		}, allContainerLogs)
		// send any messages to lagoon message queues
		// update the deployment with the status
		r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, &jobPod)
		r.updateLagoonTask(opLog, &lagoonTask, &jobPod)
		r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, allContainerLogs)
		// just delete the pod
		if cancel {
			if err := r.Delete(ctx, &jobPod); err != nil {
				return err
			}
		}
	}
	return nil
}
