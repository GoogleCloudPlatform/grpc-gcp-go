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

package grpcgcp

import (
	"context"
	"sync"
	"sort"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/resolver"
)

// Name is the name of grpc_gcp balancer.
const Name = "grpc_gcp"

const maxSize = 10
const maxConcurrentStreamsLowWatermark = 100

// newBuilder creates a new grpc_gpc balancer builder.
func newBuilder() balancer.Builder {
	return base.NewBalancerBuilderWithConfig(Name, &gcpPickerBuilder{}, base.Config{HealthCheck: true})
}

func init() {
	balancer.Register(newBuilder())
}

type gcpPickerBuilder struct{}

func (*gcpPickerBuilder) Build(readySCs map[resolver.Address]balancer.SubConn) balancer.Picker {
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
	}
}

type subConnRef struct {
	subConn     balancer.SubConn
	affinityCnt int
	streamsCnt  int
}

type gcpPicker struct {
	scRefs []*subConnRef
	addrs  []resolver.Address
	mu     sync.Mutex
}

func (p *gcpPicker) Pick(ctx context.Context, opts balancer.PickOptions) (balancer.SubConn, func(balancer.DoneInfo), error) {
	if len(p.scRefs) <= 0 {
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	p.mu.Lock()
	sc := p.getSubConnRef().subConn
	// sc := p.subConns[p.next]
	// p.next = (p.next + 1) % len(p.subConns)
	p.mu.Unlock()
	return sc, nil, nil
}

func (p *gcpPicker) getSubConnRef() *subConnRef{
	sort.Slice(p.scRefs[:], func(i, j int) bool {
		return p.scRefs[i].streamsCnt < p.scRefs[j].streamsCnt
	})

	size := len(p.scRefs)
	if size > 0 && p.scRefs[0].streamsCnt < maxConcurrentStreamsLowWatermark {
		return p.scRefs[0]
	}

	if (size < maxSize) {
		//TODO
	}
	return p.scRefs[0]
}
