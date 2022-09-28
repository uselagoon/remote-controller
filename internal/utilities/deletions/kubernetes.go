package deletions

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"gopkg.in/matryer/try.v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/uselagoon/remote-controller/internal/helpers"
)

// DeleteDeployments will delete any deployments from the namespace.
func (d *Deletions) DeleteDeployments(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	deployments := &appsv1.DeploymentList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, deployments, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list deployments in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, dep := range deployments.Items {
		if err := d.Client.Delete(ctx, &dep); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete deployment %s in %s for project %s, environment %s",
					dep.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted deployment %s in  %s for project %s, environment %s",
				dep.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeleteStatefulSets will delete any statefulsets from the namespace.
func (d *Deletions) DeleteStatefulSets(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	statefulsets := &appsv1.StatefulSetList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, statefulsets, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list statefulsets in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, ss := range statefulsets.Items {
		if err := d.Client.Delete(ctx, &ss); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete statefulset %s in %s for project %s, environment %s",
					ss.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted statefulset %s in  %s for project %s, environment %s",
				ss.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeleteDaemonSets will delete any daemonsets from the namespace.
func (d *Deletions) DeleteDaemonSets(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	daemonsets := &appsv1.DaemonSetList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, daemonsets, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list daemonsets in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, ds := range daemonsets.Items {
		if err := d.Client.Delete(ctx, &ds); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete daemonset %s in %s for project %s, environment %s",
					ds.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted daemonset %s in  %s for project %s, environment %s",
				ds.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeleteJobs will delete any jobs from the namespace.
func (d *Deletions) DeleteJobs(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	jobs := &batchv1.JobList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, jobs, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list jobs in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, ds := range jobs.Items {
		if err := d.Client.Delete(ctx, &ds); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete jobs %s in %s for project %s, environment %s",
					ds.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted jobs %s in  %s for project %s, environment %s",
				ds.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeletePods will delete any pods from the namespace.
func (d *Deletions) DeletePods(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	pods := &corev1.PodList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, pods, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list jobs in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, ds := range pods.Items {
		if err := d.Client.Delete(ctx, &ds); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete pods %s in %s for project %s, environment %s",
					ds.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted pods %s in  %s for project %s, environment %s",
				ds.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeletePVCs will delete any PVCs from the namespace.
func (d *Deletions) DeletePVCs(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	pvcs := &corev1.PersistentVolumeClaimList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, pvcs, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list pvcs in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, pvc := range pvcs.Items {
		if err := d.Client.Delete(ctx, &pvc); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete pvc %s in %s for project %s, environment %s",
					pvc.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted pvc %s in  %s for project %s, environment %s",
				pvc.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	for _, pvc := range pvcs.Items {
		if err := d.CheckPVCExists(ctx, opLog, &pvc); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Waited for pvc %s to delete in %s for project %s, environment %s, but it was not deleted in time",
					pvc.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
	}
	return true
}

// DeleteNamespace will delete the namespace.
func (d *Deletions) DeleteNamespace(ctx context.Context, opLog logr.Logger, namespace *corev1.Namespace, project, environment string) bool {
	if err := d.Client.Delete(ctx, namespace); helpers.IgnoreNotFound(err) != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to delete namespace %s for project %s, environment %s",
				namespace.ObjectMeta.Name,
				project,
				environment,
			),
		)
		return false
	}
	return true
}

// CheckPVCExists checks if the PVC being deleted has been deleted.
func (d *Deletions) CheckPVCExists(ctx context.Context, opLog logr.Logger, pvc *corev1.PersistentVolumeClaim) error {
	try.MaxRetries = d.DeleteConfig.PVCRetryAttempts
	err := try.Do(func(attempt int) (bool, error) {
		var pvcErr error
		err := d.Client.Get(ctx, types.NamespacedName{
			Namespace: pvc.ObjectMeta.Namespace,
			Name:      pvc.ObjectMeta.Name,
		}, pvc)
		if err != nil {
			// the pvc doesn't exist anymore, so exit the retry
			pvcErr = nil
			opLog.Info(fmt.Sprintf("persistent volume claim %s in %s deleted", pvc.ObjectMeta.Name, pvc.ObjectMeta.Namespace))
		} else {
			// if the pvc still exists wait 10 seconds before trying again
			msg := fmt.Sprintf("persistent volume claim %s in %s still exists", pvc.ObjectMeta.Name, pvc.ObjectMeta.Namespace)
			pvcErr = fmt.Errorf("%s: %v", msg, err)
			opLog.Info(msg)
		}
		time.Sleep(time.Duration(d.DeleteConfig.PVCRetryInterval) * time.Second)
		return attempt < d.DeleteConfig.PVCRetryAttempts, pvcErr
	})
	return err
}
