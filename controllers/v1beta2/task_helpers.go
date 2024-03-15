package v1beta2

import (
	"context"
	"fmt"

	"github.com/uselagoon/remote-controller/internal/helpers"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// createActiveStandbyRole will create the rolebinding for allowing lagoon-deployer to talk between namespaces for active/standby functionality
func (r *LagoonTaskReconciler) createActiveStandbyRole(ctx context.Context, sourceNamespace, destinationNamespace string) error {
	activeStandbyRoleBinding := &rbacv1.RoleBinding{}
	activeStandbyRoleBinding.ObjectMeta = metav1.ObjectMeta{
		Name:      "lagoon-deployer-activestandby",
		Namespace: destinationNamespace,
	}
	activeStandbyRoleBinding.RoleRef = rbacv1.RoleRef{
		Name:     "admin",
		Kind:     "ClusterRole",
		APIGroup: "rbac.authorization.k8s.io",
	}
	activeStandbyRoleBinding.Subjects = []rbacv1.Subject{
		{
			Name:      "lagoon-deployer",
			Kind:      "ServiceAccount",
			Namespace: sourceNamespace,
		},
	}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: destinationNamespace,
		Name:      "lagoon-deployer-activestandby",
	}, activeStandbyRoleBinding)
	if err != nil {
		if err := r.Create(ctx, activeStandbyRoleBinding); err != nil {
			return fmt.Errorf("there was an error creating the lagoon-deployer-activestandby role binding. Error was: %v", err)
		}
	}
	return nil
}

// deleteActiveStandbyRole
func (r *LagoonMonitorReconciler) deleteActiveStandbyRole(ctx context.Context, destinationNamespace string) error {
	activeStandbyRoleBinding := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: destinationNamespace,
		Name:      "lagoon-deployer-activestandby",
	}, activeStandbyRoleBinding)
	if err != nil {
		helpers.IgnoreNotFound(err)
	}
	err = r.Delete(ctx, activeStandbyRoleBinding)
	if err != nil {
		return fmt.Errorf("unable to delete lagoon-deployer-activestandby role binding")
	}
	return nil
}
