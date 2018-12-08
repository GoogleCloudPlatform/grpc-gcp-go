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
	"context"
	"sync"
	"sort"
	"strings"
	"reflect"

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

const maxSize = 10
const maxConcurrentStreamsLowWatermark = 100

// newBuilder creates a new grpc_gcp balancer builder.
func newBuilder() balancer.Builder {
	gpb := gcpPickerBuilder{}
	bb := base.NewBalancerBuilderWithConfig(Name, &gpb, base.Config{})
	return &gcpBalancerBuilder{
		baseBuilder: bb,
		pickerBuilder: gpb,
	}
}

func init() {
	balancer.Register(newBuilder())
}

type gcpBalancerBuilder struct{
	baseBuilder   balancer.Builder
	pickerBuilder gcpPickerBuilder
}

func (gbb *gcpBalancerBuilder) Build(cc balancer.ClientConn, opt balancer.BuildOptions) balancer.Balancer {
	gbb.pickerBuilder.cc = cc
	bbc := gbb.baseBuilder.Build(cc, opt)
	return &gcpBalancer{
		baseBalancer: bbc,
		cc:           cc,
	}
}

func (*gcpBalancerBuilder) Name() string {
	return Name
}

type gcpBalancer struct {
	baseBalancer balancer.Balancer
	cc           balancer.ClientConn
}

func (gb *gcpBalancer) HandleResolvedAddrs(addrs []resolver.Address, err error) {
	gb.baseBalancer.HandleResolvedAddrs(addrs, err)
}

func (gb *gcpBalancer) HandleSubConnStateChange(sc balancer.SubConn, s connectivity.State) {
	gb.baseBalancer.HandleSubConnStateChange(sc, s)
}

func (gb *gcpBalancer) Close() {
	gb.baseBalancer.Close()
}

type gcpPickerBuilder struct{
	cc balancer.ClientConn
}

func (gpb *gcpPickerBuilder) Build(readySCs map[resolver.Address]balancer.SubConn) balancer.Picker {
	grpclog.Infof("grpcgcpPicker: newPicker called with readySCs: %v", readySCs)
	var refs []*subConnRef
	for _, sc := range readySCs {
		refs = append(refs, &subConnRef{subConn: sc})
	}
	addrs := make([]resolver.Address, 0, len(readySCs))
	for addr := range readySCs {
		addrs = append(addrs, addr)
	}
	return &gcpPicker{
		addrs:  addrs,
		scRefs: refs,
		cc: gpb.cc,
		affinityMap: make(map[string]*subConnRef),
	}
}

type subConnRef struct {
	subConn     balancer.SubConn
	affinityCnt int
	streamsCnt  int
}

type gcpPicker struct {
	scRefs      []*subConnRef
	addrs       []resolver.Address
	cc          balancer.ClientConn
	mu          sync.Mutex
	affinityMap map[string]*subConnRef
}

func (p *gcpPicker) Pick(ctx context.Context, opts balancer.PickOptions) (balancer.SubConn, func(balancer.DoneInfo), error) {
	// TODO: use affinityKey to select subcosn
	fmt.Println("*** gcpPicker starts picking subconn")
	if len(p.scRefs) <= 0 {
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	gcpCtx, hasGcpCxt := ctx.Value(gcpKey).(*gcpContext)
	boundKey := ""

	fmt.Printf("*** before pre process: p.affinityMap: %+v\n", p.affinityMap)

	if hasGcpCxt {
		// pre process
		fmt.Println("*** starting pre process")
		cfg := gcpCtx.affinityCfg
		locator := cfg.GetAffinityKey()
		cmd := cfg.GetCommand()
		if cmd == AffinityConfig_BOUND || cmd == AffinityConfig_UNBIND {
			a, err := getAffinityKeyFromMessage(locator, gcpCtx.reqMsg)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to retrieve affinity key from request message: %v", err)
			}
			boundKey = a
		}
	}

	p.mu.Lock()
	scRef := p.getSubConnRef(boundKey)
	if scRef == nil {
		return nil, nil, balancer.ErrNoSubConnAvailable
	}
	scRef.streamsCnt++
	sc := scRef.subConn
	p.mu.Unlock()

	// post process
	callback := func (info balancer.DoneInfo) {
		fmt.Println("*** starting post process")
		if info.Err == nil {
			if hasGcpCxt {
				cfg := gcpCtx.affinityCfg
				locator := cfg.GetAffinityKey()
				cmd := cfg.GetCommand()
				if cmd == AffinityConfig_BIND {
					bindKey, err := getAffinityKeyFromMessage(locator, gcpCtx.replyMsg)
					if err == nil {
						_, ok := p.affinityMap[bindKey]
						if !ok {
							p.affinityMap[bindKey] = scRef
						}
						p.affinityMap[bindKey].affinityCnt++
					}
				} else if cmd == AffinityConfig_UNBIND {
					boundRef, ok := p.affinityMap[boundKey]
					if ok {
						boundRef.affinityCnt--
						if boundRef.affinityCnt <= 0 {
							delete(p.affinityMap, boundKey)
						}
					}
				}
			}
		}
		scRef.streamsCnt--
		fmt.Printf("*** after post process: p.affinityMap: %+v\n", p.affinityMap)
	}
	return sc, callback, nil
}

func (p *gcpPicker) getSubConnRef(boundKey string) *subConnRef{
	if boundKey != "" {
		ref, ok := p.affinityMap[boundKey]
		if ok {
			return ref
		}
	}
	sort.Slice(p.scRefs[:], func(i, j int) bool {
		return p.scRefs[i].streamsCnt < p.scRefs[j].streamsCnt
	})

	if len(p.scRefs) > 0 && p.scRefs[0].streamsCnt < maxConcurrentStreamsLowWatermark {
		return p.scRefs[0]
	}

	if len(p.scRefs) < maxSize {
		// create a new subconn if all current subconns are busy
		sc, err := p.cc.NewSubConn(p.addrs, balancer.NewSubConnOptions{})
		if err == nil {
			sc.Connect()
			newRef := &subConnRef{subConn: sc}
			p.scRefs = append(p.scRefs, newRef)
			return newRef
		}
	}

	if len(p.scRefs) == 0 {
		return nil
	}

	return p.scRefs[0]
}

func getAffinityKeyFromMessage(locator string, msg interface{}) (affinityKey string, err error) {
	if locator == "" {
		return "", fmt.Errorf("affinityKey locator is not valid")
	}

	names := strings.Split(locator, ".")
	val := reflect.ValueOf(msg).Elem()

	var ak string
	i := 0
	for ; i < len(names); i++ {
		name := names[i]
		titledName := strings.Title(name)
		valField := val.FieldByName(titledName)
		if valField.IsValid() {
			switch valField.Kind() {
			case reflect.String:
				ak = valField.String()
				break
			case reflect.Interface:
				val = reflect.ValueOf(valField.Interface())
			default:
				return "", fmt.Errorf("field %s in message is neither a string nor another message", titledName)
			}
		}
	}
	if i == len(names) {
		return ak, nil
	}
	
	return "", fmt.Errorf("cannot get valid affinity key")
}