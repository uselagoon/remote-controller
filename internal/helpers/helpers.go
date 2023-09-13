package helpers

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-version"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// BuildRunningPendingStatus .
	BuildRunningPendingStatus = []string{
		lagoonv1beta1.BuildStatusPending.String(),
		lagoonv1beta1.BuildStatusQueued.String(),
		lagoonv1beta1.BuildStatusRunning.String(),
	}
	// BuildCompletedCancelledFailedStatus .
	BuildCompletedCancelledFailedStatus = []string{
		lagoonv1beta1.BuildStatusFailed.String(),
		lagoonv1beta1.BuildStatusComplete.String(),
		lagoonv1beta1.BuildStatusCancelled.String(),
	}
	// TaskRunningPendingStatus .
	TaskRunningPendingStatus = []string{
		lagoonv1beta1.TaskStatusPending.String(),
		lagoonv1beta1.TaskStatusQueued.String(),
		lagoonv1beta1.TaskStatusRunning.String(),
	}
	// TaskCompletedCancelledFailedStatus .
	TaskCompletedCancelledFailedStatus = []string{
		lagoonv1beta1.TaskStatusFailed.String(),
		lagoonv1beta1.TaskStatusComplete.String(),
		lagoonv1beta1.TaskStatusCancelled.String(),
	}
)

const (
	// DefaultNamespacePattern is what is used when one is not provided.
	DefaultNamespacePattern = "${project}-${environment}"
)

// IgnoreNotFound will ignore not found errors
func IgnoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// ContainsString check if a slice contains a string
func ContainsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// RemoveString remove string from a sliced
func RemoveString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

// BuildContainsStatus .
func BuildContainsStatus(slice []lagoonv1beta1.LagoonBuildConditions, s lagoonv1beta1.LagoonBuildConditions) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// TaskContainsStatus .
func TaskContainsStatus(slice []lagoonv1beta1.LagoonTaskConditions, s lagoonv1beta1.LagoonTaskConditions) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// IntPtr .
func IntPtr(i int32) *int32 {
	var iPtr *int32
	iPtr = new(int32)
	*iPtr = i
	return iPtr
}

// Int64Ptr .
func Int64Ptr(i int64) *int64 {
	var iPtr *int64
	iPtr = new(int64)
	*iPtr = i
	return iPtr
}

// UintPtr .
func UintPtr(i uint) *uint {
	var iPtr *uint
	iPtr = new(uint)
	*iPtr = i
	return iPtr
}

// MakeSafe ensures that any string is dns safe
func MakeSafe(in string) string {
	out := regexp.MustCompile(`[^0-9a-z-]`).ReplaceAllString(
		strings.ToLower(in),
		"$1-$2",
	)
	return out
}

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

// RandString .
func RandString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

// HashString get the hash of a given string.
func HashString(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	bs := h.Sum(nil)
	return fmt.Sprintf("%x", bs)
}

// RemoveBuild remove a LagoonBuild from a slice of LagoonBuilds
func RemoveBuild(slice []lagoonv1beta1.LagoonBuild, s lagoonv1beta1.LagoonBuild) []lagoonv1beta1.LagoonBuild {
	result := []lagoonv1beta1.LagoonBuild{}
	for _, item := range slice {
		if item.ObjectMeta.Name == s.ObjectMeta.Name {
			continue
		}
		result = append(result, item)
	}
	return result
}

var lowerAlNum = regexp.MustCompile("[^a-z0-9]+")

// ShortName returns a deterministic random short name of 8 lowercase
// alphabetic and numeric characters. The short name is based
// on hashing and encoding the given name.
func ShortName(name string) string {
	hash := sha256.Sum256([]byte(name))
	return lowerAlNum.ReplaceAllString(strings.ToLower(base32.StdEncoding.EncodeToString(hash[:])), "")[:8]
}

