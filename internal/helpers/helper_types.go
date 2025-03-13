package helpers

// LagoonEnvironmentVariable is used to define Lagoon environment variables.
type LagoonEnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

// LagoonAPIConfiguration is for the settings for task API/SSH host/ports
type LagoonAPIConfiguration struct {
	APIHost   string
	TokenHost string
	TokenPort string
	SSHHost   string
	SSHPort   string
}
