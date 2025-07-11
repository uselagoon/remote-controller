package harbor

import (
	"fmt"
	"regexp"
	"strings"

	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/uselagoon/remote-controller/internal/helpers"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/coreos/go-semver/semver"

	dockerconfig "github.com/docker/cli/cli/config/configfile"
	dockertypes "github.com/docker/cli/cli/config/types"
)

type RobotAccountCredential struct {
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	Token     string `json:"token"`
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

// https://github.com/goharbor/harbor/pull/13685
func harborRobotV2Regex(name string) string {
	robotNameReg := `^[a-z0-9]+(?:[._-][a-z0-9]+)*$`
	// check if the robot account matches the regex that harbor supports for robot account names
	// if it is "legal" then let it through
	legal := regexp.MustCompile(robotNameReg).MatchString(name)
	if !legal {
		// if it isn't legal, then hash the name
		return helpers.HashString(name)[0:20]
	}
	return name
}

// generateRobotWithPrefix adds the robot account prefix to robot accounts
func (h *Harbor) generateRobotWithPrefixV2(projectName, environmentName string) string {
	return fmt.Sprintf("%s%s+%s", h.RobotPrefix, projectName, h.generateRobotName(environmentName))
}

func (h *Harbor) generateRobotName(environmentName string) string {
	return fmt.Sprintf("%s-%s", harborRobotV2Regex(environmentName), helpers.HashString(h.LagoonTargetName)[0:8])
}

// matchRobotAccountV2 will check if the robotaccount exists or not
func (h *Harbor) matchRobotAccountV2(robotName string,
	projectName string,
	environmentName string,
) bool {
	return robotName == h.generateRobotWithPrefixV2(projectName, environmentName)
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

// UpsertHarborSecret will create or update the secret in kubernetes.
func (h *Harbor) UpsertHarborSecret(ctx context.Context, cl client.Client, ns, name string, credentials *RobotAccountCredential) (bool, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	auths := dockerconfig.ConfigFile{
		AuthConfigs: map[string]dockertypes.AuthConfig{
			h.Hostname: {
				Username: credentials.Name,
				Password: credentials.Token,
				Auth:     base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", credentials.Name, credentials.Token))),
			},
		},
	}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      name,
	}, secret)
	if err != nil {
		// if the secret doesn't exist then create the secret
		authsBytes, _ := json.Marshal(auths)
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: authsBytes,
		}
		secret.Labels = map[string]string{
			"lagoon.sh/controller":        h.ControllerNamespace,
			"lagoon.sh/harbor-credential": "true",
		}
		err := cl.Create(ctx, secret)
		if err != nil {
			return false, fmt.Errorf("could not create secret %s/%s: %s", secret.Namespace, secret.Name, err.Error())
		}
		// return true that the credential was created
		return true, nil
	}
	// else update the secret with the newly provided credentials
	authsBytes, _ := json.Marshal(auths)
	secret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: authsBytes,
	}
	// add the controller label if it doesn't exist
	if _, ok := secret.Labels["lagoon.sh/controller"]; !ok {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels["lagoon.sh/controller"] = h.ControllerNamespace
		secret.Labels["lagoon.sh/harbor-credential"] = "true"
	}
	err = cl.Update(ctx, secret)
	if err != nil {
		return false, fmt.Errorf("could not update secret: %s/%s", secret.Namespace, secret.Name)
	}
	return true, nil
}
