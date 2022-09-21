package namespace_utilities

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	client2 "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strconv"
	"time"
)

const lagoonNSExpirationLabel = "lagoon.sh/expiration"
const lagoonNSExpirationPausedLabel = "lagoon.sh/expiration-paused"

// startSecretNSCronjob spins up a cron that looks specifically for secrets of the form
// advanced-task-toolbox-migration-done with a label of the same name. We will then delete
// these namespaces if the secret is older than a week.

//func StartNSLabelCronjob(mgr manager.Manager) {
//	c := cron.New()
//	fmt.Println("Spinning up secret searcher")
//	c.AddFunc("* * * * *", runNSDeletionLoop(mgr))
//	c.Start()
//}

func RunNSDeletionLoop(mgr manager.Manager) func() {
	log := log.FromContext(context.Background())
	return func() {
		log.Info("NS searcher running ...")
		client := mgr.GetClient()

		nsList := &v1.NamespaceList{}

		markedForDeletionRequirement, err := labels.NewRequirement(lagoonNSExpirationLabel, selection.Exists, []string{})
		markedForDeletionPausedRequirement, err := labels.NewRequirement(lagoonNSExpirationPausedLabel, selection.DoesNotExist, []string{})

		if err != nil {
			log.Info(fmt.Sprintf("bad requirement: %v", err))
			return
		}
		migrationCompleteSelector := labels.NewSelector().Add(*markedForDeletionRequirement).Add(*markedForDeletionPausedRequirement)
		migrationCompleteSecretOptionSearch := client2.ListOptions{
			LabelSelector: migrationCompleteSelector,
		}

		err = client.List(context.Background(), nsList, &migrationCompleteSecretOptionSearch)

		for _, x := range nsList.Items {
			ns := &v1.Namespace{}
			//client
			err = client.Get(context.Background(), client2.ObjectKey{Name: x.Name, Namespace: x.Namespace}, ns)
			if err != nil {
				log.Error(err, "Not able to load namespace "+x.Name)
				continue
			}

			if val, ok := ns.Labels[lagoonNSExpirationLabel]; ok {
				i, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					log.Error(err, "Unable to convert "+val+" from a unix timestamp - pausing deletion of namespace for manual intervention")
					ierr := LabelNS(context.Background(), client, ns, lagoonNSExpirationPausedLabel, "true")
					if ierr != nil {
						log.Error(ierr, "Unable to annotate ns "+ns.Name+" with "+lagoonNSExpirationPausedLabel)
					}
					continue //on to the next NS
				}
				expiryDate := time.Unix(i, 0)
				if expiryDate.Before(time.Now()) {
					log.Info(fmt.Sprintf("Preparing to delete namespace: %v", x.Name))
					err = client.Delete(context.Background(), ns)
					if err != nil {
						log.Error(err, "Unable to delete ns: "+ns.Name+" pausing deletion for manual intervention")
						ierr := LabelNS(context.Background(), client, ns, lagoonNSExpirationPausedLabel, "true")
						if ierr != nil {
							log.Error(ierr, "Unable to annotate ns "+ns.Name+" with "+lagoonNSExpirationPausedLabel)
						}
						continue
					}
					log.Info("Deleted ns: " + ns.Name)

				} else {
					log.Info(fmt.Sprintf("ns %v is expiring later, so we skip", x.Name))
					log.Info("Annotating NS with future deletion details")

					//ns.Annotations[lagoonNSExpirationLabel] = expiryDate.Format(time.RFC822)
					annotations := ns.GetAnnotations()
					if annotations == nil {
						annotations = map[string]string{}
					}
					annotations[lagoonNSExpirationLabel] = expiryDate.Format(time.RFC822)
					ns.SetAnnotations(annotations)
					err = client.Update(context.Background(), ns)
					if err != nil {
						log.Error(err, "unable to update ns: "+ns.Name)
						continue
					}
				}
			}
		}
	}
}

func LabelNS(ctx context.Context, r client2.Client, ns *v1.Namespace, labelKey string, labelValue string) error {
	log := log.FromContext(ctx)
	labels := ns.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[labelKey] = labelValue
	ns.SetLabels(labels)

	if err := r.Update(ctx, ns); err != nil {
		log.Error(err, "Unable to update namespace - setting labels")
		return err
	}
	return nil
}
