package pruner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LagoonOldProcPruner will identify and remove any long running builds or tasks.
func (p *Pruner) LagoonOldProcPruner(pruneBuilds, pruneTasks bool) {
	ctx := context.Background()
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonOldProcPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := p.APIReader.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, "unable to list namespaces created by Lagoon, there may be none or something went wrong")
		return
	}

	// now we iterate through each namespace, and look for build/task pods
	for _, ns := range namespaces.Items {

		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't search it for long running tasks
			continue
		}

		podList := corev1.PodList{
			TypeMeta: metav1.TypeMeta{},
			ListMeta: metav1.ListMeta{},
			Items:    nil,
		}

		removeBuildIfCreatedBefore, removeTaskIfCreatedBefore, err := calculateRemoveBeforeTimes(p, ns, time.Now())
		if err != nil {
			opLog.Error(err, err.Error())
			return
		}

		jobTypeLabelRequirements, _ := labels.NewRequirement("lagoon.sh/jobType", selection.Exists, nil)
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
			client.MatchingLabelsSelector{
				Selector: labels.NewSelector().Add(*jobTypeLabelRequirements),
			},
		})
		if err := p.APIReader.List(ctx, &podList, listOption); err != nil {
			opLog.Error(err, "unable to list pod resources, there may be none or something went wrong")
			continue
		}

		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodRunning {
				if podType, ok := pod.GetLabels()["lagoon.sh/jobType"]; ok {
					switch {
					case podType == "task" && pruneTasks && pod.CreationTimestamp.Time.Before(removeTaskIfCreatedBefore):
						// clean up the task using the cancel task function to ensure correct cancellation
						lagoonTask := &v1beta2.LagoonTask{}
						if err := p.APIReader.Get(ctx, types.NamespacedName{
							Name:      pod.Name,
							Namespace: pod.Namespace,
						}, lagoonTask); err != nil {
							opLog.Error(err,
								fmt.Sprintf(
									"Unable to cancel %s.",
									pod.Name,
								),
							)
							return
						}
						_, _, err := v1beta2.CancelTask(ctx, p.Client, pod.Namespace, &lagoonTask.Spec)
						if err != nil {
							opLog.Error(err,
								fmt.Sprintf(
									"Unable to cancel %s.",
									pod.Name,
								),
							)
						}
						return
					case podType == "build" && pruneBuilds && pod.CreationTimestamp.Time.Before(removeBuildIfCreatedBefore):
						// clean up the task using the cancel build function to ensure correct cancellation
						envName := ns.Labels["lagoon.sh/environment"]
						projectName := ns.Labels["lagoon.sh/project"]
						jobSpec := &v1beta2.LagoonTaskSpec{
							Misc: &v1beta2.LagoonMiscInfo{
								Name: pod.Name,
							},
							Environment: v1beta2.LagoonTaskEnvironment{
								Name: envName,
							},
							Project: v1beta2.LagoonTaskProject{
								Name: projectName,
							},
						}
						_, _, err := v1beta2.CancelBuild(ctx, p.Client, pod.Namespace, jobSpec)
						if err != nil {
							opLog.Error(err,
								fmt.Sprintf(
									"Unable to cancel %s.",
									pod.Name,
								),
							)
						}
						return
					default:
						return
					}
				} else {
					continue
				}
			}
		}
	}
}

// calculateRemoveBeforeTimes will return the date/times before which a build and task should be pruned.
func calculateRemoveBeforeTimes(p *Pruner, ns corev1.Namespace, startTime time.Time) (time.Time, time.Time, error) {
	// Here we set the timeout for build and task pods
	// these are able to be overridden by a namespace level
	// specification,
	timeoutForBuildPods := p.TimeoutForBuildPods
	if nsTimeoutForBuildPods, ok := ns.GetLabels()["lagoon.sh/buildPodTimeout"]; ok {
		insTimeoutForBuildpods, err := strconv.Atoi(nsTimeoutForBuildPods)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		timeoutForBuildPods = insTimeoutForBuildpods
	}

	timeoutForTaskPods := p.TimeoutForTaskPods
	if nsTimeoutForTaskPods, ok := ns.GetLabels()["lagoon.sh/taskPodTimeout"]; ok {
		insTimeoutForTaskPods, err := strconv.Atoi(nsTimeoutForTaskPods)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		timeoutForTaskPods = insTimeoutForTaskPods
	}

	hours, err := time.ParseDuration(fmt.Sprintf("%vh", timeoutForBuildPods))
	if err != nil {
		errorText := fmt.Sprintf(
			"Unable to parse TimeoutForBuildPods '%v' - cannot run long running task removal process.",
			p.TimeoutForBuildPods,
		)
		return time.Time{}, time.Time{}, errors.New(errorText)
	}

	removeBuildIfCreatedBefore := startTime.Add(-hours)

	hours, err = time.ParseDuration(fmt.Sprintf("%vh", timeoutForTaskPods))
	if err != nil {
		errorText := fmt.Sprintf(
			"Unable to parse TimeoutForTaskPods '%v' - cannot run long running task removal process.",
			p.TimeoutForTaskPods,
		)
		return time.Time{}, time.Time{}, errors.New(errorText)
	}
	removeTaskIfCreatedBefore := startTime.Add(-hours)
	return removeBuildIfCreatedBefore, removeTaskIfCreatedBefore, nil
}
