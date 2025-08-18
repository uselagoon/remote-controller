package v1beta2

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/uselagoon/machinery/api/schema"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/metrics"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// createActiveStandbyRole will create the rolebinding for allowing lagoon-deployer to talk between namespaces for active/standby functionality
func (r *LagoonTaskReconciler) createActiveStandbyRole(ctx context.Context, sourceNamespace, destinationNamespace string) error {
	activeStandbyRoleBinding := &rbacv1.RoleBinding{}
	activeStandbyRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-deployer-activestandby",
		Namespace: destinationNamespace,
	}
	activeStandbyRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "admin",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	activeStandbyRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: sourceNamespace,
		},
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: destinationNamespace,
		Name:      "lagoon-deployer-activestandby",
	}, activeStandbyRoleBinding)
	if err != nil {
		if err := r.Create(ctx, activeStandbyRoleBinding); err != nil {
			return fmt.Errorf("there was an error creating the lagoon-deployer-activestandby role binding. Error was: %v", err)
		}
	}
	return nil
}

// updateQueuedTask will update a task if it is queued
func (r *LagoonTaskReconciler) updateQueuedTask(
	ctx context.Context,
	taskReq types.NamespacedName,
	queuePosition, queueLength int,
	opLog logr.Logger,
) error {
	var lagoonTask lagooncrd.LagoonTask
	if err := r.Get(ctx, taskReq, &lagoonTask); err != nil {
		return helpers.IgnoreNotFound(err)
	}
	if r.EnableDebug {
		opLog.Info(fmt.Sprintf("Updating task %s to queued: %s", lagoonTask.Name, fmt.Sprintf("This task is currently queued in position %v/%v", queuePosition, queueLength)))
	}
	// if we get this handler, then it is likely that the task was in a pending or running state with no actual running pod
	// so just set the logs to be cancellation message
	allContainerLogs := []byte(fmt.Sprintf(`========================================
%s
========================================
`, fmt.Sprintf("This task is currently queued in position %v/%v", queuePosition, queueLength)))
	// send any messages to lagoon message queues
	opLog.Info(fmt.Sprintf("task %v", len(allContainerLogs)))
	// update the deployment with the status, lagoon v2.12.0 supports queued status, otherwise use pending
	r.taskStatusLogsToLagoonLogs(opLog, &lagoonTask, lagooncrd.TaskStatusQueued)
	r.updateLagoonTask(opLog, &lagoonTask, lagooncrd.TaskStatusQueued)
	r.taskLogsToLagoonLogs(opLog, &lagoonTask, allContainerLogs, lagooncrd.TaskStatusQueued)
	return nil
}

// taskStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonTaskReconciler) taskStatusLogsToLagoonLogs(
	opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	taskCondition lagooncrd.TaskStatusType,
) (bool, schema.LagoonLog) {
	if r.EnableMQ {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task:job-kubernetes:" + taskCondition.ToLower(), // @TODO: this probably needs to be changed to a new task event for the controller
			Meta: &schema.LagoonLogMeta{
				Task:          &lagoonTask.Spec.Task,
				ProjectName:   lagoonTask.Spec.Project.Name,
				Environment:   lagoonTask.Spec.Environment.Name,
				EnvironmentID: lagoonTask.Spec.Environment.ID,
				ProjectID:     lagoonTask.Spec.Project.ID,
				JobName:       lagoonTask.Name,
				JobStatus:     taskCondition.ToLower(),
				RemoteID:      string(lagoonTask.UID),
				Key:           lagoonTask.Spec.Key,
				Cluster:       r.LagoonTargetName,
			},
			Message: fmt.Sprintf("*[%s]* %s Task `%s` %s",
				lagoonTask.Spec.Project.Name,
				lagoonTask.Spec.Environment.Name,
				lagoonTask.Name,
				taskCondition.ToLower(),
			),
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		msgBytes, err := json.Marshal(msg)
		if err != nil {
			opLog.Error(err, "Unable to encode message as JSON")
		}
		// @TODO: if we can't publish the message because we are deleting the resource, then should we even
		// bother to patch the resource??
		// leave it for now cause the resource will just be deleted anyway
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			return true, msg
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
	}
	return false, schema.LagoonLog{}
}

// updateLagoonTask sends the status of the task and deployment to the controllerhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonTaskReconciler) updateLagoonTask(opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	taskCondition lagooncrd.TaskStatusType,
) (bool, schema.LagoonMessage) {
	if r.EnableMQ && lagoonTask != nil {
		if taskCondition.ToLower() == "failed" || taskCondition.ToLower() == "complete" || taskCondition.ToLower() == "cancelled" {
			metrics.TaskRunningStatus.Delete(prometheus.Labels{
				"task_namespace": lagoonTask.Namespace,
				"task_name":      lagoonTask.Name,
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
				JobStatus:     taskCondition.ToLower(),
				RemoteID:      string(lagoonTask.UID),
				Key:           lagoonTask.Spec.Key,
				Cluster:       r.LagoonTargetName,
			},
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
	return false, schema.LagoonMessage{}
}

// taskLogsToLagoonLogs sends the task logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonTaskReconciler) taskLogsToLagoonLogs(
	opLog logr.Logger,
	lagoonTask *lagooncrd.LagoonTask,
	logs []byte,
	taskCondition lagooncrd.TaskStatusType,
) (bool, schema.LagoonLog) {
	if r.EnableMQ && lagoonTask != nil {
		msg := schema.LagoonLog{
			Severity: "info",
			Project:  lagoonTask.Spec.Project.Name,
			Event:    "task-logs:job-kubernetes:" + lagoonTask.Name,
			Meta: &schema.LagoonLogMeta{
				Task:        &lagoonTask.Spec.Task,
				Environment: lagoonTask.Spec.Environment.Name,
				JobName:     lagoonTask.Name,
				JobStatus:   taskCondition.ToLower(),
				RemoteID:    string(lagoonTask.UID),
				Key:         lagoonTask.Spec.Key,
				Cluster:     r.LagoonTargetName,
			},
		}
		// add the actual task log message
		msg.Message = string(logs)
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
	return false, schema.LagoonLog{}
}
