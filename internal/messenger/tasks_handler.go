package messenger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/apis/lagoon/v1beta2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ActiveStandbyPayload struct {
	SourceNamespace      string `json:"sourceNamespace"`
	DestinationNamespace string `json:"destinationNamespace"`
}

// TaskType const for the status type
type ServiceState string

// These are valid states for a service.
const (
	StateStop    ServiceState = "stop"
	StateStart   ServiceState = "start"
	StateRestart ServiceState = "restart"
)

type LagoonServiceInfo struct {
	ServiceName  string       `json:"name,omitempty"`
	ServiceState ServiceState `json:"state,omitempty"`
}

type LagoonIdling struct {
	ForceIdle  bool `json:"foreIdle,omitempty"`
	ForceScale bool `json:"forceScale,omitempty"`
}

type ServiceStateEvent struct {
	Type      string            `json:"type"`      // defines the action type
	EventType string            `json:"eventType"` // defines the eventtype field in the event notification
	Data      LagoonServiceInfo `json:"data"`      // contains the payload for the action, this could be any json so using a map
}

type IdlingEvent struct {
	Type      string       `json:"type"`      // defines the action type
	EventType string       `json:"eventType"` // defines the eventtype field in the event notification
	Data      LagoonIdling `json:"data"`      // contains the payload for the action, this could be any json so using a map
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

func (m *Messenger) ScaleOrIdleEnvironment(ctx context.Context, opLog logr.Logger, ns string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	opLog.Info(
		fmt.Sprintf(
			"Received environment idling request for project %s, environment %s - %s",
			jobSpec.Project.Name,
			jobSpec.Environment.Name,
			ns,
		),
	)
	namespace := &corev1.Namespace{}
	err := m.Client.Get(ctx, types.NamespacedName{
		Name: ns,
	}, namespace)
	if err != nil {
		return err
	}
	retPol := &IdlingEvent{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, retPol); err != nil {
		return err
	}
	if retPol.Data.ForceIdle {
		if retPol.Data.ForceScale {
			// this would be nice to be a lagoon label :)
			namespace.ObjectMeta.Labels["idling.amazee.io/force-scaled"] = "true"
		} else {
			// this would be nice to be a lagoon label :)
			namespace.ObjectMeta.Labels["idling.amazee.io/force-idled"] = "true"
		}
	} else {
		// this would be nice to be a lagoon label :)
		namespace.ObjectMeta.Labels["idling.amazee.io/unidle"] = "true"
	}
	if err := m.Client.Update(context.Background(), namespace); err != nil {
		opLog.Error(err,
			fmt.Sprintf(
				"Unable to update namespace %s to set idle state.",
				ns,
			),
		)
		return err
	}
	return nil
}

func (m *Messenger) EnvironmentServiceState(ctx context.Context, opLog logr.Logger, ns string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	retPol := &ServiceStateEvent{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, retPol); err != nil {
		return err
	}
	opLog.Info(
		fmt.Sprintf(
			"Received environment service request for project %s, environment %s service %s - %s",
			jobSpec.Project.Name,
			jobSpec.Environment.Name,
			retPol.Data.ServiceName,
			ns,
		),
	)
	deployment := &appsv1.Deployment{}
	err := m.Client.Get(ctx, types.NamespacedName{
		Name:      retPol.Data.ServiceName,
		Namespace: ns,
	}, deployment)
	if err != nil {
		return err
	}
	update := false
	switch retPol.Data.ServiceState {
	case StateRestart:
		deployment.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
		update = true
	case StateStop:
		if *deployment.Spec.Replicas > 0 {
			// if the service has replicas, then save the replica count and scale it to 0
			deployment.ObjectMeta.Annotations["service.lagoon.sh/replicas"] = strconv.FormatInt(int64(*deployment.Spec.Replicas), 10)
			replicas := int32(0)
			deployment.Spec.Replicas = &replicas
			update = true
		}
	case StateStart:
		if *deployment.Spec.Replicas == 0 {
			// if the service has no replicas, set it back to what the previous replica value was
			prevReplicas, err := strconv.Atoi(deployment.ObjectMeta.Annotations["service.lagoon.sh/replicas"])
			if err != nil {
				return err
			}
			replicas := int32(prevReplicas)
			deployment.Spec.Replicas = &replicas
			delete(deployment.ObjectMeta.Annotations, "service.lagoon.sh/replicas")
			update = true
		}
	default:
		// nothing to do
		return nil
	}
	if update {
		if err := m.Client.Update(ctx, deployment); err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Unable to update deployment %s to change its state.",
					ns,
				),
			)
			return err
		}
	}
	return nil
}
