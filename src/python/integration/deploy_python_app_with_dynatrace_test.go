package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cloudfoundry/libbuildpack/cutlass"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("CF Python Buildpack", func() {
	var (
		app                   *cutlass.App
		createdServices       []string
		dynatraceAPI          *cutlass.App
		dynatraceAPIURI       string
		dynatraceBrokenAPI    *cutlass.App
		dynatraceBrokenAPIURI string
	)

	BeforeEach(func() {
		if isSerialTest {
			Skip("Skipping parallel tests")
		}

		//API mocking App
		dynatraceAPI = cutlass.New(Fixtures("fake_dynatrace_api"))
		// TODO: remove this once go-buildpack runs on cflinuxfs4
		// This is done to have the dynatrace broker app written in go up and running
		if os.Getenv("CF_STACK") == "cflinuxfs4" {
			dynatraceAPI.Stack = "cflinuxfs3"
		}
		dynatraceAPI.SetEnv("BP_DEBUG", "true")

		Expect(dynatraceAPI.Push()).To(Succeed())
		Eventually(func() ([]string, error) { return dynatraceAPI.InstanceStates() }, 60*time.Second).Should(Equal([]string{"RUNNING"}))

		var err error
		dynatraceAPIURI, err = dynatraceAPI.GetUrl("")
		Expect(err).NotTo(HaveOccurred())

		//API mocking App with broken processmoduleconfig Endpoint
		dynatraceBrokenAPI = cutlass.New(Fixtures("fake_dynatrace_api"))

		// TODO: remove this once go-buildpack runs on cflinuxfs4
		// This is done to have the dynatrace broker app written in go up and running
		if os.Getenv("CF_STACK") == "cflinuxfs4" {
			dynatraceBrokenAPI.Stack = "cflinuxfs3"
		}
		dynatraceBrokenAPI.SetEnv("BP_DEBUG", "true")
		dynatraceBrokenAPI.SetEnv("API_CONNECTION_FAIL", "true")

		Expect(dynatraceBrokenAPI.Push()).To(Succeed())
		Eventually(func() ([]string, error) { return dynatraceBrokenAPI.InstanceStates() }, 60*time.Second).Should(Equal([]string{"RUNNING"}))

		dynatraceBrokenAPIURI, err = dynatraceBrokenAPI.GetUrl("")
		Expect(err).NotTo(HaveOccurred())

		app = cutlass.New(filepath.Join(bpDir, "fixtures", "flask"))
		app.SetEnv("BP_DEBUG", "true")
		PushAppAndConfirm(app)

		createdServices = make([]string, 0)
	})

	AfterEach(func() {
		if app != nil {
			app.Destroy()
		}
		app = nil

		for _, service := range createdServices {
			command := exec.Command("cf", "delete-service", "-f", service)
			_, err := command.Output()
			Expect(err).To(BeNil())
		}

		if dynatraceAPI != nil {
			dynatraceAPI.Destroy()
		}
		dynatraceAPI = nil

		if dynatraceBrokenAPI != nil {
			dynatraceBrokenAPI.Destroy()
		}
		dynatraceBrokenAPI = nil
	})

	Context("deploying a Python app with Dynatrace agent with single credentials service", func() {
		It("checks if Dynatrace injection was successful", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)

			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace service credentials found. Setting up Dynatrace OneAgent."))
			Expect(app.Stdout.String()).To(ContainSubstring("Starting Dynatrace OneAgent installer"))
			Expect(app.Stdout.String()).To(ContainSubstring("Copy dynatrace-env.sh"))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent installed."))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent injection is set up."))
		})
	})

	Context("deploying a Python app with Dynatrace agent with two credentials services", func() {
		It("checks if detection of second service with credentials works", func() {
			CredentialsServiceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", CredentialsServiceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, CredentialsServiceName)

			duplicateCredentialsServiceName := "dynatrace-dupe-" + cutlass.RandStringRunes(20) + "-service"
			command = exec.Command("cf", "cups", duplicateCredentialsServiceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, duplicateCredentialsServiceName)

			command = exec.Command("cf", "bind-service", app.Name, CredentialsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			command = exec.Command("cf", "bind-service", app.Name, duplicateCredentialsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.Stdout.String()).To(ContainSubstring("More than one matching service found!"))
		})
	})

	Context("deploying a Python app with Dynatrace agent with failing agent download and ignoring errors", func() {
		It("checks if skipping download errors works", func() {
			CredentialsServiceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", CredentialsServiceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s/no-such-endpoint\",\"environmentid\":\"envid\",\"skiperrors\":\"true\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, CredentialsServiceName)

			command = exec.Command("cf", "bind-service", app.Name, CredentialsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.Stdout.String()).To(ContainSubstring("Download returned with status 404"))
			Expect(app.Stdout.String()).To(ContainSubstring("Error during installer download, skipping installation"))
		})
	})

	Context("deploying a Python app with Dynatrace agent with two dynatrace services", func() {
		It("check if service detection isn't disturbed by a service with tags", func() {
			CredentialsServiceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", CredentialsServiceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, CredentialsServiceName)

			tagsServiceName := "dynatrace-tags-" + cutlass.RandStringRunes(20) + "-service"
			command = exec.Command("cf", "cups", tagsServiceName, "-p", "'{\"tag:dttest\":\"dynatrace_test\"}'")
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, tagsServiceName)

			command = exec.Command("cf", "bind-service", app.Name, CredentialsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			command = exec.Command("cf", "bind-service", app.Name, tagsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace service credentials found. Setting up Dynatrace OneAgent."))
			Expect(app.Stdout.String()).To(ContainSubstring("Starting Dynatrace OneAgent installer"))
			Expect(app.Stdout.String()).To(ContainSubstring("Copy dynatrace-env.sh"))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent installed."))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent injection is set up."))
		})
	})

	Context("deploying a Python app with Dynatrace agent with single credentials service and without manifest.json", func() {
		It("checks if Dynatrace injection was successful", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)

			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace service credentials found. Setting up Dynatrace OneAgent."))
			Expect(app.Stdout.String()).To(ContainSubstring("Starting Dynatrace OneAgent installer"))
			Expect(app.Stdout.String()).To(ContainSubstring("Copy dynatrace-env.sh"))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent installed."))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent injection is set up."))
		})
	})

	Context("deploying a Python app with Dynatrace agent with failing agent download and checking retry", func() {
		It("checks if retrying downloads works", func() {
			CredentialsServiceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", CredentialsServiceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s/no-such-endpoint\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, CredentialsServiceName)

			command = exec.Command("cf", "bind-service", app.Name, CredentialsServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.CombinedOutput()

			Eventually(app.Stdout.String).Should(ContainSubstring("Error during installer download, retrying in 4s"))
			Eventually(app.Stdout.String).Should(ContainSubstring("Error during installer download, retrying in 5s"))
			Eventually(app.Stdout.String).Should(ContainSubstring("Error during installer download, retrying in 7s"))
			Eventually(app.Stdout.String).Should(ContainSubstring("Download returned with status 404"))

			Eventually(app.Stdout.String).Should(ContainSubstring("Failed to compile droplet"))
		})
	})

	Context("deploying a Python app with Dynatrace agent with single credentials service and a redis service", func() {
		It("checks if Dynatrace injection was successful", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)
			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			redisServiceName := "redis-" + cutlass.RandStringRunes(20) + "-service"
			command = exec.Command("cf", "cups", redisServiceName, "-p", "'{\"name\":\"redis\", \"credentials\":{\"db_type\":\"redis\", \"instance_administration_api\":{\"deployment_id\":\"12345asdf\", \"instance_id\":\"12345asdf\", \"root\":\"https://doesnotexi.st\"}}}'")
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, redisServiceName)
			command = exec.Command("cf", "bind-service", app.Name, redisServiceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace service credentials found. Setting up Dynatrace OneAgent."))
			Expect(app.Stdout.String()).To(ContainSubstring("Starting Dynatrace OneAgent installer"))
			Expect(app.Stdout.String()).To(ContainSubstring("Copy dynatrace-env.sh"))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent installed."))
			Expect(app.Stdout.String()).To(ContainSubstring("Dynatrace OneAgent injection is set up."))
		})
	})

	Context("deploying a Python app with Dynatrace OneAgent with single credentials service", func() {
		It("checks if agent config update via API was successful", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)
			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Fetching updated OneAgent configuration from tenant..."))
			Expect(app.Stdout.String()).To(ContainSubstring("Finished writing updated OneAgent config back to"))
		})
	})

	Context("deploying a Python app with Dynatrace OneAgent being able to contact the API to fetch the updated OneAgent config", func() {
		It("checks if agent config update via API was successful", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"
			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)
			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Fetching updated OneAgent configuration from tenant..."))
			Expect(app.Stdout.String()).To(ContainSubstring("Successfully fetched updated OneAgent config from the API"))
		})
	})

	Context("deploying a Python app with Dynatrace OneAgent not being able to contact the API to fetch the updated OneAgent config", func() {
		It("checks if agent config update via API fails soft", func() {
			serviceName := "dynatrace-" + cutlass.RandStringRunes(20) + "-service"

			command := exec.Command("cf", "cups", serviceName, "-p", fmt.Sprintf("'{\"apitoken\":\"secretpaastoken\",\"apiurl\":\"%s\",\"environmentid\":\"envid\"}'", dynatraceBrokenAPIURI))
			_, err := command.CombinedOutput()
			Expect(err).To(BeNil())
			createdServices = append(createdServices, serviceName)
			command = exec.Command("cf", "bind-service", app.Name, serviceName)
			_, err = command.CombinedOutput()
			Expect(err).To(BeNil())

			command = exec.Command("cf", "restage", app.Name)
			_, err = command.Output()
			Expect(err).To(BeNil())

			Expect(app.ConfirmBuildpack(buildpackVersion)).To(Succeed())
			Expect(app.Stdout.String()).To(ContainSubstring("Fetching updated OneAgent configuration from tenant..."))
			Expect(app.Stdout.String()).To(ContainSubstring("Failed to fetch updated OneAgent config from the API"))
		})
	})
})
