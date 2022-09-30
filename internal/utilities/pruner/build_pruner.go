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

// LagoonBuildPruner will prune any build crds that are hanging around.
func (p *Pruner) LagoonBuildPruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonBuildPruner")
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
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting build pruner", ns.ObjectMeta.Name))
			continue
		}
		opLog.Info(fmt.Sprintf("Checking LagoonBuilds in namespace %s", ns.ObjectMeta.Name))
		lagoonBuilds := &lagoonv1beta1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
		})
		if err := p.Client.List(context.Background(), lagoonBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list LagoonBuild resources, there may be none or something went wrong"))
			continue
		}
		// sort the build pods by creation timestamp
		sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
			return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(lagoonBuilds.Items) > p.BuildsToKeep {
			for idx, lagoonBuild := range lagoonBuilds.Items {
				if idx >= p.BuildsToKeep {
					if helpers.ContainsString(
						helpers.BuildCompletedCancelledFailedStatus,
						lagoonBuild.ObjectMeta.Labels["lagoon.sh/buildStatus"],
					) {
						opLog.Info(fmt.Sprintf("Cleaning up LagoonBuild %s", lagoonBuild.ObjectMeta.Name))
						if err := p.Client.Delete(context.Background(), &lagoonBuild); err != nil {
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
func (p *Pruner) BuildPodPruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("BuildPodPruner")
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
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting build pod pruner", ns.ObjectMeta.Name))
			return
		}
		opLog.Info(fmt.Sprintf("Checking Lagoon build pods in namespace %s", ns.ObjectMeta.Name))
		buildPods := &corev1.PodList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/jobType":    "build",
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
		})
		if err := p.Client.List(context.Background(), buildPods, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
			return
		}
		// sort the build pods by creation timestamp
		sort.Slice(buildPods.Items, func(i, j int) bool {
			return buildPods.Items[i].ObjectMeta.CreationTimestamp.After(buildPods.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		if len(buildPods.Items) > p.BuildPodsToKeep {
			for idx, pod := range buildPods.Items {
				if idx >= p.BuildPodsToKeep {
					if pod.Status.Phase == corev1.PodFailed ||
						pod.Status.Phase == corev1.PodSucceeded {
						opLog.Info(fmt.Sprintf("Cleaning up pod %s", pod.ObjectMeta.Name))
						if err := p.Client.Delete(context.Background(), &pod); err != nil {
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
