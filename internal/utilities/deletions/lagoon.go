package deletions

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/uselagoon/remote-controller/internal/helpers"
)

// DeleteLagoonBuilds will delete any lagoon builds from the namespace.
func (d *Deletions) DeleteLagoonBuilds(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	lagoonBuilds := &lagoonv1beta1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, lagoonBuilds, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list lagoon build in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, lagoonBuild := range lagoonBuilds.Items {
		if err := d.Client.Delete(ctx, &lagoonBuild); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete lagoon build %s in %s for project %s, environment %s",
					lagoonBuild.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted lagoon build %s in  %s for project %s, environment %s",
				lagoonBuild.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}

// DeleteLagoonTasks will delete any lagoon tasks from the namespace.
func (d *Deletions) DeleteLagoonTasks(ctx context.Context, opLog logr.Logger, ns, project, environment string) bool {
	lagoonTasks := &lagoonv1beta1.LagoonTaskList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
	})
	if err := d.Client.List(ctx, lagoonTasks, listOption); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to list lagoon task in namespace %s for project %s, environment %s",
				ns,
				project,
				environment,
			),
		)
		return false
	}
	for _, lagoonTask := range lagoonTasks.Items {
		if err := d.Client.Delete(ctx, &lagoonTask); helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete lagoon task %s in %s for project %s, environment %s",
					lagoonTask.ObjectMeta.Name,
					ns,
					project,
					environment,
				),
			)
			return false
		}
		opLog.Info(
			fmt.Sprintf(
				"Deleted lagoon task %s in  %s for project %s, environment %s",
				lagoonTask.ObjectMeta.Name,
				ns,
				project,
				environment,
			),
		)
	}
	return true
}
