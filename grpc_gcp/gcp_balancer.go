/*
 *
 * Copyright 2018 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package grpc_gcp

import (
	"fmt"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/resolver"
	// "google.golang.org/grpc/metadata"
	// "runtime/debug"
)

// Name is the name of grpc_gcp balancer.
const Name = "grpc_gcp"

// Default settings for max pool size and max concurrent streams.
const defaultMaxConn = 10
const defaultMaxStream = 100

type gcpBalancerBuilder struct{
	name          string
	pickerBuilder *gcpPickerBuilder
	config        base.Config
}

func (bb *gcpBalancerBuilder) Build(cc balancer.ClientConn, opt balancer.BuildOptions) balancer.Balancer {
	return &gcpBalancer{
		cc:            cc,
		pickerBuilder: bb.pickerBuilder,

		affinityMap: make(map[string]*subConnRef),
		scRefs: make(map[balancer.SubConn]*subConnRef),
		csEvltr:  &connectivityStateEvaluator{},
		// Initialize picker to a picker that always return
		// ErrNoSubConnAvailable, because when state of a SubConn changes, we
		// may call UpdateBalancerState with this picker.
		picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable),
		config: bb.config,
	}
}

func (*gcpBalancerBuilder) Name() string {
	return Name
}

// newBuilder creates a new grpc_gcp balancer builder.
func newBuilder() balancer.Builder {
	return &gcpBalancerBuilder{
		name: Name,
		pickerBuilder: &gcpPickerBuilder{},
		config: base.Config{HealthCheck: true},
	}
}

// connectivityStateEvaluator gets updated by addrConns when their
// states transition, based on which it evaluates the state of
// ClientConn.
type connectivityStateEvaluator struct {
	numReady            uint64 // Number of addrConns in ready state.
	numConnecting       uint64 // Number of addrConns in connecting state.
	numTransientFailure uint64 // Number of addrConns in transientFailure.
}

// recordTransition records state change happening in every subConn and based on
// that it evaluates what aggregated state should be.
// It can only transition between Ready, Connecting and TransientFailure. Other states,
// Idle and Shutdown are transitioned into by ClientConn; in the beginning of the connection
// before any subConn is created ClientConn is in idle state. In the end when ClientConn
// closes it is in Shutdown state.
//
// recordTransition should only be called synchronously from the same goroutine.
func (cse *connectivityStateEvaluator) recordTransition(oldState, newState connectivity.State) connectivity.State {
	// Update counters.
	for idx, state := range []connectivity.State{oldState, newState} {
		updateVal := 2*uint64(idx) - 1 // -1 for oldState and +1 for new.
		switch state {
		case connectivity.Ready:
			cse.numReady += updateVal
		case connectivity.Connecting:
			cse.numConnecting += updateVal
		case connectivity.TransientFailure:
			cse.numTransientFailure += updateVal
		}
	}

	// Evaluate.
	if cse.numReady > 0 {
		return connectivity.Ready
	}
	if cse.numConnecting > 0 {
		return connectivity.Connecting
	}
	return connectivity.TransientFailure
}

type subConnRef struct {
	subConn     balancer.SubConn
	scState     connectivity.State
	affinityCnt uint32
	streamsCnt  uint32
}

type gcpBalancer struct {
	addrs         []resolver.Address
	cc            balancer.ClientConn
	pickerBuilder *gcpPickerBuilder

	csEvltr *connectivityStateEvaluator
	state   connectivity.State

	affinityMap map[string]*subConnRef
	scRefs      map[balancer.SubConn]*subConnRef

	picker   balancer.Picker
	config   base.Config
}

func (gb *gcpBalancer) HandleResolvedAddrs(addrs []resolver.Address, err error) {
	if err != nil {
		grpclog.Infof("grpc_gcp.gcpBalancer: HandleResolvedAddrs called with error %v", err)
		return
	}
	fmt.Printf("*** got resolved addresses: %+v\n", addrs)
	grpclog.Infoln("grpc_gcp.gcpBalancer: got new resolved addresses: ", addrs)
	gb.addrs = addrs

	if len(gb.scRefs) == 0 {
		gb.createNewSubConn()
	} else {
		for _, scRef := range gb.scRefs {
			// TODO(weiranf): update streams count when new addrs resolved?
			scRef.subConn.UpdateAddresses(addrs)
			scRef.subConn.Connect()
		}
	}
}

func (gb *gcpBalancer) createNewSubConn() {
	sc, err := gb.cc.NewSubConn(gb.addrs, balancer.NewSubConnOptions{HealthCheckEnabled: gb.config.HealthCheck})
	if err != nil {
		grpclog.Errorf("grpc_gcp.gcpBalancer: failed to NewSubConn: %v", err)
		return
	}
	gb.scRefs[sc] = &subConnRef{
		subConn: sc,
		scState: connectivity.Idle,
		streamsCnt: 0,
		affinityCnt: 0,
	}
	sc.Connect()
}

func (gb *gcpBalancer) bindSubConn(bindKey string, scRef *subConnRef) {
	_, ok := gb.affinityMap[bindKey]
	if !ok {
		gb.affinityMap[bindKey] = scRef
	}
	gb.affinityMap[bindKey].affinityCnt++
}

func (gb *gcpBalancer) unbindSubConn(boundKey string) {
	boundRef, ok := gb.affinityMap[boundKey]
	if ok {
		boundRef.affinityCnt--
		if boundRef.affinityCnt <= 0 {
			delete(gb.affinityMap, boundKey)
		}
	}
}

// regeneratePicker takes a snapshot of the balancer, and generates a picker
// from it. The picker is
//  - errPicker with ErrTransientFailure if the balancer is in TransientFailure,
//  - built by the pickerBuilder with all READY SubConns otherwise.
func (gb *gcpBalancer) regeneratePicker() {
	if gb.state == connectivity.TransientFailure {
		gb.picker = base.NewErrPicker(balancer.ErrTransientFailure)
		return
	}
	readyRefs := []*subConnRef{}

	// Filter out all ready SCs from full subConn map.
	for _, scRef := range gb.scRefs {
		if scRef.scState == connectivity.Ready {
			readyRefs = append(readyRefs, scRef)
		}
	}
	gb.picker = gb.pickerBuilder.Build(readyRefs, gb)
}

func (gb *gcpBalancer) HandleSubConnStateChange(sc balancer.SubConn, s connectivity.State) {
	grpclog.Infof("grpc_gcp.gcpBalancer: handle SubConn state change: %p, %v", sc, s)
	scRef, ok := gb.scRefs[sc]
	if !ok {
		grpclog.Infof("grpc_gcp.gcpBalancer: got state changes for an unknown SubConn: %p, %v", sc, s)
		return
	}
	oldS := scRef.scState
	scRef.scState = s
	switch s {
	case connectivity.Idle:
		sc.Connect()
	case connectivity.Shutdown:
		delete(gb.scRefs, sc)
	}

	oldAggrState := gb.state
	gb.state = gb.csEvltr.recordTransition(oldS, s)

	// Regenerate picker when one of the following happens:
	//  - this sc became ready from not-ready
	//  - this sc became not-ready from ready
	//  - the aggregated state of balancer became TransientFailure from non-TransientFailure
	//  - the aggregated state of balancer became non-TransientFailure from TransientFailure
	if (s == connectivity.Ready) != (oldS == connectivity.Ready) ||
		(gb.state == connectivity.TransientFailure) != (oldAggrState == connectivity.TransientFailure) {
		gb.regeneratePicker()
	}

	gb.cc.UpdateBalancerState(gb.state, gb.picker)
}

func (gb *gcpBalancer) Close() {
}

func init() {
	balancer.Register(newBuilder())
}