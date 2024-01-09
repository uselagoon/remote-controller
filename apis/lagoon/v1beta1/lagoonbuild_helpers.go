package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-version"
	"github.com/uselagoon/machinery/api/schema"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// BuildRunningPendingStatus .
	BuildRunningPendingStatus = []string{
		BuildStatusPending.String(),
		BuildStatusQueued.String(),
		BuildStatusRunning.String(),
	}
	// BuildCompletedCancelledFailedStatus .
	BuildCompletedCancelledFailedStatus = []string{
		BuildStatusFailed.String(),
		BuildStatusComplete.String(),
		BuildStatusCancelled.String(),
	}
)

// BuildContainsStatus .
func BuildContainsStatus(slice []LagoonBuildConditions, s LagoonBuildConditions) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// RemoveBuild remove a LagoonBuild from a slice of LagoonBuilds
func RemoveBuild(slice []LagoonBuild, s LagoonBuild) []LagoonBuild {
	result := []LagoonBuild{}
	for _, item := range slice {
		if item.ObjectMeta.Name == s.ObjectMeta.Name {
			continue
		}
		result = append(result, item)
	}
	return result
}

// Check if the version of lagoon provided in the internal_system scope variable is greater than or equal to the checked version
func CheckLagoonVersion(build *LagoonBuild, checkVersion string) bool {
	lagoonProjectVariables := &[]helpers.LagoonEnvironmentVariable{}
	json.Unmarshal(build.Spec.Project.Variables.Project, lagoonProjectVariables)
	lagoonVersion, err := helpers.GetLagoonVariable("LAGOON_SYSTEM_CORE_VERSION", []string{"internal_system"}, *lagoonProjectVariables)
	if err != nil {
		return false
	}
	aVer, err := version.NewSemver(lagoonVersion.Value)
	if err != nil {
		return false
	}
	bVer, err := version.NewSemver(checkVersion)
	if err != nil {
		return false
	}
	return aVer.GreaterThanOrEqual(bVer)
}

// CancelExtraBuilds cancels extra builds.
func CancelExtraBuilds(ctx context.Context, r client.Client, opLog logr.Logger, ns string, status string) error {
	pendingBuilds := &LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": BuildStatusPending.String()}),
	})
	if err := r.List(ctx, pendingBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	if len(pendingBuilds.Items) > 0 {
		// opLog.Info(fmt.Sprintf("There are %v pending builds", len(pendingBuilds.Items)))
		// if we have any pending builds, then grab the latest one and make it running
		// if there are any other pending builds, cancel them so only the latest one runs
		sort.Slice(pendingBuilds.Items, func(i, j int) bool {
			return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.After(pendingBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		for idx, pBuild := range pendingBuilds.Items {
			pendingBuild := pBuild.DeepCopy()
			if idx == 0 {
				pendingBuild.Labels["lagoon.sh/buildStatus"] = status
			} else {
				// cancel any other pending builds
				opLog.Info(fmt.Sprintf("Setting build %s as cancelled", pendingBuild.ObjectMeta.Name))
				pendingBuild.Labels["lagoon.sh/buildStatus"] = BuildStatusCancelled.String()
				pendingBuild.Labels["lagoon.sh/cancelledByNewBuild"] = "true"
			}
			if err := r.Update(ctx, pendingBuild); err != nil {
				return err
			}
		}
	}
	return nil
}

func GetBuildConditionFromPod(phase corev1.PodPhase) BuildStatusType {
	var buildCondition BuildStatusType
	switch phase {
	case corev1.PodFailed:
		buildCondition = BuildStatusFailed
	case corev1.PodSucceeded:
		buildCondition = BuildStatusComplete
	case corev1.PodPending:
		buildCondition = BuildStatusPending
	case corev1.PodRunning:
		buildCondition = BuildStatusRunning
	}
	return buildCondition
}

func GetTaskConditionFromPod(phase corev1.PodPhase) TaskStatusType {
	var taskCondition TaskStatusType
	switch phase {
	case corev1.PodFailed:
		taskCondition = TaskStatusFailed
	case corev1.PodSucceeded:
		taskCondition = TaskStatusComplete
	case corev1.PodPending:
		taskCondition = TaskStatusPending
	case corev1.PodRunning:
		taskCondition = TaskStatusRunning
	}
	return taskCondition
}

func CheckRunningBuilds(ctx context.Context, cns string, opLog logr.Logger, cl client.Client, ns corev1.Namespace) bool {
	lagoonBuilds := &LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns.ObjectMeta.Name),
		client.MatchingLabels(map[string]string{
			"lagoon.sh/controller": cns, // created by this controller
		}),
	})
	if err := cl.List(context.Background(), lagoonBuilds, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
		return false
	}
	runningBuilds := false
	sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
		return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
	})
	// if there are any builds pending or running, don't try and refresh the credentials as this
	// could break the build
	if len(lagoonBuilds.Items) > 0 {
		if helpers.ContainsString(
			BuildRunningPendingStatus,
			lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"],
		) {
			runningBuilds = true
		}
	}
	return runningBuilds
}

