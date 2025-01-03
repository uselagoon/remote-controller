/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/uselagoon/machinery/api/schema"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// +kubebuilder:object:root=true
// +kubebuilder:deprecatedversion:warning="use lagoontasks.crd.lagoon.sh/v1beta2"

// LagoonTask is the Schema for the lagoontasks API
type LagoonTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec           LagoonTaskSpec        `json:"spec,omitempty"`
	Status         LagoonTaskStatus      `json:"status,omitempty"`
	StatusMessages *LagoonStatusMessages `json:"statusMessages,omitempty"`
}

// +kubebuilder:object:root=true

// LagoonTaskList contains a list of LagoonTask
type LagoonTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LagoonTask `json:"items"`
}

// TaskStatusType const for the status type
type TaskStatusType string

// These are valid conditions of a job.
const (
	// TaskStatusPending means the job is pending.
	TaskStatusPending TaskStatusType = "Pending"
	// TaskStatusQueued means the job is queued.
	TaskStatusQueued TaskStatusType = "Queued"
	// TaskStatusRunning means the job is running.
	TaskStatusRunning TaskStatusType = "Running"
	// TaskStatusComplete means the job has completed its execution.
	TaskStatusComplete TaskStatusType = "Complete"
	// TaskStatusFailed means the job has failed its execution.
	TaskStatusFailed TaskStatusType = "Failed"
	// TaskStatusCancelled means the job been cancelled.
	TaskStatusCancelled TaskStatusType = "Cancelled"
)

func (b TaskStatusType) String() string {
	return string(b)
}

func (b TaskStatusType) ToLower() string {
	return strings.ToLower(b.String())
}

// TaskType const for the status type
type TaskType string

// These are valid conditions of a job.
const (
	// TaskTypeStandard means the task is a standard task.
	TaskTypeStandard TaskType = "standard"
	// TaskTypeAdvanced means the task is an advanced task.
	TaskTypeAdvanced TaskType = "advanced"
)

func (b TaskType) String() string {
	return string(b)
}

// LagoonTaskSpec defines the desired state of LagoonTask
type LagoonTaskSpec struct {
	Key          string                  `json:"key,omitempty"`
	Task         schema.LagoonTaskInfo   `json:"task,omitempty"`
	Project      LagoonTaskProject       `json:"project,omitempty"`
	Environment  LagoonTaskEnvironment   `json:"environment,omitempty"`
	Misc         *LagoonMiscInfo         `json:"misc,omitempty"`
	AdvancedTask *LagoonAdvancedTaskInfo `json:"advancedTask,omitempty"`
}

// LagoonTaskInfo defines what a task can use to communicate with Lagoon via SSH/API.
type LagoonTaskInfo struct {
	ID       string `json:"id"` // should be int, but the api sends it as a string :\
	Name     string `json:"name,omitempty"`
	TaskName string `json:"taskName,omitempty"`
	Service  string `json:"service,omitempty"`
	Command  string `json:"command,omitempty"`
	SSHHost  string `json:"sshHost,omitempty"`
	SSHPort  string `json:"sshPort,omitempty"`
	APIHost  string `json:"apiHost,omitempty"`
}

// LagoonAdvancedTaskInfo defines what an advanced task can use for the creation of the pod.
type LagoonAdvancedTaskInfo struct {
	RunnerImage   string `json:"runnerImage,omitempty"`
	JSONPayload   string `json:"JSONPayload,omitempty"`
	DeployerToken bool   `json:"deployerToken,omitempty"`
	SSHKey        bool   `json:"sshKey,omitempty"`
}

// LagoonMiscInfo defines the resource or backup information for a misc task.
type LagoonMiscInfo struct {
	ID           string                `json:"id"` // should be int, but the api sends it as a string :\
	Name         string                `json:"name,omitempty"`
	Backup       *LagoonMiscBackupInfo `json:"backup,omitempty"`
	MiscResource []byte                `json:"miscResource,omitempty"`
}

// LagoonMiscBackupInfo defines the information for a backup.
type LagoonMiscBackupInfo struct {
	ID       string `json:"id"` // should be int, but the api sends it as a string :\
	Source   string `json:"source"`
	BackupID string `json:"backupId"`
}

// LagoonTaskProject defines the lagoon project information.
type LagoonTaskProject struct {
	ID               string          `json:"id"` // should be int, but the api sends it as a string :\
	Name             string          `json:"name"`
	NamespacePattern string          `json:"namespacePattern,omitempty"`
	Variables        LagoonVariables `json:"variables,omitempty"`
	Organization     *Organization   `json:"organization,omitempty"`
}

