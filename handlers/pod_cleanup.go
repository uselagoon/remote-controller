package handlers

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type cleanup interface {
	BuildPodCleanup()
	TaskPodCleanup()
}

// Cleanup is used for cleaning up old pods or resources.
type Cleanup struct {
	Client              client.Client
	NumTaskPodsToKeep   int
	NumBuildPodsToKeep  int
	ControllerNamespace string
	EnableDebug         bool
}

// NewCleanup returns a cleanup with controller-runtime client.
func NewCleanup(client client.Client, numTaskPodsToKeep int, numBuildPodsToKeep int, controllerNamespace string, enableDebug bool) *Cleanup {
	return &Cleanup{
		Client:              client,
		NumTaskPodsToKeep:   numTaskPodsToKeep,
		NumBuildPodsToKeep:  numBuildPodsToKeep,
		ControllerNamespace: controllerNamespace,
		EnableDebug:         enableDebug,
	}
}

// BuildPodCleanup will clean up any build pods that are hanging around.
func (h *Cleanup) BuildPodCleanup() {
	opLog := ctrl.Log.WithName("handlers").WithName("BuildPodCleanup")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := h.Client.List(context.Background(), namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}
	for _, ns := range namespaces.Items {
		opLog.Info(fmt.Sprintf("Checking Lagoon build pods in namespace %s", ns.ObjectMeta.Name))
		buildPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "build",
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := h.Client.List(context.Background(), buildPods, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(buildPods.Items, func(i, j int) bool {
			return buildPods.Items[i].ObjectMeta.CreationTimestamp.Before(&buildPods.Items[j].ObjectMeta.CreationTimestamp)
		})
		if len(buildPods.Items) > h.NumBuildPodsToKeep {
			for idx, pod := range buildPods.Items {
				if idx >= h.NumBuildPodsToKeep &&
					pod.Status.Phase == corev1.PodFailed ||
					pod.Status.Phase == corev1.PodSucceeded {
					if err := h.Client.Delete(context.Background(), &pod); err != nil {
						opLog.Error(err, fmt.Sprintf("Unable to update status condition"))
						break
					}
				}
			}
		}
	}
	return
}

// TaskPodCleanup will clean up any task pods that are hanging around.
func (h *Cleanup) TaskPodCleanup() {
	opLog := ctrl.Log.WithName("handlers").WithName("TaskPodCleanup")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
		},
	})
	if err := h.Client.List(context.Background(), namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}
	for _, ns := range namespaces.Items {
		opLog.Info(fmt.Sprintf("Checking Lagoon task pods in namespace %s", ns.ObjectMeta.Name))
		taskPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "task",
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := h.Client.List(context.Background(), taskPods, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon task pods, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(taskPods.Items, func(i, j int) bool {
			return taskPods.Items[i].ObjectMeta.CreationTimestamp.Before(&taskPods.Items[j].ObjectMeta.CreationTimestamp)
		})
		if len(taskPods.Items) > h.NumTaskPodsToKeep {
			for idx, pod := range taskPods.Items {
				if idx >= h.NumTaskPodsToKeep &&
					pod.Status.Phase == corev1.PodFailed ||
					pod.Status.Phase == corev1.PodSucceeded {
					if err := h.Client.Delete(context.Background(), &pod); err != nil {
						opLog.Error(err, fmt.Sprintf("Unable to delete pod"))
						break
					}
				}
			}
		}
	}
	return
}
