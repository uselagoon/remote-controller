package harbor

import (
	"fmt"
	"sort"

	"context"
	"time"

	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RotateRobotCredentials will attempt to recreate any robot account credentials that need to be rotated.
func (h *Harbor) RotateRobotCredentials(ctx context.Context, cl client.Client) {
	opLog := ctrl.Log.WithName("handlers").WithName("RotateRobotCredentials")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	// @TODO: do this later so we can only run robot credentials for specific controllers
	// labelRequirements2, _ := labels.NewRequirement("lagoon.sh/controller", selection.Equals, []string{h.ControllerNamespace})
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
			// @TODO: do this later so we can only run robot credentials for specific controllers
			// Selector: labels.NewSelector().Add(*labelRequirements).Add(*labelRequirements2),
		},
	})
	if err := cl.List(ctx, namespaces, listOption); err != nil {
		opLog.Error(err, fmt.Sprintf("Unable to list namespaces created by Lagoon, there may be none or something went wrong"))
		return
	}
	// go over every namespace that has a lagoon.sh label
	// and attempt to create and update the robot account credentials as requred.
	for _, ns := range namespaces.Items {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting robot credentials check", ns.ObjectMeta.Name))
			continue
		}
		opLog.Info(fmt.Sprintf("Checking if %s needs robot credentials rotated", ns.ObjectMeta.Name))
		// check for running builds!
		lagoonBuilds := &lagoonv1beta1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := cl.List(context.Background(), lagoonBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
			continue
		}
		runningBuilds := false
		sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
			return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		// if there are any builds pending or running, don't try and refresh the credentials as this
		// could break the build
		if len(lagoonBuilds.Items) > 0 {
			if helpers.ContainsString(
				helpers.BuildRunningPendingStatus,
				lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"],
			) {
				runningBuilds = true
			}
		}
		if !runningBuilds {
			rotated, err := h.RotateRobotCredential(ctx, cl, ns, false)
			if err != nil {
				opLog.Error(err, "error")
				continue
			}
			if rotated {
				opLog.Info(fmt.Sprintf("Robot credentials rotated for %s", ns.ObjectMeta.Name))
			}
		} else {
			opLog.Info(fmt.Sprintf("There are running or pending builds in %s, skipping", ns.ObjectMeta.Name))
		}
	}
}

// rotate a specific namespaces robot credential
func (h *Harbor) RotateRobotCredential(ctx context.Context, cl client.Client, ns corev1.Namespace, force bool) (bool, error) {
	// only continue if there isn't any running builds
	robotCreds := &helpers.RegistryCredentials{}
	curVer, err := h.GetHarborVersion(ctx)
	if err != nil {
		return false, fmt.Errorf("error checking harbor version: %v", err)
	}
	if h.UseV2Functions(curVer) {
		hProject, err := h.CreateProjectV2(ctx, ns.Labels["lagoon.sh/project"])
		if err != nil {
			return false, fmt.Errorf("error getting or creating project: %v", err)
		}
		time.Sleep(1 * time.Second) // wait 1 seconds
		robotCreds, err = h.CreateOrRefreshRobotV2(ctx,
			cl,
			hProject,
			ns.Labels["lagoon.sh/environment"],
			ns.ObjectMeta.Name,
			h.RobotAccountExpiry,
			force)
		if err != nil {
			return false, fmt.Errorf("error getting or creating robot account: %v", err)
		}
	} else {
		hProject, err := h.CreateProject(ctx, ns.Labels["lagoon.sh/project"])
		if err != nil {
			return false, fmt.Errorf("error getting or creating project: %v", err)
		}
		time.Sleep(1 * time.Second) // wait 1 seconds
		robotCreds, err = h.CreateOrRefreshRobot(ctx,
			cl,
			hProject,
			ns.Labels["lagoon.sh/environment"],
			ns.ObjectMeta.Name,
			time.Now().Add(h.RobotAccountExpiry).Unix(),
			force)
		if err != nil {
			return false, fmt.Errorf("error getting or creating robot account: %v", err)
		}
	}
	time.Sleep(1 * time.Second) // wait 1 seconds

	// if we have robotcredentials to create, do that here
	return h.UpsertHarborSecret(ctx,
		cl,
		ns.ObjectMeta.Name,
		"lagoon-internal-registry-secret", //secret name in kubernetes
		robotCreds)
}
