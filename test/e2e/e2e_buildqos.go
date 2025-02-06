package e2e

import (
	"fmt"
	"os/exec"
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
		ginkgo.By("validating that the LagoonBuild build pod is created")
		cmd := exec.Command(
			utils.Kubectl(),
			"-n", build.Namespace,
			"wait",
			"--for=condition=Ready",
			"pod",
			fmt.Sprintf("lagoon-build-%s", build.Name),
			fmt.Sprintf("--timeout=%s", timeout),
		)
		_, err := utils.Run(cmd)
		if build.Name == "xpyz5m4" {
			// should fail because it gets queued at 3 builds max
			gomega.ExpectWithOffset(1, err).To(gomega.HaveOccurred())
			// then wait for the build pod to start
			verifyBuildRuns := func() error {
				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", fmt.Sprintf("lagoon-build-%s", build.Name),
					"-o", "jsonpath={.status.phase}",
					"-n", build.Namespace,
				)
				status, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if string(status) != "Running" {
					return fmt.Errorf("build pod in %s status", status)
				}
				return nil
			}
			// if this fails, then qos didn't start the pod for some reason in the duration available
			gomega.EventuallyWithOffset(1, verifyBuildRuns, duration, interval).Should(gomega.Succeed())

			ginkgo.By("validating that the lagoon-build pod completes as expected")
			verifyBuildPodCompletes := func() error {
				// Validate pod status
				cmd = exec.Command(utils.Kubectl(), "get",
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
		} else {
			// should pass because qos builds
			gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		}
	}

}
