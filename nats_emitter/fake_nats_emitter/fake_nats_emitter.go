// This file was generated by counterfeiter
package fake_nats_emitter

import (
	"sync"

	"github.com/cloudfoundry-incubator/route-emitter/routing_table"
)

type FakeNATSEmitter struct {
	EmitStub        func(messagesToEmit routing_table.MessagesToEmit) error
	emitMutex       sync.RWMutex
	emitArgsForCall []struct {
		arg1 routing_table.MessagesToEmit
	}
	emitReturns struct {
		result1 error
	}
}

func New() *FakeNATSEmitter {
	return &FakeNATSEmitter{}
}

func (fake *FakeNATSEmitter) Emit(arg1 routing_table.MessagesToEmit) error {
	fake.emitMutex.Lock()
	defer fake.emitMutex.Unlock()
	fake.emitArgsForCall = append(fake.emitArgsForCall, struct {
		arg1 routing_table.MessagesToEmit
	}{arg1})
	if fake.EmitStub != nil {
		return fake.EmitStub(arg1)
	} else {
		return fake.emitReturns.result1
	}
}

func (fake *FakeNATSEmitter) EmitCallCount() int {
	fake.emitMutex.RLock()
	defer fake.emitMutex.RUnlock()
	return len(fake.emitArgsForCall)
}

func (fake *FakeNATSEmitter) EmitArgsForCall(i int) routing_table.MessagesToEmit {
	fake.emitMutex.RLock()
	defer fake.emitMutex.RUnlock()
	return fake.emitArgsForCall[i].arg1
}

func (fake *FakeNATSEmitter) EmitReturns(result1 error) {
	fake.emitReturns = struct {
		result1 error
	}{result1}
}
