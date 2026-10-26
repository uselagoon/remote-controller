package helpers

import (
	"context"
	"testing"

	"github.com/uselagoon/machinery/api/schema"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetServiceReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	two := int32(2)
	newDep := func(name, svc string, replicas *int32) *appsv1.Deployment {
		labels := map[string]string{}
		if svc != "" {
			labels["lagoon.sh/service"] = svc
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: labels},
			Spec:       appsv1.DeploymentSpec{Replicas: replicas},
		}
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		newDep("a", "cli", &two),
		newDep("b", "nginx", nil),
		newDep("c", "", &two),
	).Build()

	services := []schema.EnvironmentService{
		{Name: "cli", Replicas: 0},
		{Name: "nginx", Replicas: 0},
		{Name: "other", Replicas: 5},
	}
	SetServiceReplicas(context.Background(), reader, "ns", services)

	expect := map[string]int32{"cli": 2, "nginx": 1, "other": 5}
	for _, svc := range services {
		if svc.Replicas != expect[svc.Name] {
			t.Errorf("service %s: expected %d replicas, got %d", svc.Name, expect[svc.Name], svc.Replicas)
		}
	}
}
