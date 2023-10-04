package messenger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ActiveStandbyPayload struct {
	SourceNamespace      string `json:"sourceNamespace"`
	DestinationNamespace string `json:"destinationNamespace"`
}

// CancelBuild handles cancelling builds or handling if a build no longer exists.
func (m *Messenger) CancelBuild(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	var jobPod corev1.Pod
	if err := m.Client.Get(context.Background(), types.NamespacedName{
		Name:      jobSpec.Misc.Name,
		Namespace: namespace,
	}, &jobPod); err != nil {
		opLog.Info(fmt.Sprintf(
			"Unable to find build pod %s to cancel it. Checking to see if LagoonBuild exists.",
			jobSpec.Misc.Name,
		))
		// since there was no build pod, check for the lagoon build resource
		var lagoonBuild lagoonv1beta1.LagoonBuild
		if err := m.Client.Get(context.Background(), types.NamespacedName{
			Name:      jobSpec.Misc.Name,
			Namespace: namespace,
		}, &lagoonBuild); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find build %s to cancel it. Sending response to Lagoon to update the build to cancelled.",
				jobSpec.Misc.Name,
			))
			// if there is no pod or build, update the build in Lagoon to cancelled, assume completely cancelled with no other information
			m.updateLagoonBuild(opLog, namespace, *jobSpec, nil)
			return nil
		}
		// as there is no build pod, but there is a lagoon build resource
		// update it to cancelled so that the controller doesn't try to run it
		// check if the build has existing status or not though to consume it
		if helpers.ContainsString(
			helpers.BuildRunningPendingStatus,
			lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"],
		) {
			lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"] = lagoonv1beta1.BuildStatusCancelled.String()
		}
		lagoonBuild.ObjectMeta.Labels["lagoon.sh/cancelBuildNoPod"] = "true"
		if err := m.Client.Update(context.Background(), &lagoonBuild); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update build %s to cancel it.",
					jobSpec.Misc.Name,
				),
			)
			return err
		}
		// and then send the response back to lagoon to say it was cancelled.
		m.updateLagoonBuild(opLog, namespace, *jobSpec, &lagoonBuild)
		return nil
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
	if err := m.Client.Update(context.Background(), &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update build %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

// CancelTask handles cancelling tasks or handling if a tasks no longer exists.
func (m *Messenger) CancelTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	var jobPod corev1.Pod
	//@TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", jobSpec.Task.ID, helpers.HashString(jobSpec.Task.ID)[0:6])
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	if err := m.Client.Get(context.Background(), types.NamespacedName{
		Name:      taskName,
		Namespace: namespace,
	}, &jobPod); err != nil {
		// since there was no task pod, check for the lagoon task resource
		var lagoonTask lagoonv1beta1.LagoonTask
		if err := m.Client.Get(context.Background(), types.NamespacedName{
			Name:      taskName,
			Namespace: namespace,
		}, &lagoonTask); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find task %s to cancel it. Sending response to Lagoon to update the task to cancelled.",
				taskName,
			))
			// if there is no pod or task, update the task in Lagoon to cancelled
			m.updateLagoonTask(opLog, namespace, *jobSpec)
			return nil
		}
		// as there is no task pod, but there is a lagoon task resource
		// update it to cancelled so that the controller doesn't try to run it
		lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"] = lagoonv1beta1.TaskStatusCancelled.String()
		if err := m.Client.Update(context.Background(), &lagoonTask); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update task %s to cancel it.",
					taskName,
				),
			)
			return err
		}
		// and then send the response back to lagoon to say it was cancelled.
		m.updateLagoonTask(opLog, namespace, *jobSpec)
		return nil
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelTask"] = "true"
	if err := m.Client.Update(context.Background(), &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update task %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

func (m *Messenger) updateLagoonBuild(opLog logr.Logger, namespace string, jobSpec lagoonv1beta1.LagoonTaskSpec, lagoonBuild *lagoonv1beta1.LagoonBuild) {
	// if the build isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the build to cancelled
	// this allows us to update builds in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	buildCondition := "cancelled"
	if lagoonBuild != nil {
		if val, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; ok {
			// if the build isnt running,pending,queued, then set the buildcondition to the value failed/complete/cancelled
			if !helpers.ContainsString(helpers.BuildRunningPendingStatus, val) {
				buildCondition = strings.ToLower(val)
			}
		}
	}
	msg := lagoonv1beta1.LagoonMessage{
		Type:      "build",
		Namespace: namespace,
		Meta: &lagoonv1beta1.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			BuildPhase:  buildCondition,
			BuildName:   jobSpec.Misc.Name,
		},
	}
	// set the start/end time to be now as the default
	// to stop the duration counter in the ui
	msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")

	// if possible, get the start and end times from the build resource, these will be sent back to lagoon to update the api
	if lagoonBuild != nil && lagoonBuild.Status.Conditions != nil {
		conditions := lagoonBuild.Status.Conditions
		// sort the build conditions by time so the first and last can be extracted
		sort.Slice(conditions, func(i, j int) bool {
			iTime, _ := time.Parse("2006-01-02T15:04:05Z", conditions[i].LastTransitionTime)
			jTime, _ := time.Parse("2006-01-02T15:04:05Z", conditions[j].LastTransitionTime)
			return iTime.Before(jTime)
		})
		// get the starting time, or fallback to default
		sTime, err := time.Parse("2006-01-02T15:04:05Z", conditions[0].LastTransitionTime)
		if err == nil {
			msg.Meta.StartTime = sTime.Format("2006-01-02 15:04:05")
		}
		// get the ending time, or fallback to default
		eTime, err := time.Parse("2006-01-02T15:04:05Z", conditions[len(conditions)-1].LastTransitionTime)
		if err == nil {
			msg.Meta.EndTime = eTime.Format("2006-01-02 15:04:05")
		}
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		opLog.Error(err, "Unable to encode message as JSON")
	}
	// publish the cancellation result back to lagoon
	if err := m.Publish("lagoon-tasks:controller", msgBytes); err != nil {
		opLog.Error(err, "Unable to publish message.")
	}
}

