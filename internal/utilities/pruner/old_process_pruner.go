package pruner

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
	//lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	//"github.com/uselagoon/remote-controller/internal/helpers"
)

// LagoonOldProcPruner will identify and remove any long running builds or tasks.
func (p *Pruner) LagoonOldProcPruner(pruneBuilds, pruneTasks bool) {
	opLog := ctrl.Log.WithName("utilities").WithName("LagoonOldProcPruner")
	opLog.Info("Beginning marking old build and task pods")
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
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, skipping build/task pruner", ns.ObjectMeta.Name))
			continue
		}

		podList := corev1.PodList{
			TypeMeta: metav1.TypeMeta{},
			ListMeta: metav1.ListMeta{},
			Items:    nil,
		}

		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": p.ControllerNamespace, // created by this controller
			}),
		})
		if err := p.Client.List(context.Background(), &podList, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list pod resources, there may be none or something went wrong"))
			continue
		}

		hours, err := time.ParseDuration(fmt.Sprintf("%vh", p.TimeoutForBuildPods))
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to parse TimeoutForBuildPods '%v' - cannot run long running task removal process.",
					p.TimeoutForBuildPods,
				),
			)
			return
		}

		removeBuildIfCreatedBefore := time.Now().Add(-time.Hour * hours)

		hours, err = time.ParseDuration(fmt.Sprintf("%vh", p.TimeoutForTaskPods))
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to parse TimeoutForTaskPods '%v' - cannot run long running task removal process.",
					p.TimeoutForBuildPods,
				),
			)
			return
		}
		removeTaskIfCreatedBefore := time.Now().Add(-time.Hour * hours)

		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodRunning {
				if podType, ok := pod.GetLabels()["lagoon.sh/jobType"]; ok {
					switch podType {
					case "task":
						if pod.CreationTimestamp.Time.Before(removeTaskIfCreatedBefore) && pruneTasks {
							pod.ObjectMeta.Labels["lagoon.sh/cancelTask"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled task due to timeout")
						}
						break
					case "build":
						if pod.CreationTimestamp.Time.Before(removeBuildIfCreatedBefore) && pruneBuilds {
							pod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled build due to timeout")
						}
						break
					default:
						return
					}
					// this isn't actually a job, so we skip
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
