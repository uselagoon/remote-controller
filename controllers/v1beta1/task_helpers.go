package v1beta1

import (
	"context"
	"fmt"

	"github.com/uselagoon/remote-controller/internal/helpers"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
)

// deleteActiveStandbyRole
func (r *LagoonTaskReconciler) deleteActiveStandbyRole(ctx context.Context, sourceNamespace string) error {
	activeStandbyRoleBinding := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      "lagoon-deployer-activestandby",
	}, activeStandbyRoleBinding)
	if err != nil {
		helpers.IgnoreNotFound(err)
	}
	err = r.Delete(ctx, activeStandbyRoleBinding)
	if err != nil {
		return fmt.Errorf("Unable to delete lagoon-deployer-activestandby role binding")
	}
	return nil
}
