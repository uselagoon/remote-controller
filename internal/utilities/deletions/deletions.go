package deletions

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/uselagoon/remote-controller/internal/harbor"
)

type DeleteConfig struct {
	PVCRetryAttempts int `json:"pvcRetryAttempts"`
	PVCRetryInterval int `json:"pvcRetryInterval"`
}

// Pruner is used for cleaning up old pods or resources.
type Deletions struct {
	Client                          client.Client
	DeleteConfig                    DeleteConfig
	Harbor                          *harbor.Harbor
	CleanupHarborRepositoryOnDelete bool
	EnableDebug                     bool
}

// New returns a pruner with controller-runtime client.
func New(
	client client.Client,
	harborConfig *harbor.Harbor,
	deleteConfig DeleteConfig,
	cleanupHarborOnDelete bool,
	enableDebug bool,
) *Deletions {
	return &Deletions{
		Client:                          client,
		Harbor:                          harborConfig,
		DeleteConfig:                    deleteConfig,
		CleanupHarborRepositoryOnDelete: cleanupHarborOnDelete,
		EnableDebug:                     enableDebug,
	}
}
