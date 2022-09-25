package pruner

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const lagoonNSExpirationLabel = "lagoon.sh/expiration"
const lagoonNSExpirationPausedLabel = "lagoon.sh/expiration-paused"

// RunNSDeletionLoop returns a function that looks specifically for secrets of the form
// advanced-task-toolbox-migration-done with a label of the same name. We will then delete
// these namespaces if the secret is older than a week.

func (h *Pruner) NamespacePruner() {
	opLog := ctrl.Log.WithName("utilities").WithName("NamespacePruner")
	nsList := &corev1.NamespaceList{}

	markedForDeletionRequirement, err := labels.NewRequirement(lagoonNSExpirationLabel, selection.Exists, []string{})
	markedForDeletionPausedRequirement, err := labels.NewRequirement(lagoonNSExpirationPausedLabel, selection.DoesNotExist, []string{})

	if err != nil {
		opLog.Info(fmt.Sprintf("bad requirement: %v", err))
		return
	}
	migrationCompleteSelector := labels.NewSelector().Add(*markedForDeletionRequirement).Add(*markedForDeletionPausedRequirement)
	migrationCompleteSecretOptionSearch := client.ListOptions{
		LabelSelector: migrationCompleteSelector,
	}

	err = h.Client.List(context.Background(), nsList, &migrationCompleteSecretOptionSearch)

	for _, x := range nsList.Items {
		ns := &corev1.Namespace{}
		//client
		err = h.Client.Get(context.Background(), client.ObjectKey{Name: x.Name, Namespace: x.Namespace}, ns)
		if err != nil {
			opLog.Error(err, "Not able to load namespace "+x.Name)
			continue
		}

		if val, ok := ns.Labels[lagoonNSExpirationLabel]; ok {
			i, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				opLog.Error(err, "Unable to convert "+val+" from a unix timestamp - pausing deletion of namespace for manual intervention")
				ierr := h.labelNamespace(context.Background(), ns, lagoonNSExpirationPausedLabel, "true")
				if ierr != nil {
					opLog.Error(ierr, "Unable to annotate namespace "+ns.Name+" with "+lagoonNSExpirationPausedLabel)
				}
				continue //on to the next NS
			}
			expiryDate := time.Unix(i, 0)
			if expiryDate.Before(time.Now()) {
				opLog.Info(fmt.Sprintf("Preparing to delete namespace: %v", x.Name))
				err = h.Client.Delete(context.Background(), ns)
				if err != nil {
					opLog.Error(err, "Unable to delete namespace: "+ns.Name+" pausing deletion for manual intervention")
					ierr := h.labelNamespace(context.Background(), ns, lagoonNSExpirationPausedLabel, "true")
					if ierr != nil {
						opLog.Error(ierr, "Unable to annotate namespace "+ns.Name+" with "+lagoonNSExpirationPausedLabel)
					}
					continue
				}
				opLog.Info("Deleted namespace: " + ns.Name)

			} else {
				opLog.Info(fmt.Sprintf("namespace %v is expiring later, so we skip", x.Name))
				opLog.Info("Annotating namespace with future deletion details")

				annotations := ns.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations[lagoonNSExpirationLabel] = expiryDate.Format(time.RFC822)
				ns.SetAnnotations(annotations)
				err = h.Client.Update(context.Background(), ns)
				if err != nil {
					opLog.Error(err, "unable to update namespace: "+ns.Name)
					continue
				}
			}
		}
	}
}

func (h *Pruner) labelNamespace(ctx context.Context, ns *corev1.Namespace, labelKey string, labelValue string) error {
	labels := ns.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[labelKey] = labelValue
	ns.SetLabels(labels)

	if err := h.Client.Update(ctx, ns); err != nil {
		return err
	}
	return nil
}
