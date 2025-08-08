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

func testCancellations(timeout string, duration, interval time.Duration) {
	var builds = []struct {
		Name      string
		Namespace string
	}{
		{
			Name:      "1m5apbc",
			Namespace: "nginx-example-main",
		},
		{
			Name:      "2m5apbc",
			Namespace: "nginx-example-main",
		},
		{
			Name:      "3m5apbc",
			Namespace: "nginx-example-main",
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
			fmt.Sprintf("@test/e2e/testdata/cancellation/lagoon-build-%s.json", build.Name),
			"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
		)
		_, err := utils.Run(cmd)
		gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		time.Sleep(3 * time.Second)
	}
	time.Sleep(5 * time.Second)
	for _, build := range builds {
		if slices.Contains([]string{"1m5apbc", "3m5apbc"}, build.Name) {
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
			if build.Name == "3m5apbc" {
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
						return fmt.Errorf("build pod in %s status", status)
					}
					return nil
				}
				gomega.EventuallyWithOffset(1, verifyBuildPodCompletes, duration, interval).Should(gomega.Succeed())
			}
		}
		if slices.Contains([]string{"2m5apbc"}, build.Name) {
			ginkgo.By("validating that the lagoon-build is cancelled as expected")
			verifyBuildPodCompletes := func() error {
				// Validate pod status
				cmd := exec.Command(utils.Kubectl(), "get",
					"lagoonbuilds", fmt.Sprintf("lagoon-build-%s", build.Name), "-o", "jsonpath={.status.phase}",
					"-n", build.Namespace,
				)
				status, err := utils.Run(cmd)
				gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred())
				if string(status) != "Cancelled" {
					return fmt.Errorf("lagoonbuild in %s status", status)
				}
				return nil
			}
			gomega.EventuallyWithOffset(1, verifyBuildPodCompletes, duration, interval).Should(gomega.Succeed())
		}
	}

}
