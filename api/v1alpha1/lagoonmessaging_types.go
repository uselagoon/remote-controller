package v1alpha1

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
	BranchName     string          `json:"branchName,omitempty"`
	BuildName      string          `json:"buildName,omitempty"`
	BuildPhase     string          `json:"buildPhase,omitempty"`
	EndTime        string          `json:"endTime,omitempty"`
	Environment    string          `json:"environment,omitempty"`
	JobName        string          `json:"jobName,omitempty"`
	JobStatus      string          `json:"jobStatus,omitempty"`
	LogLink        string          `json:"logLink,omitempty"`
	MonitoringURLs []string        `json:"monitoringUrls,omitempty"`
	Project        string          `json:"project,omitempty"`
	ProjectName    string          `json:"projectName,omitempty"`
	RemoteID       string          `json:"remoteId,omitempty"`
	Route          string          `json:"route,omitempty"`
	Routes         []string        `json:"routes,omitempty"`
	StartTime      string          `json:"startTime,omitempty"`
	Task           *LagoonTaskInfo `json:"task,omitempty"`
}

// // LagoonTaskMeta is use for the tasks metadata
// type LagoonTaskMeta struct {
// 	ID     int    `json:"id"`
// 	Name   string `json:"name"`
// 	Status string `json:"status"`
// }

// LagoonMessage is used for sending build info back to Lagoon
// messaging queue to update the environment or deployment
type LagoonMessage struct {
	Type      string         `json:"type,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Meta      *LagoonLogMeta `json:"meta,omitempty"`
	// BuildInfo *LagoonBuildInfo `json:"buildInfo,omitempty"`
}

// LagoonStatusMessages is where unsent messages are stored for re-sending.
type LagoonStatusMessages struct {
	StatusMessage      *LagoonLog     `json:"statusMessage,omitempty"`
	BuildLogMessage    *LagoonLog     `json:"buildLogMessage,omitempty"`
	EnvironmentMessage *LagoonMessage `json:"environmentMessage,omitempty"`
}
