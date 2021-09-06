package controllers

import (
	"fmt"
	"sort"

	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"
	harborv2 "github.com/mittwald/goharbor-client/v3/apiv2"
	"github.com/mittwald/goharbor-client/v3/apiv2/model"
	"github.com/mittwald/goharbor-client/v3/apiv2/model/legacy"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Harbor defines a harbor struct
type Harbor struct {
	URL                 string
	Hostname            string
	API                 string
	Username            string
	Password            string
	Log                 logr.Logger
	Client              *harborv2.RESTClient
	DeleteDisabled      bool
	WebhookAddition     bool
	RobotPrefix         string
	ExpiryInterval      time.Duration
	RotateInterval      time.Duration
	RobotAccountExpiry  time.Duration
	ControllerNamespace string
	WebhookURL          string
	WebhookEventTypes   []string
}

type robotAccountCredential struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	Token     string `json:"token"`
}

// NewHarbor create a new harbor connection.
func NewHarbor(harbor Harbor) (*Harbor, error) {
	c, err := harborv2.NewRESTClientForHost(harbor.API, harbor.Username, harbor.Password)
	if err != nil {
		return nil, err
	}
	harbor.Log = ctrl.Log.WithName("controllers").WithName("HarborIntegration")
	harbor.Client = c
	return &harbor, nil
}

