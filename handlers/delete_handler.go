package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"gopkg.in/matryer/try.v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DeleteDeployments will delete any deployments from the namespace.
func (h *Messaging) DeleteDeployments(ctx context.Context, opLog logr.Logger, ns, project, branch string) bool {
	deployments := &appsv1.DeploymentList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := h.Client.List(ctx, deployments, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list deployments in namespace %s for project %s, branch %s",
				ns,
				project,
				branch,
			),
		)
		//message.Ack(false) // ack to remove from queue
		return false
	}
	for _, dep := range deployments.Items {
		if err := h.Client.Delete(ctx, &dep); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete deployment %s in %s for project %s, branch %s",
					dep.ObjectMeta.Name,
					ns,
					project,
					branch,
				),
			)
			//message.Ack(false) // ack to remove from queue
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted deployment %s in  %s for project %s, branch %s",
				dep.ObjectMeta.Name,
				ns,
				project,
				branch,
			),
		)
	}
	return true
}

// DeleteStatefulSets will delete any statefulsets from the namespace.
func (h *Messaging) DeleteStatefulSets(ctx context.Context, opLog logr.Logger, ns, project, branch string) bool {
	statefulsets := &appsv1.StatefulSetList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := h.Client.List(ctx, statefulsets, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list statefulsets in namespace %s for project %s, branch %s",
				ns,
				project,
				branch,
			),
		)
		//message.Ack(false) // ack to remove from queue
		return false
	}
	for _, ss := range statefulsets.Items {
		if err := h.Client.Delete(ctx, &ss); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete statefulset %s in %s for project %s, branch %s",
					ss.ObjectMeta.Name,
					ns,
					project,
					branch,
				),
			)
			//message.Ack(false) // ack to remove from queue
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted statefulset %s in  %s for project %s, branch %s",
				ss.ObjectMeta.Name,
				ns,
				project,
				branch,
			),
		)
	}
	return true
}

// DeleteDaemonSets will delete any daemonsets from the namespace.
func (h *Messaging) DeleteDaemonSets(ctx context.Context, opLog logr.Logger, ns, project, branch string) bool {
	daemonsets := &appsv1.DaemonSetList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := h.Client.List(ctx, daemonsets, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list daemonsets in namespace %s for project %s, branch %s",
				ns,
				project,
				branch,
			),
		)
		//message.Ack(false) // ack to remove from queue
		return false
	}
	for _, ds := range daemonsets.Items {
		if err := h.Client.Delete(ctx, &ds); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete daemonset %s in %s for project %s, branch %s",
					ds.ObjectMeta.Name,
					ns,
					project,
					branch,
				),
			)
			//message.Ack(false) // ack to remove from queue
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted daemonset %s in  %s for project %s, branch %s",
				ds.ObjectMeta.Name,
				ns,
				project,
				branch,
			),
		)
	}
	return true
}

// DeletePVCs will delete any PVCs from the namespace.
func (h *Messaging) DeletePVCs(ctx context.Context, opLog logr.Logger, ns, project, branch string) bool {
	pvcs := &corev1.PersistentVolumeList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := h.Client.List(ctx, pvcs, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list pvcs in namespace %s for project %s, branch %s",
				ns,
				project,
				branch,
			),
		)
		//message.Ack(false) // ack to remove from queue
		return false
	}
	for _, pvc := range pvcs.Items {
		if err := h.Client.Delete(ctx, &pvc); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete pvc %s in %s for project %s, branch %s",
					pvc.ObjectMeta.Name,
					ns,
					project,
					branch,
				),
			)
			//message.Ack(false) // ack to remove from queue
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted pvc %s in  %s for project %s, branch %s",
				pvc.ObjectMeta.Name,
				ns,
				project,
				branch,
			),
		)
	}
	for _, pvc := range pvcs.Items {
		if err := h.CheckPVCExists(ctx, opLog, &pvc); err != nil {
			return false
		}
	}
	return true
}

// DeleteNamespace will delete the namespace.
func (h *Messaging) DeleteNamespace(ctx context.Context, opLog logr.Logger, namespace *corev1.Namespace, project, branch string) bool {
	if err := h.Client.Delete(ctx, namespace); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to delete namespace %s for project %s, branch %s",
				namespace.ObjectMeta.Name,
				project,
				branch,
			),
		)
		//message.Ack(false) // ack to remove from queue
		return false
	}
	return true
}

// CheckPVCExists .
func (h *Messaging) CheckPVCExists(ctx context.Context, opLog logr.Logger, pvc *corev1.PersistentVolume) error {
	try.MaxRetries = 60
	err := try.Do(func(attempt int) (bool, error) {
		var ingressErr error
		err := h.Client.Get(ctx, types.NamespacedName{
			Namespace: pvc.ObjectMeta.Namespace,
			Name:      pvc.ObjectMeta.Name,
		}, pvc)
		if err != nil {
			// the ingress doesn't exist anymore, so exit the retry
			ingressErr = nil
			opLog.Info(fmt.Sprintf("persistent volume claim %s in %s deleted", pvc.ObjectMeta.Name, pvc.ObjectMeta.Namespace))
		} else {
			// if the ingress still exists wait 5 seconds before trying again
			msg := fmt.Sprintf("persistent volume claim %s in %s still exists", pvc.ObjectMeta.Name, pvc.ObjectMeta.Namespace)
			ingressErr = fmt.Errorf("%s: %v", msg, err)
			opLog.Info(msg)
		}
		time.Sleep(1 * time.Second)
		return attempt < 60, ingressErr
	})
	return err
}
