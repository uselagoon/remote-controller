package helpers

// LagoonEnvironmentVariable is used to define Lagoon environment variables.
type LagoonEnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

// Auths struct contains an embedded RegistriesStruct of name auths
type Auths struct {
	Registries RegistriesStruct `json:"auths"`
}

// RegistriesStruct is a map of registries to their credentials
type RegistriesStruct map[string]RegistryCredentials

// RegistryCredentials defines the fields stored per registry in an docker config secret
type RegistryCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Auth     string `json:"auth"`
}
