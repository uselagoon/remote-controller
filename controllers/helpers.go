package controllers

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelAppManaged for discovery.
	LabelAppManaged = "lagoon.amazee.io/managed-by"
	// DefaultNamespacePattern is what is used when one is not provided.
	DefaultNamespacePattern = "${project}-${environment}"
)

// ignoreNotFound will ignore not found errors
func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// containsString check if a slice contains a string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeString remove string from a sliced
func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func jobContainsStatus(slice []lagoonv1alpha1.LagoonConditions, s lagoonv1alpha1.LagoonConditions) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func intPtr(i int32) *int32 {
	var iPtr *int32
	iPtr = new(int32)
	*iPtr = i
	return iPtr
}

func int64Ptr(i int64) *int64 {
	var iPtr *int64
	iPtr = new(int64)
	*iPtr = i
	return iPtr
}

func uintPtr(i uint) *uint {
	var iPtr *uint
	iPtr = new(uint)
	*iPtr = i
	return iPtr
}

// make safe ensures that any string is dns safe
func makeSafe(in string) string {
	out := regexp.MustCompile(`[^0-9a-z-]`).ReplaceAllString(
		strings.ToLower(in),
		"$1-$2",
	)
	return out
}

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

func randString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

// get the hash of a given string.
func hashString(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	bs := h.Sum(nil)
	return fmt.Sprintf("%x", bs)
}

// removeBuild remove a LagoonBuild from a slice of LagoonBuilds
func removeBuild(slice []lagoonv1alpha1.LagoonBuild, s lagoonv1alpha1.LagoonBuild) []lagoonv1alpha1.LagoonBuild {
	result := []lagoonv1alpha1.LagoonBuild{}
	for _, item := range slice {
		if item.ObjectMeta.Name == s.ObjectMeta.Name {
			continue
		}
		result = append(result, item)
	}
	return result
}

var lowerAlNum = regexp.MustCompile("[^a-z0-9]+")

// shortName returns a deterministic random short name of 8 lowercase
// alphabetic and numeric characters. The short name is based
// on hashing and encoding the given name.
func shortName(name string) string {
	hash := sha256.Sum256([]byte(name))
	return lowerAlNum.ReplaceAllString(strings.ToLower(base32.StdEncoding.EncodeToString(hash[:])), "")[:8]
}

func stringToUintPtr(s string) *uint {
	// get the environment id and turn it into a *uint to send back to lagoon for logging
	// lagoon sends this as a string :(
	u64, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return nil
	}
	return uintPtr(uint(u64))
}

// replaceOrAddVariable will replace or add an environment variable to a slice of environment variables
func replaceOrAddVariable(vars *[]LagoonEnvironmentVariable, name, value, scope string) {
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

// variableExists checks if a variable exists in a slice of environment variables
func variableExists(vars *[]LagoonEnvironmentVariable, name, value string) bool {
	exists := false
	for _, v := range *vars {
		if v.Name == name && v.Value == value {
			exists = true
		}
	}
	return exists
}

func cancelExtraBuilds(ctx context.Context, r client.Client, opLog logr.Logger, pendingBuilds *lagoonv1alpha1.LagoonBuildList, ns string, status string) error {
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(ns),
		client.MatchingLabels(map[string]string{"lagoon.sh/buildStatus": "Pending"}),
	})
	if err := r.List(ctx, pendingBuilds, listOption); err != nil {
		return fmt.Errorf("Unable to list builds in the namespace, there may be none or something went wrong: %v", err)
	}
	opLog.Info(fmt.Sprintf("There are %v Pending builds", len(pendingBuilds.Items)))
	// if we have any pending builds, then grab the latest one and make it running
	// if there are any other pending builds, cancel them so only the latest one runs
	sort.Slice(pendingBuilds.Items, func(i, j int) bool {
		return pendingBuilds.Items[i].ObjectMeta.CreationTimestamp.After(pendingBuilds.Items[j].ObjectMeta.CreationTimestamp.Time)
	})
	if len(pendingBuilds.Items) > 0 {
		for idx, pBuild := range pendingBuilds.Items {
			pendingBuild := pBuild.DeepCopy()
			if idx == 0 {
				pendingBuild.Labels["lagoon.sh/buildStatus"] = status
			} else {
				// cancel any other pending builds
				pendingBuild.Labels["lagoon.sh/buildStatus"] = "Cancelled"
			}
			if err := r.Update(ctx, pendingBuild); err != nil {
				return err
			}
		}
	}
	return nil
}
