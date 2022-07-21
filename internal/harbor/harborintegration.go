package harbor

import (
	"fmt"
	"sort"
	"strings"

	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/go-logr/logr"
	harborclientv3 "github.com/mittwald/goharbor-client/v3/apiv2"

	harborclientv5 "github.com/mittwald/goharbor-client/v5/apiv2"
	"github.com/mittwald/goharbor-client/v5/apiv2/pkg/config"

	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	"github.com/uselagoon/remote-controller/internal/helpers"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/coreos/go-semver/semver"
)

type robotAccountCredential struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	Token     string `json:"token"`
}

// Harbor defines a harbor struct
type Harbor struct {
	URL                   string
	Hostname              string
	API                   string
	Username              string
	Password              string
	Log                   logr.Logger
	ClientV3              *harborclientv3.RESTClient
	ClientV5              *harborclientv5.RESTClient
	DeleteDisabled        bool
	WebhookAddition       bool
	RobotPrefix           string
	ExpiryInterval        time.Duration
	RotateInterval        time.Duration
	RobotAccountExpiry    time.Duration
	ControllerNamespace   string
	NamespacePrefix       string
	RandomNamespacePrefix bool
	WebhookURL            string
	WebhookEventTypes     []string
	LagoonTargetName      string
	Config                *config.Options
}

// NewHarbor create a new harbor connection.
func NewHarbor(harbor Harbor) (*Harbor, error) {
	harbor.Log = ctrl.Log.WithName("controllers").WithName("HarborIntegration")
	c, err := harborclientv3.NewRESTClientForHost(harbor.API, harbor.Username, harbor.Password)
	if err != nil {
		return nil, err
	}
	harbor.ClientV3 = c
	harbor.Config = &config.Options{}
	c2, err := harborclientv5.NewRESTClientForHost(harbor.API, harbor.Username, harbor.Password, harbor.Config)
	if err != nil {
		return nil, err
	}
	harbor.ClientV5 = c2
	return &harbor, nil
}

// GetHarborVersion returns the version of harbor.
func (h *Harbor) GetHarborVersion(ctx context.Context) (string, error) {
	harborVersion, err := h.ClientV5.GetSystemInfo(ctx)
	if err != nil {
		return "", err
	}
	// harbor versions are returned as `v2.1.2-abcdef`, this returns just the `2.1.2` of the version
	// `[1:] strips the v`
	version := strings.Split(*harborVersion.HarborVersion, "-")[0][1:]
	return version, nil
}

// UseV2Functions .
func (h *Harbor) UseV2Functions(version string) bool {
	currentVersion := semver.New(version)
	harborV2 := semver.New("2.2.0")
	// invert the result
	return !currentVersion.LessThan(*harborV2)
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
		if ns.Status.Phase == corev1.NamespaceTerminating {
			// if the namespace is terminating, don't try to renew the robot credentials
			opLog.Info(fmt.Sprintf("Namespace %s is being terminated, aborting robot credentials check", ns.ObjectMeta.Name))
			return
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
			return
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
			// only continue if there isn't any running builds
			robotCreds := &helpers.RegistryCredentials{}
			curVer, err := h.GetHarborVersion(ctx)
			if err != nil {
				// @TODO: resource unknown
				opLog.Error(err, "error checking harbor version")
				break
			}
			if h.UseV2Functions(curVer) {
				hProject, err := h.CreateProjectV2(ctx, ns.Labels["lagoon.sh/project"])
				if err != nil {
					// @TODO: resource unknown
					opLog.Error(err, "error getting or creating project")
					break
				}
				time.Sleep(1 * time.Second) // wait 1 seconds
				robotCreds, err = h.CreateOrRefreshRobotV2(ctx,
					cl,
					hProject,
					ns.Labels["lagoon.sh/environment"],
					ns.ObjectMeta.Name,
					h.RobotAccountExpiry)
				if err != nil {
					opLog.Error(err, "error getting or creating robot account")
					break
				}
			} else {
				hProject, err := h.CreateProject(ctx, ns.Labels["lagoon.sh/project"])
				if err != nil {
					// @TODO: resource unknown
					opLog.Error(err, "error getting or creating project")
					break
				}
				time.Sleep(1 * time.Second) // wait 1 seconds
				robotCreds, err = h.CreateOrRefreshRobot(ctx,
					cl,
					hProject,
					ns.Labels["lagoon.sh/environment"],
					ns.ObjectMeta.Name,
					time.Now().Add(h.RobotAccountExpiry).Unix())
				if err != nil {
					opLog.Error(err, "error getting or creating robot account")
					break
				}
			}
			time.Sleep(1 * time.Second) // wait 1 seconds
			if robotCreds != nil {
				// if we have robotcredentials to create, do that here
				if err := UpsertHarborSecret(ctx,
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
func (h *Harbor) matchRobotAccount(robotName string,
	projectName string,
	environmentName string,
) bool {
	// pre global-robot-accounts (2.2.0+)
	if robotName == h.addPrefix(fmt.Sprintf("%s-%s", environmentName, helpers.HashString(h.LagoonTargetName)[0:8])) {
		return true
	}
	return false
}

// matchRobotAccountV2 will check if the robotaccount exists or not
func (h *Harbor) matchRobotAccountV2(robotName string,
	projectName string,
	environmentName string,
) bool {
	if robotName == h.addPrefixV2(projectName, environmentName) {
		return true
	}
	return false
}

// already expired?
func (h *Harbor) shouldRotate(creationTime string, interval time.Duration) bool {
	created, err := time.Parse(time.RFC3339Nano, creationTime)
	if err != nil {
		h.Log.Error(err, "error parsing time")
		return true
	}
	return created.UTC().Add(interval).Before(time.Now().UTC())
}

// expiresSoon checks if the robot account will expire soon
func (h *Harbor) expiresSoon(expiresAt int64, duration time.Duration) bool {
	now := time.Now().UTC().Add(duration)
	expiry := time.Unix(expiresAt, 0)
	return expiry.Before(now)
}

// makeHarborSecret creates the secret definition.
func makeHarborSecret(credentials robotAccountCredential) helpers.RegistryCredentials {
	return helpers.RegistryCredentials{
		Username: credentials.Name,
		Password: credentials.Token,
		Auth: base64.StdEncoding.EncodeToString(
			[]byte(
				fmt.Sprintf("%s:%s", credentials.Name, credentials.Token),
			),
		)}
}

// UpsertHarborSecret will create or update the secret in kubernetes.
func UpsertHarborSecret(ctx context.Context, cl client.Client, ns, name, baseURL string, registryCreds *helpers.RegistryCredentials) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	dcj := &helpers.Auths{
		Registries: make(map[string]helpers.RegistryCredentials),
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
