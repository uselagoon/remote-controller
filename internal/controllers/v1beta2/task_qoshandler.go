package v1beta2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lagooncrd "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskQoS is use for the quality of service configuration for lagoon tasks.
// if MaxTasks is lower than MaxNamespaceTasks, then the limit imposed is the lower value of MaxTasks
// MaxNamespaceTasks will only be compared against if MaxTasks is greater than MaxNamespaceTasks
type TaskQoS struct {
	MaxTasks          int
	MaxNamespaceTasks int
}

func (r *LagoonTaskReconciler) qosTaskProcessor(ctx context.Context,
	opLog logr.Logger,
	lagoonTask lagooncrd.LagoonTask) (ctrl.Result, error) {
	if r.EnableDebug {
		opLog.Info("Checking which task next")
	}
	// handle the QoS task process here
	// if the task is already running, then there is no need to check which task can be started next
	if lagoonTask.Labels["lagoon.sh/taskStatus"] == lagooncrd.TaskStatusRunning.String() {
		// this is done so that all running state updates don't try to force the queue processor to run unnecessarily
		// downside is that this can lead to queue/state changes being less frequent for queued tasks in the api
		// any new tasks, or complete/failed/cancelled tasks will still force the whichtasknext processor to run though
		return ctrl.Result{}, nil
	}
	// handle the QoS task process here
	return ctrl.Result{}, r.whichTaskNext(ctx, opLog)
}

func (r *LagoonTaskReconciler) whichTaskNext(ctx context.Context, opLog logr.Logger) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabels(map[string]string{
			"lagoon.sh/taskStatus": lagooncrd.TaskStatusRunning.String(),
			"lagoon.sh/controller": r.ControllerNamespace,
		}),
	})
	runningTasks := &lagooncrd.LagoonTaskList{}
	if err := r.List(ctx, runningTasks, listOption); err != nil {
		return fmt.Errorf("unable to list tasks in the cluster, there may be none or something went wrong: %v", err)
	}
	tasksToStart := r.TaskQoS.MaxTasks - len(runningTasks.Items)
	if len(runningTasks.Items) >= r.TaskQoS.MaxTasks {
		// if the maximum number of tasks is hit, then drop out and try again next time
		if r.EnableDebug {
			opLog.Info(fmt.Sprintf("Currently %v running tasks, no room for new tasks to be started", len(runningTasks.Items)))
		}
		//nolint:errcheck
		go r.processQueue(ctx, opLog, tasksToStart, true)
		return nil
	}
	if tasksToStart > 0 {
		opLog.Info(fmt.Sprintf("Currently %v running tasks, room for %v tasks to be started", len(runningTasks.Items), tasksToStart))
		// if there are any free slots to start a task, do that here
		//nolint:errcheck
		go r.processQueue(ctx, opLog, tasksToStart, false)
	}
	return nil
}

var runningTaskQueueProcess bool

