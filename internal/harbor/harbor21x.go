package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	harborclientv3model "github.com/mittwald/goharbor-client/v3/apiv2/model"
	harborclientv3legacy "github.com/mittwald/goharbor-client/v3/apiv2/model/legacy"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateProject will create a project if one doesn't exist, but will update as required.
func (h *Harbor) CreateProject(ctx context.Context, projectName string) (*harborclientv3model.Project, error) {
	project, err := h.ClientV3.GetProjectByName(ctx, projectName)
	if err != nil {
		if err.Error() == "project not found on server side" || err.Error() == "resource unknown" {
			project, err = h.ClientV3.NewProject(ctx, projectName, helpers.Int64Ptr(-1))
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error creating project %s", projectName))
				return nil, err
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			tStr := "true"
			err = h.ClientV3.UpdateProject(ctx, &harborclientv3model.Project{
				Name:      projectName,
				ProjectID: project.ProjectID,
				Metadata: &harborclientv3model.ProjectMetadata{
					AutoScan:             &tStr,
					ReuseSysCveAllowlist: &tStr,
					Public:               "false",
				},
			}, helpers.Int64Ptr(-1))
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error updating project %s", projectName))
				return nil, err
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			project, err = h.ClientV3.GetProjectByName(ctx, projectName)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error getting project after updating %s", projectName))
				return nil, err
			}
			h.Log.Info(fmt.Sprintf("Created harbor project %s", projectName))
		} else {
			h.Log.Info(fmt.Sprintf("Error finding project %s", projectName))
			return nil, err
		}
	}

	// TODO: Repository support not required yet
	// this is a place holder
	// w, err := h.ClientV3.ListRepositories(ctx, project)
	// if err != nil {
	// 	return nil, err
	// }
	// for _, x := range w {
	// 	fmt.Println(x)
	// }

	if h.WebhookAddition {
		wps, err := h.ClientV3.ListProjectWebhookPolicies(ctx, project)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error listing project %s webhooks", project.Name))
			return nil, err
		}
		exists := false
		for _, wp := range wps {
			// if the webhook policy already exists with the name we want
			// then update it with any changes that may be required
			if wp.Name == "Lagoon Default Webhook" {
				exists = true
				newPolicy := &harborclientv3legacy.WebhookPolicy{
					Name:      wp.Name,
					ProjectID: int64(project.ProjectID),
					Enabled:   true,
					Targets: []*harborclientv3legacy.WebhookTargetObject{
						{
							Type:           "http",
							SkipCertVerify: true,
							Address:        h.WebhookURL,
						},
					},
					EventTypes: h.WebhookEventTypes,
				}
				err = h.ClientV3.UpdateProjectWebhookPolicy(ctx, project, int(wp.ID), newPolicy)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error updating project %s webhook", project.Name))
					return nil, err
				}
			}
		}
		if !exists {
			// otherwise create the webhook if it doesn't exist
			newPolicy := &harborclientv3legacy.WebhookPolicy{
				Name:      "Lagoon Default Webhook",
				ProjectID: int64(project.ProjectID),
				Enabled:   true,
				Targets: []*harborclientv3legacy.WebhookTargetObject{
					{
						Type:           "http",
						SkipCertVerify: true,
						Address:        h.WebhookURL,
					},
				},
				EventTypes: h.WebhookEventTypes,
			}
			err = h.ClientV3.AddProjectWebhookPolicy(ctx, project, newPolicy)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error adding project %s webhook", project.Name))
				return nil, err
			}
		}
	}
	return project, nil
}

