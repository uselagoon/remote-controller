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
func (p *Pruner) LagoonOldProcPruner() {
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

		//Now let's look for build and task pods
		// NOTE: build and task pods really are the same kind of thing, right - they're simply pods, but tagged differently
		// So what we're going to do is just find those that match the various

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

		opLog.Info(fmt.Sprintf("Running task/build recon"))

		hours, err := time.ParseDuration(fmt.Sprintf("%vh", p.TimeoutForWorkerPods))
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to parse TimeoutForWorkerPods '%v' - cannot run long running task removal process.",
					p.TimeoutForWorkerPods,
				),
			)
			return
		}
		removeIfCreatedBefore := time.Now().Add(-time.Hour * hours)

		for _, pod := range podList.Items {
			if pod.Status.Phase == corev1.PodRunning {
				opLog.Info(fmt.Sprintf("Found a pod named - %v", pod.Name))

				if pod.CreationTimestamp.Time.Before(removeIfCreatedBefore) || true {
					if podType, ok := pod.GetLabels()["lagoon.sh/jobType"]; ok {
						switch podType {
						case "task":
							//podTypeName = "task"
							pod.ObjectMeta.Labels["lagoon.sh/cancelTask"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled task due to timeout")
							break
						case "build":
							//podTypeName = "build"
							pod.ObjectMeta.Labels["lagoon.sh/cancelBuild"] = "true"
							pod.ObjectMeta.Annotations["lagoon.sh/cancelReason"] = fmt.Sprintf("Cancelled build due to timeout")
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
						opLog.Info("Cancelled pod %v - timeout", pod.Name)
					} else {
						continue
					}

				}
			}
		}
		opLog.Info(fmt.Sprintf("Endint task/build recon"))
	}
}
