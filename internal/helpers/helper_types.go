package helpers

import (
	"context"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

func K8UPVersions(ctx context.Context, c client.Client) (bool, bool, error) {
	k8upv1alpha1Exists := false
	k8upv1Exists := false
	crdv1alpha1 := &apiextensionsv1.CustomResourceDefinition{}
	if err := c.Get(context.TODO(), types.NamespacedName{Name: "restores.backup.appuio.ch"}, crdv1alpha1); err != nil {
		if err := IgnoreNotFound(err); err != nil {
			return k8upv1alpha1Exists, k8upv1Exists, err
		}
	}
	if crdv1alpha1.ObjectMeta.Name == "restores.backup.appuio.ch" {
		k8upv1alpha1Exists = true
	}
	crdv1 := &apiextensionsv1.CustomResourceDefinition{}
	if err := c.Get(context.TODO(), types.NamespacedName{Name: "restores.k8up.io"}, crdv1); err != nil {
		if err := IgnoreNotFound(err); err != nil {
			return k8upv1alpha1Exists, k8upv1Exists, err
		}
	}
	if crdv1.ObjectMeta.Name == "restores.k8up.io" {
		k8upv1Exists = true
	}
	return k8upv1alpha1Exists, k8upv1Exists, nil
}
