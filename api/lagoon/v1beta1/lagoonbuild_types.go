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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:unservedversion
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=`.status.phase`,description="Status of the LagoonBuild"
// +kubebuilder:printcolumn:name="BuildStep",type="string",JSONPath=`.status.conditions[?(@.type == "BuildStep")].reason`,description="The build step of the LagoonBuild"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// LagoonBuildSpec defines the desired state of LagoonBuild
type LagoonBuildSpec struct {
}

// LagoonBuildStatus defines the observed state of LagoonBuild
type LagoonBuildStatus struct {
	// Conditions provide a standard mechanism for higher-level status reporting from a controller.
	// They are an extension mechanism which allows tools and other controllers to collect summary information about
	// resources without needing to understand resource-specific status details.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	Phase      string             `json:"phase,omitempty"`
}

func init() {
	SchemeBuilder.Register(&LagoonBuild{}, &LagoonBuildList{})
}
