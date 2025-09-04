package deletions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
)

func (d *Deletions) ProcessDeletion(ctx context.Context, opLog logr.Logger, namespace *corev1.Namespace) error {
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
		return fmt.Errorf("namespace %s is not a lagoon environment", namespace.Name)
	}

	/*
		clean up associated harbor resources
	*/
	if d.CleanupHarborRepositoryOnDelete {
		// always clean up if the flag is enabled
		cleanupRepo := true
		cleanupRobot := true
		// but if the secret is labeled with cleanup labels, use these
		secret := &corev1.Secret{}
		err := d.Client.Get(ctx, types.NamespacedName{
			Namespace: namespace.Name,
			Name:      "lagoon-internal-registry-secret",
		}, secret)
		if err != nil {
			return err
		}
		// these can be used on the `lagoon-internal-registry-secret` to prevent the resources
		// being cleaned up in harbor by setting them to `false`
		// this is useful for migrating environments, where the source or destination can have these added
		// so that when the migration is complete the resulting images aren't also removed
		// @deprecate these two labels in favour for the `retain` ones
		if val, ok := secret.Labels["harbor.lagoon.sh/cleanup-repositories"]; ok {
			opLog.WithName("DeleteNamespace").Info("Secret label 'harbor.lagoon.sh/cleanup-repositories' is deprecated, use 'harbor.lagoon.sh/retain-repositories' instead")
			cleanupRepo, _ = strconv.ParseBool(val)
		}
		if val, ok := secret.Labels["harbor.lagoon.sh/cleanup-robotaccount"]; ok {
			opLog.WithName("DeleteNamespace").Info("Secret label 'harbor.lagoon.sh/cleanup-robotaccount' is deprecated, use 'harbor.lagoon.sh/retain-robotaccount' instead")
			cleanupRobot, _ = strconv.ParseBool(val)
		}
		// use a retain label for "retain=true", this is clearer than "cleanup"
		if val, ok := secret.Labels["harbor.lagoon.sh/retain-repositories"]; ok {
			retain, _ := strconv.ParseBool(val)
			cleanupRepo = !retain
		}
		if val, ok := secret.Labels["harbor.lagoon.sh/retain-robotaccount"]; ok {
			retain, _ := strconv.ParseBool(val)
			cleanupRobot = !retain
		}
		// also check the namespace for the retain labels
		if val, ok := namespace.Labels["harbor.lagoon.sh/retain-repositories"]; ok {
			retain, _ := strconv.ParseBool(val)
			cleanupRepo = !retain
		}
		if val, ok := namespace.Labels["harbor.lagoon.sh/retain-robotaccount"]; ok {
			retain, _ := strconv.ParseBool(val)
			cleanupRobot = !retain
		}
		if cleanupRepo || cleanupRobot {
			// either of the repo or the robot need cleaning up when the namespace is terminated, then perform the required actions here
			curVer, err := d.Harbor.GetHarborVersion(ctx)
			if err != nil {
				return err
			}
			if d.Harbor.UseV2Functions(curVer) {
				if cleanupRepo {
					d.Harbor.DeleteRepository(ctx, project, environment)
				}
				if cleanupRobot {
					projectHarbor, err := d.Harbor.ClientV5.GetProject(ctx, project)
					if err != nil {
						opLog.Info(fmt.Sprintf("Error getting project %s, err: %v", project, err))
						return err
					}
					d.Harbor.DeleteRobotAccount(ctx, int64(projectHarbor.ProjectID), project, environment)
				}
			}
		}
	}
	/*
		get any deployments/statefulsets/daemonsets
		then delete them
	*/
	if del := lagoonv1beta2.DeleteLagoonTasks(ctx, opLog.WithName("DeleteLagoonTasks"), d.Client, namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting tasks")
	}
	if del := lagoonv1beta2.DeleteLagoonBuilds(ctx, opLog.WithName("DeleteLagoonBuilds"), d.Client, namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting builds")
	}
	if del := d.DeleteDeployments(ctx, opLog.WithName("DeleteDeployments"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting deployments")
	}
	if del := d.DeleteStatefulSets(ctx, opLog.WithName("DeleteStatefulSets"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting statefulsets")
	}
	if del := d.DeleteDaemonSets(ctx, opLog.WithName("DeleteDaemonSets"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting daemonsets")
	}
	if del := d.DeleteIngress(ctx, opLog.WithName("DeleteIngress"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting ingress")
	}
	if del := d.DeleteJobs(ctx, opLog.WithName("DeleteJobs"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting jobs")
	}
	if del := d.DeletePods(ctx, opLog.WithName("DeletePods"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting pods")
	}
	if del := d.DeletePVCs(ctx, opLog.WithName("DeletePVCs"), namespace.Name, project, environment); !del {
		return fmt.Errorf("error deleting pvcs")
	}
	/*
		then delete the namespace
	*/
	if del := d.DeleteNamespace(ctx, opLog.WithName("DeleteNamespace"), namespace, project, environment); !del {
		return fmt.Errorf("error deleting namespace")
	}
	opLog.WithName("DeleteNamespace").Info(
		fmt.Sprintf(
			"Deleted namespace %s for project %s, environment %s",
			namespace.Name,
			project,
			environment,
		),
	)
	return nil
}
