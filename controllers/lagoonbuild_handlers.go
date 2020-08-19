package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// buildLogsToLagoonLogs sends the build logs to the lagoon-logs message queue
// it contains the actual pod log output that is sent to elasticsearch, it is what eventually is displayed in the UI
func (r *LagoonMonitorReconciler) buildLogsToLagoonLogs(lagoonBuild *lagoonv1alpha1.LagoonBuild, jobPod *corev1.Pod, logs []byte) {
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
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "build-logs:builddeploy-kubernetes:" + lagoonBuild.ObjectMeta.Name,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				JobName:    lagoonBuild.ObjectMeta.Name,
				BranchName: lagoonBuild.Spec.Project.Environment,
				BuildPhase: condition,
				RemoteID:   string(jobPod.ObjectMeta.UID),
				LogLink:    lagoonBuild.Spec.Project.UILink,
			},
			Message: fmt.Sprintf(`========================================
Logs on pod %s
========================================
%s`, jobPod.ObjectMeta.Name, logs),
		}
		msgBytes, _ := json.Marshal(msg)
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateBuildLogMessage(context.Background(), lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(context.Background(), lagoonBuild)
	}
}

// updateDeploymentAndEnvironmentTask sends the status of the build and deployment to the operatorhandler message queue in lagoon,
// this is for the handler in lagoon to process.
func (r *LagoonMonitorReconciler) updateDeploymentAndEnvironmentTask(lagoonBuild *lagoonv1alpha1.LagoonBuild,
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
			Type:      "build",
			Namespace: lagoonBuild.ObjectMeta.Namespace,
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				Environment: lagoonBuild.Spec.Project.Environment,
				Project:     lagoonBuild.Spec.Project.Name,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				LogLink:     lagoonBuild.Spec.Project.UILink,
				RemoteID:    string(jobPod.ObjectMeta.UID),
			},
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if lagoonEnv != nil {
			operatorMsg.Meta.Route = ""
			if route, ok := lagoonEnv.Data["LAGOON_ROUTE"]; ok {
				operatorMsg.Meta.Route = route
			}
			operatorMsg.Meta.Routes = []string{}
			if routes, ok := lagoonEnv.Data["LAGOON_ROUTES"]; ok {
				operatorMsg.Meta.Routes = strings.Split(routes, ",")
			}
			operatorMsg.Meta.MonitoringURLs = []string{}
			if monitoringUrls, ok := lagoonEnv.Data["LAGOON_MONITORING_URLS"]; ok {
				operatorMsg.Meta.MonitoringURLs = strings.Split(monitoringUrls, ",")
			}
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
			r.updateEnvironmentMessage(context.Background(), lagoonBuild, operatorMsg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(context.Background(), lagoonBuild)
	}
}

// buildStatusLogsToLagoonLogs sends the logs to lagoon-logs message queue, used for general messaging
func (r *LagoonMonitorReconciler) buildStatusLogsToLagoonLogs(lagoonBuild *lagoonv1alpha1.LagoonBuild,
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
		msg := lagoonv1alpha1.LagoonLog{
			Severity: "info",
			Project:  lagoonBuild.Spec.Project.Name,
			Event:    "task:builddeploy-kubernetes:" + condition, //@TODO: this probably needs to be changed to a new task event for the operator
			Meta: &lagoonv1alpha1.LagoonLogMeta{
				ProjectName: lagoonBuild.Spec.Project.Name,
				BranchName:  lagoonBuild.Spec.Project.Environment,
				BuildPhase:  condition,
				BuildName:   lagoonBuild.ObjectMeta.Name,
				LogLink:     lagoonBuild.Spec.Project.UILink,
			},
			Message: fmt.Sprintf("*[%s]* %s Build `%s` %s",
				lagoonBuild.Spec.Project.Name,
				lagoonBuild.Spec.Project.Environment,
				lagoonBuild.ObjectMeta.Name,
				string(jobPod.Status.Phase),
			),
		}
		// if we aren't being provided the lagoon config, we can skip adding the routes etc
		if lagoonEnv != nil {
			msg.Meta.Route = ""
			if route, ok := lagoonEnv.Data["LAGOON_ROUTE"]; ok {
				msg.Meta.Route = route
			}
			msg.Meta.Routes = []string{}
			if routes, ok := lagoonEnv.Data["LAGOON_ROUTES"]; ok {
				msg.Meta.Routes = strings.Split(routes, ",")
			}
			msg.Meta.MonitoringURLs = []string{}
			if monitoringUrls, ok := lagoonEnv.Data["LAGOON_MONITORING_URLS"]; ok {
				msg.Meta.MonitoringURLs = strings.Split(monitoringUrls, ",")
			}
		}
		msgBytes, _ := json.Marshal(msg)
		if err := r.Messaging.Publish("lagoon-logs", msgBytes); err != nil {
			// if we can't publish the message, set it as a pending message
			// overwrite whatever is there as these are just current state messages so it doesn't
			// really matter if we don't smootly transition in what we send back to lagoon
			r.updateBuildStatusMessage(context.Background(), lagoonBuild, msg)
			return
		}
		// if we are able to publish the message, then we need to remove any pending messages from the resource
		// and make sure we don't try and publish again
		r.removeBuildPendingMessageStatus(context.Background(), lagoonBuild)
	}
}

// updateBuildStatusCondition is used to patch the lagoon build with the status conditions for the build, plus any logs
func (r *LagoonMonitorReconciler) updateBuildStatusCondition(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	condition lagoonv1alpha1.LagoonConditions, log []byte) error {
	// set the transition time
	condition.LastTransitionTime = time.Now().UTC().Format(time.RFC3339)
	if !jobContainsStatus(lagoonBuild.Status.Conditions, condition) {
		lagoonBuild.Status.Conditions = append(lagoonBuild.Status.Conditions, condition)
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": lagoonBuild.Status.Conditions,
				"log":        log,
			},
		})
		if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
			return fmt.Errorf("Unable to update status condition: %v", err)
		}
	}
	return nil
}

// updateBuildStatusMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateBuildStatusMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
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
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateEnvironmentMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateEnvironmentMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
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
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// updateBuildLogMessage this is called if the message queue is unavailable, it stores the message that would be sent in the lagoon build
func (r *LagoonMonitorReconciler) updateBuildLogMessage(ctx context.Context,
	lagoonBuild *lagoonv1alpha1.LagoonBuild,
	buildMessage lagoonv1alpha1.LagoonLog) error {
	// set the transition time
	mergePatch, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"lagoon.sh/pendingMessages": "true",
			},
		},
		"statusMessages": map[string]interface{}{
			"buildLogMessage": buildMessage,
		},
	})
	if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
		return fmt.Errorf("Unable to update status condition: %v", err)
	}
	return nil
}

// removeBuildPendingMessageStatus purges the status messages from the resource once they are successfully re-sent
func (r *LagoonMonitorReconciler) removeBuildPendingMessageStatus(ctx context.Context, lagoonBuild *lagoonv1alpha1.LagoonBuild) error {
	// if we have the pending messages label as true, then we want to remove this label and any pending statusmessages
	// so we can avoid double handling, or an old pending message from being sent after a new pending message
	if val, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/pendingMessages"]; !ok {
		if val == "true" {
			mergePatch, _ := json.Marshal(map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"lagoon.sh/pendingMessages": "false",
					},
				},
				"statusMessages": nil,
			})
			if err := r.Patch(ctx, lagoonBuild, client.ConstantPatch(types.MergePatchType, mergePatch)); err != nil {
				return fmt.Errorf("Unable to update status condition: %v", err)
			}
		}
	}
	return nil
}
