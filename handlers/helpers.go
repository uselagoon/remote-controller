package handlers

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	// LabelAppManaged for discovery.
	LabelAppManaged = "lagoon.amazee.io/managed-by"
	// DefaultNamespacePattern is what is used when one is not provided.
	DefaultNamespacePattern = "${project}-${environment}"
)

var (
	// RunningPendingStatus .
	RunningPendingStatus = []string{
		string(lagoonv1alpha1.BuildStatusPending),
		string(lagoonv1alpha1.BuildStatusRunning),
	}
	// CompletedCancelledFailedStatus .
	CompletedCancelledFailedStatus = []string{
		string(lagoonv1alpha1.BuildStatusFailed),
		string(lagoonv1alpha1.BuildStatusComplete),
		string(lagoonv1alpha1.BuildStatusCancelled),
	}
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