func (m *Messenger) updateLagoonTask(opLog logr.Logger, namespace string, jobSpec lagoonv1beta1.LagoonTaskSpec) {
	//@TODO: use `taskName` in the future only
	taskName := fmt.Sprintf("lagoon-task-%s-%s", jobSpec.Task.ID, helpers.HashString(jobSpec.Task.ID)[0:6])
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	// if the task isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the task to cancelled
	// this allows us to update tasks in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	msg := lagoonv1beta1.LagoonMessage{
		Type:      "task",
		Namespace: namespace,
		Meta: &lagoonv1beta1.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			JobName:     taskName,
			JobStatus:   "cancelled",
			Task: &lagoonv1beta1.LagoonTaskInfo{
				TaskName: jobSpec.Task.TaskName,
				ID:       jobSpec.Task.ID,
				Name:     jobSpec.Task.Name,
				Service:  jobSpec.Task.Service,
			},
		},
	}
	// if the task isn't found at all, then set the start/end time to be now
	// to stop the duration counter in the ui
	msg.Meta.StartTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msg.Meta.EndTime = time.Now().UTC().Format("2006-01-02 15:04:05")
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		opLog.Error(err, "Unable to encode message as JSON")
	}
	// publish the cancellation result back to lagoon
	if err := m.Publish("lagoon-tasks:controller", msgBytes); err != nil {
		opLog.Error(err, "Unable to publish message.")
	}
}

// IngressRouteMigration handles running the ingress migrations.
func (m *Messenger) IngressRouteMigration(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	return m.createAdvancedTask(namespace, jobSpec, nil)
}

// ActiveStandbySwitch handles running the active standby switch setup advanced task.
func (m *Messenger) ActiveStandbySwitch(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	asPayload := &ActiveStandbyPayload{}
	err := json.Unmarshal([]byte(jobSpec.AdvancedTask.JSONPayload), asPayload)
	if err != nil {
		return fmt.Errorf("Unable to unmarshal json payload: %v", err)
	}
	return m.createAdvancedTask(namespace, jobSpec, map[string]string{
		"lagoon.sh/activeStandby":                     "true",
		"lagoon.sh/activeStandbyDestinationNamespace": asPayload.DestinationNamespace,
		"lagoon.sh/activeStandbySourceNamespace":      asPayload.SourceNamespace,
	})
}

// AdvancedTask handles running the ingress migrations.
func (m *Messenger) AdvancedTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	return m.createAdvancedTask(namespace, jobSpec, nil)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func (m *Messenger) createAdvancedTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec, additionalLabels map[string]string) error {
	return createAdvancedTask(namespace, jobSpec, m, additionalLabels)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func createAdvancedTask(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec, m *Messenger, additionalLabels map[string]string) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// create the advanced task
	task := lagoonv1beta1.LagoonTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lagoon-advanced-task-" + helpers.RandString(6),
			Namespace: namespace,
			Labels: map[string]string{
				"lagoon.sh/taskType":   lagoonv1beta1.TaskTypeAdvanced.String(),
				"lagoon.sh/taskStatus": lagoonv1beta1.TaskStatusPending.String(),
				"lagoon.sh/controller": m.ControllerNamespace,
			},
		},
		Spec: *jobSpec,
	}
	// add additional labels if required
	for key, value := range additionalLabels {
		task.ObjectMeta.Labels[key] = value
	}
	if err := m.Client.Create(context.Background(), &task); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create task for job %s.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}
