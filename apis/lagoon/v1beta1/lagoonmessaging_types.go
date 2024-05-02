package v1beta1

import "github.com/uselagoon/machinery/api/schema"

// LagoonStatusMessages is where unsent messages are stored for re-sending.
type LagoonStatusMessages struct {
	StatusMessage   *schema.LagoonLog `json:"statusMessage,omitempty"`
	BuildLogMessage *schema.LagoonLog `json:"buildLogMessage,omitempty"`
	TaskLogMessage  *schema.LagoonLog `json:"taskLogMessage,omitempty"`
	// LagoonMessage is used for sending build info back to Lagoon
	// messaging queue to update the environment or deployment
	EnvironmentMessage *schema.LagoonMessage `json:"environmentMessage,omitempty"`
}
