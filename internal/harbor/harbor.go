package harbor

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	harborclientv5 "github.com/mittwald/goharbor-client/v5/apiv2"
	"github.com/mittwald/goharbor-client/v5/apiv2/pkg/config"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Harbor defines a harbor struct
type Harbor struct {
	URL                   string
	Hostname              string
	API                   string
	Username              string
	Password              string
	Log                   logr.Logger
	ClientV5              *harborclientv5.RESTClient
	DeleteDisabled        bool
	WebhookAddition       bool
	RobotPrefix           string
	ExpiryInterval        time.Duration
	RotateInterval        time.Duration
	RobotAccountExpiry    time.Duration
	ControllerNamespace   string
	NamespacePrefix       string
	RandomNamespacePrefix bool
	WebhookURL            string
	WebhookEventTypes     []string
	LagoonTargetName      string
	Config                *config.Options
	TLSSkipVerify         bool
}

// New create a new harbor connection.
func New(harbor Harbor) (*Harbor, error) {
	harbor.Log = ctrl.Log.WithName("controllers").WithName("HarborIntegration")
	if harbor.TLSSkipVerify {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	harbor.Config = &config.Options{
		Page:     1,
		PageSize: 100,
	}
	c2, err := harborclientv5.NewRESTClientForHost(harbor.API, harbor.Username, harbor.Password, harbor.Config)
	if err != nil {
		return nil, err
	}
	harbor.ClientV5 = c2
	return &harbor, nil
}
