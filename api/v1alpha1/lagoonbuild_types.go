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

// BuildConditionType const for the status type
type BuildConditionType string

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

	Build        Build       `json:"build"`
	Project      Project     `json:"project"`
	Branch       Branch      `json:"branch,omitempty"`
	Pullrequest  Pullrequest `json:"pullrequest,omitempty"`
	Promote      Promote     `json:"promote,omitempty"`
	GitReference string      `json:"gitReference"`
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

	Spec           LagoonBuildSpec       `json:"spec,omitempty"`
	Status         LagoonBuildStatus     `json:"status,omitempty"`
	StatusMessages *LagoonStatusMessages `json:"statusMessages,omitempty"`
}

// LagoonStatusMessages is where unsent messages are stored for re-sending.
type LagoonStatusMessages struct {
	StatusMessage      *LagoonLog     `json:"statusMessage,omitempty"`
	BuildLogMessage    *LagoonLog     `json:"buildLogMessage,omitempty"`
	EnvironmentMessage *LagoonMessage `json:"environmentMessage,omitempty"`
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
	Image string `json:"image"`
	Type  string `json:"type"`
}

// Project contains the project information from lagoon.
type Project struct {
	Name                  string     `json:"name"`
	Environment           string     `json:"environment"`
	GitURL                string     `json:"giturl"`
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

// LagoonMessaging

// LagoonLog is used to sendToLagoonLogs messaging queue
// this is general logging information
type LagoonLog struct {
	Severity string         `json:"severity,omitempty"`
	Project  string         `json:"project,omitempty"`
	UUID     string         `json:"uuid,omitempty"`
	Event    string         `json:"event,omitempty"`
	Meta     *LagoonLogMeta `json:"meta,omitempty"`
	Message  string         `json:"message,omitempty"`
}

// LagoonLogMeta is the metadata that is used by logging in Lagoon.
type LagoonLogMeta struct {
	ProjectName string `json:"projectName,omitempty"`
	BranchName  string `json:"branchName,omitempty"`
	JobName     string `json:"jobName,omitempty"`
	BuildPhase  string `json:"buildPhase,omitempty"`
	RemoteID    string `json:"remoteId,omitempty"`
}

// LagoonMessage is used for sending build info back to Lagoon
// messaging queue to update the environment or deployment
type LagoonMessage struct {
	Type      string           `json:"type,omitempty"`
	Namespace string           `json:"namespace,omitempty"`
	BuildInfo *LagoonBuildInfo `json:"buildInfo,omitempty"`
}

// LagoonBuildInfo contains all the information the operatorhandler in Lagoon needs
// to be able to update an environment or deployment task.
type LagoonBuildInfo struct {
	Environment    string `json:"environment,omitempty"`
	Route          string `json:"route,omitempty"`
	Routes         string `json:"routes,omitempty"`
	MonitoringURLs string `json:"monitoringUrls,omitempty"`
	Project        string `json:"project,omitempty"`
	JobUID         string `json:"jobUid,omitempty"`
	BuildPhase     string `json:"buildPhase,omitempty"`
	BuildName      string `json:"buildName,omitempty"`
	StartTime      string `json:"startTime,omitempty"`
	EndTime        string `json:"endTime,omitempty"`
}
