package grpc_gcp

import (
	"fmt"
	"context"
	"sync"
	"sort"
	"strings"
	"reflect"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
)

type gcpPickerBuilder struct{}

func (gpb *gcpPickerBuilder) Build(readySCs []balancer.SubConn, gb *gcpBalancer) balancer.Picker {
	var refs []*subConnRef
	for _, sc := range readySCs {
		refs = append(refs, &subConnRef{subConn: sc})
	}
	return &gcpPicker{
		gcpBalancer: gb,
		scRefs: refs,
		affinityMap: make(map[string]*subConnRef),
	}
}

type subConnRef struct {
	subConn     balancer.SubConn
	affinityCnt uint32
	streamsCnt  uint32
}

type gcpPicker struct {
	gcpBalancer *gcpBalancer
	scRefs      []*subConnRef
	addrs       []resolver.Address
	mu          sync.Mutex
	affinityMap map[string]*subConnRef
	maxConn     uint32
	maxStream   uint32
}

func (p *gcpPicker) Pick(ctx context.Context, opts balancer.PickOptions) (balancer.SubConn, func(balancer.DoneInfo), error) {
	// TODO: use affinityKey to select subcosn
	fmt.Println("*** gcpPicker starts picking subconn")
	if len(p.scRefs) <= 0 {
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	gcpCtx, hasGcpCtx := ctx.Value(gcpKey).(*gcpContext)
	boundKey := ""

	fmt.Printf("*** before pre process: p.affinityMap: %+v\n", p.affinityMap)

	if hasGcpCtx {
		// pre process
		fmt.Println("*** starting pre process")
		affinity := gcpCtx.affinityCfg
		channelPool := gcpCtx.channelPoolCfg
		if channelPool != nil {
			// Initialize p.maxConn and p.maxStream for the first time.
			if p.maxConn == 0 {
				if channelPool.GetMaxSize() == 0{
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
	p.mu.Unlock()

	// post process
	callback := func (info balancer.DoneInfo) {
		fmt.Println("*** starting post process")
		if info.Err == nil {
			if hasGcpCtx {
				affinity := gcpCtx.affinityCfg
				locator := affinity.GetAffinityKey()
				cmd := affinity.GetCommand()
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
	return scRef.subConn, callback, nil
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

	if len(p.scRefs) > 0 && p.scRefs[0].streamsCnt < p.maxStream {
		return p.scRefs[0]
	}

	if len(p.scRefs) < int(p.maxConn) {
		// Ask balancer to create new subconn when all current subconns are busy,
		// and let this picker return ErrNoSubConnAvailable.
		fmt.Println("*** creating new subconn")
		p.gcpBalancer.createNewSubconn()
		return nil
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