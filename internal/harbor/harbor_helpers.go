package harbor

import (
	"fmt"
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
)

type robotAccountCredential struct {
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
