package ratelimit

import (
	"testing"
	"time"
)

// fakeClock lets the tests move time forward; the limiter must not call time.Now itself.
func fakeClock() (*time.Time, func() time.Time) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	return &now, func() time.Time { return now }
}

func TestAllow_StopsAtTheLimit(t *testing.T) {
	t.Parallel()

	_, now := fakeClock()
	l := New(3, time.Minute, now)

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("call %d must be allowed", i+1)
		}
	}

	ok, wait := l.Allow("alice")
	if ok {
		t.Fatal("the fourth call must be rejected")
	}

	if wait <= 0 || wait > time.Minute {
		t.Fatalf("unexpected wait %v", wait)
	}
}

func TestAllow_WindowResets(t *testing.T) {
	t.Parallel()

	clock, now := fakeClock()
	l := New(1, time.Minute, now)

	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("first call must be allowed")
	}

	if ok, _ := l.Allow("alice"); ok {
		t.Fatal("second call inside the window must be rejected")
	}

	*clock = clock.Add(time.Minute + time.Second)

	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("after the window elapsed the call must be allowed again")
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	t.Parallel()

	_, now := fakeClock()
	l := New(1, time.Minute, now)

	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("alice must be allowed")
	}

	if ok, _ := l.Allow("bob"); !ok {
		t.Fatal("bob has its own budget")
	}
}

func TestAllow_ZeroDisables(t *testing.T) {
	t.Parallel()

	_, now := fakeClock()
	l := New(0, time.Minute, now)

	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("limit 0 means unlimited, call %d was rejected", i+1)
		}
	}
}

func TestAllow_Concurrent(t *testing.T) {
	t.Parallel()

	_, now := fakeClock()
	l := New(50, time.Minute, now)

	done := make(chan struct{})

	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				l.Allow("shared") //nolint:errcheck // result asserted by the counter below
			}

			done <- struct{}{}
		}()
	}

	for i := 0; i < 8; i++ {
		<-done
	}

	// Exactly 50 of the 160 calls must have been allowed: the counter must be exact
	// under concurrency, otherwise the limit is meaningless.
	allowed := 0

	l.mu.Lock()
	allowed = l.keys["shared"].used
	l.mu.Unlock()

	if allowed != 50 {
		t.Fatalf("expected exactly 50 allowed calls, got %d", allowed)
	}
}
