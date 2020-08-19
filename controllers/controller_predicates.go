package controllers

// contains all the event watch conditions for secret and ingresses

import (
	"regexp"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PodPredicates .
type PodPredicates struct {
	predicate.Funcs
}

// Create .
func (PodPredicates) Create(e event.CreateEvent) bool {
	if value, ok := e.Meta.GetLabels()["lagoon.sh/buildName"]; ok {
		match, _ := regexp.MatchString("^lagoon-build", value)
		return match
	}
	if value, ok := e.Meta.GetLabels()["lagoon.sh/jobType"]; ok {
		if value == "task" {
			return true
		}
	}
	return false
}

// Delete .
func (PodPredicates) Delete(e event.DeleteEvent) bool {
	if value, ok := e.Meta.GetLabels()["lagoon.sh/buildName"]; ok {
		match, _ := regexp.MatchString("^lagoon-build", value)
		return match
	}
	if value, ok := e.Meta.GetLabels()["lagoon.sh/jobType"]; ok {
		if value == "task" {
			return true
		}
	}
	return false
}

// Update .
func (PodPredicates) Update(e event.UpdateEvent) bool {
	if _, okOld := e.MetaOld.GetLabels()["lagoon.sh/buildName"]; okOld {
		if value, ok := e.MetaNew.GetLabels()["lagoon.sh/buildName"]; ok {
			match, _ := regexp.MatchString("^lagoon-build", value)
			return match
		}
	}
	if _, ok := e.MetaOld.GetLabels()["lagoon.sh/jobType"]; ok {
		if value, ok := e.MetaNew.GetLabels()["lagoon.sh/jobType"]; ok {
			if value == "task" {
				return true
			}
		}
	}
	return false
}

// Generic .
func (PodPredicates) Generic(e event.GenericEvent) bool {
	if value, ok := e.Meta.GetLabels()["lagoon.sh/buildName"]; ok {
		match, _ := regexp.MatchString("^lagoon-build", value)
		return match
	}
	if value, ok := e.Meta.GetLabels()["lagoon.sh/jobType"]; ok {
		if value == "task" {
			return true
		}
	}
	return false
}
