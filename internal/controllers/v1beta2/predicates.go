package v1beta2

// contains all the event watch conditions for secret and ingresses

import (
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/uselagoon/remote-controller/internal/metrics"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BuildPodPredicates is used by the filter for the monitor controller to make sure the correct resources
// are acted on.
type BuildPodPredicates struct {
	predicate.Funcs
	ControllerNamespace string
}

// Create is used when a creation event is received by the controller.
func (p BuildPodPredicates) Create(e event.CreateEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/buildName"]; ok {
						match, _ := regexp.MatchString("^lagoon-build", value)
						return match
					}
				}
			}
		}
	}
	return false
}

// Delete is used when a deletion event is received by the controller.
func (p BuildPodPredicates) Delete(e event.DeleteEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/buildName"]; ok {
						match, _ := regexp.MatchString("^lagoon-build", value)
						return match
					}
				}
			}
		}
	}
	return false
}

// Update is used when an update event is received by the controller.
func (p BuildPodPredicates) Update(e event.UpdateEvent) bool {
	if controller, ok := e.ObjectOld.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.ObjectNew.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if _, okOld := e.ObjectOld.GetLabels()["lagoon.sh/buildName"]; okOld {
						if value, ok := e.ObjectNew.GetLabels()["lagoon.sh/buildName"]; ok {
							oldBuildStep := "running"
							newBuildStep := "running"
							if value, ok := e.ObjectNew.GetLabels()["lagoon.sh/buildStep"]; ok {
								newBuildStep = value
							}
							if value, ok := e.ObjectOld.GetLabels()["lagoon.sh/buildStep"]; ok {
								oldBuildStep = value
							}
							if newBuildStep != oldBuildStep {
								metrics.BuildStatus.With(prometheus.Labels{
									"build_namespace": e.ObjectOld.GetNamespace(),
									"build_name":      e.ObjectOld.GetName(),
									"build_step":      newBuildStep,
								}).Set(1)
							}
							time.AfterFunc(31*time.Second, func() {
								metrics.BuildStatus.Delete(prometheus.Labels{
									"build_namespace": e.ObjectOld.GetNamespace(),
									"build_name":      e.ObjectOld.GetName(),
									"build_step":      oldBuildStep,
								})
							})
							match, _ := regexp.MatchString("^lagoon-build", value)
							return match
						}
					}
				}
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (p BuildPodPredicates) Generic(e event.GenericEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/buildName"]; ok {
						match, _ := regexp.MatchString("^lagoon-build", value)
						return match
					}
				}
			}
		}
	}
	return false
}

// TaskPodPredicates is used by the filter for the monitor controller to make sure the correct resources
// are acted on.
type TaskPodPredicates struct {
	predicate.Funcs
	ControllerNamespace string
}

// Create is used when a creation event is received by the controller.
func (p TaskPodPredicates) Create(e event.CreateEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/jobType"]; ok {
						if value == "task" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// Delete is used when a deletion event is received by the controller.
func (p TaskPodPredicates) Delete(e event.DeleteEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/jobType"]; ok {
						if value == "task" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// Update is used when an update event is received by the controller.
func (p TaskPodPredicates) Update(e event.UpdateEvent) bool {
	if controller, ok := e.ObjectOld.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.ObjectNew.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if _, ok := e.ObjectOld.GetLabels()["lagoon.sh/jobType"]; ok {
						if value, ok := e.ObjectNew.GetLabels()["lagoon.sh/jobType"]; ok {
							if value == "task" {
								return true
							}
						}
					}
				}
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (p TaskPodPredicates) Generic(e event.GenericEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == p.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					if value, ok := e.Object.GetLabels()["lagoon.sh/jobType"]; ok {
						if value == "task" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// BuildPredicates is used by the filter for the build controller to make sure the correct resources
// are acted on.
type BuildPredicates struct {
	predicate.Funcs
	ControllerNamespace string
}

// Create is used when a creation event is received by the controller.
func (b BuildPredicates) Create(e event.CreateEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == b.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Delete is used when a deletion event is received by the controller.
func (b BuildPredicates) Delete(e event.DeleteEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == b.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Update is used when an update event is received by the controller.
func (b BuildPredicates) Update(e event.UpdateEvent) bool {
	if controller, ok := e.ObjectOld.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == b.ControllerNamespace {
			if value, ok := e.ObjectNew.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (b BuildPredicates) Generic(e event.GenericEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == b.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// TaskPredicates is used by the filter for the task controller to make sure the correct resources
// are acted on.
type TaskPredicates struct {
	predicate.Funcs
	ControllerNamespace string
}

// Create is used when a creation event is received by the controller.
func (t TaskPredicates) Create(e event.CreateEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == t.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Delete is used when a deletion event is received by the controller.
func (t TaskPredicates) Delete(e event.DeleteEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == t.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Update is used when an update event is received by the controller.
func (t TaskPredicates) Update(e event.UpdateEvent) bool {
	if controller, ok := e.ObjectOld.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == t.ControllerNamespace {
			if value, ok := e.ObjectNew.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}

// Generic is used when any other event is received by the controller.
func (t TaskPredicates) Generic(e event.GenericEvent) bool {
	if controller, ok := e.Object.GetLabels()["lagoon.sh/controller"]; ok {
		if controller == t.ControllerNamespace {
			if value, ok := e.Object.GetLabels()["crd.lagoon.sh/version"]; ok {
				if value == crdVersion {
					return true
				}
			}
		}
	}
	return false
}
