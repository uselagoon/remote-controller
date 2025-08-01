package helpers

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// IntPtr .
func IntPtr(i int) *int {
	iPtr := new(int)
	*iPtr = i
	return iPtr
}

// Int32Ptr .
func Int32Ptr(i int32) *int32 {
	iPtr := new(int32)
	*iPtr = i
	return iPtr
}

// Int64Ptr .
func Int64Ptr(i int64) *int64 {
	iPtr := new(int64)
	*iPtr = i
	return iPtr
}

// UintPtr .
func UintPtr(i uint) *uint {
	iPtr := new(uint)
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

// GenerateNamespaceName handles the generation of the namespace name from environment and project name with prefixes and patterns
func GenerateNamespaceName(pattern, environmentName, projectname, prefix, controllerNamespace string, randomPrefix bool) string {
	nsPattern := pattern
	if pattern == "" {
		nsPattern = DefaultNamespacePattern
	}
	environmentName = ShortenEnvironment(projectname, MakeSafe(environmentName))
	// lowercase and dnsify the namespace against the namespace pattern
	ns := MakeSafe(
		strings.ReplaceAll(
			strings.ReplaceAll(
				nsPattern,
				"${environment}",
				environmentName,
			),
			"${project}",
			projectname,
		),
	)
	// If there is a namespaceprefix defined, and random prefix is disabled
	// then add the prefix to the namespace
	if prefix != "" && !randomPrefix {
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

// this converts the buildstep to a status condition reason where possible
// otherwise it will just return an unknown condition
func BuildStepToStatusConditions(s, c string, t time.Time) (metav1.Condition, metav1.Condition) {
	reason := cases.Title(language.English, cases.NoLower).String(s)
	reason = strings.ReplaceAll(reason, "-", "_")
	condition1 := metav1.Condition{
		Type:               "BuildStep",
		Reason:             reason,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(t),
	}
	errs := metav1validation.ValidateCondition(condition1, field.NewPath("reason"))
	if len(errs) > 0 {
		// if the build step can't be converted, just return unknown
		condition1.Reason = "Unknown"
	}
	condition2 := metav1.Condition{
		Type:               reason,
		Reason:             c,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(t),
	}
	errs = metav1validation.ValidateCondition(condition2, field.NewPath("type"))
	if len(errs) > 0 {
		// if the build step can't be converted, just return unknown
		condition2.Type = "Unknown"
	}
	return condition1, condition2
}

// this converts the taskstep to a status condition reason where possible
// otherwise it will just return an unknown condition
func TaskStepToStatusCondition(c string, t time.Time) metav1.Condition {
	return metav1.Condition{
		Type:               "TaskCondition",
		Reason:             c,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(t),
	}
}

// there is currently no reason to check the `lagoon-platform-env` secret, just the lagoon-env configmap or lagoon-env secret
// as it contains values that are provided back to the API at the completion of a build/buildstep
func GetLagoonEnvRoutes(ctx context.Context, opLog logr.Logger, c client.Client, namespace string) (string, []string, error) {
	var route string
	var routes []string
	lagoonEnvSecret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      "lagoon-env",
	}, lagoonEnvSecret)
	if err != nil {
		// fall back to check if the lagoon-env configmap exists
		lagoonEnvConfigMap := &corev1.ConfigMap{}
		err := c.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      "lagoon-env",
		}, lagoonEnvConfigMap)
		if err != nil {
			return "", nil, fmt.Errorf("no lagoon-env secret or configmap in namespace %s", namespace)
		} else {
			if r, ok := lagoonEnvConfigMap.Data["LAGOON_ROUTE"]; ok {
				route = r
			}
			if rs, ok := lagoonEnvConfigMap.Data["LAGOON_ROUTES"]; ok {
				routes = strings.Split(rs, ",")
			}
			return route, routes, nil
		}
	}
	if r, ok := lagoonEnvSecret.Data["LAGOON_ROUTE"]; ok {
		route = string(r)
	}
	if rs, ok := lagoonEnvSecret.Data["LAGOON_ROUTES"]; ok {
		routes = strings.Split(string(rs), ",")
	}
	return route, routes, nil
}
