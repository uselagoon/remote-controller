package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	harborclientv5 "github.com/mittwald/goharbor-client/v5/apiv2"
	harborclientv5model "github.com/mittwald/goharbor-client/v5/apiv2/model"
	"github.com/mittwald/goharbor-client/v5/apiv2/pkg/config"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dockerconfig "github.com/docker/cli/cli/config/configfile"
)

// CreateOrRefreshRobotV2 will create or refresh a robot account and return the credentials if needed.
func (h *Harbor) CreateOrRefreshRobotV2(ctx context.Context,
	k8s client.Client,
	project *harborclientv5model.Project,
	environmentName, namespace string,
	expiry time.Duration,
	force bool,
) (*RobotAccountCredential, error) {
	// create a cluster specific robot account name
	robotName := h.generateRobotName(environmentName)

	expiryDays := int64(math.Ceil(expiry.Hours() / 24))
	robots, err := h.getRobotsByProjectID(ctx, int64(project.ProjectID))
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
		auths := dockerconfig.ConfigFile{}
		// unmarshal it
		if err := json.Unmarshal(secretData, &auths); err != nil {
			return nil, fmt.Errorf("could not unmarshal Harbor RobotAccount credential")
		}
		// set the force recreate robot account flag here
		forceRecreate = true
		// if the defined regional harbor key exists using the hostname then set the flag to false
		// if the account is set to expire, the loop below will catch it for us
		// just the hostname, as this is what all new robot accounts are created with
		if _, ok := auths.AuthConfigs[h.Hostname]; ok {
			forceRecreate = false
		}
	}
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
				err := h.ClientV5.DeleteRobotAccountByID(
					ctx,
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
				err := h.ClientV5.DeleteRobotAccountByID(
					ctx,
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
				err := h.ClientV5.DeleteRobotAccountByID(
					ctx,
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
				err := h.ClientV5.DeleteRobotAccountByID(
					ctx,
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

// DeleteRobotAccount will delete robot account related to an environment
func (h *Harbor) DeleteRobotAccount(ctx context.Context, projectID int64, projectName, branch string) {
	environmentName := helpers.ShortenEnvironment(projectName, helpers.MakeSafe(branch))
	robots, err := h.getRobotsByProjectID(ctx, projectID)
	if err != nil {
		h.Log.Info(fmt.Sprintf("Error listing project %s robot accounts", projectName))
		return
	}
	for _, robot := range robots {
		if h.matchRobotAccountV2(robot.Name, projectName, environmentName) {
			err := h.ClientV5.DeleteProjectRobotV1(
				ctx,
				projectName,
				int64(robot.ID),
			)
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error deleting project %s robot account %s", projectName, robot.Name))
				return
			}
			h.Log.Info(
				fmt.Sprintf(
					"Deleted harbor robot account %s in  project %s, environment %s",
					robot.Name,
					projectName,
					environmentName,
				),
			)
		}
	}
}

func (h *Harbor) CreateRobotAccountV2(ctx context.Context, robotName, projectName string, expiryDays int64) (*RobotAccountCredential, error) {
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
	harborRegistryCredentials := RobotAccountCredential{
		Token: token.Secret,
		Name:  token.Name,
	}
	h.Log.Info(fmt.Sprintf("Created robot account %s", token.Name))
	return &harborRegistryCredentials, nil
}

func (h *Harbor) getRobotsByProjectID(ctx context.Context, id int64) ([]*harborclientv5model.Robot, error) {
	query := fmt.Sprintf("Level=project,ProjectID=%d", id)
	conf := &config.Options{
		Page:     1,
		PageSize: 100,
		Query:    query,
	}
	cl, err := harborclientv5.NewRESTClientForHost(h.API, h.Username, h.Password, conf)
	if err != nil {
		return nil, err
	}
	return cl.ListRobotAccounts(ctx)
}
