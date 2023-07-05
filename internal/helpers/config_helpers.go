package helpers

func GetAPIValues(apiConfig LagoonAPIConfiguration, value string) string {
	switch value {
	// LAGOON_CONFIG_X are the supported path now
	case "LAGOON_CONFIG_API_HOST":
		return apiConfig.APIHost
	case "LAGOON_CONFIG_TOKEN_HOST":
		return apiConfig.TokenHost
	case "LAGOON_CONFIG_TOKEN_PORT":
		return apiConfig.TokenPort
	case "LAGOON_CONFIG_SSH_HOST":
		return apiConfig.SSHHost
	case "LAGOON_CONFIG_SSH_PORT":
		return apiConfig.SSHPort
	// TODO: deprecate TASK_X
	case "TASK_API_HOST":
		return apiConfig.APIHost
	case "TASK_SSH_HOST":
		return apiConfig.TokenHost
	case "TASK_SSH_PORT":
		return apiConfig.TokenPort
	}
	return ""
}
