package messenger

import (
	"github.com/cheshir/go-mq"
	"github.com/uselagoon/remote-controller/internal/utilities/deletions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type removeTask struct {
	ProjectName                      string `json:"projectName"`
	Type                             string `json:"type"`
	ForceDeleteProductionEnvironment bool   `json:"forceDeleteProductionEnvironment"`
	PullrequestNumber                string `json:"pullrequestNumber"`
	Branch                           string `json:"branch"`
	BranchName                       string `json:"branchName"`
	NamespacePattern                 string `json:"namespacePattern,omitempty"`
}

type messenger interface {
	Consumer(string)
	Publish(string, []byte)
	GetPendingMessages()
}

// Messaging is used for the config and client information for the messaging queue.
type Messenger struct {
	Config                           mq.Config
	Client                           client.Client
	ConnectionAttempts               int
	ConnectionRetryInterval          int
	ControllerNamespace              string
	NamespacePrefix                  string
	RandomNamespacePrefix            bool
	AdvancedTaskSSHKeyInjection      bool
	AdvancedTaskDeployTokenInjection bool
	DeletionHandler                  *deletions.Deletions
	EnableDebug                      bool
}

// New returns a messaging with config and controller-runtime client.
func New(config mq.Config,
	client client.Client,
	startupAttempts int,
	startupInterval int,
	controllerNamespace,
	namespacePrefix string,
	randomNamespacePrefix,
	advancedTaskSSHKeyInjection bool,
	advancedTaskDeployTokenInjection bool,
	deletionHandler *deletions.Deletions,
	enableDebug bool,
) *Messenger {
	return &Messenger{
		Config:                           config,
		Client:                           client,
		ConnectionAttempts:               startupAttempts,
		ConnectionRetryInterval:          startupInterval,
		ControllerNamespace:              controllerNamespace,
		NamespacePrefix:                  namespacePrefix,
		RandomNamespacePrefix:            randomNamespacePrefix,
		AdvancedTaskSSHKeyInjection:      advancedTaskSSHKeyInjection,
		AdvancedTaskDeployTokenInjection: advancedTaskDeployTokenInjection,
		DeletionHandler:                  deletionHandler,
		EnableDebug:                      enableDebug,
	}
}
