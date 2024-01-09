package pruner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LagoonOldProcPruner will identify and remove any long running builds or tasks.
func (p *Pruner) LagoonOldProcPruner(pruneBuilds, pruneTasks bool) {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonOldProcPruner")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := p.Client.List(context.Background(), namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}

	//now we iterate through each namespace, and look for build/task pods
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
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
			client.MatchingLabelsSelector{
				Selector: labels.NewSelector().Add(*jobTypeLabelRequirements),
			},
		})
		if err := p.Client.List(context.Background(), &podList, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list pod resources, there may be none or something went wrong"))
			continue
		}

		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodRunning {
				if podType, ok := pod.GetLabels()["lagoon.sh/jobType"]; ok {

					updatePod := false
					switch podType {
					case "task":
						if pod.CreationTimestamp.Time.Before(removeTaskIfCreatedBefore) && pruneTasks {
							updatePod = true
							pod.ObjectMeta.Labels["lagoon.sh/cancelTask"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled task due to timeout")
						}
					case "build":
						if pod.CreationTimestamp.Time.Before(removeBuildIfCreatedBefore) && pruneBuilds {
							updatePod = true
							pod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled build due to timeout")
						}
					default:
						return
					}
					if !updatePod {
						return
					}

					if err := p.Client.Update(context.Background(), &pod); err != nil {
						opLog.Error(err,
							fmt.Sprintf(
								"Unable to update %s to cancel it.",
								pod.Name,
							),
						)
					}
					opLog.Info(fmt.Sprintf("Cancelled pod %v - timeout", pod.Name))
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
			p.TimeoutForBuildPods,
		)
		return time.Time{}, time.Time{}, errors.New(errorText)
	}
	removeTaskIfCreatedBefore := startTime.Add(-hours)
	return removeBuildIfCreatedBefore, removeTaskIfCreatedBefore, nil
}
