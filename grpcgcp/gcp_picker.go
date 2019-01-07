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
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/GoogleCloudPlatform/grpc-gcp-go/grpcgcp/grpc_gcp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
)

func newGCPPicker(readySCRefs []*subConnRef, gb *gcpBalancer) balancer.Picker {
	return &gcpPicker{
		gcpBalancer: gb,
		scRefs:      readySCRefs,
	}
}

type gcpPicker struct {
	gcpBalancer *gcpBalancer
	scRefs      []*subConnRef
	mu          sync.Mutex
	maxConn     uint32
	maxStream   uint32
}

func (p *gcpPicker) Pick(
	ctx context.Context,
	opts balancer.PickOptions,
) (balancer.SubConn, func(balancer.DoneInfo), error) {
	if len(p.scRefs) <= 0 {
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	gcpCtx, hasGcpCtx := ctx.Value(gcpKey).(*gcpContext)
	boundKey := ""

	if hasGcpCtx {
		affinity := gcpCtx.affinityCfg
		channelPool := gcpCtx.channelPoolCfg
		if channelPool != nil {
			// Initialize p.maxConn and p.maxStream for the first time.
			if p.maxConn == 0 {
				if channelPool.GetMaxSize() == 0 {
					p.maxConn = defaultMaxConn
				} else {
					p.maxConn = channelPool.GetMaxSize()
				}
			}
			if p.maxStream == 0 {
				if channelPool.GetMaxConcurrentStreamsLowWatermark() == 0 {
					p.maxStream = defaultMaxStream
				} else {
					p.maxStream = channelPool.GetMaxConcurrentStreamsLowWatermark()
				}
			}
		}
		locator := affinity.GetAffinityKey()
		cmd := affinity.GetCommand()
		if cmd == grpc_gcp.AffinityConfig_BOUND || cmd == grpc_gcp.AffinityConfig_UNBIND {
			a, err := getAffinityKeyFromMessage(locator, gcpCtx.reqMsg)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"failed to retrieve affinity key from request message: %v", err)
			}
			boundKey = a
		}
	}

	p.mu.Lock()
	scRef := p.getSubConnRef(boundKey)
	if scRef == nil {
		p.mu.Unlock()
		return nil, nil, balancer.ErrNoSubConnAvailable
	}
	scRef.streamsCnt++
	p.mu.Unlock()

	// define callback for post process once call is done
	callback := func(info balancer.DoneInfo) {
		if info.Err == nil {
			if hasGcpCtx {
				affinity := gcpCtx.affinityCfg
				locator := affinity.GetAffinityKey()
				cmd := affinity.GetCommand()
				if cmd == grpc_gcp.AffinityConfig_BIND {
					bindKey, err := getAffinityKeyFromMessage(locator, gcpCtx.replyMsg)
					if err == nil {
						p.gcpBalancer.bindSubConn(bindKey, scRef)
					}
				} else if cmd == grpc_gcp.AffinityConfig_UNBIND {
					p.gcpBalancer.unbindSubConn(boundKey)
				}
			}
		}
		scRef.streamsCnt--
	}
	return scRef.subConn, callback, nil
}

// getSubConnRef returns the subConnRef object that contains the subconn
// ready to be used by picker.
func (p *gcpPicker) getSubConnRef(boundKey string) *subConnRef {
	if boundKey != "" {
		if ref, ok := p.gcpBalancer.affinityMap[boundKey]; ok {
			if ref.scState != connectivity.Ready {
				// It's possible that the bound subconn is not in the readySubConns list,
				// If it's not ready yet, we throw ErrNoSubConnAvailable
				return nil
			}
			return ref
		}
	}

	sort.Slice(p.scRefs[:], func(i, j int) bool {
		return p.scRefs[i].streamsCnt < p.scRefs[j].streamsCnt
	})

	if len(p.scRefs) > 0 && p.scRefs[0].streamsCnt < p.maxStream {
		return p.scRefs[0]
	}

	if len(p.gcpBalancer.scRefs) < int(p.maxConn) {
		// Ask balancer to create new subconn when all current subconns are busy and
		// the number of subconns has not reached maximum.
		p.gcpBalancer.newSubConn()

		// Let this picker return ErrNoSubConnAvailable because it needs some time
		// for the subconn to be READY.
		return nil
	}

	if len(p.scRefs) == 0 {
		return nil
	}
	return p.scRefs[0]
}

// getAffinityKeyFromMessage retrieves the affinity key from proto message using
// the key locator defined in the affinity config.
func getAffinityKeyFromMessage(
	locator string,
	msg interface{},
) (affinityKey string, err error) {
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
			case reflect.Interface:
				val = reflect.ValueOf(valField.Interface())
			default:
				return "",
					fmt.Errorf("field %s in message is neither a string nor another message", titledName)
			}
		}
	}
	if i == len(names) {
		return ak, nil
	}

	return "", fmt.Errorf("cannot get valid affinity key")
}

// NewErrPicker returns a picker that always returns err on Pick().
func NewErrPicker(err error) balancer.Picker {
	return &errPicker{err: err}
}

type errPicker struct {
	err error // Pick() always returns this err.
}

func (p *errPicker) Pick(ctx context.Context, opts balancer.PickOptions) (balancer.SubConn, func(balancer.DoneInfo), error) {
	return nil, nil, p.err
}
