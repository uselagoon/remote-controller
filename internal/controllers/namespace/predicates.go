package namespace

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// NamespacePredicates defines the funcs for predicates
type NamespacePredicates struct {
	predicate.Funcs
}

// Create is used when a creation event is received by the controller.
func (n NamespacePredicates) Create(e event.CreateEvent) bool {
	return false
}

// Delete is used when a deletion event is received by the controller.
func (n NamespacePredicates) Delete(e event.DeleteEvent) bool {
	return false
}

// Update is used when an update event is received by the controller.
func (n NamespacePredicates) Update(e event.UpdateEvent) bool {
	if oldIdled, ok := e.ObjectOld.GetLabels()["idling.amazee.io/idled"]; ok {
		if newIdled, ok := e.ObjectNew.GetLabels()["idling.amazee.io/idled"]; ok {
			if oldIdled != newIdled {
				return true
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (n NamespacePredicates) Generic(e event.GenericEvent) bool {
	return false
}
