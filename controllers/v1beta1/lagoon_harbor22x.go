package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"

	harborclientv5model "github.com/mittwald/goharbor-client/v5/apiv2/model"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// addPrefix adds the robot account prefix to robot accounts
// @TODO: Harbor 2.2.0 changes this behavior, see note below in `matchRobotAccount`
func (h *Harbor) addPrefixV2(projectName, environmentName string) string {
	return fmt.Sprintf("%s%s+%s", h.RobotPrefix, projectName, environmentName)
}

// CreateProjectV2 will create a project if one doesn't exist, but will update as required.
func (h *Harbor) CreateProjectV2(ctx context.Context, projectName string) (*harborclientv5model.Project, error) {
	exists, err := h.ClientV5.ProjectExists(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error checking project %s exists, err: %v", projectName, err))
		return nil, err
	}
	if !exists {
		err := h.ClientV5.NewProject(ctx, &harborclientv5model.ProjectReq{
			ProjectName: projectName,
		})
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error creating project %s, err: %v", projectName, err))
			return nil, err
		}
		project, err := h.ClientV5.GetProject(ctx, projectName)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error getting project %s, err: %v", projectName, err))
			return nil, err
		}
		stor := int64(-1)
		tStr := "true"
		project.Metadata = &harborclientv5model.ProjectMetadata{
			AutoScan:             &tStr,
			ReuseSysCVEAllowlist: &tStr,
			Public:               "false",
		}
		err = h.ClientV5.UpdateProject(ctx, project, &stor)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error updating project %s, err: %v", projectName, err))
			return nil, err
		}
	}
	project, err := h.ClientV5.GetProject(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error getting project %s, err: %v", projectName, err))
		return nil, err
	}

	// TODO: Repository support not required yet
	// this is a place holder
	// w, err := h.ClientV5.ListRepositories(ctx, int(project.ProjectID))
	// if err != nil {
	// 	return nil, err
	// }
	// for _, x := range w {
	// 	fmt.Println(x)
	// }

	if h.WebhookAddition {
		wps, err := h.ClientV5.ListProjectWebhookPolicies(ctx, int(project.ProjectID))
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
				wp.Targets = []*harborclientv5model.WebhookTargetObject{
					{
						Type:           "http",
						SkipCertVerify: true,
						Address:        h.WebhookURL,
					},
				}
				wp.Enabled = true
				wp.EventTypes = []string{"SCANNING_FAILED", "SCANNING_COMPLETED"}
				err = h.ClientV5.UpdateProjectWebhookPolicy(ctx, int(wp.ProjectID), wp)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error updating project %s webhook", project.Name))
					return nil, err
				}
			}
		}
		if !exists {
			// otherwise create the webhook if it doesn't exist
			newPolicy := &harborclientv5model.WebhookPolicy{
				Name:      "Lagoon Default Webhook",
				ProjectID: int64(project.ProjectID),
				Enabled:   true,
				Targets: []*harborclientv5model.WebhookTargetObject{
					{
						Type:           "http",
						SkipCertVerify: true,
						Address:        h.WebhookURL,
					},
				},
				EventTypes: []string{"SCANNING_FAILED", "SCANNING_COMPLETED"},
			}
			err = h.ClientV5.AddProjectWebhookPolicy(ctx, int(project.ProjectID), newPolicy)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error adding project %s webhook", project.Name))
				return nil, err
			}
		}
	}
	return project, nil
}

// CreateOrRefreshRobotV2 will create or refresh a robot account and return the credentials if needed.
func (h *Harbor) CreateOrRefreshRobotV2(ctx context.Context,
	k8s client.Client,
	project *harborclientv5model.Project,
	environmentName, namespace string,
	expiry int64,
) (*helpers.RegistryCredentials, error) {
	robots, err := h.ClientV5.ListProjectRobotsV1(
		ctx,
		project.Name,
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
				err := h.ClientV5.DeleteProjectRobotV1(
					ctx,
					project.Name,
					int64(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			h.Log.Info(fmt.Sprintf("disable delete %v / %v", robot.Disable, h.DeleteDisabled))
			if robot.Disable && h.DeleteDisabled {
				// if accounts are disabled, and deletion of disabled accounts is enabled
				// then this will delete the account to get re-created
				h.Log.Info(fmt.Sprintf("Harbor robot account %s disabled, deleting it", robot.Name))
				err := h.ClientV5.DeleteProjectRobotV1(
					ctx,
					project.Name,
					int64(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			h.Log.Info(fmt.Sprintf("should rotate %v / %v / %s / %s", h.shouldRotate(robot.CreationTime.String(), h.RotateInterval), robot.CreationTime.String(), h.RotateInterval))
			if h.shouldRotate(robot.CreationTime.String(), h.RotateInterval) {
				// this forces a rotation after a certain period, whether its expiring or already expired.
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  should rotate, deleting it", robot.Name))
				err := h.ClientV5.DeleteProjectRobotV1(
					ctx,
					project.Name,
					int64(robot.ID),
				)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
					return nil, err
				}
				deleted = true
				continue
			}
			h.Log.Info(fmt.Sprintf("expires soon %v / %s / %s", h.expiresSoon(robot.ExpiresAt, h.ExpiryInterval), robot.ExpiresAt, h.ExpiryInterval))
			if h.expiresSoon(robot.ExpiresAt, h.ExpiryInterval) {
				// if the account is about to expire, then refresh the credentials
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  expires soon, deleting it", robot.Name))
				err := h.ClientV5.DeleteProjectRobotV1(
					ctx,
					project.Name,
					int64(robot.ID),
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
		token, err := h.ClientV5.AddProjectRobotV1(
			ctx,
			project.Name,
			&harborclientv5model.RobotCreateV1{
				Name:      environmentName,
				ExpiresAt: expiry,
				Access: []*harborclientv5model.Access{
					{Action: "push", Resource: fmt.Sprintf("/project/%d/repository", project.ProjectID)},
					{Action: "pull", Resource: fmt.Sprintf("/project/%d/repository", project.ProjectID)},
				},
			},
		)
		if err != nil {
			h.Log.Info(fmt.Sprintf("Error adding project %s robot account %s", project.Name, h.addPrefixV2(project.Name, environmentName)))
			return nil, err
		}
		// then craft and return the harbor credential secret
		harborRegistryCredentials := makeHarborSecret(
			robotAccountCredential{
				Token: token.Payload.Secret,
				Name:  token.Payload.Name,
			},
		)
		h.Log.Info(fmt.Sprintf("Created robot account %s", token.Payload.Name))
		return &harborRegistryCredentials, nil
	}
	return nil, err
}
