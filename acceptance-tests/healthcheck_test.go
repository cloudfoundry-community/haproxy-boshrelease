package acceptance_tests

import (
	"context"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var _ = Describe("HTTP Health Check", func() {
	AfterEach(func() {
		deleteDeployment()
	})

	It("Correctly fails to start if there is no healthy backend", func() {
		opsfileHTTPHealthcheck := `---
# Enable health check
- type: replace
  path: /instance_groups/name=haproxy/jobs/name=haproxy/properties/ha_proxy/enable_health_check_http?
  value: true
`
		haproxyBackendPort := 12000
		haproxyInfo, _ := deployHAProxy(haproxyBackendPort, []string{opsfileHTTPHealthcheck}, map[string]interface{}{})

		dumpHAProxyConfig(haproxyInfo)

		// FIXME: this behaviour will change once https://github.com/cloudfoundry-incubator/haproxy-boshrelease/issues/195
		// is fixed
		By("Waiting monit to report HAProxy is now unhealthy (due to no healthy backend instances)")
		// BOSH initially thinks that the process is healthy, but later switches to 'failing' when
		// monit reports the failing health check due to no healthy backend instances.
		// We will up to wait one minute for the status to stabilise
		Eventually(func() string {
			return boshInstances()[0].ProcessState
		}, time.Minute, time.Second).Should(Equal("failing"))

		By("Starting a local http server to act as a backend")
		closeLocalServer, localPort, err := startLocalHTTPServer(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "Hello cloud foundry")
		})
		defer closeLocalServer()
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Creating a reverse SSH tunnel from HAProxy backend (port %d) to local HTTP server (port %d)", haproxyBackendPort, localPort))
		ctx, cancelFunc := context.WithCancel(context.Background())
		defer cancelFunc()
		err = startReverseSSHPortForwarder(haproxyInfo.SSHUser, haproxyInfo.PublicIP, haproxyInfo.SSHPrivateKey, haproxyBackendPort, localPort, ctx)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting monit to report HAProxy is now healthy (due to having a healthy backend instance)")
		// Since the backend is listening, HAProxy healthcheck should start returning healthy
		// and monit should in turn start reporting a healthy process
		// We will up to wait one minute for the status to stabilise
		Eventually(func() string {
			return boshInstances()[0].ProcessState
		}, time.Minute, time.Second).Should(Equal("running"))

		By("The healthcheck health endpoint should report a 200 status code")
		resp, err := http.Get(fmt.Sprintf("http://%s:8080/health", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		By("Sending a request to HAProxy works")
		resp, err = http.Get(fmt.Sprintf("http://%s", haproxyInfo.PublicIP))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Eventually(gbytes.BufferReader(resp.Body)).Should(gbytes.Say("Hello cloud foundry"))
	})
})
