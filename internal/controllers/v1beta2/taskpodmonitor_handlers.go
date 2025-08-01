package v1beta2

// this file is used by the `lagoonmonitor` controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *TaskMonitorReconciler) handleTaskMonitor(ctx context.Context, opLog logr.Logger, req ctrl.Request, jobPod corev1.Pod) error {
	// get the task associated to this pod, we wil need update it at some point
	var lagoonTask lagooncrd.LagoonTask
	err := r.Get(ctx, types.NamespacedName{
		Namespace: jobPod.Namespace,
		Name:      jobPod.Labels["lagoon.sh/taskName"],
	}, &lagoonTask)
	if err != nil {
		return err
	}
	// ensure the task is not in the queue
	r.QueueCache.Remove(jobPod.Name)
	cancel := false
	if cancelTask, ok := jobPod.Labels["lagoon.sh/cancelTask"]; ok {
		cancel, _ = strconv.ParseBool(cancelTask)
	}
	_, ok := r.Cache.Get(lagoonTask.Name)
	if ok {
		opLog.Info(fmt.Sprintf("Cached cancellation exists for: %s", lagoonTask.Name))
		// this object exists in the cache meaning the task has been cancelled, set cancel to true and remove from cache
		r.Cache.Remove(lagoonTask.Name)
		cancel = true
	}
	if cancel {
		opLog.Info(fmt.Sprintf("Attempting to cancel task %s", lagoonTask.Name))
		return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, cancel)
	}
	bc := lagooncrd.NewCachedTaskItem(lagoonTask, string(jobPod.Status.Phase))
	r.TasksCache.Add(lagoonTask.Name, bc.String())
	switch jobPod.Status.Phase {
	case corev1.PodPending:

		for _, container := range jobPod.Status.ContainerStatuses {
			if container.State.Waiting != nil && helpers.ContainsString(failureStates, container.State.Waiting.Reason) {
				// if we have a failure state, then fail the task and get the logs from the container
				opLog.Info(fmt.Sprintf("Task failed, container exit reason was: %v", container.State.Waiting.Reason))
				lagoonTask.Labels["lagoon.sh/taskStatus"] = lagooncrd.TaskStatusFailed.String()
				if err := r.Update(ctx, &lagoonTask); err != nil {
					return err
				}
				opLog.Info(fmt.Sprintf("Marked task %s as %s", lagoonTask.Name, lagooncrd.TaskStatusFailed.String()))
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
				opLog.Info(fmt.Sprintf("Deleted failed task pod: %s", jobPod.Name))
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
				logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
				return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, []byte(logMsg), false)
			}
		}
		return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, false)
	case corev1.PodRunning:
		// if the pod is running and detects a change to the pod (eg, detecting an updated lagoon.sh/taskStep label)
		// then ship or store the logs
		// get the task associated to this pod, the information in the resource is used for shipping the logs
		var lagoonTask lagooncrd.LagoonTask
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.Namespace,
				Name:      jobPod.Labels["lagoon.sh/taskName"],
			}, &lagoonTask)
		if err != nil {
			return err
		}
	case corev1.PodFailed, corev1.PodSucceeded:
		// get the task associated to this pod, we wil need update it at some point
		var lagoonTask lagooncrd.LagoonTask
		err := r.Get(ctx, types.NamespacedName{
			Namespace: jobPod.Namespace,
			Name:      jobPod.Labels["lagoon.sh/taskName"],
		}, &lagoonTask)
		if err != nil {
			return err
		}
		// remove the task from the task cache
		r.TasksCache.Remove(jobPod.Name)
	}
	// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
	return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, false)
}

