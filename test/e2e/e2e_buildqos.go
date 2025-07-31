package e2e

import (
	"fmt"
	"os/exec"
	"slices"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/uselagoon/remote-controller/test/utils"
)

func testBuildQoS(timeout string, duration, interval time.Duration) {
	var builds = []struct {
		Name      string
		Namespace string
	}{
		{
			Name:      "xpyz5m1",
			Namespace: "nginx-example-main",
		},
		{
			Name:      "xpyz5m2",
			Namespace: "nginx-example-dev1",
		},
		{
			Name:      "xpyz5m3",
			Namespace: "nginx-example-dev2",
		},
		{
			Name:      "xpyz5m4",
			Namespace: "nginx-example-dev3",
		},
		{
			Name:      "xpyz5m5",
			Namespace: "nginx-example-dev4",
		},
		{
			Name:      "xpyz5m6",
			Namespace: "nginx-example-dev5",
		},
		{
			Name:      "xpyz5m7",
			Namespace: "nginx-example-dev6",
		},
		{
			Name:      "xpyz5m8",
			Namespace: "nginx-example-dev7",
		},
		{
			Name:      "xpyz5m9",
			Namespace: "nginx-example-dev8",
		},
	}

	ginkgo.By("validating that lagoonbuilds are queing")
	for _, build := range builds {
		ginkgo.By("creating a LagoonBuild resource via rabbitmq")
		cmd := exec.Command(
			"curl",
			"-s",
			"-u",
			"guest:guest",
			"-H",
			"'Accept: application/json'",
			"-H",
			"'Content-Type:application/json'",
			"-X",
			"POST",
			"-d",
			fmt.Sprintf("@test/e2e/testdata/buildqos/lagoon-build-%s.json", build.Name),
			"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
		)
		_, err := utils.Run(cmd)
		gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		time.Sleep(1 * time.Second)
	}
	time.Sleep(5 * time.Second)
	for _, build := range builds {
		if slices.Contains([]string{"xpyz5m1", "xpyz5m2", "xpyz5m3", "xpyz5m4", "xpyz5m5", "xpyz5m6", "xpyz5m7", "xpyz5m8", "xpyz5m9"}, build.Name) {
			ginkgo.By("validating that the LagoonBuild build pod is created")
			verifyBuildPodCreates := func() error {
				// Validate pod status
				cmd := exec.Command(
					utils.Kubectl(),
					"-n", build.Namespace,
					"wait",
					"--for=create",
					"pod",
					fmt.Sprintf("lagoon-build-%s", build.Name),
					fmt.Sprintf("--timeout=%s", timeout),
				)
				_, err := utils.Run(cmd)
				return err
			}
			gomega.EventuallyWithOffset(1, verifyBuildPodCreates, duration, interval).Should(gomega.Succeed())
			ginkgo.By("validating that the lagoon-build pod completes as expected")
			verifyBuildPodCompletes := func() error {
				// Validate pod status
				cmd := exec.Command(utils.Kubectl(), "get",
					"pods", fmt.Sprintf("lagoon-build-%s", build.Name), "-o", "jsonpath={.status.phase}",
					"-n", build.Namespace,
				)
				status, err := utils.Run(cmd)
				gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred())
				if string(status) != "Succeeded" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			gomega.EventuallyWithOffset(1, verifyBuildPodCompletes, duration, interval).Should(gomega.Succeed())
		}
	}

}
