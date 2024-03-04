package pruner

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ExpirationLabel = "lagoon.sh/expiration"
const ExpirationPausedLabel = "lagoon.sh/expiration-paused"

// RunNSDeletionLoop returns a function that looks specifically for secrets of the form
// advanced-task-toolbox-migration-done with a label of the same name. We will then delete
// these namespaces if the secret is older than a week.

func (p *Pruner) NamespacePruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("NamespacePruner")
	ctx := context.Background()
	nsList := &corev1.NamespaceList{}

	markedForDeletionRequirement, err := labels.NewRequirement(ExpirationLabel, selection.Exists, []string{})
	if err != nil {
		opLog.Info(fmt.Sprintf("bad requirement: %v", err))
		return
	}
	markedForDeletionPausedRequirement, err := labels.NewRequirement(ExpirationPausedLabel, selection.DoesNotExist, []string{})
	if err != nil {
		opLog.Info(fmt.Sprintf("bad requirement: %v", err))
		return
	}
	migrationCompleteSelector := labels.NewSelector().Add(*markedForDeletionRequirement).Add(*markedForDeletionPausedRequirement)
	migrationCompleteSecretOptionSearch := client.ListOptions{
		LabelSelector: migrationCompleteSelector,
	}

	err = p.Client.List(ctx, nsList, &migrationCompleteSecretOptionSearch)
	if err != nil {
		opLog.Info(fmt.Sprintf("bad requirement: %v", err))
		return
	}
	for _, x := range nsList.Items {
		ns := &corev1.Namespace{}
		// client
		err = p.Client.Get(ctx, client.ObjectKey{Name: x.Name, Namespace: x.Namespace}, ns)
		if err != nil {
			opLog.Error(err, fmt.Sprintf("Not able to load namespace %s", x.Name))
			continue
		}

		delete := int64(0)

		var expireConfig corev1.ConfigMap
		err := p.Client.Get(ctx, client.ObjectKey{Name: "lagoon-namespace-expiry", Namespace: x.Namespace}, &expireConfig)
		if helpers.IgnoreNotFound(err) != nil {
			opLog.Error(err, fmt.Sprintf("unable to check namespace for lagoon-namespace-expiry: %s", ns.Name))
			continue
		} else {
			if val, ok := expireConfig.Data["timestamp"]; ok {
				i, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					opLog.Error(err, fmt.Sprintf("Unable to convert %v from a unix timestamp - pausing deletion of namespace for manual intervention", val))
					ierr := p.labelNamespace(ctx, ns, ExpirationPausedLabel, "true")
					if ierr != nil {
						opLog.Error(ierr, fmt.Sprintf("Unable to annotate namespace %s with %s", ns.Name, ExpirationPausedLabel))
					}
					continue //on to the next NS
				}
				delete = i
			}
		}

		// if the configmap doesn't exist, check the namespace labels
		if delete == 0 {
			if val, ok := ns.Labels[ExpirationLabel]; ok {
				i, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					opLog.Error(err, fmt.Sprintf("Unable to convert %v from a unix timestamp - pausing deletion of namespace for manual intervention", val))
					ierr := p.labelNamespace(ctx, ns, ExpirationPausedLabel, "true")
					if ierr != nil {
						opLog.Error(ierr, fmt.Sprintf("Unable to annotate namespace %s with %s", ns.Name, ExpirationPausedLabel))
					}
					continue //on to the next NS
				}
				delete = i
			}
		}

		if delete > 0 {
			expiryDate := time.Unix(delete, 0)
			if expiryDate.Before(time.Now()) {
				opLog.Info(fmt.Sprintf("Preparing to delete namespace: %v", x.Name))
				err := p.DeletionHandler.ProcessDeletion(ctx, opLog, ns)
				if err != nil {
					opLog.Error(err, fmt.Sprintf("Unable to delete namespace: %s pausing deletion for manual intervention", ns.Name))
					ierr := p.labelNamespace(ctx, ns, ExpirationPausedLabel, "true")
					if ierr != nil {
						opLog.Error(ierr, fmt.Sprintf("Unable to annotate namespace %s with %s", ns.Name, ExpirationPausedLabel))
					}
					continue
				}
				opLog.Info(fmt.Sprintf("Deleted namespace: %s", ns.Name))
			} else {
				opLog.Info(fmt.Sprintf("namespace %v is expiring later, so we skip", x.Name))
				opLog.Info("Annotating namespace with future deletion details")

				annotations := ns.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations[ExpirationLabel] = expiryDate.Format(time.RFC822)
				ns.SetAnnotations(annotations)
				err = p.Client.Update(ctx, ns)
				if err != nil {
					opLog.Error(err, fmt.Sprintf("unable to update namespace: %s", ns.Name))
					continue
				}
			}
		}
	}
}

func (p *Pruner) labelNamespace(ctx context.Context, ns *corev1.Namespace, labelKey string, labelValue string) error {
	labels := ns.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[labelKey] = labelValue
	ns.SetLabels(labels)

	if err := p.Client.Update(ctx, ns); err != nil {
		return err
	}
	return nil
}
