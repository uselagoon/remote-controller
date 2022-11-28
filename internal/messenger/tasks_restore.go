package messenger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lagoonv1beta1 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	k8upv1alpha1 "github.com/vshn/k8up/api/v1alpha1"
)

// ResticRestore handles creating the restic restore jobs.
func (m *Messenger) ResticRestore(namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
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
	// check the version, if there is no version in the payload, assume it is k8up v2
	if vers == "backup.appuio.ch/v1alpha1" {
		return m.createv1alpha1Restore(opLog, namespace, jobSpec)
	} else {
		if err := m.createv1Restore(opLog, namespace, jobSpec); err != nil {
			return m.createv1alpha1Restore(opLog, namespace, jobSpec)
		}
		return err
	}
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
func (m *Messenger) createv1alpha1Restore(opLog logr.Logger, namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	restorev1alpha1 := &k8upv1alpha1.Restore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, restorev1alpha1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				restorev1alpha1.ObjectMeta.Name,
			),
		)
		// just log the error then return
		return nil
	}
	restorev1alpha1.SetNamespace(namespace)
	if err := m.Client.Create(context.Background(), restorev1alpha1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create restore %s with k8up v1alpha1 api.",
				jobSpec.Misc.Name,
			),
		)
		// just log the error then return
		return nil
	}
	return nil
}

// createv1Restore will create a restore task using the restores.k8up.io v1 api (k8up v2)
func (m *Messenger) createv1Restore(opLog logr.Logger, namespace string, jobSpec *lagoonv1beta1.LagoonTaskSpec) error {
	restorev1 := &k8upv1.Restore{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, restorev1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to unmarshal the json into a job %s.",
				restorev1.ObjectMeta.Name,
			),
		)
		// just log the error then return
		return nil
	}
	restorev1.SetNamespace(namespace)
	if err := m.Client.Create(context.Background(), restorev1); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create restore %s with k8up v1 api.",
				jobSpec.Misc.Name,
			),
		)
		// just log the error then return
		return nil
	}
	return nil
}
