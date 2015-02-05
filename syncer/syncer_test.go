package syncer_test

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/apcera/nats"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/receptor/fake_receptor"
	"github.com/cloudfoundry-incubator/route-emitter/cfroutes"
	"github.com/cloudfoundry-incubator/route-emitter/nats_emitter/fake_nats_emitter"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
	"github.com/cloudfoundry-incubator/route-emitter/routing_table/fake_routing_table"
	. "github.com/cloudfoundry-incubator/route-emitter/syncer"
	fake_metrics_sender "github.com/cloudfoundry/dropsonde/metric_sender/fake"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/gunk/diegonats"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const logGuid = "some-log-guid"

var _ = Describe("Syncer", func() {
	const (
		processGuid  = "process-guid-1"
		instanceGuid = "instance-guid-1"
		lrpHost      = "1.2.3.4"
	)

	var (
		receptorClient *fake_receptor.FakeClient
		natsClient     *diegonats.FakeNATSClient
		emitter        *fake_nats_emitter.FakeNATSEmitter
		table          *fake_routing_table.FakeRoutingTable
		syncer         *Syncer
		process        ifrit.Process
		syncMessages   routing_table.MessagesToEmit
		messagesToEmit routing_table.MessagesToEmit
		syncDuration   time.Duration

		desiredResponse receptor.DesiredLRPResponse
		actualResponses []receptor.ActualLRPResponse

		routerStartMessages chan<- *nats.Msg
		fakeMetricSender    *fake_metrics_sender.FakeMetricSender
	)

	BeforeEach(func() {
		receptorClient = new(fake_receptor.FakeClient)
		natsClient = diegonats.NewFakeClient()
		emitter = &fake_nats_emitter.FakeNATSEmitter{}
		table = &fake_routing_table.FakeRoutingTable{}
		syncDuration = 10 * time.Second

		startMessages := make(chan *nats.Msg)
		routerStartMessages = startMessages

		natsClient.WhenSubscribing("router.start", func(callback nats.MsgHandler) error {
			go func() {
				for msg := range startMessages {
					callback(msg)
				}
			}()

			return nil
		})

		//what follows is fake data to distinguish between
		//the "sync" and "emit" codepaths
		dummyEndpoint := routing_table.Endpoint{InstanceGuid: "instance-guid-1", Host: "1.1.1.1", Port: 11}
		dummyMessage := routing_table.RegistryMessageFor(dummyEndpoint, routing_table.Routes{URIs: []string{"foo.com", "bar.com"}, LogGuid: logGuid})
		syncMessages = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		dummyEndpoint = routing_table.Endpoint{InstanceGuid: "instance-guid-2", Host: "2.2.2.2", Port: 22}
		dummyMessage = routing_table.RegistryMessageFor(dummyEndpoint, routing_table.Routes{URIs: []string{"baz.com"}, LogGuid: logGuid})
		messagesToEmit = routing_table.MessagesToEmit{
			RegistrationMessages: []routing_table.RegistryMessage{dummyMessage},
		}

		table.SyncReturns(syncMessages)
		table.MessagesToEmitReturns(messagesToEmit)

		desiredResponse = receptor.DesiredLRPResponse{
			ProcessGuid: processGuid,
			Ports:       []uint16{8080},
			Routes:      cfroutes.CFRoutes{{Hostnames: []string{"route-1", "route-2"}, Port: 8080}}.RoutingInfo(),
			LogGuid:     logGuid,
		}

		actualResponses = []receptor.ActualLRPResponse{
			{
				ProcessGuid:  processGuid,
				InstanceGuid: instanceGuid,
				CellID:       "cell-id",
				Domain:       "domain",
				Index:        1,
				Address:      lrpHost,
				Ports: []receptor.PortMapping{
					{HostPort: 1234, ContainerPort: 8080},
				},
				State: receptor.ActualLRPStateRunning,
			},
			{
				Index: 0,
				State: receptor.ActualLRPStateUnclaimed,
			},
		}

		receptorClient.DesiredLRPsReturns([]receptor.DesiredLRPResponse{desiredResponse}, nil)
		receptorClient.ActualLRPsReturns(actualResponses, nil)

		fakeMetricSender = fake_metrics_sender.NewFakeMetricSender()
		metrics.Initialize(fakeMetricSender)
		table.RouteCountReturns(123)
	})

	JustBeforeEach(func() {
		logger := lagertest.NewTestLogger("test")
		syncer = NewSyncer(receptorClient, table, emitter, syncDuration, natsClient, logger)
		process = ifrit.Envoke(syncer)
	})

	AfterEach(func() {
		process.Signal(os.Interrupt)
		Eventually(process.Wait()).Should(Receive(BeNil()))
		close(routerStartMessages)
	})

	Describe("on startup", func() {
		It("should sync the table", func() {
			Ω(table.SyncCallCount()).Should(Equal(1))

			key := routing_table.RoutingKey{ProcessGuid: processGuid, ContainerPort: 8080}

			routes, endpoints := table.SyncArgsForCall(0)
			Ω(routes[key]).Should(Equal(routing_table.Routes{
				URIs:    []string{"route-1", "route-2"},
				LogGuid: logGuid,
			}))
			Ω(endpoints[key]).Should(Equal([]routing_table.Endpoint{
				{InstanceGuid: instanceGuid, Host: lrpHost, Port: 1234, ContainerPort: 8080},
			}))
			Ω(emitter.EmitCallCount()).Should(Equal(1))
			emittedMessages, _, _ := emitter.EmitArgsForCall(0)
			Ω(emittedMessages).Should(Equal(syncMessages))
		})
	})

	Describe("getting the heartbeat interval from the router", func() {
		var greetings chan *nats.Msg
		BeforeEach(func() {
			greetings = make(chan *nats.Msg, 3)
			natsClient.WhenPublishing("router.greet", func(msg *nats.Msg) error {
				greetings <- msg
				return nil
			})
		})

		Context("when the router emits a router.start", func() {
			JustBeforeEach(func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				}
			})

			It("should emit routes with the frequency of the passed-in-interval", func() {
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))
			})

			It("passes a 'synced routes' counter to Emit", func() {
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				_, registerCounter, _ := emitter.EmitArgsForCall(1)
				Expect(string(*registerCounter)).To(Equal("RoutesSynced"))
			})

			It("should only greet the router once", func() {
				Eventually(greetings).Should(Receive())
				Consistently(greetings, 1).ShouldNot(Receive())
			})

			It("sends a 'routes total' metric", func() {
				Eventually(func() float64 {
					return fakeMetricSender.GetValue("RoutesTotal").Value
				}, 2).Should(BeEquivalentTo(123))
			})
		})

		Context("when the router does not emit a router.start", func() {
			It("should keep greeting the router until it gets an interval", func() {
				//get the first greeting
				Eventually(greetings, 2).Should(Receive())

				//get the second greeting, and respond
				var msg *nats.Msg
				Eventually(greetings, 2).Should(Receive(&msg))
				go natsClient.Publish(msg.Reply, []byte(`{"minimumRegisterIntervalInSeconds":1}`))

				//should now be emitting regularly at the specified interval
				Eventually(emitter.EmitCallCount, 2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				Eventually(emitter.EmitCallCount, 2).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()

				Ω(t2.Sub(t1)).Should(BeNumerically("~", 1*time.Second, 200*time.Millisecond))

				//should no longer be greeting the router
				Consistently(greetings).ShouldNot(Receive())
			})
		})

		Context("after getting the first interval, when a second interval arrives", func() {
			JustBeforeEach(func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":1}`),
				}
			})

			It("should modify its update rate", func() {
				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":2}`),
				}

				//first emit should be pretty quick, it is in response to the incoming heartbeat interval
				Eventually(emitter.EmitCallCount, 0.2).Should(Equal(2))
				emittedMessages, _, _ := emitter.EmitArgsForCall(1)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t1 := time.Now()

				//subsequent emit should follow the interval
				Eventually(emitter.EmitCallCount, 3).Should(Equal(3))
				emittedMessages, _, _ = emitter.EmitArgsForCall(2)
				Ω(emittedMessages).Should(Equal(messagesToEmit))
				t2 := time.Now()
				Ω(t2.Sub(t1)).Should(BeNumerically("~", 2*time.Second, 200*time.Millisecond))
			})

			It("sends a 'routes total' metric", func() {
				Eventually(func() float64 {
					return fakeMetricSender.GetValue("RoutesTotal").Value
				}, 2*time.Second).Should(BeEquivalentTo(123))
			})
		})

		Context("if it never hears anything from a router anywhere", func() {
			It("should still be able to shutdown", func() {
				process.Signal(os.Interrupt)
				Eventually(process.Wait()).Should(Receive(BeNil()))
			})
		})
	})

	Describe("syncing", func() {
		BeforeEach(func() {
			receptorClient.ActualLRPsStub = func() ([]receptor.ActualLRPResponse, error) {
				time.Sleep(100 * time.Millisecond)
				return nil, nil
			}
			syncDuration = 500 * time.Millisecond
		})

		It("should sync on the specified interval", func() {
			//we set the emit interval real high to avoid colliding with our sync interval
			routerStartMessages <- &nats.Msg{
				Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
			}

			Eventually(table.SyncCallCount).Should(Equal(2))
			Eventually(emitter.EmitCallCount).Should(Equal(2))
			t1 := time.Now()

			Eventually(table.SyncCallCount).Should(Equal(3))
			Eventually(emitter.EmitCallCount).Should(Equal(3))
			t2 := time.Now()

			emittedMessages, _, _ := emitter.EmitArgsForCall(1)
			Ω(emittedMessages).Should(Equal(syncMessages))

			emittedMessages, _, _ = emitter.EmitArgsForCall(2)
			Ω(emittedMessages).Should(Equal(syncMessages))

			Ω(t2.Sub(t1)).Should(BeNumerically("~", 500*time.Millisecond, 100*time.Millisecond))
		})

		It("should emit the sync duration", func() {
			Eventually(func() float64 {
				return fakeMetricSender.GetValue("RouteEmitterSyncDuration").Value
			}, 10*time.Second).Should(BeNumerically(">=", 100*time.Millisecond))
		})

		It("sends a 'routes total' metric", func() {
			Eventually(func() float64 {
				return fakeMetricSender.GetValue("RoutesTotal").Value
			}).Should(BeEquivalentTo(123))
		})

		It("passes a 'synced routes' counter to Emit", func() {
			Eventually(emitter.EmitCallCount).Should(Equal(1))
			_, registerCounter, _ := emitter.EmitArgsForCall(0)
			Expect(string(*registerCounter)).To(Equal("RoutesSynced"))
		})

		Context("when fetching actuals fails", func() {
			BeforeEach(func() {
				lock := &sync.Mutex{}
				calls := 0

				receptorClient.ActualLRPsStub = func() ([]receptor.ActualLRPResponse, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}

					return []receptor.ActualLRPResponse{}, nil
				}
			})

			It("should not call sync until the error resolves", func() {
				Ω(table.SyncCallCount()).Should(Equal(0))

				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
				}

				Eventually(table.SyncCallCount).Should(Equal(1))
			})
		})

		Context("when fetching desireds fails", func() {
			BeforeEach(func() {
				lock := &sync.Mutex{}
				calls := 0
				receptorClient.DesiredLRPsStub = func() ([]receptor.DesiredLRPResponse, error) {
					lock.Lock()
					defer lock.Unlock()
					if calls == 0 {
						calls++
						return nil, errors.New("bam")
					}

					return []receptor.DesiredLRPResponse{}, nil
				}
			})

			It("should not call sync until the error resolves", func() {
				Ω(table.SyncCallCount()).Should(Equal(0))

				routerStartMessages <- &nats.Msg{
					Data: []byte(`{"minimumRegisterIntervalInSeconds":10}`),
				}

				Eventually(table.SyncCallCount).Should(Equal(1))
			})
		})
	})
})
