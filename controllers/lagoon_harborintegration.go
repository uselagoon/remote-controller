package controllers

import (
	"fmt"
	"sort"

	"context"
	"encoding/base64"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"
	harborv2 "github.com/mittwald/goharbor-client/v3/apiv2"
	"github.com/mittwald/goharbor-client/v3/apiv2/model"
	"github.com/mittwald/goharbor-client/v3/apiv2/model/legacy"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Harbor defines a harbor struct
type Harbor struct {
	URL                 string
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
		if err.Error() == "project not found on server side" {
			project, err = h.Client.NewProject(ctx, projectName, int64Ptr(-1))
			if err != nil {
				return nil, err
			}
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
				return nil, err
			}
			project, err = h.Client.GetProjectByName(ctx, projectName)
			if err != nil {
				return nil, err
			}
			h.Log.Info(fmt.Sprintf("Created harbor project %s", project.Name))
		} else {
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
				return nil, err
			}
		}
	}
	return project, nil
}

// CreateOrRefreshRobot will create or refresh a robot account and return the credentials if needed.
func (h *Harbor) CreateOrRefreshRobot(ctx context.Context,
	project *model.Project,
	robotName, namespace, secretName string,
	expiry int64,
) (*corev1.Secret, error) {
	robots, err := h.Client.ListProjectRobots(
		ctx,
		project,
	)
	if err != nil {
		return nil, err
	}
	exists := false
	deleted := false
	for _, robot := range robots {
		if h.matchRobotAccount(robot, project, robotName) {
			exists = true
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
			return nil, err
		}
		// then craft and return the harbor credential secret
		harborSecret := makeHarborSecret(
			namespace,
			secretName,
			h.URL,
			robotAccountCredential{
				Token: token,
				Name:  h.addPrefix(robotName),
			},
		)
		h.Log.Info(fmt.Sprintf("Created robot account %s", h.addPrefix(robotName)))
		return &harborSecret, nil
	}
	return nil, err
}

// RotateRobotCredentials will attempt to recreate any robot account credentials that need to be rotated.
func (h *Harbor) RotateRobotCredentials(ctx context.Context, cl client.Client) {
	opLog := ctrl.Log.WithName("handlers").WithName("RotateRobotCredentials")
	namespaces := &corev1.NamespaceList{}
	labelRequirements, _ := labels.NewRequirement("lagoon.sh/environmentType", selection.Exists, nil)
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.MatchingLabelsSelector{
			Selector: labels.NewSelector().Add(*labelRequirements),
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
				// "lagoon.sh/jobType":    "build",
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
			if lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"] == "Running" || lagoonBuilds.Items[0].Labels["lagoon.sh/buildStatus"] == "Pending" {
				runningBuilds = true
			}
		}
		if !runningBuilds {
			// only continue if there isn't any running builds
			hProject, err := h.CreateProject(ctx, ns.Labels["lagoon.sh/project"])
			if err != nil {
				opLog.Error(err, "error getting or creating project")
				break
			}
			robotCreds, err := h.CreateOrRefreshRobot(ctx,
				hProject,
				ns.Labels["lagoon.sh/environment"],
				ns.ObjectMeta.Name,
				"lagoon-internal-registry-secret",
				time.Now().Add(h.RobotAccountExpiry).Unix())
			if err != nil {
				opLog.Error(err, "error getting or creating robot account")
				break
			}
			if robotCreds != nil {
				// if we have robotcredentials to create, do that here
				if err := upsertHarborSecret(ctx, cl, robotCreds); err != nil {
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
func makeHarborSecret(namespace, name string, baseURL string, credentials robotAccountCredential) corev1.Secret {
	auth := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", credentials.Name, credentials.Token)))
	configJSON := fmt.Sprintf(`{"auths":{"%s":{"username":"%s","password":"%s","auth":"%s"}}}`, baseURL, credentials.Name, credentials.Token, auth)
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(configJSON),
		},
	}
}

// upsertHarborSecret will create or update the secret in kubernetes.
func upsertHarborSecret(ctx context.Context, cl client.Client, secret *corev1.Secret) error {
	err := cl.Create(ctx, secret)
	if apierrs.IsAlreadyExists(err) {
		err = cl.Update(ctx, secret)
		if err != nil {
			return fmt.Errorf("could not update secret: %s/%s", secret.ObjectMeta.Namespace, secret.ObjectMeta.Name)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not create secret %s/%s: %s", secret.ObjectMeta.Namespace, secret.ObjectMeta.Name, err.Error())
	}
	return nil
}
