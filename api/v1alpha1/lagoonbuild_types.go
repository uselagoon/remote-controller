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

type BuildConditionType string

const BuildStatus string = "Status"

// These are valid conditions of a job.
const (
	// BuildComplete means the build has completed its execution.
	BuildComplete BuildConditionType = "Complete"
	// BuildFailed means the job has failed its execution.
	BuildFailed BuildConditionType = "Failed"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

//+kubebuilder:subresource:status

// LagoonBuildSpec defines the desired state of LagoonBuild
type LagoonBuildSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Build       Build       `json:"build"`
	Git         Git         `json:"git"`
	Project     Project     `json:"project"`
	Branch      Branch      `json:"branch,omitempty"`
	Pullrequest Pullrequest `json:"pullrequest,omitempty"`
	Promote     Promote     `json:"promote,omitempty"`
}

// LagoonBuildStatus defines the observed state of LagoonBuild
type LagoonBuildStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []LagoonBuildConditions `json:"conditions,omitempty"`
	Log        []byte                  `json:"log,omitempty"`
}

// LagoonBuildConditions defines the observed conditions of the migrations
type LagoonBuildConditions struct {
	LastTransitionTime string                 `json:"lastTransitionTime"`
	Status             corev1.ConditionStatus `json:"status"`
	Type               BuildConditionType     `json:"type"`
	// Condition          string                 `json:"condition"`
}

// +kubebuilder:object:root=true

// LagoonBuild is the Schema for the lagoonbuilds API
type LagoonBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LagoonBuildSpec   `json:"spec,omitempty"`
	Status LagoonBuildStatus `json:"status,omitempty"`
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

type Build struct {
	CI    string `json:"ci,omitempty"`
	Image string `json:"image"`
	Type  string `json:"type"`
}

type Git struct {
	URL       string `json:"url"`
	Reference string `json:"reference"`
}

type Project struct {
	Name                  string     `json:"name"`
	Environment           string     `json:"environment"`
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

type Branch struct {
	Name string `json:"name,omitempty"`
}

type Variables struct {
	Project     []byte `json:"project,omitempty"`
	Environment []byte `json:"environment,omitempty"`
}

type Pullrequest struct {
	HeadBranch string `json:"headBranch,omitempty"`
	HeadSha    string `json:"headSha,omitempty"`
	BaseBranch string `json:"baseBranch,omitempty"`
	BaseSha    string `json:"baseSha,omitempty"`
	Title      string `json:"title,omitempty"`
	Number     int    `json:"number,omitempty"`
}

type Promote struct {
	SourceEnvironment string `json:"sourceEnvironment,omitempty"`
	SourceProject     string `json:"sourceProject,omitempty"`
}

type Monitoring struct {
	Contact      string `json:"contact,omitempty"`
	StatuspageID string `json:"statuspageID,omitempty"`
}