// taskLogsToLagoonLogs sends the task logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *TaskMonitorReconciler) taskLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	jobPod *corev1.Pod,
	condition string,
	logs []byte,
) error {
	if r.EnableMQ && lagoonTask != nil {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task-logs:job-kubernetes:" + lagoonTask.Name,
			Meta: &schema.LagoonLogMeta{
				Task:        &lagoonTask.Spec.Task,
				Environment: lagoonTask.Spec.Environment.Name,
				JobName:     lagoonTask.Name,
				JobStatus:   condition,
				RemoteID:    string(lagoonTask.UID),
				Key:         lagoonTask.Spec.Key,
				Cluster:     r.LagoonTargetName,
			},
		}
		if jobPod.Spec.NodeName != "" {
			msg.Message = fmt.Sprintf(`================================================================================
Logs on pod %s, assigned to node %s on cluster %s
================================================================================
%s`, jobPod.Name, jobPod.Spec.NodeName, r.LagoonTargetName, logs)
		} else {
			msg.Message = fmt.Sprintf(`================================================================================
Logs on pod %s, assigned to cluster %s
================================================================================
%s`, jobPod.Name, r.LagoonTargetName, logs)
		}
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("task-logs:job-kubernetes:%s", jobPod.Name),
					jobPod.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// updateLagoonTask sends the status of the task and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *TaskMonitorReconciler) updateLagoonTask(opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	jobPod *corev1.Pod,
	condition string,
) error {
	if r.EnableMQ && lagoonTask != nil {
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			time.AfterFunc(31*time.Second, func() {
				metrics.TaskRunningStatus.Delete(prometheus.Labels{
					"task_namespace": lagoonTask.Namespace,
					"task_name":      lagoonTask.Name,
				})
			})
			time.Sleep(2 * time.Second) // smol sleep to reduce race of final messages with previous messages
		}
		msg := schema.LagoonMessage{
			Type:      "task",
			Namespace: lagoonTask.Namespace,
			Meta: &schema.LagoonLogMeta{
				Task:          &lagoonTask.Spec.Task,
				Environment:   lagoonTask.Spec.Environment.Name,
				Project:       lagoonTask.Spec.Project.Name,
				EnvironmentID: lagoonTask.Spec.Environment.ID,
				ProjectID:     lagoonTask.Spec.Project.ID,
				JobName:       lagoonTask.Name,
				JobStatus:     condition,
				RemoteID:      string(lagoonTask.UID),
				Key:           lagoonTask.Spec.Key,
				Cluster:       r.LagoonTargetName,
			},
		}
		if _, ok := jobPod.Annotations["lagoon.sh/taskData"]; ok {
			// if the task contains `taskData` annotation, this is used to send data back to lagoon
			// lagoon will use the data to perform an action against the api or something else
			// the data in taskData should be base64 encoded
			msg.Meta.AdvancedData = jobPod.Annotations["lagoon.sh/taskData"]
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
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published task update message for %s to lagoon-tasks:controller queue",
					jobPod.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// taskStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *TaskMonitorReconciler) taskStatusLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	condition string,
) error {
	if r.EnableMQ && lagoonTask != nil {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task:job-kubernetes:" + condition, // @TODO: this probably needs to be changed to a new task event for the controller
			Meta: &schema.LagoonLogMeta{
				Task:          &lagoonTask.Spec.Task,
				ProjectName:   lagoonTask.Spec.Project.Name,
				Environment:   lagoonTask.Spec.Environment.Name,
				EnvironmentID: lagoonTask.Spec.Environment.ID,
				ProjectID:     lagoonTask.Spec.Project.ID,
				JobName:       lagoonTask.Name,
				JobStatus:     condition,
				RemoteID:      string(lagoonTask.UID),
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
			opLog.Error(err, "unable to encode message as JSON")
		}
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return err
		}
		if r.EnableDebug {
			opLog.Info(
				fmt.Sprintf(
					"Published event %s for %s to lagoon-logs exchange",
					fmt.Sprintf("task:job-kubernetes:%s", condition),
					lagoonTask.Name,
				),
			)
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return nil
}

// updateTaskWithLogs collects logs from the task containers and ships or stores them
func (r *TaskMonitorReconciler) updateTaskWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonTask lagooncrd.LagoonTask,
	jobPod corev1.Pod,
	logs []byte,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	taskCondition := lagooncrd.GetTaskConditionFromPod(jobPod.Status.Phase)
	collectLogs := true
	if cancel {
		taskCondition = lagooncrd.TaskStatusCancelled
	}
	// if the task status is Pending or Running
	// then the taskCondition is Failed, Complete, or Cancelled
	// then update the task to reflect the current pod status
	// we do this so we don't update the status of the task again
	if helpers.ContainsString(
		lagooncrd.TaskRunningPendingFailedStatus,
		lagoonTask.Labels["lagoon.sh/taskStatus"],
	) || cancel {
		opLog.Info(
			fmt.Sprintf(
				"Updating task status for %s to %v",
				jobPod.Name,
				taskCondition,
			),
		)
		// grab all the logs from the containers in the task pod and just merge them all together
		// we only have 1 container at the moment in a taskpod anyway so it doesn't matter
		// if we do move to multi container tasks, then worry about it
		var allContainerLogs []byte
		var err error
		if logs == nil {
			if collectLogs {
				allContainerLogs, err = r.collectLogs(ctx, req, jobPod)
				if err == nil {
					if cancel {
						cancellationMessage := "Task cancelled"
						if cancellationDetails, ok := jobPod.GetAnnotations()["lagoon.sh/cancelReason"]; ok {
							cancellationMessage = fmt.Sprintf("%v : %v", cancellationMessage, cancellationDetails)
						}
						allContainerLogs = append(allContainerLogs, []byte(fmt.Sprintf(`
========================================
%v
========================================`, cancellationMessage))...)
					}
				} else {
					allContainerLogs = []byte(fmt.Sprintf(`
========================================
Task %s
========================================`, taskCondition))
				}
			}
		} else {
			allContainerLogs = logs
		}

		mergeMap := map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"lagoon.sh/taskStatus":  taskCondition.String(),
					"lagoon.sh/taskStarted": "true",
				},
			},
		}

		condition := helpers.TaskStepToStatusCondition(taskCondition.String(), time.Now().UTC())
		_ = meta.SetStatusCondition(&lagoonTask.Status.Conditions, condition)
		mergeMap["status"] = map[string]interface{}{
			"conditions": lagoonTask.Status.Conditions,
			"phase":      taskCondition.String(),
		}

		// send any messages to lagoon message queues
		// update the deployment with the status
		if err = r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, taskCondition.ToLower()); err != nil {
			opLog.Error(err, "unable to publish task status logs")
		}
		if err = r.updateLagoonTask(opLog, &lagoonTask, &jobPod, taskCondition.ToLower()); err != nil {
			opLog.Error(err, "unable to publish task update")
		}
		// if the container logs can't be retrieved, we don't want to send any task logs back, as this will nuke
		// any previously received logs
		if !strings.Contains(string(allContainerLogs), "unable to retrieve container logs for containerd") {
			if err = r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, taskCondition.ToLower(), allContainerLogs); err != nil {
				opLog.Error(err, "unable to publish task logs")
			}
		}
		mergePatch, _ := json.Marshal(mergeMap)
		// check if the task exists
		if err := r.Get(ctx, req.NamespacedName, &lagoonTask); err == nil {
			// if it does, try to patch it
			if err := r.Patch(ctx, &lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, "unable to update resource")
			}
		}
		// just delete the pod
		if cancel {
			if err := r.Get(ctx, req.NamespacedName, &jobPod); err == nil {
				if r.EnableDebug {
					opLog.Info(fmt.Sprintf("Task pod exists %s", jobPod.Name))
				}
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
