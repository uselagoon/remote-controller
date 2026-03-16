package deployments

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// DeploymentsPredicates defines the funcs for predicates
type DeploymentsPredicates struct {
	predicate.Funcs
}

// Create is used when a creation event is received by the controller.
func (n DeploymentsPredicates) Create(e event.CreateEvent) bool {
	return false
}

// Delete is used when a deletion event is received by the controller.
func (n DeploymentsPredicates) Delete(e event.DeleteEvent) bool {
	return false
}

// Update is used when an update event is received by the controller.
func (n DeploymentsPredicates) Update(e event.UpdateEvent) bool {
	if _, ok := e.ObjectOld.GetLabels()["lagoon.sh/service"]; ok {
		// any time a deployment is modified
		return true
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (n DeploymentsPredicates) Generic(e event.GenericEvent) bool {
	return false
}
