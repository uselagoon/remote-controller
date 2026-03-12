package messenger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/uselagoon/machinery/api/schema"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	k8upv1alpha1 "github.com/vshn/k8up/api/v1alpha1"
)

type cancelRestore struct {
	RestoreName string `json:"restoreName"`
	BackupID    string `json:"backupId"`
}

// ResticRestore handles creating the restic restore jobs.
func (m *Messenger) ResticRestore(ctx context.Context, namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec, v1alpha1, v1, cancel bool) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	vers, err := checkRestoreVersionFromCore(jobSpec.Misc.MiscResource)
	if err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				jobSpec.Misc.Name,
			),
		)
		// just log the error then return
		return nil
	}

	handlev1alpha1 := false
	handlev1 := false
	// check the version, if there is no version in the payload, assume it is k8up v2
	if m.SupportK8upV2 {
		if vers == "backup.appuio.ch/v1alpha1" {
			if v1alpha1 {
				handlev1alpha1 = true
			}
		} else {
			if v1 {
				handlev1 = true
			} else if v1alpha1 {
				handlev1alpha1 = true
			}
		}
	} else {
		if v1alpha1 {
			handlev1alpha1 = true
		}
	}

	if handlev1alpha1 {
		if cancel {
			return m.cancelv1alpha1Restore(ctx, opLog, namespace, jobSpec)
		} else {
			return m.createv1alpha1Restore(ctx, opLog, namespace, jobSpec)
		}
	}
	if handlev1 {
		if cancel {
			return m.cancelv1Restore(ctx, opLog, namespace, jobSpec)
		} else {
			return m.createv1Restore(ctx, opLog, namespace, jobSpec)
		}
	}
	return nil
}

// checkRestoreVersionFromCore checks the message payload from lagoon to see if the version is provided
// we do this as older versions of lagoon sent the entire payload to be created in the remote, newer versions of lagoon
// will only send the data. since restores haven't changed in k8up this works
func checkRestoreVersionFromCore(resource []byte) (string, error) {
	misc := make(map[string]interface{})
	if err := json.Unmarshal(resource, &misc); err != nil {
		return "", err
	}
	// check if a version exists
	if ok := misc["apiVersion"] == "backup.appuio.ch/v1alpha1"; ok {
		return "backup.appuio.ch/v1alpha1", nil
	}
	return "", nil
}

// createv1alpha1Restore will create a restore task using the restores.backup.appuio.ch v1alpha1 api (k8up v1)
func (m *Messenger) createv1alpha1Restore(ctx context.Context, opLog logr.Logger, namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	restorev1alpha1 := &k8upv1alpha1.Restore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, restorev1alpha1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				restorev1alpha1.Name,
			),
		)
		return err
	}
	restorev1alpha1.SetNamespace(namespace)
	if err := m.Client.Create(ctx, restorev1alpha1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create restore %s with k8up v1alpha1 api.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

// createv1Restore will create a restore task using the restores.k8up.io v1 api (k8up v2)
func (m *Messenger) createv1Restore(ctx context.Context, opLog logr.Logger, namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	restorev1 := &k8upv1.Restore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, restorev1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				restorev1.Name,
			),
		)
		return err
	}
	restorev1.SetNamespace(namespace)
	if err := m.Client.Create(ctx, restorev1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create restore %s with k8up v1 api.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}

// cancelv1alpha1Restore will attempt to cancel a restore task using the restores.backup.appuio.ch v1alpha1 api (k8up v1)
func (m *Messenger) cancelv1alpha1Restore(ctx context.Context, opLog logr.Logger, namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	restorev1alpha1 := &k8upv1alpha1.Restore{}
	cr := &cancelRestore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, &cr); err != nil {
		return err
	}
	if err := m.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cr.RestoreName}, restorev1alpha1); helpers.IgnoreNotFound(err) != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to get restore %s with k8up v1alpha1 api.",
				cr.RestoreName,
			),
		)
		return err
	}
	if restorev1alpha1.Name != "" {
		if err := m.Client.Delete(ctx, restorev1alpha1); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete restore %s with k8up v1alpha1 api.",
					cr.RestoreName,
				),
			)
			return err
		}
	}
	// if no matching restore found, or the restore is deleted, send the cancellation message back to core
	m.pubRestoreCancel(opLog, namespace, cr.RestoreName, jobSpec)
	return nil
}

// cancelv1Restore will attempt to cancel a restore task using the restores.k8up.io v1 api (k8up v2)
func (m *Messenger) cancelv1Restore(ctx context.Context, opLog logr.Logger, namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	restorev1 := &k8upv1.Restore{}
	cr := &cancelRestore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, &cr); err != nil {
		return err
	}
	if err := m.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cr.RestoreName}, restorev1); helpers.IgnoreNotFound(err) != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to get restore %s with k8up v1 api.",
				cr.RestoreName,
			),
		)
		return err
	}
	if restorev1.Name != "" {
		if err := m.Client.Delete(ctx, restorev1); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to delete restore %s with k8up v1alpha1 api.",
					cr.RestoreName,
				),
			)
			return err
		}
	}
	// if no matching restore found, or the restore is deleted, send the cancellation message back to core
	m.pubRestoreCancel(opLog, namespace, cr.RestoreName, jobSpec)
	return nil
}

func (m *Messenger) pubRestoreCancel(opLog logr.Logger, namespace, restorename string, jobSpec *lagoonv1beta2.LagoonTaskSpec) {
	msg := schema.LagoonMessage{
		Type:      "restore:cancel",
		Namespace: namespace,
		Meta: &schema.LagoonLogMeta{
			Environment: jobSpec.Environment.Name,
			Project:     jobSpec.Project.Name,
			JobName:     restorename,
		},
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		opLog.Error(err, "Unable to encode message as JSON")
	}
	// publish the cancellation result back to lagoon
	if err := m.Publish("lagoon-tasks:controller", msgBytes); err != nil {
		opLog.Error(err, "Unable to publish message.")
	}
}
