package grpc_gcp

import (
	"google.golang.org/grpc/connectivity"
	"fmt"
	"context"
	"sync"
	"sort"
	"strings"
	"reflect"

	"google.golang.org/grpc/balancer"
)

type gcpPickerBuilder struct{}

func (gpb *gcpPickerBuilder) Build(readySCRefs []*subConnRef, gb *gcpBalancer) balancer.Picker {
	fmt.Printf("*** Building new picker with ready subConnRefs: %+v\n", readySCRefs)
	return &gcpPicker{
		gcpBalancer: gb,
		scRefs: readySCRefs,
	}
}

type gcpPicker struct {
	gcpBalancer *gcpBalancer
	scRefs      []*subConnRef
	mu          sync.Mutex
	maxConn     uint32
	maxStream   uint32
}

func (p *gcpPicker) Pick(ctx context.Context, opts balancer.PickOptions) (balancer.SubConn, func(balancer.DoneInfo), error) {
	// TODO: use affinityKey to select subcosn
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
		fmt.Printf("xxx cmd: %v, locator: %s, boundKey: %s\n", cmd, locator, boundKey)
	}

	fmt.Println("--> p.mu.Lock()")
	p.mu.Lock()
	scRef := p.getSubConnRef(boundKey)
	if scRef == nil {
		p.mu.Unlock()
		return nil, nil, balancer.ErrNoSubConnAvailable
	}
	scRef.streamsCnt++
	p.mu.Unlock()
	fmt.Println("--< p.mu unlocked")

	// define callback for post process
	callback := func (info balancer.DoneInfo) {
		if info.Err == nil {
			if hasGcpCtx {
				affinity := gcpCtx.affinityCfg
				locator := affinity.GetAffinityKey()
				cmd := affinity.GetCommand()
				if cmd == AffinityConfig_BIND {
					bindKey, err := getAffinityKeyFromMessage(locator, gcpCtx.replyMsg)
					if err == nil {
						p.gcpBalancer.bindSubConn(bindKey, scRef)
					}
				} else if cmd == AffinityConfig_UNBIND {
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
func (p *gcpPicker) getSubConnRef(boundKey string) *subConnRef{
	if boundKey != "" {
		if ref, ok := p.gcpBalancer.affinityMap[boundKey]; ok {
			if ref.scState != connectivity.Ready {
				fmt.Println("****** bound connection is not READY")
				// It's possible that the bound subconn is not in the readySubConns list,
				// If it's not ready yet, we throw ErrNoSubConnAvailable
				return nil
			} else {
				fmt.Println("****** bound connection is READY")
				return ref
			}
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
		p.gcpBalancer.createNewSubConn()
		return nil
	}

	if len(p.scRefs) == 0 {
		return nil
	}

	return p.scRefs[0]
}

// getAffinityKeyFromMessage retrieves the affinity key from proto message using the key locator
// defined in the affinity config.
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