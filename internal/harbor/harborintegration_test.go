package harbor

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	harborclientv3 "github.com/mittwald/goharbor-client/v3/apiv2"
	harborclientv5 "github.com/mittwald/goharbor-client/v5/apiv2"
	"github.com/mittwald/goharbor-client/v5/apiv2/pkg/config"
)

func TestHarbor_matchRobotAccount(t *testing.T) {
	type fields struct {
		URL                   string
		Hostname              string
		API                   string
		Username              string
		Password              string
		Log                   logr.Logger
		ClientV3              *harborclientv3.RESTClient
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
	}
	type args struct {
		robotName       string
		projectName     string
		environmentName string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "test1",
			fields: fields{
				RobotPrefix:      "robot$",
				LagoonTargetName: "ci-local-controller-kubernetes",
			},
			args: args{
				robotName:       "robot$main-954f2d24",
				projectName:     "example-com",
				environmentName: "main",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Harbor{
				URL:                   tt.fields.URL,
				Hostname:              tt.fields.Hostname,
				API:                   tt.fields.API,
				Username:              tt.fields.Username,
				Password:              tt.fields.Password,
				Log:                   tt.fields.Log,
				ClientV3:              tt.fields.ClientV3,
				ClientV5:              tt.fields.ClientV5,
				DeleteDisabled:        tt.fields.DeleteDisabled,
				WebhookAddition:       tt.fields.WebhookAddition,
				RobotPrefix:           tt.fields.RobotPrefix,
				ExpiryInterval:        tt.fields.ExpiryInterval,
				RotateInterval:        tt.fields.RotateInterval,
				RobotAccountExpiry:    tt.fields.RobotAccountExpiry,
				ControllerNamespace:   tt.fields.ControllerNamespace,
				NamespacePrefix:       tt.fields.NamespacePrefix,
				RandomNamespacePrefix: tt.fields.RandomNamespacePrefix,
				WebhookURL:            tt.fields.WebhookURL,
				WebhookEventTypes:     tt.fields.WebhookEventTypes,
				LagoonTargetName:      tt.fields.LagoonTargetName,
				Config:                tt.fields.Config,
			}
			if got := h.matchRobotAccount(tt.args.robotName, tt.args.projectName, tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.matchRobotAccount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHarbor_matchRobotAccountV2(t *testing.T) {
	type fields struct {
		URL                   string
		Hostname              string
		API                   string
		Username              string
		Password              string
		Log                   logr.Logger
		ClientV3              *harborclientv3.RESTClient
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
	}
	type args struct {
		robotName       string
		projectName     string
		environmentName string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "test1",
			fields: fields{
				RobotPrefix:      "robot$",
				LagoonTargetName: "ci-local-controller-kubernetes",
			},
			args: args{
				robotName:       "robot$example-com+main-954f2d24",
				projectName:     "example-com",
				environmentName: "main",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Harbor{
				URL:                   tt.fields.URL,
				Hostname:              tt.fields.Hostname,
				API:                   tt.fields.API,
				Username:              tt.fields.Username,
				Password:              tt.fields.Password,
				Log:                   tt.fields.Log,
				ClientV3:              tt.fields.ClientV3,
				ClientV5:              tt.fields.ClientV5,
				DeleteDisabled:        tt.fields.DeleteDisabled,
				WebhookAddition:       tt.fields.WebhookAddition,
				RobotPrefix:           tt.fields.RobotPrefix,
				ExpiryInterval:        tt.fields.ExpiryInterval,
				RotateInterval:        tt.fields.RotateInterval,
				RobotAccountExpiry:    tt.fields.RobotAccountExpiry,
				ControllerNamespace:   tt.fields.ControllerNamespace,
				NamespacePrefix:       tt.fields.NamespacePrefix,
				RandomNamespacePrefix: tt.fields.RandomNamespacePrefix,
				WebhookURL:            tt.fields.WebhookURL,
				WebhookEventTypes:     tt.fields.WebhookEventTypes,
				LagoonTargetName:      tt.fields.LagoonTargetName,
				Config:                tt.fields.Config,
			}
			if got := h.matchRobotAccountV2(tt.args.robotName, tt.args.projectName, tt.args.environmentName); got != tt.want {
				t.Errorf("Harbor.matchRobotAccountV2() = %v, want %v", got, tt.want)
			}
		})
	}
}
