package dockerhost

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var lagoonDockerHostGVK = schema.GroupVersionKind{
	Group:   "crd.lagoon.sh",
	Version: "v1alpha1",
	Kind:    "LagoonDockerHost",
}

// desiredReplicas works out how many docker-hosts should run. Every full multiplier*qosMax of queued builds adds one
// replica above the minimum. With nothing queued and nothing running the pool returns to the minimum, otherwise
// the current size is held so that hosts with running builds are not removed.
func desiredReplicas(current, minReplicas int64, maxReplicas *int64, qosMax, multiplier, queued, running int) int64 {
	desired := current
	switch {
	case queued == 0 && running == 0:
		desired = minReplicas
	case qosMax > 0 && multiplier > 0:
		target := minReplicas + int64(queued/(multiplier*qosMax))
		if target > desired {
			desired = target
		}
	}
	if maxReplicas != nil && desired > *maxReplicas {
		desired = *maxReplicas
	}
	if desired < minReplicas {
		desired = minReplicas
	}
	return desired
}

// EffectiveQoS scales a qos limit, which is defined for a pool running minReplicas docker-hosts, by the number of
// ready docker-hosts. Without the LagoonDockerHost resource the limit is returned unchanged.
func (d *DockerHost) EffectiveQoS(limit int) int {
	return scaleLimit(limit, d.readyHosts.Load(), d.minHosts.Load())
}

func scaleLimit(limit int, ready, minReplicas int64) int {
	if minReplicas < 1 {
		minReplicas = 1
	}
	if ready <= minReplicas {
		return limit
	}
	return int(int64(limit) * ready / minReplicas)
}

// ScaleForQueue scales the named LagoonDockerHost in the docker-host namespace based on the build queue.
// qosMax is the unscaled qos limit. It also records the ready capacity used by EffectiveQoS.
func (d *DockerHost) ScaleForQueue(ctx context.Context, qosMax, queued, running int) {
	if d.ScaleName == "" {
		return
	}
	item := &unstructured.Unstructured{}
	item.SetGroupVersionKind(lagoonDockerHostGVK)
	if err := d.Client.Get(ctx, types.NamespacedName{Namespace: d.DockerHostNamespace, Name: d.ScaleName}, item); err != nil {
		if !meta.IsNoMatchError(err) && !apierrors.IsNotFound(err) {
			d.Log.Error(err, "unable to get lagoondockerhost for scaling", "name", d.ScaleName)
		}
		d.readyHosts.Store(0)
		d.minHosts.Store(0)
		return
	}
	minReplicas, found, _ := unstructured.NestedInt64(item.Object, "spec", "minReplicas")
	if !found {
		minReplicas = 1
	}
	var maxReplicas *int64
	if v, found, _ := unstructured.NestedInt64(item.Object, "spec", "maxReplicas"); found {
		maxReplicas = &v
	}
	current, found, _ := unstructured.NestedInt64(item.Object, "spec", "replicas")
	if !found {
		current = minReplicas
	}
	ready, _, _ := unstructured.NestedInt64(item.Object, "status", "readyReplicas")
	d.readyHosts.Store(ready)
	d.minHosts.Store(minReplicas)
	desired := desiredReplicas(current, minReplicas, maxReplicas, qosMax, d.ScaleMultiplier, queued, running)
	if desired == current {
		return
	}
	patch := client.RawPatch(
		"application/merge-patch+json",
		[]byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, desired)),
	)
	if err := d.Client.Patch(ctx, item, patch); err != nil {
		d.Log.Error(err, "unable to scale lagoondockerhost", "name", d.ScaleName)
		return
	}
	d.Log.Info(fmt.Sprintf("Scaled lagoondockerhost %s from %d to %d replicas (%d queued, %d running)",
		d.ScaleName, current, desired, queued, running))
}
