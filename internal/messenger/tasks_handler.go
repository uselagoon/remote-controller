package messenger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	lagoonv1beta2 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ActiveStandbyPayload struct {
	SourceNamespace      string `json:"sourceNamespace"`
	DestinationNamespace string `json:"destinationNamespace"`
}

// IngressRouteMigration handles running the ingress migrations.
func (m *Messenger) IngressRouteMigration(namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	return m.createAdvancedTask(namespace, jobSpec, nil)
}

// ActiveStandbySwitch handles running the active standby switch setup advanced task.
func (m *Messenger) ActiveStandbySwitch(namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	// always set these to true for ingress migration tasks
	jobSpec.AdvancedTask.DeployerToken = true
	jobSpec.AdvancedTask.SSHKey = true
	asPayload := &ActiveStandbyPayload{}
	asPayloadDecoded, err := base64.StdEncoding.DecodeString(jobSpec.AdvancedTask.JSONPayload)
	if err != nil {
		return fmt.Errorf("unable to base64 decode payload: %v", err)
	}
	err = json.Unmarshal([]byte(asPayloadDecoded), asPayload)
	if err != nil {
		return fmt.Errorf("unable to unmarshal json payload: %v", err)
	}
	return m.createAdvancedTask(namespace, jobSpec, map[string]string{
		"lagoon.sh/activeStandby":                     "true",
		"lagoon.sh/activeStandbyDestinationNamespace": asPayload.DestinationNamespace,
		"lagoon.sh/activeStandbySourceNamespace":      asPayload.SourceNamespace,
	})
}

// AdvancedTask handles running the ingress migrations.
func (m *Messenger) AdvancedTask(namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	return m.createAdvancedTask(namespace, jobSpec, nil)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func (m *Messenger) createAdvancedTask(namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec, additionalLabels map[string]string) error {
	return createAdvancedTask(namespace, jobSpec, m, additionalLabels)
}

// CreateAdvancedTask takes care of creating actual advanced tasks
func createAdvancedTask(namespace string, jobSpec *lagoonv1beta2.LagoonTaskSpec, m *Messenger, additionalLabels map[string]string) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// create the advanced task
	taskName := fmt.Sprintf("lagoon-advanced-task-%s", helpers.RandString(6))
	if jobSpec.Task.TaskName != "" {
		taskName = jobSpec.Task.TaskName
	}
	task := lagoonv1beta2.LagoonTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: namespace,
			Labels: map[string]string{
				"lagoon.sh/taskType":   lagoonv1beta2.TaskTypeAdvanced.String(),
				"lagoon.sh/taskStatus": lagoonv1beta2.TaskStatusPending.String(),
				"lagoon.sh/controller": m.ControllerNamespace,
			},
		},
		Spec: *jobSpec,
	}
	// add additional labels if required
	for key, value := range additionalLabels {
		task.ObjectMeta.Labels[key] = value
	}
	if err := m.Client.Create(context.Background(), &task); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to create task for job %s.",
				jobSpec.Misc.Name,
			),
		)
		return err
	}
	return nil
}
