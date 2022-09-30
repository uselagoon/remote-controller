package pruner

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"
)

// LagoonTaskPruner will prune any build crds that are hanging around.
func (p *Pruner) LagoonTaskPruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonTaskPruner")
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
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pruner", ns.ObjectMeta.Name))
			continue
		}
		opLog.Info(fmt.Sprintf("Checking LagoonTasks in namespace %s", ns.ObjectMeta.Name))
		lagoonTasks := &lagoonv1beta1.LagoonTaskList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
		})
		if err := p.Client.List(context.Background(), lagoonTasks, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list LagoonTask resources, there may be none or something went wrong"))
			continue
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonTasks.Items, func(i, j int) bool {
			return lagoonTasks.Items[i].ObjectMeta.CreationTimestamp.After(lagoonTasks.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(lagoonTasks.Items) > p.TasksToKeep {
			for idx, lagoonTask := range lagoonTasks.Items {
				if idx >= p.TasksToKeep {
					if helpers.ContainsString(
						helpers.TaskCompletedCancelledFailedStatus,
						lagoonTask.ObjectMeta.Labels["lagoon.sh/taskStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonTask %s", lagoonTask.ObjectMeta.Name))
						if err := p.Client.Delete(context.Background(), &lagoonTask); err != nil {
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

// TaskPodPruner will prune any task pods that are hanging around.
func (p *Pruner) TaskPodPruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("TaskPodPruner")
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
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod pruner", ns.ObjectMeta.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking Lagoon task pods in namespace %s", ns.ObjectMeta.Name))
		taskPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "task",
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
		})
		if err := p.Client.List(context.Background(), taskPods, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon task pods, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(taskPods.Items, func(i, j int) bool {
			return taskPods.Items[i].ObjectMeta.CreationTimestamp.After(taskPods.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(taskPods.Items) > p.TaskPodsToKeep {
			for idx, pod := range taskPods.Items {
				if idx >= p.TaskPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.ObjectMeta.Name))
						if err := p.Client.Delete(context.Background(), &pod); err != nil {
							opLog.Error(err, fmt.Sprintf("Unable to delete pod"))
							break
						}
					}
				}
			}
		}
	}
	return
}
