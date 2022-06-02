package v1beta1

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
			return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, cancel)
		}
	}
	if jobPod.Status.Phase == corev1.PodPending {
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
				logMsg := fmt.Sprintf("%v: %v", container.State.Waiting.Reason, container.State.Waiting.Message)
				return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, []byte(logMsg), false)
			}
		}
		return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, false)
	} else if jobPod.Status.Phase == corev1.PodRunning {
		// if the pod is running and detects a change to the pod (eg, detecting an updated lagoon.sh/taskStep label)
		// then ship or store the logs
		// get the task associated to this pod, the information in the resource is used for shipping the logs
		var lagoonTask lagoonv1beta1.LagoonTask
		err := r.Get(ctx,
			types.NamespacedName{
				Namespace: jobPod.ObjectMeta.Namespace,
				Name:      jobPod.ObjectMeta.Labels["lagoon.sh/taskName"],
			}, &lagoonTask)
		if err != nil {
			return err
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
	}
	// if it isn't pending, failed, or complete, it will be running, we should tell lagoon
	return r.updateTaskWithLogs(ctx, req, lagoonTask, jobPod, nil, false)
}

// taskLogsToLagoonLogs sends the task logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonMonitorReconciler) taskLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
	condition string,
	logs []byte,
) (bool, lagoonv1beta1.LagoonLog) {
	if r.EnableMQ {
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
			return true, msg
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonLog{}
}

// updateLagoonTask sends the status of the task and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonMonitorReconciler) updateLagoonTask(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
	condition string,
) (bool, lagoonv1beta1.LagoonMessage) {
	namespace := helpers.GenerateNamespaceName(
		lagoonTask.Spec.Project.NamespacePattern, // the namespace pattern or `openshiftProjectPattern` from Lagoon is never received by the controller
		lagoonTask.Spec.Environment.Name,
		lagoonTask.Spec.Project.Name,
		r.NamespacePrefix,
		r.ControllerNamespace,
		r.RandomNamespacePrefix,
	)
	if r.EnableMQ {
		if condition == "failed" || condition == "complete" || condition == "cancelled" {
			time.AfterFunc(31*time.Second, func() {
				taskRunningStatus.Delete(prometheus.Labels{
					"task_namespace": lagoonTask.ObjectMeta.Namespace,
					"task_name":      lagoonTask.ObjectMeta.Name,
				})
			})
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
			return true, msg
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonMessage{}
}

// taskStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) taskStatusLogsToLagoonLogs(opLog logr.Logger,
	lagoonTask *lagoonv1beta1.LagoonTask,
	jobPod *corev1.Pod,
	condition string,
) (bool, lagoonv1beta1.LagoonLog) {
	if r.EnableMQ {
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
			return true, msg
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, lagoonv1beta1.LagoonLog{}
}

// updateTaskWithLogs collects logs from the task containers and ships or stores them
func (r *LagoonMonitorReconciler) updateTaskWithLogs(
	ctx context.Context,
	req ctrl.Request,
	lagoonTask lagoonv1beta1.LagoonTask,
	jobPod corev1.Pod,
	logs []byte,
	cancel bool,
) error {
	opLog := r.Log.WithValues("lagoonmonitor", req.NamespacedName)
	var taskCondition lagoonv1beta1.TaskStatusType
	switch jobPod.Status.Phase {
	case corev1.PodFailed:
		taskCondition = lagoonv1beta1.TaskStatusFailed
	case corev1.PodSucceeded:
		taskCondition = lagoonv1beta1.TaskStatusComplete
	case corev1.PodPending:
		taskCondition = lagoonv1beta1.TaskStatusPending
	case corev1.PodRunning:
		taskCondition = lagoonv1beta1.TaskStatusRunning
	}
	collectLogs := true
	if cancel {
		taskCondition = lagoonv1beta1.TaskStatusCancelled
	}
	// if the task status is Pending or Running
	// then the taskCondition is Failed, Complete, or Cancelled
	// then update the task to reflect the current pod status
	// we do this so we don't update the status of the task again
	if helpers.ContainsString(
		helpers.TaskRunningPendingStatus,
		lagoonTask.Labels["lagoon.sh/taskStatus"],
	) || cancel {
		opLog.Info(
			fmt.Sprintf(
				"Updating task status for %s to %v",
				jobPod.ObjectMeta.Name,
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
						allContainerLogs = append(allContainerLogs, []byte(fmt.Sprintf(`
========================================
Task cancelled
========================================`))...)
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
					"lagoon.sh/taskStatus": string(taskCondition),
				},
			},
		}

		condition := lagoonv1beta1.LagoonTaskConditions{
			Type:               taskCondition,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: time.Now().UTC().Format(time.RFC3339),
		}
		if !helpers.TaskContainsStatus(lagoonTask.Status.Conditions, condition) {
			lagoonTask.Status.Conditions = append(lagoonTask.Status.Conditions, condition)
			mergeMap["status"] = map[string]interface{}{
				"conditions": lagoonTask.Status.Conditions,
				"log":        allContainerLogs,
			}
		}

		// send any messages to lagoon message queues
		// update the deployment with the status
		pendingStatus, pendingStatusMessage := r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, strings.ToLower(string(taskCondition)))
		pendingEnvironment, pendingEnvironmentMessage := r.updateLagoonTask(opLog, &lagoonTask, &jobPod, strings.ToLower(string(taskCondition)))
		var pendingTaskLog bool
		var pendingTaskLogMessage lagoonv1beta1.LagoonLog
		// if the container logs can't be retrieved, we don't want to send any task logs back, as this will nuke
		// any previously received logs
		if !strings.Contains(string(allContainerLogs), "unable to retrieve container logs for containerd") {
			pendingTaskLog, pendingTaskLogMessage = r.taskLogsToLagoonLogs(opLog, &lagoonTask, &jobPod, strings.ToLower(string(taskCondition)), allContainerLogs)
		}

		if pendingStatus || pendingEnvironment || pendingTaskLog {
			mergeMap["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["lagoon.sh/pendingMessages"] = "true"
			if pendingStatus {
				mergeMap["statusMessages"].(map[string]interface{})["statusMessage"] = pendingStatusMessage
			}
			if pendingEnvironment {
				mergeMap["statusMessages"].(map[string]interface{})["environmentMessage"] = pendingEnvironmentMessage
			}
			if pendingTaskLog {
				mergeMap["statusMessages"].(map[string]interface{})["taskLogMessage"] = pendingTaskLogMessage
			}
		}
		if !pendingStatus && !pendingEnvironment && !pendingTaskLog {
			mergeMap["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["lagoon.sh/pendingMessages"] = nil
			mergeMap["statusMessages"] = nil
		}
		mergePatch, _ := json.Marshal(mergeMap)
		// check if the task exists
		if err := r.Get(ctx, req.NamespacedName, &lagoonTask); err == nil {
			// if it does, try to patch it
			if err := r.Patch(ctx, &lagoonTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
				opLog.Error(err, fmt.Sprintf("Unable to update resource"))
			}
		}
		// just delete the pod
		if cancel {
			if err := r.Get(ctx, req.NamespacedName, &jobPod); err == nil {
				if r.EnableDebug {
					opLog.Info(fmt.Sprintf("Task pod exists %s", jobPod.ObjectMeta.Name))
				}
				if err := r.Delete(ctx, &jobPod); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