// StringToUintPtr .
func StringToUintPtr(s string) *uint {
	// get the environment id and turn it into a *uint to send back to lagoon for logging
	// lagoon sends this as a string :(
	u64, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return nil
	}
	return UintPtr(uint(u64))
}

// ReplaceOrAddVariable will replace or add an environment variable to a slice of environment variables
func ReplaceOrAddVariable(vars *[]LagoonEnvironmentVariable, name, value, scope string) {
	exists := false
	existsIdx := 0
	for idx, v := range *vars {
		if v.Name == name {
			exists = true
			existsIdx = idx
		}
	}
	if exists {
		(*vars)[existsIdx].Value = value
	} else {
		(*vars) = append((*vars), LagoonEnvironmentVariable{
			Name:  name,
			Value: value,
			Scope: scope})
	}
}

// VariableExists checks if a variable exists in a slice of environment variables
func VariableExists(vars *[]LagoonEnvironmentVariable, name, value string) bool {
	exists := false
	for _, v := range *vars {
		if v.Name == name && v.Value == value {
			exists = true
		}
	}
	return exists
}

// GetLagoonVariable returns a given environment variable
func GetLagoonVariable(name string, scope []string, variables []LagoonEnvironmentVariable) (*LagoonEnvironmentVariable, error) {
	for _, v := range variables {
		scoped := true
		if scope != nil {
			scoped = containsStr(scope, v.Scope)
		}
		if v.Name == name && scoped {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("variable %s not found", name)
}

// containsStr checks if a string slice contains a specific string.
func containsStr(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

// Check if the version of lagoon provided in the internal_system scope variable is greater than or equal to the checked version
func CheckLagoonVersion(build *lagoonv1beta1.LagoonBuild, checkVersion string) bool {
	lagoonProjectVariables := &[]LagoonEnvironmentVariable{}
	json.Unmarshal(build.Spec.Project.Variables.Project, lagoonProjectVariables)
	lagoonVersion, err := GetLagoonVariable("LAGOON_SYSTEM_CORE_VERSION", []string{"internal_system"}, *lagoonProjectVariables)
	if err != nil {
		return false
	}
	aVer, err := version.NewSemver(lagoonVersion.Value)
	if err != nil {
		return false
	}
	bVer, err := version.NewSemver(checkVersion)
	if err != nil {
		return false
	}
	return aVer.GreaterThanOrEqual(bVer)
}

// CancelExtraBuilds cancels extra builds.
func CancelExtraBuilds(ctx context.Context, r client.Client, opLog logr.Logger, ns string, status string) error {
	pendingBuilds := &lagoonv1beta1.LagoonBuildList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": lagoonv1beta1.BuildStatusPending.String()}),
	})
	if err := r.List(ctx, pendingBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	if len(pendingBuilds.Items) > 0 {
		// opLog.Info(fmt.Sprintf("There are %v pending builds", len(pendingBuilds.Items)))
		// if we have any pending builds, then grab the latest one and make it running
		// if there are any other pending builds, cancel them so only the latest one runs
		sort.Slice(pendingBuilds.Items, func(i, j int) bool {
			return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.After(pendingBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
		})
		for idx, pBuild := range pendingBuilds.Items {
			pendingBuild := pBuild.DeepCopy()
			if idx == 0 {
				pendingBuild.Labels["lagoon.sh/buildStatus"] = status
			} else {
				// cancel any other pending builds
				opLog.Info(fmt.Sprintf("Setting build %s as cancelled", pendingBuild.ObjectMeta.Name))
				pendingBuild.Labels["lagoon.sh/buildStatus"] = lagoonv1beta1.BuildStatusCancelled.String()
				pendingBuild.Labels["lagoon.sh/cancelledByNewBuild"] = "true"
			}
			if err := r.Update(ctx, pendingBuild); err != nil {
				return err
			}
		}
	}
	return nil
}

func GetBuildConditionFromPod(phase corev1.PodPhase) lagoonv1beta1.BuildStatusType {
	var buildCondition lagoonv1beta1.BuildStatusType
	switch phase {
	case corev1.PodFailed:
		buildCondition = lagoonv1beta1.BuildStatusFailed
	case corev1.PodSucceeded:
		buildCondition = lagoonv1beta1.BuildStatusComplete
	case corev1.PodPending:
		buildCondition = lagoonv1beta1.BuildStatusPending
	case corev1.PodRunning:
		buildCondition = lagoonv1beta1.BuildStatusRunning
	}
	return buildCondition
}

func GetTaskConditionFromPod(phase corev1.PodPhase) lagoonv1beta1.TaskStatusType {
	var taskCondition lagoonv1beta1.TaskStatusType
	switch phase {
	case corev1.PodFailed:
		taskCondition = lagoonv1beta1.TaskStatusFailed
	case corev1.PodSucceeded:
		taskCondition = lagoonv1beta1.TaskStatusComplete
	case corev1.PodPending:
		taskCondition = lagoonv1beta1.TaskStatusPending
	case corev1.PodRunning:
		taskCondition = lagoonv1beta1.TaskStatusRunning
	}
	return taskCondition
}

// GenerateNamespaceName handles the generation of the namespace name from environment and project name with prefixes and patterns
func GenerateNamespaceName(pattern, environmentName, projectname, prefix, controllerNamespace string, randomPrefix bool) string {
	nsPattern := pattern
	if pattern == "" {
		nsPattern = DefaultNamespacePattern
	}
	environmentName = ShortenEnvironment(projectname, MakeSafe(environmentName))
	// lowercase and dnsify the namespace against the namespace pattern
	ns := MakeSafe(
		strings.Replace(
			strings.Replace(
				nsPattern,
				"${environment}",
				environmentName,
				-1,
			),
			"${project}",
			projectname,
			-1,
		),
	)
	// If there is a namespaceprefix defined, and random prefix is disabled
	// then add the prefix to the namespace
	if prefix != "" && randomPrefix == false {
		ns = fmt.Sprintf("%s-%s", prefix, ns)
	}
	// If the randomprefix is enabled, then add a prefix based on the hash of the controller namespace
	if randomPrefix {
		ns = fmt.Sprintf("%s-%s", HashString(controllerNamespace)[0:8], ns)
	}
	// Once the namespace is fully calculated, then truncate the generated namespace
	// to 63 characters to not exceed the kubernetes namespace limit
	if len(ns) > 63 {
		ns = fmt.Sprintf("%s-%s", ns[0:58], HashString(ns)[0:4])
	}
	return ns
}

// ShortenEnvironment shortens the environment name down the same way that Lagoon does
func ShortenEnvironment(project, environment string) string {
	overlength := 58 - len(project)
	if len(environment) > overlength {
		environment = fmt.Sprintf("%s-%s", environment[0:overlength-5], HashString(environment)[0:4])
	}
	return environment
}

// GetLagoonFeatureFlags pull all the environment variables and check if there are any with the flag prefix
func GetLagoonFeatureFlags() map[string]string {
	flags := make(map[string]string)
	for _, envVar := range os.Environ() {
		envVarSplit := strings.SplitN(envVar, "=", 2)
		// if the variable name contains the flag prefix, add it to the map
		if strings.Contains(envVarSplit[0], "LAGOON_FEATURE_FLAG_") {
			flags[envVarSplit[0]] = envVarSplit[1]
		}
	}
	return flags
}

// GetEnv get environment variable
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// GetEnvInt get environment variable as int
func GetEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		valueInt, e := strconv.Atoi(value)
		if e == nil {
			return valueInt
		}
	}
	return fallback
}

// GetEnvBool get environment variable as bool
// accepts fallback values 1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False
// anything else is false.
func GetEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		rVal, _ := strconv.ParseBool(value)
		return rVal
	}
	return fallback
}
