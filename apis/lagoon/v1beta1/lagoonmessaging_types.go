package v1beta1

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
	BranchName    string          `json:"branchName,omitempty"`
	BuildName     string          `json:"buildName,omitempty"`
	BuildPhase    string          `json:"buildPhase,omitempty"` // @TODO: deprecate once controller-handler is fixed
	BuildStatus   string          `json:"buildStatus,omitempty"`
	BuildStep     string          `json:"buildStep,omitempty"`
	EndTime       string          `json:"endTime,omitempty"`
	Environment   string          `json:"environment,omitempty"`
	EnvironmentID *uint           `json:"environmentId,omitempty"`
	JobName       string          `json:"jobName,omitempty"`   // used by tasks/jobs
	JobStatus     string          `json:"jobStatus,omitempty"` // used by tasks/jobs
	JobStep       string          `json:"jobStep,omitempty"`   // used by tasks/jobs
	LogLink       string          `json:"logLink,omitempty"`
	Project       string          `json:"project,omitempty"`
	ProjectID     *uint           `json:"projectId,omitempty"`
	ProjectName   string          `json:"projectName,omitempty"`
	RemoteID      string          `json:"remoteId,omitempty"`
	Route         string          `json:"route,omitempty"`
	Routes        []string        `json:"routes,omitempty"`
	StartTime     string          `json:"startTime,omitempty"`
	Services      []string        `json:"services,omitempty"`
	Task          *LagoonTaskInfo `json:"task,omitempty"`
	Key           string          `json:"key,omitempty"`
	AdvancedData  string          `json:"advancedData,omitempty"`
	Cluster       string          `json:"clusterName,omitempty"`
}

// LagoonMessage is used for sending build info back to Lagoon
// messaging queue to update the environment or deployment
type LagoonMessage struct {
	Type      string         `json:"type,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Meta      *LagoonLogMeta `json:"meta,omitempty"`
}
