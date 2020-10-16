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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobConditionType const for the status type
type JobConditionType string

// These are valid conditions of a job.
const (
	// BuildComplete means the build has completed its execution.
	JobComplete JobConditionType = "Complete"
	// BuildFailed means the job has failed its execution.
	JobFailed JobConditionType = "Failed"
	// BuildFailed means the job has failed its execution.
	JobCancelled JobConditionType = "Cancelled"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

//+kubebuilder:subresource:status

// LagoonBuildSpec defines the desired state of LagoonBuild
type LagoonBuildSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Build        Build       `json:"build"`
	Project      Project     `json:"project"`
	Branch       Branch      `json:"branch,omitempty"`
	Pullrequest  Pullrequest `json:"pullrequest,omitempty"`
	Promote      Promote     `json:"promote,omitempty"`
	GitReference string      `json:"gitReference"`
	// @TODO: Openshift should be deprecated as the controller will define if it is openshift or not
	// Openshift    bool        `json:"openshift"`
}

// LagoonBuildStatus defines the observed state of LagoonBuild
type LagoonBuildStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []LagoonConditions `json:"conditions,omitempty"`
	Log        []byte             `json:"log,omitempty"`
}

// LagoonConditions defines the observed conditions of the pods.
type LagoonConditions struct {
	LastTransitionTime string                 `json:"lastTransitionTime"`
	Status             corev1.ConditionStatus `json:"status"`
	Type               JobConditionType       `json:"type"`
	// Condition          string                 `json:"condition"`
}

// +kubebuilder:object:root=true

// LagoonBuild is the Schema for the lagoonbuilds API
type LagoonBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec           LagoonBuildSpec       `json:"spec,omitempty"`
	Status         LagoonBuildStatus     `json:"status,omitempty"`
	StatusMessages *LagoonStatusMessages `json:"statusMessages,omitempty"`
}

// +kubebuilder:object:root=true

// LagoonBuildList contains a list of LagoonBuild
type LagoonBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LagoonBuild `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LagoonBuild{}, &LagoonBuildList{})
}

// Build contains the type of build, and the image to use for the builder.
type Build struct {
	CI    string `json:"ci,omitempty"`
	Image string `json:"image,omitempty"`
	Type  string `json:"type"`
}

// Project contains the project information from lagoon.
type Project struct {
	Name                  string     `json:"name"`
	Environment           string     `json:"environment"`
	UILink                string     `json:"uiLink,omitempty"`
	GitURL                string     `json:"gitUrl"`
	NamespacePattern      string     `json:"namespacePattern,omitempty"`
	RouterPattern         string     `json:"routerPattern,omitempty"`
	EnvironmentType       string     `json:"environmentType"`
	ProductionEnvironment string     `json:"productionEnvironment"`
	StandbyEnvironment    string     `json:"standbyEnvironment"`
	DeployTarget          string     `json:"deployTarget"`
	ProjectSecret         string     `json:"projectSecret"`
	SubFolder             string     `json:"subfolder,omitempty"`
	Key                   []byte     `json:"key"`
	Monitoring            Monitoring `json:"monitoring"`
	Variables             Variables  `json:"variables"`
	Registry              string     `json:"registry,omitempty"`
}

// Variables contains the project and environment variables from lagoon.
type Variables struct {
	Project     []byte `json:"project,omitempty"`
	Environment []byte `json:"environment,omitempty"`
}

// Branch contains the branch name used for a branch deployment.
type Branch struct {
	Name string `json:"name,omitempty"`
}

// Pullrequest contains the information for a pullrequest deployment.
type Pullrequest struct {
	HeadBranch string `json:"headBranch,omitempty"`
	HeadSha    string `json:"headSha,omitempty"`
	BaseBranch string `json:"baseBranch,omitempty"`
	BaseSha    string `json:"baseSha,omitempty"`
	Title      string `json:"title,omitempty"`
	Number     int    `json:"number,omitempty"`
}

// Promote contains the information for a promote deployment.
type Promote struct {
	SourceEnvironment string `json:"sourceEnvironment,omitempty"`
	SourceProject     string `json:"sourceProject,omitempty"`
}

// Monitoring contains the monitoring information for the project in Lagoon.
type Monitoring struct {
	Contact      string `json:"contact,omitempty"`
	StatuspageID string `json:"statuspageID,omitempty"`
}