// DeleteLagoonBuilds will delete any lagoon builds from the namespace.
func DeleteLagoonBuilds(ctx context.Context, opLog logr.Logger, cl client.Client, ns, project, environment string) bool {
	lagoonBuilds := &LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := cl.List(ctx, lagoonBuilds, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list lagoon build in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, lagoonBuild := range lagoonBuilds.Items {
		if err := cl.Delete(ctx, &lagoonBuild); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete lagoon build %s in %s for project %s, environment %s",
					lagoonBuild.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted lagoon build %s in  %s for project %s, environment %s",
				lagoonBuild.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

func LagoonBuildPruner(ctx context.Context, cl client.Client, cns string, buildsToKeep int) {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonBuildPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := cl.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting build pruner", ns.ObjectMeta.Name))
			continue
		}
		opLog.Info(fmt.Sprintf("Checking LagoonBuilds in namespace %s", ns.ObjectMeta.Name))
		lagoonBuilds := &LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": cns, // created by this controller
			}),
		})
		if err := cl.List(ctx, lagoonBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuild resources, there may be none or something went wrong"))
			continue
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
			return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(lagoonBuilds.Items) > buildsToKeep {
			for idx, lagoonBuild := range lagoonBuilds.Items {
				if idx >= buildsToKeep {
					if helpers.ContainsString(
						BuildCompletedCancelledFailedStatus,
						lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonBuild %s", lagoonBuild.ObjectMeta.Name))
						if err := cl.Delete(ctx, &lagoonBuild); err != nil {
							opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
							break
						}
					}
				}
			}
		}
	}
	return
}

// BuildPodPruner will prune any build pods that are hanging around.
func BuildPodPruner(ctx context.Context, cl client.Client, cns string, buildPodsToKeep int) {
	opLog := ctrl.Log.WithName("utilities").WithName("BuildPodPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := cl.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting build pod pruner", ns.ObjectMeta.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking Lagoon build pods in namespace %s", ns.ObjectMeta.Name))
		buildPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "build",
				"lagoon.sh/controller": cns, // created by this controller
			}),
		})
		if err := cl.List(ctx, buildPods, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(buildPods.Items, func(i, j int) bool {
			return buildPods.Items[i].ObjectMeta.CreationTimestamp.After(buildPods.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(buildPods.Items) > buildPodsToKeep {
			for idx, pod := range buildPods.Items {
				if idx >= buildPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.ObjectMeta.Name))
						if err := cl.Delete(ctx, &pod); err != nil {
							opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
							break
						}
					}
				}
			}
		}
	}
	return
}

func updateLagoonBuild(opLog logr.Logger, namespace string, jobSpec LagoonTaskSpec, lagoonBuild *LagoonBuild) ([]byte, error) {
	// if the build isn't found by the controller
	// then publish a response back to controllerhandler to tell it to update the build to cancelled
	// this allows us to update builds in the API that may have gone stale or not updated from `New`, `Pending`, or `Running` status
	buildCondition := "cancelled"
	if lagoonBuild != nil {
		if val, ok := lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"]; ok {
			// if the build isnt running,pending,queued, then set the buildcondition to the value failed/complete/cancelled
			if !helpers.ContainsString(BuildRunningPendingStatus, val) {
				buildCondition = strings.ToLower(val)
			}
		}
	}
	msg := schema.LagoonMessage{
		Type:      "build",
		Namespace: namespace,
		Meta: &schema.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			BuildStatus: buildCondition,
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
		return nil, fmt.Errorf("Unable to encode message as JSON: %v", err)
	}
	return msgBytes, nil
}

// CancelBuild handles cancelling builds or handling if a build no longer exists.
func CancelBuild(ctx context.Context, cl client.Client, namespace string, body []byte) (bool, []byte, error) {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	jobSpec := &LagoonTaskSpec{}
	json.Unmarshal(body, jobSpec)
	var jobPod corev1.Pod
	if err := cl.Get(ctx, types.NamespacedName{
		Name:      jobSpec.Misc.Name,
		Namespace: namespace,
	}, &jobPod); err != nil {
		opLog.Info(fmt.Sprintf(
			"Unable to find build pod %s to cancel it. Checking to see if LagoonBuild exists.",
			jobSpec.Misc.Name,
		))
		// since there was no build pod, check for the lagoon build resource
		var lagoonBuild LagoonBuild
		if err := cl.Get(ctx, types.NamespacedName{
			Name:      jobSpec.Misc.Name,
			Namespace: namespace,
		}, &lagoonBuild); err != nil {
			opLog.Info(fmt.Sprintf(
				"Unable to find build %s to cancel it. Sending response to Lagoon to update the build to cancelled.",
				jobSpec.Misc.Name,
			))
			// if there is no pod or build, update the build in Lagoon to cancelled, assume completely cancelled with no other information
			// and then send the response back to lagoon to say it was cancelled.
			b, err := updateLagoonBuild(opLog, namespace, *jobSpec, nil)
			return false, b, err
		}
		// as there is no build pod, but there is a lagoon build resource
		// update it to cancelled so that the controller doesn't try to run it
		// check if the build has existing status or not though to consume it
		if helpers.ContainsString(
			BuildRunningPendingStatus,
			lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"],
		) {
			lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"] = BuildStatusCancelled.String()
		}
		lagoonBuild.ObjectMeta.Labels["lagoon.sh/cancelBuildNoPod"] = "true"
		if err := cl.Update(ctx, &lagoonBuild); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update build %s to cancel it.",
					jobSpec.Misc.Name,
				),
			)
			return false, nil, err
		}
		// and then send the response back to lagoon to say it was cancelled.
		b, err := updateLagoonBuild(opLog, namespace, *jobSpec, &lagoonBuild)
		return true, b, err
	}
	jobPod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
	if err := cl.Update(ctx, &jobPod); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update build %s to cancel it.",
				jobSpec.Misc.Name,
			),
		)
		return false, nil, err
	}
	return false, nil, nil
}
