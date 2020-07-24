package controllers

import (
	lagoonv1alpha1 "github.com/amazeeio/lagoon-kbd/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func buildContainsStatus(slice []lagoonv1alpha1.LagoonBuildConditions, s lagoonv1alpha1.LagoonBuildConditions) bool {
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
