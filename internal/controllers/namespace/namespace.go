/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package namespace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	"github.com/uselagoon/remote-controller/internal/messenger"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// NamespaceReconciler reconciles idling
type NamespaceReconciler struct {
	client.Client
	Log              logr.Logger
	Scheme           *runtime.Scheme
	EnableMQ         bool
	Messaging        *messenger.Messenger
	LagoonTargetName string
}

type Idled struct {
	Idled bool `json:"idled"`
}

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("namespace", req.NamespacedName)

	var namespace corev1.Namespace
	if err := r.Get(ctx, req.NamespacedName, &namespace); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}

	// this would be nice to be a lagoon label :)
	if val, ok := namespace.Labels["idling.amazee.io/idled"]; ok {
		idled, _ := strconv.ParseBool(val)
		opLog.Info(fmt.Sprintf("environment %s idle state %t", namespace.Name, idled))
		if r.EnableMQ {
			var projectName, environmentName string
			if p, ok := namespace.Labels["lagoon.sh/project"]; ok {
				projectName = p
			}
			if e, ok := namespace.Labels["lagoon.sh/environment"]; ok {
				environmentName = e
			}
			idling := Idled{
				Idled: idled,
			}
			idlingJSON, _ := json.Marshal(idling)
			msg := schema.LagoonMessage{
				Type:      "idling",
				Namespace: namespace.Name,
				Meta: &schema.LagoonLogMeta{
					Environment:  environmentName,
					Project:      projectName,
					Cluster:      r.LagoonTargetName,
					AdvancedData: base64.StdEncoding.EncodeToString(idlingJSON),
				},
			}
			msgBytes, err := json.Marshal(msg)
			if err != nil {
				opLog.Error(err, "Unable to encode message as JSON")
			}
			// @TODO: if we can't publish the message because for some reason, log the error and move on
			// this may result in the state being out of sync in lagoon but eventually will be consistent
			if err := r.Messaging.Publish("lagoon-tasks:controller", msgBytes); err != nil {
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the watch on the namespace resource with an event filter (see predicates.go)
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(NamespacePredicates{}).
		Complete(r)
}

// will ignore not found errors
func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
