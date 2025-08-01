/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/uselagoon/remote-controller/test/utils"
)

const (
	namespace = "remote-controller-system"
	timeout   = "600s"
)

var (
	// projectimage stores the name of the image used in the example
	projectimage     = "example.com/remote-controller:v0.0.1"
	testtaskimage    = "example.com/test-task-image:v0.0.1"
	harborversion    string
	builddeployimage string

	duration = 600 * time.Second
	interval = 1 * time.Second

	metricLabels = []string{
		"lagoon_builds_cancelled_total",
		"lagoon_builds_completed_total",
		"lagoon_builds_failed_total",
		"lagoon_builds_pending_current",
		"lagoon_builds_running_current",
		"lagoon_builds_started_total",
		"lagoon_tasks_cancelled_total",
		"lagoon_tasks_completed_total",
		"lagoon_tasks_failed_total",
		"lagoon_tasks_running_current",
		"lagoon_tasks_started_total",
	}

	createPolicyWant        = `{"algorithm":"or","rules":[{"action":"retain","params":{"latestPulledN":3},"scope_selectors":{"repository":[{"decoration":"repoMatches","kind":"doublestar","pattern":"[^pr\\-]*/*"}]},"tag_selectors":[{"decoration":"matches","extras":"{\"untagged\":true}","kind":"doublestar","pattern":"**"}],"template":"latestPulledN"},{"action":"retain","params":{"latestPulledN":1},"scope_selectors":{"repository":[{"decoration":"repoMatches","kind":"doublestar","pattern":"pr-*"}]},"tag_selectors":[{"decoration":"matches","extras":"{\"untagged\":true}","kind":"doublestar","pattern":"**"}],"template":"latestPulledN"}],"scope":{"level":"project"},"trigger":{"kind":"Schedule","settings":{"cron":"0 3 3 * * 3"}}}`
	deletePolicyWant        = `{"algorithm":"or","rules":[],"scope":{"level":"project"},"trigger":{"kind":"Schedule","settings":{"cron":""}}}`
	projectRepositoriesWant = `[{"artifact_count":1,"name":"nginx-example/dev8/nginx"},{"artifact_count":1,"name":"nginx-example/dev7/nginx"},{"artifact_count":1,"name":"nginx-example/dev6/nginx"},{"artifact_count":1,"name":"nginx-example/dev5/nginx"},{"artifact_count":1,"name":"nginx-example/dev4/nginx"},{"artifact_count":1,"name":"nginx-example/dev3/nginx"},{"artifact_count":1,"name":"nginx-example/dev2/nginx"},{"artifact_count":1,"name":"nginx-example/dev1/nginx"},{"artifact_count":1,"name":"nginx-example/main/nginx"}]`
)

func init() {
	harborversion = os.Getenv("HARBOR_VERSION")
	builddeployimage = os.Getenv("OVERRIDE_BUILD_DEPLOY_DIND_IMAGE")
}

