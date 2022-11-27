package deletions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	"github.com/uselagoon/remote-controller/internal/harbor"
)

func (d *Deletions) ProcessDeletion(ctx context.Context, opLog logr.Logger, namespace *corev1.Namespace) error {
	cleanupHarbor := false
	if val, ok := namespace.Labels[HarborCleanup]; ok {
		cleanupHarbor, _ = strconv.ParseBool(val)
	}
	// get the namespace project and environment labels
	project := ""
	if val, ok := namespace.Labels["lagoon.sh/project"]; ok {
		project = val
	}
	environment := ""
	if val, ok := namespace.Labels["lagoon.sh/environment"]; ok {
		environment = val
	}
	if project == "" && environment == "" {
		return fmt.Errorf("Namespace %s is not a lagoon environment", namespace.Name)
	}

	/*
		get any deployments/statefulsets/daemonsets
		then delete them
	*/
	if d.CleanupHarborRepositoryOnDelete && cleanupHarbor {
		lagoonHarbor, err := harbor.New(d.Harbor)
		if err != nil {
			return err
		}
		curVer, err := lagoonHarbor.GetHarborVersion(ctx)
		if err != nil {
			return err
		}
		if lagoonHarbor.UseV2Functions(curVer) {
			lagoonHarbor.DeleteRepository(ctx, project, environment)
		}
	}
	if del := d.DeleteLagoonTasks(ctx, opLog.WithName("DeleteLagoonTasks"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting tasks")
	}
	if del := d.DeleteLagoonBuilds(ctx, opLog.WithName("DeleteLagoonBuilds"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting builds")
	}
	if del := d.DeleteDeployments(ctx, opLog.WithName("DeleteDeployments"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting deployments")
	}
	if del := d.DeleteStatefulSets(ctx, opLog.WithName("DeleteStatefulSets"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting statefulsets")
	}
	if del := d.DeleteDaemonSets(ctx, opLog.WithName("DeleteDaemonSets"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting daemonsets")
	}
	if del := d.DeleteIngress(ctx, opLog.WithName("DeleteIngress"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting ingress")
	}
	if del := d.DeleteJobs(ctx, opLog.WithName("DeleteJobs"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting jobs")
	}
	if del := d.DeletePods(ctx, opLog.WithName("DeletePods"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting pods")
	}
	if del := d.DeletePVCs(ctx, opLog.WithName("DeletePVCs"), namespace.ObjectMeta.Name, project, environment); del == false {
		return fmt.Errorf("error deleting pvcs")
	}
	/*
		then delete the namespace
	*/
	if del := d.DeleteNamespace(ctx, opLog.WithName("DeleteNamespace"), namespace, project, environment); del == false {
		return fmt.Errorf("error deleting namespace")
	}
	opLog.WithName("DeleteNamespace").Info(
		fmt.Sprintf(
			"Deleted namespace %s for project %s, environment %s",
			namespace.ObjectMeta.Name,
			project,
			environment,
		),
	)
	return nil
}
