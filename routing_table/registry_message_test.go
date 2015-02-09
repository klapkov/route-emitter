package routing_table_test

import (
	"encoding/json"

	"github.com/cloudfoundry-incubator/route-emitter/routing_table"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RegistryMessage", func() {
	var expectedMessage routing_table.RegistryMessage

	BeforeEach(func() {
		expectedMessage = routing_table.RegistryMessage{
			Host:              "1.1.1.1",
			Port:              61001,
			URIs:              []string{"host-1.example.com", "host-2.example.com"},
			App:               "app-guid",
			PrivateInstanceId: "instance-guid",
		}
	})

	Describe("serialization", func() {
		var expectedJSON string

		BeforeEach(func() {
			expectedJSON = `{
				"host": "1.1.1.1",
				"port": 61001,
				"uris": ["host-1.example.com", "host-2.example.com"],
				"app" : "app-guid",
				"private_instance_id": "instance-guid"
			}`
		})

		It("marshals correctly", func() {
			payload, err := json.Marshal(expectedMessage)
			Ω(err).ShouldNot(HaveOccurred())

			Ω(payload).Should(MatchJSON(expectedJSON))
		})

		It("unmarshals correctly", func() {
			message := routing_table.RegistryMessage{}

			err := json.Unmarshal([]byte(expectedJSON), &message)
			Ω(err).ShouldNot(HaveOccurred())
			Ω(message).Should(Equal(expectedMessage))
		})
	})

	Describe("RegistryMessageFor", func() {
		It("creates a valid message from an endpoint and routes", func() {
			endpoint := routing_table.Endpoint{
				InstanceGuid:  "instance-guid",
				Host:          "1.1.1.1",
				Port:          61001,
				ContainerPort: 11,
			}
			routes := routing_table.Routes{
				Hostnames: []string{"host-1.example.com", "host-2.example.com"},
				LogGuid:   "app-guid",
			}

			message := routing_table.RegistryMessageFor(endpoint, routes)
			Ω(message).Should(Equal(expectedMessage))
		})
	})
})
