/*
 *
 * Copyright 2026 gRPC authors.
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
	"sync"
	"sync/atomic"
	"time"
)

// GCPFallbackState holds the fallback mode and the call statistics behind it.
// A single state can be shared by multiple GCPFallback instances so that they
// fall back and recover together. A GCPFallback creates its own state when
// none is provided.
//
// The periodic error rate check runs once per state, using the options of the
// first GCPFallback with fallback enabled to use it. Instances sharing a state
// should be configured with the same error rate options.
type GCPFallbackState struct {
	// Updated together under mu; read lock-free. generation increments on every mode change.
	inFallback atomic.Bool
	generation atomic.Uint64

	primarySuccesses  atomic.Uint64
	primaryFailures   atomic.Uint64
	fallbackSuccesses atomic.Uint64
	fallbackFailures  atomic.Uint64

	// mu serializes the transitions and guards the fields below.
	mu                       sync.Mutex
	primaryProbeSuccesses    uint64
	firstPrimaryProbeSuccess time.Time
	rateCheckStarted         bool
	closed                   bool

	done chan struct{}
}

// recoveryConfig is the recovery part of GCPFallbackOptions, passed per call
// so that the state holds no per-instance configuration.
type recoveryConfig struct {
	enabled      bool
	minSuccesses uint64
	minDuration  time.Duration
}

// NewGCPFallbackState creates a state to share between GCPFallback instances
// through GCPFallbackOptions.SharedState. The caller must close it.
func NewGCPFallbackState() *GCPFallbackState {
	return &GCPFallbackState{}
}

func (s *GCPFallbackState) isInFallback() bool {
	return s.inFallback.Load()
}

// Close stops the periodic error rate check. It does not close any connection
// and does not stop the probing of the GCPFallback instances using this state.
func (s *GCPFallbackState) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true
	close(s.doneLocked())
}

func (s *GCPFallbackState) doneLocked() chan struct{} {
	if s.done == nil {
		s.done = make(chan struct{})
	}
	return s.done
}

func (s *GCPFallbackState) recordPrimary(failure bool) {
	if failure {
		s.primaryFailures.Add(1)
		return
	}
	s.primarySuccesses.Add(1)
}

func (s *GCPFallbackState) recordFallback(failure bool) {
	if failure {
		s.fallbackFailures.Add(1)
		return
	}
	s.fallbackSuccesses.Add(1)
}

// drainPrimary reads and resets the primary counters, closing an error rate window.
func (s *GCPFallbackState) drainPrimary() (successes, failures uint64) {
	return s.primarySuccesses.Swap(0), s.primaryFailures.Swap(0)
}

func (s *GCPFallbackState) drainFallback() (successes, failures uint64) {
	return s.fallbackSuccesses.Swap(0), s.fallbackFailures.Swap(0)
}

// triggerFallback switches to fallback mode if generation still matches
// expectedGeneration, and reports whether the switch happened.
func (s *GCPFallbackState) triggerFallback(expectedGeneration uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.generation.Load() != expectedGeneration || s.inFallback.Load() {
		return false
	}
	s.resetProbeProgressLocked()
	s.generation.Add(1)
	s.inFallback.Store(true)
	return true
}

// recordPrimaryProbeResult records a primary probe result and reports whether
// it recovered to primary mode. Stale probes (generation != expectedGeneration)
// are ignored.
func (s *GCPFallbackState) recordPrimaryProbeResult(success bool, expectedGeneration uint64, cfg recoveryConfig) bool {
	if !cfg.enabled {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.generation.Load() != expectedGeneration || !s.inFallback.Load() {
		return false
	}
	if !success {
		s.resetProbeProgressLocked()
		return false
	}

	now := time.Now()
	if s.firstPrimaryProbeSuccess.IsZero() {
		s.firstPrimaryProbeSuccess = now
	}
	s.primaryProbeSuccesses++
	if s.primaryProbeSuccesses < cfg.minSuccesses || now.Sub(s.firstPrimaryProbeSuccess) < cfg.minDuration {
		return false
	}

	// Drop results recorded by calls that raced with the fallback, so the next
	// error rate window starts empty.
	s.primarySuccesses.Store(0)
	s.primaryFailures.Store(0)
	s.resetProbeProgressLocked()
	s.generation.Add(1)
	s.inFallback.Store(false)
	return true
}

func (s *GCPFallbackState) resetProbeProgressLocked() {
	s.primaryProbeSuccesses = 0
	s.firstPrimaryProbeSuccess = time.Time{}
}

// startRateCheck runs check every period until Close. Only the first call per
// state takes effect.
func (s *GCPFallbackState) startRateCheck(period time.Duration, check func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rateCheckStarted || s.closed || period <= 0 {
		return
	}
	s.rateCheckStarted = true
	done := s.doneLocked()

	go func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}
