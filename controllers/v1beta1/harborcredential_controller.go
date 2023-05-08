package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/uselagoon/remote-controller/internal/harbor"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

// HarborCredentialReconciler reconciles a namespace object
type HarborCredentialReconciler struct {
	client.Client
	Log                 logr.Logger
	Scheme              *runtime.Scheme
	LFFHarborEnabled    bool
	Harbor              harbor.Harbor
	ControllerNamespace string
}

// all the things
// +kubebuilder:rbac:groups=*,resources=*,verbs=*

func (r *HarborCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	opLog := r.Log.WithValues("harbor", req.NamespacedName)

	var harborSecret corev1.Secret
	if err := r.Get(ctx, req.NamespacedName, &harborSecret); err != nil {
		return ctrl.Result{}, helpers.IgnoreNotFound(err)
	}

	// check if the credentials need to be force rotated
	value, ok := harborSecret.ObjectMeta.Labels["harbor.lagoon.sh/force-rotate"]
	if ok && value == "true" {
		opLog.Info(fmt.Sprintf("Rotating harbor credentials for namespace %s", harborSecret.ObjectMeta.Namespace))
		lagoonHarbor, err := harbor.New(r.Harbor)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("Error creating harbor client, check your harbor configuration. Error was: %v", err)
		}
		var ns corev1.Namespace
		if err := r.Get(ctx, types.NamespacedName{Name: harborSecret.ObjectMeta.Namespace}, &ns); err != nil {
			return ctrl.Result{}, helpers.IgnoreNotFound(err)
		}
		rotated, err := lagoonHarbor.RotateRobotCredential(ctx, r.Client, ns, true)
		if err != nil {
			// @TODO: resource unknown
			return ctrl.Result{}, fmt.Errorf("Error was: %v", err)
		}
		if rotated {
			opLog.Info(fmt.Sprintf("Robot credentials rotated for %s", ns.ObjectMeta.Name))
		}
		mergePatch, _ := json.Marshal(map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"harbor.lagoon.sh/force-rotate": nil,
				},
			},
		})
		if err := r.Patch(ctx, &harborSecret, client.RawPatch(types.MergePatchType, mergePatch)); err != nil {
			return ctrl.Result{}, fmt.Errorf("There was an error patching the harbor secret. Error was: %v", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the watch on the namespace resource with an event filter (see controller_predicates.go)
func (r *HarborCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WithEventFilter(SecretPredicates{
			ControllerNamespace: r.ControllerNamespace,
		}).
		Complete(r)
}
