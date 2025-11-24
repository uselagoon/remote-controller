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

package deployments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"github.com/uselagoon/remote-controller/internal/messenger"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// DeploymentsReconciler reconciles idling
type DeploymentsReconciler struct {
	client.Client
	Log              logr.Logger
	Scheme           *runtime.Scheme
	EnableMQ         bool
	Messaging        *messenger.Messenger
	LagoonTargetName string
}

type ServiceState struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Replicas int32  `json:"replicas"`
}

func (r *DeploymentsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("deployment", req.NamespacedName)

	var deployment appsv1.Deployment
	if err := r.Get(ctx, req.NamespacedName, &deployment); err != nil {
		return ctrl.Result{}, ignoreNotFound(err)
	}
	opLog.Info(fmt.Sprintf("deployment %s", deployment.Name))
	opLog.Info(fmt.Sprintf(`{"replicas":%d}`, *deployment.Spec.Replicas))
	// this would be nice to be a lagoon label :)
	if val, ok := deployment.Labels["idling.amazee.io/idled"]; ok {
		var namespace corev1.Namespace
		if err := r.Get(ctx, types.NamespacedName{
			Name: deployment.Namespace,
		}, &namespace); err != nil {
			return ctrl.Result{}, ignoreNotFound(err)
		}
		opLog.Info(fmt.Sprintf("deployment %s idle state %v", deployment.Name, val))
		if r.EnableMQ {
			environmentName := namespace.Labels["lagoon.sh/environment"]
			eID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/environmentId"])
			envID := helpers.UintPtr(uint(eID))
			projectName := namespace.Labels["lagoon.sh/project"]
			pID, _ := strconv.Atoi(namespace.Labels["lagoon.sh/projectId"])
			projectID := helpers.UintPtr(uint(pID))
			serviceName := deployment.Labels["lagoon.sh/service"]
			serviceType := deployment.Labels["lagoon.sh/service-type"]
			state := ServiceState{
				Name:     serviceName,
				Type:     serviceType,
				Replicas: *deployment.Spec.Replicas,
			}
			stateJSON, _ := json.Marshal(state)
			msg := schema.LagoonMessage{
				Type:      "servicestate",
				Namespace: namespace.Name,
				Meta: &schema.LagoonLogMeta{
					EnvironmentID: envID,
					ProjectID:     projectID,
					Environment:   environmentName,
					Project:       projectName,
					Cluster:       r.LagoonTargetName,
					AdvancedData:  base64.StdEncoding.EncodeToString(stateJSON),
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
func (r *DeploymentsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		WithEventFilter(DeploymentsPredicates{}).
		Complete(r)
}

// will ignore not found errors
func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
