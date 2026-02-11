package messenger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	lagoonv1beta2 "github.com/uselagoon/remote-controller/api/lagoon/v1beta2"
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
				"lagoon.sh/taskType":    lagoonv1beta2.TaskTypeAdvanced.String(),
				"lagoon.sh/taskStatus":  lagoonv1beta2.TaskStatusPending.String(),
				"lagoon.sh/controller":  m.ControllerNamespace,
				"crd.lagoon.sh/version": "v1beta2",
			},
		},
		Spec: *jobSpec,
	}
	// add additional labels if required
	for key, value := range additionalLabels {
		task.Labels[key] = value
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

type Idling struct {
	Idle       bool `json:"idle"`
	ForceScale bool `json:"forceScale"`
}

type Service struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func (m *Messenger) ScaleOrIdleEnvironment(ctx context.Context, opLog logr.Logger, ns string, jobSpec *lagoonv1beta2.LagoonTaskSpec) error {
	namespace := &corev1.Namespace{}
	err := m.Client.Get(ctx, types.NamespacedName{
		Name: ns,
	}, namespace)
	if err != nil {
		return err
	}
	idling := Idling{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, &idling); err != nil {
		opLog.Error(err,
			"Unable to unmarshal the idling json.",
		)
		return err
	}
	if idling.Idle {
		if idling.ForceScale {
			// this would be nice to be a lagoon label :)
			namespace.Labels["idling.amazee.io/force-scaled"] = "true"
		} else {
			// this would be nice to be a lagoon label :)
			namespace.Labels["idling.amazee.io/force-idled"] = "true"
		}
	} else {
		// this would be nice to be a lagoon label :)
		namespace.Labels["idling.amazee.io/unidle"] = "true"
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
	deployment := &appsv1.Deployment{}
	service := Service{}
	if err := json.Unmarshal(jobSpec.Misc.MiscResource, &service); err != nil {
		opLog.Error(err,
			"Unable to unmarshal the service json.",
		)
		return err
	}
	err := m.Client.Get(ctx, types.NamespacedName{
		Name:      service.Name,
		Namespace: ns,
	}, deployment)
	if err != nil {
		return err
	}
	update := false
	switch service.State {
	case "restart":
		deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
		update = true
	case "stop":
		if *deployment.Spec.Replicas > 0 {
			// if the service has replicas, then save the replica count and scale it to 0
			deployment.Annotations["service.lagoon.sh/replicas"] = strconv.FormatInt(int64(*deployment.Spec.Replicas), 10)
			replicas := int32(0)
			deployment.Spec.Replicas = &replicas
			update = true
		}
	case "start":
		if *deployment.Spec.Replicas == 0 {
			// if the service has no replicas, set it back to what the previous replica value was
			prevReplicas, err := strconv.ParseInt(deployment.Annotations["service.lagoon.sh/replicas"], 10, 32)
			if err != nil {
				// unable to get replica value, default to 1 to allow scale up
				// if a hpa is active, this will be ignored
				prevReplicas = 1
			}
			replicas := int32(1)
			if prevReplicas > 0 && prevReplicas <= math.MaxInt32 {
				replicas = int32(prevReplicas)
			}
			deployment.Spec.Replicas = &replicas
			delete(deployment.Annotations, "service.lagoon.sh/replicas")
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