var _ = Describe("controller", Ordered, func() {
	// get the ingress lb ip for use later
	ip, err := utils.GetIngressLB()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	BeforeAll(func() {
		By("start local services")
		Expect(utils.StartLocalServices()).To(Succeed())

		By("removing manager namespace")
		cmd := exec.Command(utils.Kubectl(), "delete", "ns", namespace)
		_, _ = utils.Run(cmd)

		By("creating manager namespace")
		cmd = exec.Command(utils.Kubectl(), "create", "ns", namespace)
		_, _ = utils.Run(cmd)

		By("create broker secret")
		cmd = exec.Command(utils.Kubectl(), "-n", namespace, "create", "secret", "generic", "lagoon-broker-tls",
			"--from-file=tls.crt=local-dev/certificates/clienttls.crt",
			"--from-file=tls.key=local-dev/certificates/clienttls.key",
			"--from-file=ca.crt=local-dev/certificates/ca.crt")
		_, _ = utils.Run(cmd)

		// when running a re-test, it is best to make sure the old namespace doesn't exist
		By("removing existing test resources")
		// remove the old namespace
		utils.CleanupNamespace("nginx-example-main")
		utils.CleanupNamespace("nginx-example-dev1")
		utils.CleanupNamespace("nginx-example-dev2")
		utils.CleanupNamespace("nginx-example-dev3")
		utils.CleanupNamespace("nginx-example-dev4")
		utils.CleanupNamespace("nginx-example-dev5")
		utils.CleanupNamespace("nginx-example-dev6")
		utils.CleanupNamespace("nginx-example-dev7")
		utils.CleanupNamespace("nginx-example-dev8")

		By("delete harbor project")
		_ = utils.DeleteHarborProject(ip, "nginx-example")

		// clean up the k8up crds
		utils.UninstallK8upCRDs()
	})

	// comment to prevent cleaning up controller namespace and local services
	AfterAll(func() {
		By("dump controller logs")
		cmd := exec.Command(utils.Kubectl(), "get",
			"pods", "-l", "control-plane=controller-manager",
			"-o", "go-template={{ range .items }}"+
				"{{ if not .metadata.deletionTimestamp }}"+
				"{{ .metadata.name }}"+
				"{{ \"\\n\" }}{{ end }}{{ end }}",
			"-n", namespace,
		)
		podOutput, err := utils.Run(cmd)
		if err == nil {
			podNames := utils.GetNonEmptyLines(string(podOutput))
			controllerPodName := podNames[0]
			cmd = exec.Command(utils.Kubectl(), "logs",
				controllerPodName, "-c", "manager",
				"-n", namespace,
			)
			podlogs, err := utils.Run(cmd)
			if err == nil {
				fmt.Fprintf(GinkgoWriter, "info: %s\n", podlogs)
				_ = os.WriteFile("logs.txt", []byte(podlogs), 0644)
			}
			// cmd = exec.Command(utils.Kubectl(), "logs",
			// 	controllerPodName, "-c", "manager",
			// 	"-n", namespace, "--previous",
			// )
			// podlogs, err = utils.Run(cmd)
			// if err == nil {
			// 	fmt.Fprintf(GinkgoWriter, "info: previous %s\n", podlogs)
			// }
		}

		By("stop metrics consumer")
		utils.StopMetricsConsumer()

		By("removing manager namespace")
		cmd = exec.Command(utils.Kubectl(), "delete", "ns", namespace)
		_, _ = utils.Run(cmd)

		By("stop local services")
		utils.StopLocalServices()
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			// start tests
			var controllerPodName string
			var err error

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the test-task-image image on Kind")
			err = utils.LoadImageToKindClusterWithName(testtaskimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage), fmt.Sprintf("OVERRIDE_BUILD_DEPLOY_DIND_IMAGE=%s", builddeployimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())

			By("start metrics consumer")
			Expect(utils.StartMetricsConsumer()).To(Succeed())

			time.Sleep(10 * time.Second)

			By("validating that lagoonbuilds are working")
			for _, name := range []string{"7m5zypx", "8m5zypx", "9m5zypx"} {
				if name == "9m5zypx" {
					By("creating a LagoonBuild resource via rabbitmq")
					err = utils.PublishMessage(fmt.Sprintf("@test/e2e/testdata/lagoon-build-%s.json", name))
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				} else {
					By("creating a LagoonBuild resource")
					cmd = exec.Command(
						utils.Kubectl(),
						"apply",
						"-f",
						fmt.Sprintf("test/e2e/testdata/lagoon-build-%s.yaml", name),
					)
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				}

				time.Sleep(10 * time.Second)

				By("validating that the LagoonBuild build pod is created")
				cmd = exec.Command(
					utils.Kubectl(),
					"-n", "nginx-example-main",
					"wait",
					"--for=condition=Ready",
					"pod",
					fmt.Sprintf("lagoon-build-%s", name),
					fmt.Sprintf("--timeout=%s", timeout),
				)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				By("validating that the lagoon-build pod completes as expected")
				verifyBuildPodCompletes := func() error {
					// Validate pod status
					cmd = exec.Command(utils.Kubectl(), "get",
						"pods", fmt.Sprintf("lagoon-build-%s", name), "-o", "jsonpath={.status.phase}",
						"-n", "nginx-example-main",
					)
					status, err := utils.Run(cmd)
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					if string(status) != "Succeeded" {
						return fmt.Errorf("controller pod in %s status", status)
					}
					return nil
				}
				EventuallyWithOffset(1, verifyBuildPodCompletes, duration, interval).Should(Succeed())

				if name == "8m5zypx" {
					By("validating that the namespace has organization name label")
					cmd = exec.Command(
						utils.Kubectl(),
						"get",
						"namespace",
						"-l",
						"organization.lagoon.sh/name=test-org",
					)
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
					By("validating that the namespace has organization id label")
					cmd = exec.Command(
						utils.Kubectl(),
						"get",
						"namespace",
						"-l",
						"organization.lagoon.sh/id=123",
					)
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				}
			}

			By("validating that only 1 build pod remains in a namespace")
			verifyOnlyOneBuildPod := func() error {
				cmd = exec.Command(utils.Kubectl(), "get",
					"pods", "-l", "lagoon.sh/jobType=build",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", "nginx-example-main",
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				// if there is more than 1 build, the check has failed
				if len(podNames) > 1 {
					return fmt.Errorf("expect 1 build pod, but got %d", len(podNames))
				}
				return nil
			}
			EventuallyWithOffset(1, verifyOnlyOneBuildPod, duration, interval).Should(Succeed())

			By("validating that LagoonTasks are working")
			for _, name := range []string{"1m5zypx", "7m5zypx"} {
				if name == "1m5zypx" {
					By("creating dynamic secret resource")
					cmd = exec.Command(
						utils.Kubectl(),
						"apply",
						"-f",
						fmt.Sprintf("test/e2e/testdata/dynamic-secret-%s.yaml", name),
					)
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())

					By("creating a LagoonTask resource")
					cmd = exec.Command(
						utils.Kubectl(),
						"apply",
						"-f",
						fmt.Sprintf("test/e2e/testdata/lagoon-task-%s.yaml", name),
					)
					_, err = utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				} else {
					By("creating a LagoonTask resource via rabbitmq")
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
						fmt.Sprintf("@test/e2e/testdata/lagoon-task-%s.json", name),
						"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
					)
					_, err := utils.Run(cmd)
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				}
				time.Sleep(10 * time.Second)

				By("validating that the lagoon-task pod completes as expected")
				verifyTaskPodCompletes := func() error {
					// Validate pod status
					cmd = exec.Command(utils.Kubectl(), "get",
						"pods", fmt.Sprintf("lagoon-task-%s", name), "-o", "jsonpath={.status.phase}",
						"-n", "nginx-example-main",
					)
					status, err := utils.Run(cmd)
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					if string(status) != "Succeeded" {
						return fmt.Errorf("controller pod in %s status", status)
					}
					return nil
				}
				EventuallyWithOffset(1, verifyTaskPodCompletes, duration, interval).Should(Succeed())

				if name == "1m5zypx" {
					By("validating that the dynamic secret is mounted")
					cmd = exec.Command(utils.Kubectl(), "get",
						"pods", fmt.Sprintf("lagoon-task-%s", name), "-o", "jsonpath={.spec.containers[0].volumeMounts}",
						"-n", "nginx-example-main",
					)
					volumes, err := utils.Run(cmd)
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					ExpectWithOffset(2, volumes).Should(ContainSubstring(fmt.Sprintf("/var/run/secrets/lagoon/dynamic/dynamic-secret-%s", name)))
				}
			}

			By("validating that restore tasks are working")
			restores := map[string]string{
				"k8up-v1alpha1": "restore-bf072a0-uqxqo3",
				"k8up-v1":       "restore-bf072a0-uqxqo4",
			}
			for name, restore := range restores {
				By(fmt.Sprintf("installing %s crds", name))
				err := utils.InstallK8upCRD(name)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				time.Sleep(10 * time.Second)

				By(fmt.Sprintf("creating a %s restore task via rabbitmq", name))
				err = utils.PublishMessage(fmt.Sprintf("@test/e2e/testdata/%s-restore.json", name))
				ExpectWithOffset(1, err).NotTo(HaveOccurred())

				time.Sleep(10 * time.Second)

				By("validating that the restore is created")
				restoreversion := "restores.k8up.io"
				tmpl, err := os.ReadFile("test/e2e/testdata/results/restore.tpl")
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
				if name == "k8up-v1alpha1" {
					restoreversion = "restores.backup.appuio.ch"
				}
				cmd = exec.Command(utils.Kubectl(), "get",
					restoreversion, restore,
					"-n", "nginx-example-main", "-o", fmt.Sprintf("go-template=%s", string(tmpl)),
				)
				result, err := utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
				testResult, err := os.ReadFile(fmt.Sprintf("test/e2e/testdata/results/%s.yaml", name))
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
				Expect(strings.TrimSpace(string(result))).To(Equal(string(testResult)))
			}

			By("validating that the harbor robot credentials get rotated successfully")
			cmd = exec.Command(utils.Kubectl(), "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)
			podOutput, err := utils.Run(cmd)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			podNames := utils.GetNonEmptyLines(string(podOutput))
			controllerPodName = podNames[0]
			ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))
			verifyRobotCredentialsRotate := func() error {
				cmd = exec.Command(utils.Kubectl(), "logs",
					controllerPodName, "-c", "manager",
					"-n", namespace,
				)
				podlogs, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if !strings.Contains(string(podlogs), "Robot credentials rotated for nginx-example-main") {
					return fmt.Errorf("robot credentials not rotated yet")
				}
				return nil
			}
			EventuallyWithOffset(1, verifyRobotCredentialsRotate, duration, interval).Should(Succeed())

			// this tests the build qos functionality by starting 4 builds with a qos of max 3 (defined in the controller config provided by kustomize)
			testBuildQoS(timeout, duration, interval)

			// test the harbor policy cleanup process
			By("check harbor project policies before creating policy")
			_, err = utils.QueryHarborProjectPolicies(ip, "nginx-example")
			if err != nil {
				if !strings.Contains(err.Error(), "project metadata value is empty: retention_id") {
					ExpectWithOffset(1, err).NotTo(HaveOccurred())
				}
			}
			By("creating harbor policy update via rabbitmq")
			err = utils.PublishMessage("@test/e2e/testdata/create-retention-policy.json")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			time.Sleep(10 * time.Second)
			By("check harbor project policies after creating policy")
			after, err := utils.QueryHarborProjectPolicies(ip, "nginx-example")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			err = comparePolicy(createPolicyWant, after)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("delete harbor policy via rabbitmq")
			err = utils.PublishMessage("@test/e2e/testdata/remove-retention-policy.json")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			time.Sleep(10 * time.Second)
			By("check harbor project policies after deleting policy")
			after, err = utils.QueryHarborProjectPolicies(ip, "nginx-example")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			err = comparePolicy(deletePolicyWant, after)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("check harbor project repositories before deleting environment")
			before, err := utils.QueryHarborRepositories(ip, "nginx-example")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			err = compareRepositories(projectRepositoriesWant, before)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			time.Sleep(5 * time.Second)

			// this tests the task qos functionality by starting 4 tasks with a qos of max 3 (defined in the controller config provided by kustomize)
			testTaskQoS(timeout, duration, interval)

			time.Sleep(5 * time.Second)

			By("delete environments via rabbitmq")
			for _, env := range []string{"main", "dev1", "dev2", "dev3", "dev4", "dev5", "dev6", "dev7", "dev8"} {
				cmd = exec.Command(
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
					fmt.Sprintf("@test/e2e/testdata/remove-environment-%s.json", env),
					"http://172.17.0.1:15672/api/exchanges/%2f/lagoon-tasks/publish",
				)
				_, err = utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			}
			By("validating that the namespaces are deleted")
			for _, env := range []string{"main", "dev1", "dev2", "dev3", "dev4", "dev5", "dev6", "dev7", "dev8"} {
				verifyNamespaceRemoved := func() error {
					cmd = exec.Command(utils.Kubectl(), "get",
						"namespace", fmt.Sprintf("nginx-example-%s", env),
						"-o", "jsonpath={.status.phase}",
					)
					status, err := utils.Run(cmd)
					if err == nil {
						ExpectWithOffset(2, err).NotTo(HaveOccurred())
						if string(status) == "Active" || string(status) == "Terminating" {
							return fmt.Errorf("namespace in %s status\n", status)
						}
					}
					return nil
				}
				EventuallyWithOffset(1, verifyNamespaceRemoved, duration, interval).Should(Succeed())
			}

			By("check harbor project repositories after deleting environment")
			after, err = utils.QueryHarborRepositories(ip, "nginx-example")
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			err = compareRepositories("null", after)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that unauthenticated metrics requests fail")
			runCmd := `curl -s -k https://remote-controller-controller-manager-metrics-service.remote-controller-system.svc.cluster.local:8443/metrics | grep -v "#" | grep "lagoon_"`
			_, err = utils.RunCommonsCommand(namespace, runCmd)
			ExpectWithOffset(2, err).To(HaveOccurred())

			By("validating that authenticated metrics requests succeed with metrics")
			runCmd = `curl -s -k -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" https://remote-controller-controller-manager-metrics-service.remote-controller-system.svc.cluster.local:8443/metrics | grep -v "#" | grep "lagoon_"`
			output, err := utils.RunCommonsCommand(namespace, runCmd)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			fmt.Printf("metrics: %s", string(output))
			err = utils.CheckStringContainsStrings(string(output), metricLabels)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			// End tests
		})
	})
})

