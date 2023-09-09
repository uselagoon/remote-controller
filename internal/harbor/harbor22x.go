package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	harborclientv5model "github.com/mittwald/goharbor-client/v5/apiv2/model"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
	expiry time.Duration,
	force bool,
) (*helpers.RegistryCredentials, error) {

	// create a cluster specific robot account name
	robotName := h.generateRobotName(environmentName)

	expiryDays := int64(math.Ceil(expiry.Hours() / 24))
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
	tempRobots := robots[:0]
	for _, robot := range robots {
		if h.matchRobotAccount(robot.Name, project.Name, environmentName) && !robot.Editable {
			// this is an old (legacy) robot account, get rid of it
			// if accounts are disabled, and deletion of disabled accounts is enabled
			// then this will delete the account to get re-created
			h.Log.Info(fmt.Sprintf("Harbor robot account %s is a legacy account, deleting it", robot.Name))
			err := h.ClientV5.DeleteProjectRobotV1(
				ctx,
				project.Name,
				int64(robot.ID),
			)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", project.Name, robot.Name))
				return nil, err
			}
			continue
		}
		// only add non legacy robots into the slice
		tempRobots = append(tempRobots, robot)
	}
	robots = tempRobots
	for _, robot := range robots {
		if h.matchRobotAccountV2(robot.Name, project.Name, environmentName) && robot.Editable {
			h.Log.Info(fmt.Sprintf("Harbor robot account %s matched", robot.Name))
			// if it is a new robot account, follow through here
			exists = true
			if forceRecreate || force {
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
		return h.CreateRobotAccountV2(ctx, robotName, project.Name, expiryDays)
	}
	return nil, err
}

// DeleteRepository will delete repositories related to an environment
func (h *Harbor) DeleteRepository(ctx context.Context, projectName, branch string) {
	environmentName := helpers.ShortenEnvironment(projectName, helpers.MakeSafe(branch))
	h.Config.PageSize = 100
	pageCount := int64(1)
	listRepositories := h.ListRepositories(ctx, projectName, pageCount)
	for _, repo := range listRepositories {
		if strings.Contains(repo.Name, fmt.Sprintf("%s/%s", projectName, environmentName)) {
			repoName := strings.Replace(repo.Name, fmt.Sprintf("%s/", projectName), "", 1)
			err := h.ClientV5.DeleteRepository(ctx, projectName, repoName)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error deleting harbor repository %s", repo.Name))
			}
		}
	}
	if len(listRepositories) > 100 {
		// h.Log.Info(fmt.Sprintf("more than pagesize repositories returned"))
		pageCount = int64(len(listRepositories) / 100)
		var page int64
		for page = 2; page <= pageCount; page++ {
			listRepositories := h.ListRepositories(ctx, projectName, page)
			for _, repo := range listRepositories {
				if strings.Contains(repo.Name, fmt.Sprintf("%s/%s", projectName, environmentName)) {
					repoName := strings.Replace(repo.Name, fmt.Sprintf("%s/", projectName), "", 1)
					err := h.ClientV5.DeleteRepository(ctx, projectName, repoName)
					if err != nil {
						h.Log.Info(fmt.Sprintf("Error deleting harbor repository %s", repo.Name))
					}
				}
			}
		}
	}
}

// ListRepositories .
func (h *Harbor) ListRepositories(ctx context.Context, projectName string, pageNumber int64) []*harborclientv5model.Repository {
	listRepositories, err := h.ClientV5.ListRepositories(ctx, projectName)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error listing harbor repositories for project %s", projectName))
	}
	return listRepositories
}

func (h *Harbor) CreateRobotAccountV2(ctx context.Context, robotName, projectName string, expiryDays int64) (*helpers.RegistryCredentials, error) {
	robotf := harborclientv5model.RobotCreate{
		Level:    "project",
		Name:     robotName,
		Duration: expiryDays,
		Permissions: []*harborclientv5model.RobotPermission{
			{
				Kind:      "project",
				Namespace: projectName,
				Access: []*harborclientv5model.Access{
					{
						Action:   "push",
						Resource: "repository",
					},
					{
						Action:   "pull",
						Resource: "repository",
					},
				},
			},
		},
		Disable:     false,
		Description: fmt.Sprintf("Robot account created in %s", h.LagoonTargetName),
	}
	token, err := h.ClientV5.NewRobotAccount(ctx, &robotf)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error adding project %s robot account %s", projectName, robotName))
		return nil, err
	}
	// then craft and return the harbor credential secret
	harborRegistryCredentials := makeHarborSecret(
		robotAccountCredential{
			Token: token.Secret,
			Name:  token.Name,
		},
	)
	h.Log.Info(fmt.Sprintf("Created robot account %s", token.Name))
	return &harborRegistryCredentials, nil
}
