package balancers

import (
	"testing"
	"time"
)

type spyBalancer struct {
	nextKey   uint
	popErr    error
	deletes   []uint
	reduces   []uint
	successes []uint
}

func (s *spyBalancer) Pop() (uint, error) {
	if s.popErr != nil {
		return 0, s.popErr
	}
	return s.nextKey, nil
}

func (s *spyBalancer) Delete(key uint) { s.deletes = append(s.deletes, key) }
func (s *spyBalancer) Reduce(key uint) { s.reduces = append(s.reduces, key) }
func (s *spyBalancer) Success(key uint) {
	s.successes = append(s.successes, key)
}

func resetBreakerState(t *testing.T) {
	t.Helper()
	mu.Lock()
	nodes = make(map[uint]*Node)
	mu.Unlock()
}

func withBreakerConfig(t *testing.T, maxFailures int, sleepWindow time.Duration, maxRequests int) {
	t.Helper()
	oldMaxFailures := MaxFailures
	oldSleepWindow := SleepWindow
	oldMaxRequests := MaxRequests

	MaxFailures = maxFailures
	SleepWindow = sleepWindow
	MaxRequests = maxRequests

	t.Cleanup(func() {
		MaxFailures = oldMaxFailures
		SleepWindow = oldSleepWindow
		MaxRequests = oldMaxRequests
	})
}

func TestBreakerPopInitializesNode(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 50*time.Millisecond, 2)

	spy := &spyBalancer{nextKey: 42}
	breaker := BalancerWrapperBreaker(spy)

	key, err := breaker.Pop()
	if err != nil {
		t.Fatalf("Pop() unexpected error: %v", err)
	}
	if key != 42 {
		t.Fatalf("Pop() key = %d, want %d", key, 42)
	}

	mu.Lock()
	node, ok := nodes[42]
	mu.Unlock()
	if !ok {
		t.Fatalf("expected node for key %d to be initialized", 42)
	}
	if node.state != StateClosed {
		t.Fatalf("node.state = %v, want %v", node.state, StateClosed)
	}
	if node.failCount != 0 || node.successCount != 0 {
		t.Fatalf("node counts = (fail=%d, success=%d), want both 0", node.failCount, node.successCount)
	}
}

func TestBreakerDeleteTripsToOpen(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 80*time.Millisecond, 2)

	spy := &spyBalancer{nextKey: 7}
	breaker := BalancerWrapperBreaker(spy)

	if _, err := breaker.Pop(); err != nil {
		t.Fatalf("Pop() unexpected error: %v", err)
	}

	breaker.Delete(7)
	breaker.Delete(7)

	mu.Lock()
	stateAfterTwo := nodes[7].state
	mu.Unlock()
	if stateAfterTwo != StateClosed {
		t.Fatalf("after 2 failures, state = %v, want %v", stateAfterTwo, StateClosed)
	}

	beforeTrip := time.Now()
	breaker.Delete(7)

	mu.Lock()
	node := nodes[7]
	mu.Unlock()
	if node.state != StateOpen {
		t.Fatalf("after %d failures, state = %v, want %v", MaxFailures, node.state, StateOpen)
	}
	if node.expiry.Before(beforeTrip) {
		t.Fatalf("expiry = %v, expected after %v", node.expiry, beforeTrip)
	}
	if len(spy.deletes) != 3 {
		t.Fatalf("underlying Delete calls = %d, want %d", len(spy.deletes), 3)
	}
}

func TestBreakerWrapperDeletesOpenNodes(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 200*time.Millisecond, 2)

	mu.Lock()
	nodes[1] = &Node{state: StateOpen, expiry: time.Now().Add(5 * time.Second)}
	mu.Unlock()

	spy := &spyBalancer{nextKey: 1}
	_ = BalancerWrapperBreaker(spy)

	if len(spy.deletes) != 1 || spy.deletes[0] != 1 {
		t.Fatalf("BalancerWrapperBreaker Delete calls = %v, want [1]", spy.deletes)
	}
}

func TestBreakerWrapperMovesExpiredOpenToHalfOpen(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 200*time.Millisecond, 2)

	mu.Lock()
	nodes[1] = &Node{state: StateOpen, expiry: time.Now().Add(-1 * time.Second)}
	mu.Unlock()

	spy := &spyBalancer{nextKey: 1}
	_ = BalancerWrapperBreaker(spy)

	if len(spy.deletes) != 0 {
		t.Fatalf("expected no deletes for expired open node, got %v", spy.deletes)
	}

	mu.Lock()
	state := nodes[1].state
	mu.Unlock()
	if state != StateHalfOpen {
		t.Fatalf("node.state = %v, want %v", state, StateHalfOpen)
	}
}

func TestBreakerHalfOpenSuccessClosesAfterMaxRequests(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 200*time.Millisecond, 2)

	mu.Lock()
	nodes[7] = &Node{state: StateHalfOpen}
	mu.Unlock()

	spy := &spyBalancer{nextKey: 7}
	breaker := BalancerWrapperBreaker(spy)

	breaker.Success(7)
	mu.Lock()
	stateAfterOne := nodes[7].state
	mu.Unlock()
	if stateAfterOne != StateHalfOpen {
		t.Fatalf("after 1 success, state = %v, want %v", stateAfterOne, StateHalfOpen)
	}

	breaker.Success(7)
	mu.Lock()
	node := nodes[7]
	mu.Unlock()
	if node.state != StateClosed {
		t.Fatalf("after %d successes, state = %v, want %v", MaxRequests, node.state, StateClosed)
	}
	if node.successCount != 0 {
		t.Fatalf("successCount = %d, want 0 after reset", node.successCount)
	}
	if len(spy.successes) != 2 {
		t.Fatalf("underlying Success calls = %d, want %d", len(spy.successes), 2)
	}
}

func TestBreakerHalfOpenDeleteReopens(t *testing.T) {
	resetBreakerState(t)
	withBreakerConfig(t, 3, 200*time.Millisecond, 2)

	mu.Lock()
	nodes[7] = &Node{state: StateHalfOpen}
	mu.Unlock()

	spy := &spyBalancer{nextKey: 7}
	breaker := BalancerWrapperBreaker(spy)

	before := time.Now()
	breaker.Delete(7)

	mu.Lock()
	node := nodes[7]
	mu.Unlock()
	if node.state != StateOpen {
		t.Fatalf("after half-open failure, state = %v, want %v", node.state, StateOpen)
	}
	if node.expiry.Before(before) {
		t.Fatalf("expiry = %v, expected after %v", node.expiry, before)
	}
	if len(spy.deletes) != 1 || spy.deletes[0] != 7 {
		t.Fatalf("underlying Delete calls = %v, want [7]", spy.deletes)
	}
}
