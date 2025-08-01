package v1beta2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// TaskRunningPendingStatus .
	TaskRunningPendingStatus = []string{
		TaskStatusPending.String(),
		TaskStatusQueued.String(),
		TaskStatusRunning.String(),
	}
	// TaskCompletedCancelledFailedStatus .
	TaskCompletedCancelledFailedStatus = []string{
		TaskStatusFailed.String(),
		TaskStatusComplete.String(),
		TaskStatusCancelled.String(),
	}
	// TaskRunningPendingStatus .
	TaskRunningPendingFailedStatus = []string{
		TaskStatusPending.String(),
		TaskStatusQueued.String(),
		TaskStatusRunning.String(),
		TaskStatusFailed.String(),
	}
)

// TaskContainsStatus .
func TaskContainsStatus(slice []metav1.Condition, s metav1.Condition) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// DeleteLagoonTasks will delete any lagoon tasks from the namespace.
func DeleteLagoonTasks(ctx context.Context, opLog logr.Logger, cl client.Client, ns, project, environment string) bool {
	lagoonTasks := &LagoonTaskList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := cl.List(ctx, lagoonTasks, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"unable to list lagoon task in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, lagoonTask := range lagoonTasks.Items {
		if err := cl.Delete(ctx, &lagoonTask); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"unable to delete lagoon task %s in %s for project %s, environment %s",
					lagoonTask.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted lagoon task %s in  %s for project %s, environment %s",
				lagoonTask.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// LagoonTaskPruner will prune any build crds that are hanging around.
func LagoonTaskPruner(ctx context.Context, cl client.Client, cns string, tasksToKeep int) {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonTaskPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := cl.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, "unable to list namespaces created by Lagoon, there may be none or something went wrong")
		return
	}
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pruner", ns.Name))
			continue
		}
		opLog.Info(fmt.Sprintf("Checking LagoonTasks in namespace %s", ns.Name))
		lagoonTasks := &LagoonTaskList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": cns, // created by this controller
			}),
		})
		if err := cl.List(ctx, lagoonTasks, listOption); err != nil {
			opLog.Error(err, "unable to list LagoonTask resources, there may be none or something went wrong")
			continue
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonTasks.Items, func(i, j int) bool {
			return lagoonTasks.Items[i].CreationTimestamp.After(lagoonTasks.Items[j].CreationTimestamp.Time)
		})
		if len(lagoonTasks.Items) > tasksToKeep {
			for idx, lagoonTask := range lagoonTasks.Items {
				if idx >= tasksToKeep {
					if helpers.ContainsString(
						TaskCompletedCancelledFailedStatus,
						lagoonTask.Labels["lagoon.sh/taskStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonTask %s", lagoonTask.Name))
						if err := cl.Delete(ctx, &lagoonTask); err != nil {
							opLog.Error(err, "unable to update status condition")
							break
						}
					}
				}
			}
		}
	}
}

// TaskPodPruner will prune any task pods that are hanging around.
func TaskPodPruner(ctx context.Context, cl client.Client, cns string, taskPodsToKeep int) {
	opLog := ctrl.Log.WithName("utilities").WithName("TaskPodPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := cl.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, "unable to list namespaces created by Lagoon, there may be none or something went wrong")
		return
	}
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod pruner", ns.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking Lagoon task pods in namespace %s", ns.Name))
		taskPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "task",
				"lagoon.sh/controller": cns, // created by this controller
			}),
		})
		if err := cl.List(ctx, taskPods, listOption); err != nil {
			opLog.Error(err, "unable to list Lagoon task pods, there may be none or something went wrong")
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(taskPods.Items, func(i, j int) bool {
			return taskPods.Items[i].CreationTimestamp.After(taskPods.Items[j].CreationTimestamp.Time)
		})
		if len(taskPods.Items) > taskPodsToKeep {
			for idx, pod := range taskPods.Items {
				if idx >= taskPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.Name))
						if err := cl.Delete(ctx, &pod); err != nil {
							opLog.Error(err, "unable to delete pod")
							break
						}
					}
				}
			}
		}
	}
}