// this is a processor for any tasks that are currently `queued` status. all normal task activity will still be performed
// this just allows the controller to update any tasks that are in the queue periodically
// if this ran on every single event, it would flood the queue with messages, so it is restricted using `runningTaskQueueProcess` global
// to only run the process at any one time til it is complete
// tasksToStart is the number of tasks that can be started at the time the process is called
// limitHit is used to determine if the task limit has been hit, this is used to prevent new tasks from being started inside this process
func (r *LagoonTaskReconciler) processQueue(ctx context.Context, opLog logr.Logger, tasksToStart int, limitHit bool) error {
	// this should only ever be able to run one instance of at a time within a single controller
	// this is because this process is quite heavy when it goes to submit the queue messages to the api
	// the downside of this is that there can be delays with the messages it sends to the actual
	// status of the tasks, but task complete/fail/cancel will always win out on the lagoon-core side
	// so this isn't that much of an issue if there are some delays in the messages
	opLog = opLog.WithName("QueueProcessor")
	if !runningTaskQueueProcess {
		runningTaskQueueProcess = true
		if r.EnableDebug {
			opLog.Info("Processing queue")
		}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.MatchingLabels(map[string]string{
				"lagoon.sh/taskStatus": lagooncrd.TaskStatusPending.String(),
				"lagoon.sh/controller": r.ControllerNamespace,
			}),
		})
		pendingTasks := &lagooncrd.LagoonTaskList{}
		if err := r.List(ctx, pendingTasks, listOption); err != nil {
			runningTaskQueueProcess = false
			return fmt.Errorf("unable to list tasks in the cluster, there may be none or something went wrong: %v", err)
		}
		if len(pendingTasks.Items) > 0 {
			if r.EnableDebug {
				opLog.Info(fmt.Sprintf("There are %v pending tasks", len(pendingTasks.Items)))
			}
			// if we have any pending tasks, then grab the latest one and make it running
			// if there are any other pending tasks, cancel them so only the latest one runs
			sortTasks(pendingTasks)
			for idx, pTask := range pendingTasks.Items {
				// need to +1 to index because 0
				// if the `limitHit` is not set it means that task qos has reached the maximum that this remote has allowed to start
				if idx+1 <= tasksToStart && !limitHit {
					if r.EnableDebug {
						opLog.Info(fmt.Sprintf("Checking if task %s can be started", pTask.Name))
					}
					// if we do have a `lagoon.sh/taskStatus` set, then process as normal
					runningNSTasks := &lagooncrd.LagoonTaskList{}
					listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
						client.InNamespace(pTask.Namespace),
						client.MatchingLabels(map[string]string{
							"lagoon.sh/taskStatus": lagooncrd.TaskStatusRunning.String(),
							"lagoon.sh/controller": r.ControllerNamespace,
						}),
					})
					// list any tasks that are running
					if err := r.List(ctx, runningNSTasks, listOption); err != nil {
						runningTaskQueueProcess = false
						return fmt.Errorf("unable to list tasks in the namespace, there may be none or something went wrong: %v", err)
					}

					// if there is a limit to the number of tasks per namespace, enforce that here
					if len(runningNSTasks.Items) < r.TaskQoS.MaxNamespaceTasks {
						pendingTasks := &lagooncrd.LagoonTaskList{}
						listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
							client.InNamespace(pTask.Namespace),
							client.MatchingLabels(map[string]string{"lagoon.sh/taskStatus": lagooncrd.TaskStatusPending.String()}),
						})
						if err := r.List(ctx, pendingTasks, listOption); err != nil {
							return fmt.Errorf("unable to list tasks in the namespace, there may be none or something went wrong: %v", err)
						}
						/*
							if namespaces or sorting allows for additional edge cases
							then in the future this section should be updated to accomodate these additional rule sets
							right now the sorting sorts by creation time, and then only the first pending item is started
							all other tasks remain pending
						*/
						sortTasks(pendingTasks)
						// opLog.Info(fmt.Sprintf("There are %v pending tasks", len(pendingTasks.Items)))
						// if we have any pending tasks, then grab the latest one and make it running
						// this is where the task controller will take over and start the pod
						for idx, pTask := range pendingTasks.Items {
							pendingTask := pTask.DeepCopy()
							// if the task is the first in the sorted list of pending tasks in this namespace, then start it
							if idx == 0 {
								pendingTask.Labels["lagoon.sh/taskStatus"] = lagooncrd.TaskStatusRunning.String()
							}
							if err := r.Update(ctx, pendingTask); err != nil {
								return err
							}
						}
						// don't handle the queued process for this task, continue to next in the list
						continue
					}
					// The object is not being deleted, so if it does not have our finalizer,
					// then lets add the finalizer and update the object. This is equivalent
					// registering our finalizer.
					if !helpers.ContainsString(pTask.Finalizers, taskFinalizer) {
						pTask.Finalizers = append(pTask.Finalizers, taskFinalizer)
						// use patches to avoid update errors
						mergePatch, _ := json.Marshal(map[string]interface{}{
							"metadata": map[string]interface{}{
								"finalizers": pTask.Finalizers,
							},
						})
						if err := r.Patch(ctx, &pTask, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
							runningTaskQueueProcess = false
							return err
						}
					}
				}
				// update the task to be queued, and add a log message with the task log with the current position in the queue
				// this position will update as tasks are created/processed, so the position of a task could change depending on
				// higher or lower priority tasks being created
				if err := r.updateQueuedTask(pTask, (idx + 1), len(pendingTasks.Items), opLog); err != nil {
					runningTaskQueueProcess = false
					return nil
				}
			}
		}
		runningTaskQueueProcess = false
	}
	return nil
}
