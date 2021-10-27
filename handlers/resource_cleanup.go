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

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
)

type cleanup interface {
	LagoonBuildCleanup()
	BuildPodCleanup()
	TaskPodCleanup()
}

// Cleanup is used for cleaning up old pods or resources.
type Cleanup struct {
	Client              client.Client
	BuildsToKeep        int
	TasksToKeep         int
	BuildPodsToKeep     int
	TaskPodsToKeep      int
	ControllerNamespace string
	EnableDebug         bool
}

// NewCleanup returns a cleanup with controller-runtime client.
func NewCleanup(client client.Client, buildsToKeep int, buildPodsToKeep int, tasksToKeep int, taskPodsToKeep int, controllerNamespace string, enableDebug bool) *Cleanup {
	return &Cleanup{
		Client:              client,
		BuildsToKeep:        buildsToKeep,
		TasksToKeep:         tasksToKeep,
		BuildPodsToKeep:     buildPodsToKeep,
		TaskPodsToKeep:      taskPodsToKeep,
		ControllerNamespace: controllerNamespace,
		EnableDebug:         enableDebug,
	}
}

// LagoonBuildCleanup will clean up any build crds that are hanging around.
func (h *Cleanup) LagoonBuildCleanup() {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonBuildCleanup")
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
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod cleanup", ns.ObjectMeta.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking LagoonBuilds in namespace %s", ns.ObjectMeta.Name))
		lagoonBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "build",
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := h.Client.List(context.Background(), lagoonBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuild resources, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
			return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(lagoonBuilds.Items) > h.BuildsToKeep {
			for idx, lagoonBuild := range lagoonBuilds.Items {
				if idx >= h.BuildsToKeep {
					if containsString(
						BuildCompletedCancelledFailedStatus,
						lagoonBuild.ObjectMeta.Annotations["lagoon.sh/buildStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonBuild %s", lagoonBuild.ObjectMeta.Name))
						if err := h.Client.Delete(context.Background(), &lagoonBuild); err != nil {
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

// LagoonTaskCleanup will clean up any build crds that are hanging around.
func (h *Cleanup) LagoonTaskCleanup() {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTaskCleanup")
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
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod cleanup", ns.ObjectMeta.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking LagoonTasks in namespace %s", ns.ObjectMeta.Name))
		lagoonTasks := &lagoonv1alpha1.LagoonTaskList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "task",
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := h.Client.List(context.Background(), lagoonTasks, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list LagoonTask resources, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonTasks.Items, func(i, j int) bool {
			return lagoonTasks.Items[i].ObjectMeta.CreationTimestamp.After(lagoonTasks.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(lagoonTasks.Items) > h.TasksToKeep {
			for idx, lagoonTask := range lagoonTasks.Items {
				if idx >= h.TasksToKeep {
					if containsString(
						TaskCompletedCancelledFailedStatus,
						lagoonTask.ObjectMeta.Annotations["lagoon.sh/taskStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonTask %s", lagoonTask.ObjectMeta.Name))
						if err := h.Client.Delete(context.Background(), &lagoonTask); err != nil {
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
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod cleanup", ns.ObjectMeta.Name))
			return
		}
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
			return buildPods.Items[i].ObjectMeta.CreationTimestamp.After(buildPods.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(buildPods.Items) > h.BuildPodsToKeep {
			for idx, pod := range buildPods.Items {
				if idx >= h.BuildPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.ObjectMeta.Name))
						if err := h.Client.Delete(context.Background(), &pod); err != nil {
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
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting task pod cleanup", ns.ObjectMeta.Name))
			return
		}
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
			return taskPods.Items[i].ObjectMeta.CreationTimestamp.After(taskPods.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(taskPods.Items) > h.TaskPodsToKeep {
			for idx, pod := range taskPods.Items {
				if idx >= h.TaskPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.ObjectMeta.Name))
						if err := h.Client.Delete(context.Background(), &pod); err != nil {
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
