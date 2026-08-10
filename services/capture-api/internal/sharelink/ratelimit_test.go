package sharelink

import (
	"testing"
	"time"
)

func TestLimiter_AllowsUpToMax(t *testing.T) {
	l := &Limiter{Max: 3, Window: time.Minute}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.Allow("k", now) {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if l.Allow("k", now) {
		t.Fatalf("4th attempt should be rejected")
	}
}

func TestLimiter_ResetsAfterWindow(t *testing.T) {
	l := &Limiter{Max: 1, Window: time.Minute}
	now := time.Now()
	if !l.Allow("k", now) {
		t.Fatalf("first attempt should be allowed")
	}
	if l.Allow("k", now) {
		t.Fatalf("second attempt within window should be rejected")
	}
	if !l.Allow("k", now.Add(2*time.Minute)) {
		t.Fatalf("attempt after window should be allowed")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := &Limiter{Max: 1, Window: time.Minute}
	now := time.Now()
	if !l.Allow("a", now) || !l.Allow("b", now) {
		t.Fatalf("distinct keys should each get their own budget")
	}
}
