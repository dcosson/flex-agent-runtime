package transport

import (
	"sync"
	"time"
)

type inputLimiter struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	lastSeen time.Time
}

func newInputLimiter(rateBytesPerSec, burstBytes int) *inputLimiter {
	now := time.Now()
	r := float64(rateBytesPerSec)
	b := float64(burstBytes)
	return &inputLimiter{
		rate:     r,
		burst:    b,
		tokens:   b,
		lastSeen: now,
	}
}

func (l *inputLimiter) Allow(n int) bool {
	if n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(l.lastSeen).Seconds()
	l.lastSeen = now
	l.tokens += elapsed * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	need := float64(n)
	if need > l.tokens {
		return false
	}
	l.tokens -= need
	return true
}
