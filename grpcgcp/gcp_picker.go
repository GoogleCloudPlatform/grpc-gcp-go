/*
 *
 * Copyright 2019 gRPC authors.
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
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"google.golang.org/grpc/balancer"
)

func newGCPPicker(readySCRefs []*subConnRef, gb *gcpBalancer) balancer.Picker {
	return &gcpPicker{
		gb:     gb,
		scRefs: readySCRefs,
	}
}

type gcpPicker struct {
	gb     *gcpBalancer
	mu     sync.Mutex
	scRefs []*subConnRef
}

func (p *gcpPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if len(p.scRefs) <= 0 {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	gcpCtx, hasGcpCtx := info.Ctx.Value(gcpKey).(*gcpContext)
	boundKey := ""
	locator := ""
	var cmd grpc_gcp.AffinityConfig_Command

	if mcfg, ok := p.gb.methodCfg[info.FullMethodName]; ok && hasGcpCtx {
		locator = mcfg.GetAffinityKey()
		cmd = mcfg.GetCommand()
		if cmd == grpc_gcp.AffinityConfig_BOUND || cmd == grpc_gcp.AffinityConfig_UNBIND {
			a, err := getAffinityKeyFromMessage(locator, gcpCtx.reqMsg)
			if err != nil {
				return balancer.PickResult{}, fmt.Errorf(
					"failed to retrieve affinity key from request message: %v", err)
			}
			boundKey = a
		}
	}

	scRef, err := p.getAndIncrementSubConnRef(boundKey)
	if err != nil {
		return balancer.PickResult{}, err
	}
	if scRef == nil {
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
	}

	// define callback for post process once call is done
	callback := func(info balancer.DoneInfo) {
		scRef.streamsDecr()
		if info.Err != nil {
			return
		}
		switch cmd {
		case grpc_gcp.AffinityConfig_BIND:
			bindKey, err := getAffinityKeyFromMessage(locator, gcpCtx.replyMsg)
			if err == nil {
				p.gb.bindSubConn(bindKey, scRef.subConn)
			}
		case grpc_gcp.AffinityConfig_UNBIND:
			p.gb.unbindSubConn(boundKey)
		}
	}
	return balancer.PickResult{SubConn: scRef.subConn, Done: callback}, nil
}

func (p *gcpPicker) getAndIncrementSubConnRef(boundKey string) (*subConnRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	scRef, err := p.getSubConnRef(boundKey)
	if err != nil {
		return nil, err
	}
	if scRef != nil {
		scRef.streamsIncr()
	}
	return scRef, nil
}

// getSubConnRef returns the subConnRef object that contains the subconn
// ready to be used by picker.
func (p *gcpPicker) getSubConnRef(boundKey string) (*subConnRef, error) {
	if boundKey != "" {
		if ref, ok := p.gb.getReadySubConnRef(boundKey); ok {
			return ref, nil
		}
	}

	return p.getLeastBusySubConnRef()
}

func (p *gcpPicker) getLeastBusySubConnRef() (*subConnRef, error) {
	minScRef := p.scRefs[0]
	minStreamsCnt := minScRef.getStreamsCnt()
	for _, scRef := range p.scRefs {
		if scRef.getStreamsCnt() < minStreamsCnt {
			minStreamsCnt = scRef.getStreamsCnt()
			minScRef = scRef
		}
	}

	// If the least busy connection still has capacity, use it
	if minStreamsCnt < int32(p.gb.cfg.GetChannelPool().GetMaxConcurrentStreamsLowWatermark()) {
		return minScRef, nil
	}

	if p.gb.cfg.GetChannelPool().GetMaxSize() == 0 || p.gb.getConnectionPoolSize() < int(p.gb.cfg.GetChannelPool().GetMaxSize()) {
		// Ask balancer to create new subconn when all current subconns are busy and
		// the connection pool still has capacity (either unlimited or maxSize is not reached).
		p.gb.newSubConn()

		// Let this picker return ErrNoSubConnAvailable because it needs some time
		// for the subconn to be READY.
		return nil, balancer.ErrNoSubConnAvailable
	}

	// If no capacity for the pool size and every connection reachs the soft limit,
	// Then picks the least busy one anyway.
	return minScRef, nil
}

// getAffinityKeyFromMessage retrieves the affinity key from proto message using
// the key locator defined in the affinity config.
func getAffinityKeyFromMessage(
	locator string,
	msg interface{},
) (affinityKey string, err error) {
	names := strings.Split(locator, ".")
	if len(names) == 0 {
		return "", fmt.Errorf("Empty affinityKey locator")
	}

	if msg == nil {
		return "", fmt.Errorf("cannot get string value from nil message")
	}
	val := reflect.ValueOf(msg).Elem()

	// Fields in names except for the last one.
	for _, name := range names[:len(names)-1] {
		valField := val.FieldByName(strings.Title(name))
		if valField.Kind() != reflect.Ptr && valField.Kind() != reflect.Struct {
			return "", fmt.Errorf("Invalid locator path for %v", locator)
		}
		val = valField.Elem()
	}

	valField := val.FieldByName(strings.Title(names[len(names)-1]))
	if valField.Kind() != reflect.String {
		return "", fmt.Errorf("Cannot get string value from %v", locator)
	}
	return valField.String(), nil
}

// NewErrPicker returns a picker that always returns err on Pick().
func newErrPicker(err error) balancer.Picker {
	return &errPicker{err: err}
}

type errPicker struct {
	err error // Pick() always returns this err.
}

func (p *errPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, p.err
}
