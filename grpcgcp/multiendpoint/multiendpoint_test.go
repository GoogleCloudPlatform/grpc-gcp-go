/*
 *
 * Copyright 2023 gRPC authors.
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

package multiendpoint

import (
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	recoveryTO  = time.Millisecond * 20
	switchDelay = time.Millisecond * 40
)

var (
	threeEndpoints = []string{"first", "second", "third"}
	fourEndpoints  = []string{"fourth", "first", "third", "second"}
	now            = time.Now()
	fInd           uint32
	pendingFns     = make(map[time.Time][]uint32)
	fns            = make(map[uint32]func())
)

type FakeTimer struct {
	time.Timer

	fnId uint32
}

func (ft *FakeTimer) Stop() bool {
	delete(fns, ft.fnId)
	return true
}

func (ft *FakeTimer) Reset(d time.Duration) bool {
	return true
}

func init() {
	timeNow = func() time.Time {
		return now
	}
	timeAfterFunc = func(d time.Duration, f func()) timerAlike {
		t := now.Add(d)
		id := atomic.AddUint32(&fInd, 1)
		if _, ok := pendingFns[t]; !ok {
			pendingFns[t] = []uint32{id}
		}
		pendingFns[t] = append(pendingFns[t], id)
		fns[id] = f
		return &FakeTimer{fnId: id}
	}
}

func advanceTime(t *testing.T, d time.Duration) {
	t.Helper()
	now = now.Add(d)

	times := make([]time.Time, 0, len(pendingFns))
	for t2 := range pendingFns {
		times = append(times, t2)
	}

	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	for _, t2 := range times {
		if t2.After(now) {
			break
		}
		for _, fid := range pendingFns[t2] {
			if fn, ok := fns[fid]; ok {
				fn()
				delete(fns, fid)
			}
		}
		delete(pendingFns, t2)
	}
}

func advanceTimeConcurring(t *testing.T, d time.Duration, cfns []func()) {
	t.Helper()
	now = now.Add(d)

	times := make([]time.Time, 0, len(pendingFns))
	for t2 := range pendingFns {
		times = append(times, t2)
	}

	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	for _, t2 := range times {
		if t2.After(now) {
			break
		}
		for _, fid := range pendingFns[t2] {
			if fn, ok := fns[fid]; ok {
				cfn := func() {}
				if len(cfns) > 0 {
					cfn = cfns[0]
					cfns = cfns[1:]
				}
				wg := &sync.WaitGroup{}
				wg.Add(2)
				r := rand.Int()
				go func() {
					if r%2 == 0 {
						cfn()
					} else {
						fn()
					}
					wg.Done()
				}()
				go func() {
					if r%2 == 0 {
						fn()
					} else {
						cfn()
					}
					wg.Done()
				}()
				wg.Wait()
				delete(fns, fid)
			}
		}
		delete(pendingFns, t2)
	}
}

func initPlain(t *testing.T, es []string) MultiEndpoint {
	t.Helper()
	me, err := NewMultiEndpoint(&MultiEndpointOptions{
		Endpoints: es,
	})
	if err != nil {
		t.Fatalf("multiendpointBuilder.Build() returns unexpected error: %v", err)
	}
	return me
}

func initWithDelays(t *testing.T, es []string, r time.Duration, d time.Duration) MultiEndpoint {
	t.Helper()
	me, err := NewMultiEndpoint(&MultiEndpointOptions{
		Endpoints:       es,
		RecoveryTimeout: r,
		SwitchingDelay:  d,
	})
	if err != nil {
		t.Fatalf("multiendpointBuilder.Build() returns unexpected error: %v", err)
	}
	return me
}

func TestRestrictEmptyEndpoints(t *testing.T) {
	b := &MultiEndpointOptions{
		RecoveryTimeout: 1,
		SwitchingDelay:  2,
	}
	expectedErr := "endpoints list cannot be empty"
	if _, err := NewMultiEndpoint(b); err == nil || err.Error() != expectedErr {
		t.Errorf("multiendpointBuilder.Build() returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestCurrentIsFirstAfterInit(t *testing.T) {
	me, err := NewMultiEndpoint(&MultiEndpointOptions{
		Endpoints: threeEndpoints,
	})
	if err != nil {
		t.Fatalf("multiendpointBuilder.Build() returns unexpected error: %v", err)
	}
	if c := me.Current(); c != threeEndpoints[0] {
		t.Errorf("Current() returns %q, want: %q", c, threeEndpoints[0])
	}
}

func TestReturnsTopPriorityAvailableEndpointWithoutRecovery(t *testing.T) {
	me := initPlain(t, threeEndpoints)

	// Returns first after creation.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes available.
	me.SetEndpointAvailability(threeEndpoints[1], true)

	// Second is the current as the only available.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Third becomes available.
	me.SetEndpointAvailability(threeEndpoints[2], true)

	// Second is still the current because it has higher priority.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// First becomes available.
	me.SetEndpointAvailability(threeEndpoints[0], true)

	// First becomes the current because it has higher priority.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[1], false)

	// Second becoming unavailable should not affect the current first.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// First becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[0], false)

	// Third becomes the current as the only remaining available.
	if c, want := me.Current(), threeEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Third becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[2], false)

	// After all endpoints became unavailable the multiEndpoint sticks to the last used endpoint.
	if c, want := me.Current(), threeEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestCurrentReturnsTopPriorityAvailableEndpointWithRecovery(t *testing.T) {

	me := initWithDelays(t, threeEndpoints, recoveryTO, 0)

	// Returns first after creation.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes available.
	me.SetEndpointAvailability(threeEndpoints[1], true)

	// First is still the current to allow it to become available within recovery timeout.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Second becomes current as an available endpoint with top priority.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Third becomes available.
	me.SetEndpointAvailability(threeEndpoints[2], true)

	// Second is still the current because it has higher priority.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[1], false)

	// Second is still current, allowing upto recoveryTimeout to recover.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Halfway through recovery timeout the second recovers.
	advanceTime(t, recoveryTO/2)
	me.SetEndpointAvailability(threeEndpoints[1], true)

	// Second is the current.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After the initial recovery timeout, the second is still current.
	advanceTime(t, recoveryTO/2)
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[1], false)

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Changes to an available endpoint -- third.
	if c, want := me.Current(), threeEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// First becomes available.
	me.SetEndpointAvailability(threeEndpoints[0], true)

	// First becomes current immediately.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// First becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[0], false)

	// First is still current, allowing upto recoveryTimeout to recover.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Changes to an available endpoint -- third.
	if c, want := me.Current(), threeEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Third becomes unavailable
	me.SetEndpointAvailability(threeEndpoints[2], false)

	// Third is still current, allowing upto recoveryTimeout to recover.
	if c, want := me.Current(), threeEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Halfway through recovery timeout the second becomes available.
	advanceTime(t, recoveryTO/2)
	me.SetEndpointAvailability(threeEndpoints[1], true)

	// Second becomes current immediately.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Second becomes unavailable.
	me.SetEndpointAvailability(threeEndpoints[1], false)

	// Second is still current, allowing upto recoveryTimeout to recover.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// After all endpoints became unavailable the multiEndpoint sticks to the last used endpoint.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsErrorWhenEmptyEndpoints(t *testing.T) {
	me := initPlain(t, threeEndpoints)
	expectedErr := "endpoints list cannot be empty"
	if err := me.SetEndpoints([]string{}); err == nil || err.Error() != expectedErr {
		t.Errorf("multiendpointBuilder.Build() returns wrong err: %v, want: %v", err, expectedErr)
	}
}

func TestSetEndpointsUpdatesEndpoints(t *testing.T) {
	me := initPlain(t, threeEndpoints)
	me.SetEndpoints(fourEndpoints)

	// "first" which is now under index 1 still current because no other available.
	if c, want := me.Current(), fourEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsWithRecovery(t *testing.T) {
	me := initWithDelays(t, threeEndpoints, recoveryTO, 0)
	me.SetEndpoints(fourEndpoints)

	// "first" which is now under index 1 still current because no other available.
	if c, want := me.Current(), fourEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsPreservingStates(t *testing.T) {
	me := initPlain(t, threeEndpoints)

	// Second is available.
	me.SetEndpointAvailability(threeEndpoints[1], true)
	me.SetEndpoints(fourEndpoints)

	// "second" which is now under index 3 still must remain available.
	if c, want := me.Current(), fourEndpoints[3]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsSwitchToTopPriorityAvailable(t *testing.T) {
	me := initPlain(t, threeEndpoints)

	// Second and third is available.
	me.SetEndpointAvailability(threeEndpoints[1], true)
	me.SetEndpointAvailability(threeEndpoints[2], true)

	me.SetEndpoints(fourEndpoints)

	// "third" which is now under index 2 must become current, because "second" has lower priority.
	if c, want := me.Current(), fourEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsSwitchToTopPriorityAvailableWithRecovery(t *testing.T) {
	me := initWithDelays(t, threeEndpoints, recoveryTO, 0)

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Second and third is available.
	me.SetEndpointAvailability(threeEndpoints[1], true)
	me.SetEndpointAvailability(threeEndpoints[2], true)

	me.SetEndpoints(fourEndpoints)

	// "third" which is now under index 2 must become current, because "second" has lower priority.
	if c, want := me.Current(), fourEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsRemovesOnlyActiveEndpoint(t *testing.T) {
	extraEndpoints := append(threeEndpoints, "extra")
	me := initPlain(t, extraEndpoints)

	// Extra is available.
	me.SetEndpointAvailability("extra", true)

	// Extra is current
	if c, want := me.Current(), extraEndpoints[3]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Extra is removed.
	me.SetEndpoints(fourEndpoints)

	// "fourth" which is under index 0 must become current, because no endpoints are available.
	if c, want := me.Current(), fourEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsUpdatesEndpointsRemovesOnlyActiveEndpointWithRecovery(t *testing.T) {
	extraEndpoints := append(threeEndpoints, "extra")
	me := initWithDelays(t, extraEndpoints, recoveryTO, 0)

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Extra is available.
	me.SetEndpointAvailability("extra", true)

	// Extra is removed.
	me.SetEndpoints(fourEndpoints)

	// "fourth" which is under index 0 must become current, because no endpoints available.
	if c, want := me.Current(), fourEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsRecoveringEndpointGetsRemoved(t *testing.T) {
	extraEndpoints := append(threeEndpoints, "extra")
	me := initWithDelays(t, extraEndpoints, recoveryTO, 0)

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// Extra is available.
	me.SetEndpointAvailability("extra", true)

	// Extra is recovering.
	me.SetEndpointAvailability("extra", false)

	// Extra is removed.
	me.SetEndpoints(fourEndpoints)

	// "fourth" which is under index 0 must become current, because no endpoints available.
	if c, want := me.Current(), fourEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After recovery timeout has passed.
	advanceTime(t, recoveryTO)

	// "fourth" is still current.
	if c, want := me.Current(), fourEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointAvailableSubsequentUnavailableShouldNotExtendRecoveryTimeout(t *testing.T) {
	// All endpoints are recovering.
	me := initWithDelays(t, threeEndpoints, recoveryTO, 0)

	// Before recovery timeout repeat unavailable signal.
	advanceTime(t, recoveryTO/2)
	me.SetEndpointAvailability(threeEndpoints[0], false)

	// After the initial timeout it must become unavailable.
	advanceTime(t, recoveryTO/2)
	if c, want := me.(*multiEndpoint).endpoints[threeEndpoints[0]], unavailable; c.status != want {
		t.Fatalf("%q endpoint state is %q, want: %q", threeEndpoints[0], c.status, want)
	}
}

func TestSetEndpointAvailableRecoveringUnavailableRace(t *testing.T) {
	// All endpoints are recovering.
	me := initWithDelays(t, threeEndpoints, recoveryTO, 0)

	// Set "second" available to have something to fallback to.
	me.SetEndpointAvailability(threeEndpoints[1], true)

	for i := 0; i < 100; i++ {
		// Right at the recovery timeout we enable the "first". This should race with the "first"
		// becoming unavailable from its recovery timer. If this race condition is not covered then
		// the test will most likely fail or at least be flaky.
		advanceTimeConcurring(t, recoveryTO, []func(){
			func() { me.SetEndpointAvailability(threeEndpoints[0], true) },
		})

		// It is expected that the scheduled Recovering->Unavailable state change (from the recovery
		// timeout) and Recovering->Available from the above setEndpointAvailable are guarded by
		// mutex and cannot run in parallel. Moreover, if Recovering->Unavailable is run after
		// Recovering->Available, then Recovering->Unavailable has no effect because it was planned
		// to move the endpoint to Unavailable after recovery timeout but the endpoint became
		// Availalble a moment earlier.
		// Thus in any case the "first" endpoint must be current at this moment.
		if c, want := me.Current(), threeEndpoints[0]; c != want {
			t.Fatalf("Current() returns %q, want: %q", c, want)
		}

		advanceTime(t, recoveryTO)
		// Make sure the "first" endpoint is still current after another recovery timeout.
		if c, want := me.Current(), threeEndpoints[0]; c != want {
			t.Fatalf("Current() returns %q, want: %q", c, want)
		}

		// Send it back to recovery state and start recovery timer.
		me.SetEndpointAvailability(threeEndpoints[0], false)
	}
}

func TestSetEndpointAvailableDoNotSwitchToUnavailableFromAvailable(t *testing.T) {
	me := initWithDelays(t, threeEndpoints, recoveryTO, switchDelay)
	// Second and third endpoint are available.
	me.SetEndpointAvailability(threeEndpoints[1], true)
	me.SetEndpointAvailability(threeEndpoints[2], true)

	advanceTime(t, recoveryTO)
	// Second is current after recovery timeout.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// First becomes available.
	me.SetEndpointAvailability(threeEndpoints[0], true)

	// Switching is planned to "first" after switching delay. "second" is still current.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Almost at switching delay the "first" endpoint becomes unavailable again.
	advanceTime(t, switchDelay-(switchDelay/10))
	me.SetEndpointAvailability(threeEndpoints[0], false)

	// After switching delay the current must be "second". No switching to the recovering
	// "first" should occur.
	advanceTime(t, switchDelay/5)
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointAvailableDoNotSwitchPreemptively(t *testing.T) {
	me := initWithDelays(t, threeEndpoints, recoveryTO, switchDelay)

	// All unavailable after recovery timeout.
	advanceTime(t, recoveryTO)

	// Only second endpoint is available.
	me.SetEndpointAvailability(threeEndpoints[1], true)

	// After switching delay the second should be current.
	advanceTime(t, switchDelay)
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Third becomes available. This shouldn't schedule the switch as second is still
	// the most preferable.
	me.SetEndpointAvailability(threeEndpoints[2], true)

	advanceTime(t, switchDelay/2)
	// Halfway to switch delay the first endpoint becomes available.
	me.SetEndpointAvailability(threeEndpoints[0], true)

	advanceTime(t, switchDelay/2)
	// After complete switching delay since third become available, the second should still be
	// current because we didn't schedule the switch when third became available.
	if c, want := me.Current(), threeEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	advanceTime(t, switchDelay/2)
	// But after switching delay passed since first became available it should become current.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}

func TestSetEndpointsSwitchingDelayed(t *testing.T) {
	me := initWithDelays(t, threeEndpoints, recoveryTO, switchDelay)
	// All endpoints are available.
	for _, e := range threeEndpoints {
		me.SetEndpointAvailability(e, true)
	}

	// First is current.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Prepend a new endpoint and make it available.
	extraEndpoints := []string{"extra"}
	extraEndpoints = append(extraEndpoints, threeEndpoints...)

	me.SetEndpoints(extraEndpoints)
	me.SetEndpointAvailability(extraEndpoints[0], true)

	// The current endpoint should not change instantly.
	if c, want := me.Current(), threeEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// But after switching delay it should.
	advanceTime(t, switchDelay)
	if c, want := me.Current(), extraEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Make current endpoint unavailable.
	me.SetEndpointAvailability(extraEndpoints[0], false)

	// Should wait for recovery timeout.
	if c, want := me.Current(), extraEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Should switch to a healthy endpoint after recovery timeout and not the switching delay.
	advanceTime(t, recoveryTO)
	if c, want := me.Current(), extraEndpoints[1]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Prepend another endpoint.
	updatedEndpoints := []string{"extra2"}
	updatedEndpoints = append(updatedEndpoints, extraEndpoints...)

	me.SetEndpoints(updatedEndpoints)
	// Now the endpoints are:
	// 0 extra2 UNAVAILABLE
	// 1 extra UNAVAILABLE
	// 2 first AVAILABLE <-- current
	// 3 second AVAILABLE
	// 4 third AVAILABLE

	// Make "extra" endpoint available.
	me.SetEndpointAvailability("extra", true)

	// Should wait for the switching delay.
	// Halfway it should be still "first" endpoint.
	advanceTime(t, switchDelay/2)
	if c, want := me.Current(), updatedEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// Now another higher priority endpoint becomes available.
	me.SetEndpointAvailability("extra2", true)

	// Still "first" endpoint is current because switching delay has not passed.
	if c, want := me.Current(), updatedEndpoints[2]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}

	// After another half of the switching delay has passed it should switch to the "extra2" because
	// it is a top priority available endpoint at the moment.
	advanceTime(t, switchDelay/2)
	if c, want := me.Current(), updatedEndpoints[0]; c != want {
		t.Fatalf("Current() returns %q, want: %q", c, want)
	}
}