// LagoonTaskEnvironment defines the lagoon environment information.
type LagoonTaskEnvironment struct {
	ID              string `json:"id"` // should be int, but the api sends it as a string :\
	Name            string `json:"name"`
	Project         string `json:"project"` // should be int, but the api sends it as a string :\
	EnvironmentType string `json:"environmentType"`
}

// LagoonTaskStatus defines the observed state of LagoonTask
type LagoonTaskStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []LagoonTaskConditions `json:"conditions,omitempty"`
	Log        []byte                 `json:"log,omitempty"`
}

// LagoonTaskConditions defines the observed conditions of task pods.
type LagoonTaskConditions struct {
	LastTransitionTime string                 `json:"lastTransitionTime"`
	Status             corev1.ConditionStatus `json:"status"`
	Type               TaskStatusType         `json:"type"`
	Reason             string                 `json:"reason"`
	Message            string                 `json:"message"`
}

func init() {
	SchemeBuilder.Register(&LagoonTask{}, &LagoonTaskList{})
}

// convert to string as required for environment ID for backwards compatability
func (a *LagoonTaskEnvironment) UnmarshalJSON(data []byte) error {
	tmpMap := map[string]interface{}{}
	json.Unmarshal(data, &tmpMap)
	if value, ok := tmpMap["id"]; ok {
		a.ID = fmt.Sprintf("%v", value)
	}
	return nil
}

// convert to uint as required for environment ID for backwards compatability
func (a *LagoonTaskEnvironment) MarshalJSON() ([]byte, error) {
	type LagoonTaskEnvironmentA LagoonTaskEnvironment
	id, _ := strconv.Atoi(a.ID)
	idUint := uint(id)
	return json.Marshal(&struct {
		ID *uint `json:"id"`
		*LagoonTaskEnvironmentA
	}{
		ID:                     &idUint,
		LagoonTaskEnvironmentA: (*LagoonTaskEnvironmentA)(a),
	})
}

// convert to string as required for project ID for backwards compatability
func (a *LagoonTaskProject) UnmarshalJSON(data []byte) error {
	tmpMap := map[string]interface{}{}
	json.Unmarshal(data, &tmpMap)
	if value, ok := tmpMap["id"]; ok {
		a.ID = fmt.Sprintf("%v", value)
	}
	return nil
}

// convert to uint as required for environment ID for backwards compatability
func (a *LagoonTaskProject) MarshalJSON() ([]byte, error) {
	type LagoonTaskProjectA LagoonTaskProject
	id, _ := strconv.Atoi(a.ID)
	idUint := uint(id)
	return json.Marshal(&struct {
		ID *uint `json:"id"`
		*LagoonTaskProjectA
	}{
		ID:                 &idUint,
		LagoonTaskProjectA: (*LagoonTaskProjectA)(a),
	})
}

// this is a custom unmarshal function that will check deployerToken and sshKey which come from Lagoon as `1|0` booleans because javascript
// this converts them from floats to bools
func (a *LagoonAdvancedTaskInfo) UnmarshalJSON(data []byte) error {
	tmpMap := map[string]interface{}{}
	json.Unmarshal(data, &tmpMap)
	if value, ok := tmpMap["deployerToken"]; ok {
		if reflect.TypeOf(value).Kind() == reflect.Float64 {
			vBool, err := strconv.ParseBool(fmt.Sprintf("%v", value))
			if err == nil {
				a.DeployerToken = vBool
			}
		}
		if reflect.TypeOf(value).Kind() == reflect.Bool {
			a.DeployerToken = value.(bool)
		}
	}
	if value, ok := tmpMap["sshKey"]; ok {
		if reflect.TypeOf(value).Kind() == reflect.Float64 {
			vBool, err := strconv.ParseBool(fmt.Sprintf("%v", value))
			if err == nil {
				a.SSHKey = vBool
			}
		}
		if reflect.TypeOf(value).Kind() == reflect.Bool {
			a.SSHKey = value.(bool)
		}
	}
	if value, ok := tmpMap["RunnerImage"]; ok {
		a.RunnerImage = value.(string)
	}
	if value, ok := tmpMap["runnerImage"]; ok {
		a.RunnerImage = value.(string)
	}
	if value, ok := tmpMap["JSONPayload"]; ok {
		a.JSONPayload = value.(string)
	}
	return nil
}
