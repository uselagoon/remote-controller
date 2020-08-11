package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// taskLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
func (r *LagoonMonitorReconciler) taskLogsToLagoonLogs(lagoonTask *lagoonv1alpha1.LagoonTask, jobPod *corev1.Pod, logs []byte) {
	if r.EnableMQ {
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "build-logs:job-kubernetes:" + lagoonTask.ObjectMeta.Name,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				JobName:   lagoonTask.ObjectMeta.Name,
				JobStatus: string(jobPod.Status.Phase),
				RemoteID:  string(jobPod.ObjectMeta.UID),
			},
			Message: fmt.Sprintf(`
========================================
Logs on pod %s
========================================
%s`, jobPod.ObjectMeta.Name, logs),
		}
		msgBytes, _ := json.Marshal(msg)
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.lagoonTask(context.Background(), lagoonTask, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeTaskPendingMessageStatus(context.Background(), lagoonTask)
	}
}

// updateLagoonTask sends the status of the build and deployment to the operatorhandler message queue in lagoon,
// this is for the operatorhandler to process.
func (r *LagoonMonitorReconciler) updateLagoonTask(lagoonTask *lagoonv1alpha1.LagoonTask,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap) {
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
		operatorMsg := lagoonv1alpha1.LagoonMessage{
			Type:      "task",
			Namespace: lagoonTask.ObjectMeta.Namespace,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				Environment: lagoonTask.Spec.Environment.Name,
				Project:     lagoonTask.Spec.Project.Name,
				JobName:     lagoonTask.ObjectMeta.Name,
				JobStatus:   condition,
				RemoteID:    string(jobPod.ObjectMeta.UID),
			},
		}
		// we can add the build start time here
		if jobPod.Status.StartTime != nil {
			operatorMsg.Meta.StartTime = jobPod.Status.StartTime.Time.UTC().Format("2006-01-02 15:04:05")
		}
		// and then once the pod is terminated we can add the terminated time here
		if jobPod.Status.ContainerStatuses != nil {
			if jobPod.Status.ContainerStatuses[0].State.Terminated != nil {
				operatorMsg.Meta.EndTime = jobPod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time.UTC().Format("2006-01-02 15:04:05")
			}
		}
		operatorMsgBytes, _ := json.Marshal(operatorMsg)
		if err := r.Messaging.Publish("lagoon-tasks:operator", operatorMsgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateTaskEnvironmentMessage(context.Background(), lagoonTask, operatorMsg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeTaskPendingMessageStatus(context.Background(), lagoonTask)
	}
}

// updateTaskStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonMonitorReconciler) updateTaskStatusCondition(ctx context.Context,
	lagoonTask *lagoonv1alpha1.LagoonTask,
	condition lagoonv1alpha1.LagoonConditions, log []byte) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !jobContainsStatus(lagoonTask.Status.Conditions, condition) {
		lagoonTask.Status.Conditions = append(lagoonTask.Status.Conditions, condition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": lagoonTask.Status.Conditions,
				"log":        log,
			},
		})
		if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}

// updateTaskEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateTaskEnvironmentMessage(ctx context.Context,
	lagoonTask *lagoonv1alpha1.LagoonTask,
	envMessage lagoonv1alpha1.LagoonMessage) error {
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
	if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateTaskLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) lagoonTask(ctx context.Context,
	lagoonTask *lagoonv1alpha1.LagoonTask,
	taskMessage lagoonv1alpha1.LagoonLog) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"buildLogMessage": taskMessage,
		},
	})
	if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateTaskStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateTaskStatusMessage(ctx context.Context,
	lagoonTask *lagoonv1alpha1.LagoonTask,
	statusMessage lagoonv1alpha1.LagoonLog) error {
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
	if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// taskStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) taskStatusLogsToLagoonLogs(lagoonTask *lagoonv1alpha1.LagoonTask,
	jobPod *corev1.Pod,
	lagoonEnv *corev1.ConfigMap) {
	if r.EnableMQ {
		condition := "active"
		switch jobPod.Status.Phase {
		case corev1.PodFailed:
			condition = "failed"
		case corev1.PodRunning:
			condition = "active"
		case corev1.PodSucceeded:
			condition = "succeeded"
		}
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task:job-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the operator
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				ProjectName: lagoonTask.Spec.Project.Name,
				Environment: lagoonTask.Spec.Environment.Name,
				JobName:     lagoonTask.ObjectMeta.Name,
				JobStatus:   string(jobPod.Status.Phase),
				RemoteID:    string(jobPod.ObjectMeta.UID),
			},
			Message: fmt.Sprintf("*[%s]* Task `%s` *%s* %s",
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Task.ID,
				lagoonTask.Spec.Task.Name,
				condition,
			),
		}
		msgBytes, _ := json.Marshal(msg)
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

// removeTaskPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonMonitorReconciler) removeTaskPendingMessageStatus(ctx context.Context, lagoonTask *lagoonv1alpha1.LagoonTask) error {
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
			if err := r.Patch(ctx, lagoonTask, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}
