package v1beta1

// contains all the event watch conditions for secret and ingresses

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// SecretPredicates defines the funcs for predicates
type SecretPredicates struct {
	predicate.Funcs
	ControllerNamespace string
}

// Create is used when a creation event is received by the controller.
func (n SecretPredicates) Create(e event.CreateEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == n.ControllerNamespace {
			if val, ok := e.Object.GetLabels()["lagoon.sh/harbor-credential"]; ok {
				if val == "true" {
					return true
				}
			}
		}
	}
	return false
}

// Delete is used when a deletion event is received by the controller.
func (n SecretPredicates) Delete(e event.DeleteEvent) bool {
	return false
}

// Update is used when an update event is received by the controller.
func (n SecretPredicates) Update(e event.UpdateEvent) bool {
	if controller, ok := e.ObjectOld.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == n.ControllerNamespace {
			if val, ok := e.ObjectOld.GetLabels()["lagoon.sh/harbor-credential"]; ok {
				if val == "true" {
					return true
				}
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (n SecretPredicates) Generic(e event.GenericEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == n.ControllerNamespace {
			if val, ok := e.Object.GetLabels()["lagoon.sh/harbor-credential"]; ok {
				if val == "true" {
					return true
				}
			}
		}
	}
	return false
}