func updateLagoonTask(namespace string, taskSpec LagoonTaskSpec) ([]byte, error) {
	// @TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", taskSpec.Task.ID, helpers.HashString(taskSpec.Task.ID)[0:6])
	if taskSpec.Task.TaskName != "" {
		taskName = taskSpec.Task.TaskName
	}
	// if the task isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the task to cancelled
	// this allows us to update tasks in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	msg := schema.LagoonMessage{
		Type:      "task",
		Namespace: namespace,
		Meta: &schema.LagoonLogMeta{
			Environment: taskSpec.Environment.Name,
			Project:     taskSpec.Project.Name,
			JobName:     taskName,
			JobStatus:   "cancelled",
			Task: &schema.LagoonTaskInfo{
				TaskName: taskSpec.Task.TaskName,
				ID:       taskSpec.Task.ID,
				Name:     taskSpec.Task.Name,
				Service:  taskSpec.Task.Service,
			},
		},
	}
	// if the task isn't found at all, then set the start/end time to be now
	// to stop the duration counter in the ui
	msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("unable to encode message as JSON: %v", err)
	}
	return msgBytes, nil
}

// CancelTask handles cancelling tasks or handling if a tasks no longer exists.
func CancelTask(ctx context.Context, cl client.Client, namespace string, body []byte) (bool, []byte, error) {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	jobSpec := &LagoonTaskSpec{}
	_ = json.Unmarshal(body, jobSpec)
	var jobPod corev1.Pod
	// @TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", jobSpec.Task.ID, helpers.HashString(jobSpec.Task.ID)[0:6])
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	if err := cl.Get(ctx, types.NamespacedName{
		Name:      taskName,
		Namespace: namespace,
	}, &jobPod); err != nil {
		// since there was no task pod, check for the lagoon task resource
		var lagoonTask LagoonTask
		if err := cl.Get(ctx, types.NamespacedName{
			Name:      taskName,
			Namespace: namespace,
		}, &lagoonTask); err != nil {
			opLog.Info(fmt.Sprintf(
				"unable to find task %s to cancel it. Sending response to Lagoon to update the task to cancelled.",
				taskName,
			))
			// if there is no pod or task, update the task in Lagoon to cancelled
			b, err := updateLagoonTask(namespace, *jobSpec)
			return false, b, err
		}
		// as there is no task pod, but there is a lagoon task resource
		// update it to cancelled so that the controller doesn't try to run it
		lagoonTask.Labels["lagoon.sh/taskStatus"] = TaskStatusCancelled.String()
		if err := cl.Update(ctx, &lagoonTask); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"unable to update task %s to cancel it.",
					taskName,
				),
			)
			return false, nil, err
		}
		// and then send the response back to lagoon to say it was cancelled.
		b, err := updateLagoonTask(namespace, *jobSpec)
		return true, b, err
	}
	jobPod.Labels["lagoon.sh/cancelTask"] = "true"
	if err := cl.Update(ctx, &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"unable to update task %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return false, nil, err
	}
	return false, nil, nil
}

// returns all tasks that are running in a given namespace
func NamespaceRunningTasks(namespace string, runningTasks []string) ([]CachedTaskItem, error) {
	var tasks []CachedTaskItem
	for _, str := range runningTasks {
		var b CachedTaskItem
		if err := json.Unmarshal([]byte(str), &b); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
		}
		if b.Namespace == namespace {
			tasks = append(tasks, b)
		}
	}
	return tasks, nil
}

// returns all tasks that are currently in the queue
func SortQueuedTasks(pendingTasks []string) ([]CachedTaskQueueItem, error) {
	var tasks []CachedTaskQueueItem
	for _, str := range pendingTasks {
		var b CachedTaskQueueItem
		if err := json.Unmarshal([]byte(str), &b); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
		}
		tasks = append(tasks, b)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreationTimestamp < tasks[j].CreationTimestamp
	})
	return tasks, nil
}

// returns all tasks that are currently in the queue in a given namespace
func SortQueuedNamespaceTasks(namespace string, pendingTasks []string) ([]CachedTaskQueueItem, error) {
	var tasks []CachedTaskQueueItem
	for _, str := range pendingTasks {
		var b CachedTaskQueueItem
		if err := json.Unmarshal([]byte(str), &b); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
		}
		if b.Namespace == namespace {
			tasks = append(tasks, b)
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreationTimestamp < tasks[j].CreationTimestamp
	})
	return tasks, nil
}