func comparePolicy(want, got string) error {
	// this removes the next scheduled time from the payload as it can vary
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		return err
	}
	delete(m["scope"].(map[string]interface{}), "ref")
	delete(m["trigger"].(map[string]interface{})["settings"].(map[string]interface{}), "next_scheduled_time")
	delete(m, "id")
	p, err := json.Marshal(m)
	if err != nil {
		return err
	}

	if want != string(p) {
		return fmt.Errorf("resulting policies don't match:\nwant: %s\ngot: %s", want, string(p))
	}
	return nil
}

type HarborProjectRepository struct {
	ArtifactCount int64  `json:"artifact_count"`
	Name          string `json:"name"`
	PullCount     int64  `json:"pull_count"`
}

func compareRepositories(want, got string) error {
	if got == "null" && want == "null" {
		return nil
	}
	// this removes the next scheduled time from the payload as it can vary
	var m []interface{}
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		return err
	}
	for idx := range m {
		delete(m[idx].(map[string]interface{}), "id")
		delete(m[idx].(map[string]interface{}), "creation_time")
		delete(m[idx].(map[string]interface{}), "update_time")
		delete(m[idx].(map[string]interface{}), "project_id")
		delete(m[idx].(map[string]interface{}), "pull_count")
	}
	p, err := json.Marshal(m)
	if err != nil {
		return err
	}
	hpg := []HarborProjectRepository{}
	_ = json.Unmarshal(p, &hpg)

	hpw := []HarborProjectRepository{}
	_ = json.Unmarshal([]byte(want), &hpw)

	sort.Slice(hpg, func(i, j int) bool {
		return hpg[i].Name < hpg[j].Name
	})
	sort.Slice(hpw, func(i, j int) bool {
		return hpw[i].Name < hpw[j].Name
	})

	hpgb, _ := json.Marshal(hpg)
	hpwb, _ := json.Marshal(hpw)
	if string(hpwb) != string(hpgb) {
		return fmt.Errorf("resulting project respostories don't match:\nwant: %s\ngot: %s", string(hpwb), string(hpgb))
	}
	return nil
}