// CreateOrRefreshRobot will create or refresh a robot account and return the credentials if needed.
func (h *Harbor) CreateOrRefreshRobot(ctx context.Context,
	k8s client.Client,
	project *harborclientv3model.Project,
	environmentName, namespace string,
	expiry int64,
) (*helpers.RegistryCredentials, error) {

	// create a cluster specific robot account name
	robotName := fmt.Sprintf("%s-%s", environmentName, helpers.HashString(h.LagoonTargetName)[0:8])

	robots, err := h.ClientV3.ListProjectRobots(
		ctx,
		project,
	)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error listing project %s robot accounts", project.Name))
		return nil, err
	}
	exists := false
	deleted := false
	forceRecreate := false
	secret := &corev1.Secret{}
	err = k8s.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "lagoon-internal-registry-secret",
	}, secret)
	if err != nil {
		// the lagoon registry secret doesn't exist, force re-create the robot account
		forceRecreate = true
	}
	// check if the secret contains the .dockerconfigjson data
	if secretData, ok := secret.Data[".dockerconfigjson"]; ok {
		auths := helpers.Auths{}
		// unmarshal it
		if err := json.Unmarshal(secretData, &auths); err != nil {
			return nil, fmt.Errorf("Could not unmarshal Harbor RobotAccount credential")
		}
		// set the force recreate robot account flag here
		forceRecreate = true
		// if the defined regional harbor key exists using the hostname then set the flag to false
		// if the account is set to expire, the loop below will catch it for us
		// just the hostname, as this is what all new robot accounts are created with
		if _, ok := auths.Registries[h.Hostname]; ok {
			forceRecreate = false
		}
	}
	for _, robot := range robots {
		if h.matchRobotAccount(robot.Name, project.Name, environmentName) {
			exists = true
			if forceRecreate {
				// if the secret doesn't exist in kubernetes, then force re-creation of the robot
				// account is required, as there isn't a way to get the credentials after
				// robot accounts are created
				h.Log.Info(fmt.Sprintf("Kubernetes secret doesn't exist, robot account %s needs to be re-created", robot.Name))
				err := h.ClientV3.DeleteProjectRobot(
					ctx,
					project,
					int(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			if robot.Disabled && h.DeleteDisabled {
				// if accounts are disabled, and deletion of disabled accounts is enabled
				// then this will delete the account to get re-created
				h.Log.Info(fmt.Sprintf("Harbor robot account %s disabled, deleting it", robot.Name))
				err := h.ClientV3.DeleteProjectRobot(
					ctx,
					project,
					int(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			if h.shouldRotate(robot.CreationTime, h.RotateInterval) {
				// this forces a rotation after a certain period, whether its expiring or already expired.
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  should rotate, deleting it", robot.Name))
				err := h.ClientV3.DeleteProjectRobot(
					ctx,
					project,
					int(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			if h.expiresSoon(robot.ExpiresAt, h.ExpiryInterval) {
				// if the account is about to expire, then refresh the credentials
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  expires soon, deleting it", robot.Name))
				err := h.ClientV3.DeleteProjectRobot(
					ctx,
					project,
					int(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
		}
	}
	if !exists || deleted {
		// if it doesn't exist, or was deleted
		// create a new robot account
		token, err := h.ClientV3.AddProjectRobot(
			ctx,
			project,
			&harborclientv3legacy.RobotAccountCreate{
				Name:        robotName,
				Description: fmt.Sprintf("Robot account created in %s", h.LagoonTargetName),
				ExpiresAt:   expiry,
				Access: []*harborclientv3legacy.RobotAccountAccess{
					{Action: "push", Resource: fmt.Sprintf("/project/%d/repository", project.ProjectID)},
					{Action: "pull", Resource: fmt.Sprintf("/project/%d/repository", project.ProjectID)},
				},
			},
		)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error adding project %s robot account %s", project.Name, robotName))
			return nil, err
		}
		// then craft and return the harbor credential secret
		harborRegistryCredentials := makeHarborSecret(
			robotAccountCredential{
				Token: token,
				Name:  h.addPrefix(robotName),
			},
		)
		h.Log.Info(fmt.Sprintf("Created robot account %s", h.addPrefix(robotName)))
		return &harborRegistryCredentials, nil
	}
	return nil, err
}