// CreateProject will create a project if one doesn't exist, but will update as required.
func (h *Harbor) CreateProject(ctx context.Context, projectName string) (*model.Project, error) {
	project, err := h.Client.GetProjectByName(ctx, projectName)
	if err != nil {
		if err.Error() == "project not found on server side" || err.Error() == "resource unknown" {
			project, err = h.Client.NewProject(ctx, projectName, int64Ptr(-1))
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error creating project %s", projectName))
				return nil, err
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			tStr := "true"
			err = h.Client.UpdateProject(ctx, &model.Project{
				Name:      projectName,
				ProjectID: project.ProjectID,
				Metadata: &model.ProjectMetadata{
					AutoScan:             &tStr,
					ReuseSysCveAllowlist: &tStr,
					Public:               "false",
				},
			}, int64Ptr(-1))
			if err != nil {
				h.Log.Info(fmt.Sprintf("Error updating project %s", projectName))
				return nil, err
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			project, err = h.Client.GetProjectByName(ctx, projectName)
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
	// w, err := h.Client.ListRepositories(ctx, project)
	// if err != nil {
	// 	return nil, err
	// }
	// for _, x := range w {
	// 	fmt.Println(x)
	// }

	if h.WebhookAddition {
		wps, err := h.Client.ListProjectWebhookPolicies(ctx, project)
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
				newPolicy := &legacy.WebhookPolicy{
					Name:      wp.Name,
					ProjectID: int64(project.ProjectID),
					Enabled:   true,
					Targets: []*legacy.WebhookTargetObject{
						{
							Type:           "http",
							SkipCertVerify: true,
							Address:        h.WebhookURL,
						},
					},
					EventTypes: h.WebhookEventTypes,
				}
				err = h.Client.UpdateProjectWebhookPolicy(ctx, project, int(wp.ID), newPolicy)
				if err != nil {
					h.Log.Info(fmt.Sprintf("Error updating project %s webhook", project.Name))
					return nil, err
				}
			}
		}
		if !exists {
			// otherwise create the webhook if it doesn't exist
			newPolicy := &legacy.WebhookPolicy{
				Name:      "Lagoon Default Webhook",
				ProjectID: int64(project.ProjectID),
				Enabled:   true,
				Targets: []*legacy.WebhookTargetObject{
					{
						Type:           "http",
						SkipCertVerify: true,
						Address:        h.WebhookURL,
					},
				},
				EventTypes: h.WebhookEventTypes,
			}
			err = h.Client.AddProjectWebhookPolicy(ctx, project, newPolicy)
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
	cl client.Client,
	project *model.Project,
	robotName, namespace string,
	expiry int64,
) (*RegistryCredentials, error) {
	robots, err := h.Client.ListProjectRobots(
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
	err = cl.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "lagoon-internal-registry-secret",
	}, secret)
	if err != nil {
		// the lagoon registry secret doesn't exist, force re-create the robot account
		forceRecreate = true
	}
	// check if the secret contains the .dockerconfigjson data
	if secretData, ok := secret.Data[".dockerconfigjson"]; ok {
		auths := Auths{}
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
		if h.matchRobotAccount(robot, project, robotName) {
			exists = true
			if forceRecreate {
				// if the secret doesn't exist in kubernetes, then force re-creation of the robot
				// account is required, as there isn't a way to get the credentials after
				// robot accounts are created
				h.Log.Info(fmt.Sprintf("Kubernetes secret doesn't exist, robot account %s needs to be re-created", robot.Name))
				err := h.Client.DeleteProjectRobot(
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
				err := h.Client.DeleteProjectRobot(
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
			if h.shouldRotate(robot, h.RotateInterval) {
				// this forces a rotation after a certain period, whether its expiring or already expired.
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  should rotate, deleting it", robot.Name))
				err := h.Client.DeleteProjectRobot(
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
			if h.expiresSoon(robot, h.ExpiryInterval) {
				// if the account is about to expire, then refresh the credentials
				h.Log.Info(fmt.Sprintf("Harbor robot account %s  expires soon, deleting it", robot.Name))
				err := h.Client.DeleteProjectRobot(
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
		token, err := h.Client.AddProjectRobot(
			ctx,
			project,
			&legacy.RobotAccountCreate{
				Name:      robotName,
				ExpiresAt: expiry,
				Access: []*legacy.RobotAccountAccess{
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
		opLog.Info(fmt.Sprintf("Checking if %s needs robot credentials rotated", ns.ObjectMeta.Name))
		// check for running builds!
		lagoonBuilds := &lagoonv1alpha1.LagoonBuildList{}
		listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
			client.InNamespace(ns.ObjectMeta.Name),
			client.MatchingLabels(map[string]string{
				"lagoon.sh/controller": h.ControllerNamespace, // created by this controller
			}),
		})
		if err := cl.List(context.Background(), lagoonBuilds, listOption); err != nil {
			opLog.Error(err, fmt.Sprintf("Unable to list Lagoon build pods, there may be none or something went wrong"))
			return
		}
		runningBuilds := false
		sort.Slice(lagoonBuilds.Items, func(i, j int) bool {
			return lagoonBuilds.Items[i].ObjectMeta.CreationTimestamp.After(lagoonBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		// if there are any builds pending or running, don't try and refresh the credentials as this
		// could break the build
		if len(lagoonBuilds.Items) > 0 {
			if lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"] == string(lagoonv1alpha1.BuildStatusRunning) ||
				lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"] == string(lagoonv1alpha1.BuildStatusPending) {
				runningBuilds = true
			}
		}
		if !runningBuilds {
			// only continue if there isn't any running builds
			hProject, err := h.CreateProject(ctx, ns.Labels["lagoon.sh/project"])
			if err != nil {
				// @TODO: resource unknown
				opLog.Error(err, "error getting or creating project")
				break
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			robotCreds, err := h.CreateOrRefreshRobot(ctx,
				cl,
				hProject,
				ns.Labels["lagoon.sh/environment"],
				ns.ObjectMeta.Name,
				time.Now().Add(h.RobotAccountExpiry).Unix())
			if err != nil {
				opLog.Error(err, "error getting or creating robot account")
				break
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			if robotCreds != nil {
				// if we have robotcredentials to create, do that here
				if err := upsertHarborSecret(ctx,
					cl,
					ns.ObjectMeta.Name,
					"lagoon-internal-registry-secret", //secret name in kubernetes
					h.Hostname,
					robotCreds); err != nil {
					opLog.Error(err, "error creating or updating robot account credentials")
					break
				}
				opLog.Info(fmt.Sprintf("Robot credentials rotated for %s", ns.ObjectMeta.Name))
			}
		} else {
			opLog.Info(fmt.Sprintf("There are running or pending builds in %s, skipping", ns.ObjectMeta.Name))
		}
	}
}

// addPrefix adds the robot account prefix to robot accounts
// @TODO: Harbor 2.2.0 changes this behavior, see note below in `matchRobotAccount`
func (h *Harbor) addPrefix(str string) string {
	return h.RobotPrefix + str
}

// matchRobotAccount will check if the robotaccount exists or not
func (h *Harbor) matchRobotAccount(robot *legacy.RobotAccount,
	project *model.Project,
	accountSuffix string,
) bool {
	// pre global-robot-accounts (2.2.0+)
	if robot.Name == h.addPrefix(accountSuffix) {
		return true
	}
	// 2.2.0 introduces "global" robot accounts
	// when using the old API they get created
	// with a different name: robot${project-name}+{provided-name}
	// on the GET side we map them back to robot${provided-name}
	if robot.Name == h.addPrefix(fmt.Sprintf("%s+%s", project.Name, accountSuffix)) {
		return true
	}
	return false
}

// already expired?
func (h *Harbor) shouldRotate(robot *legacy.RobotAccount, interval time.Duration) bool {
	created, err := time.Parse(time.RFC3339Nano, robot.CreationTime)
	if err != nil {
		h.Log.Error(err, "error parsing time")
		return true
	}
	return created.UTC().Add(interval).Before(time.Now().UTC())
}

// expiresSoon checks if the robot account will expire soon
func (h *Harbor) expiresSoon(robot *legacy.RobotAccount, duration time.Duration) bool {
	now := time.Now().UTC().Add(duration)
	expiry := time.Unix(robot.ExpiresAt, 0)
	return expiry.Before(now)
}

// makeHarborSecret creates the secret definition.
func makeHarborSecret(credentials robotAccountCredential) RegistryCredentials {
	return RegistryCredentials{
		Username: credentials.Name,
		Password: credentials.Token,
		Auth: base64.StdEncoding.EncodeToString(
			[]byte(
				fmt.Sprintf("%s:%s", credentials.Name, credentials.Token),
			),
		)}
}

// upsertHarborSecret will create or update the secret in kubernetes.
func upsertHarborSecret(ctx context.Context, cl client.Client, ns, name, baseURL string, registryCreds *RegistryCredentials) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	dcj := &Auths{
		Registries: make(map[string]RegistryCredentials),
	}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      name,
	}, secret)
	if err != nil {
		// if the secret doesn't exist
		// create it
		dcj.Registries[baseURL] = *registryCreds
		dcjBytes, _ := json.Marshal(dcj)
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(dcjBytes),
		}
		err := cl.Create(ctx, secret)
		if err != nil {
			return fmt.Errorf("could not create secret %s/%s: %s", secret.ObjectMeta.Namespace, secret.ObjectMeta.Name, err.Error())
		}
		return nil
	}
	// if the secret exists
	// update the secret with the new credentials
	json.Unmarshal([]byte(secret.Data[corev1.DockerConfigJsonKey]), &dcj)
	// add or update the credential
	dcj.Registries[baseURL] = *registryCreds
	dcjBytes, _ := json.Marshal(dcj)
	secret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(dcjBytes),
	}
	err = cl.Update(ctx, secret)
	if err != nil {
		return fmt.Errorf("could not update secret: %s/%s", secret.ObjectMeta.Namespace, secret.ObjectMeta.Name)
	}
	return nil
}
