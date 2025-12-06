package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Store struct {
	limit    rate.Limit
	burst    int
	ttl      time.Duration
	mu       sync.Mutex
	limiters map[string]*clientLimiter
}

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewStore(requestsPerMinute int, burst int, ttl time.Duration) *Store {
	limit := rate.Limit(float64(requestsPerMinute) / 60.0)
	if requestsPerMinute <= 0 {
		limit = rate.Inf
	}
	if burst <= 0 {
		burst = 1
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &Store{
		limit:    limit,
		burst:    burst,
		ttl:      ttl,
		limiters: make(map[string]*clientLimiter),
	}
}

func (s *Store) Allow(key string) bool {
	limiter := s.getLimiter(key)
	return limiter.Allow()
}

func (s *Store) Wait(ctx context.Context, key string) error {
	limiter := s.getLimiter(key)
	return limiter.Wait(ctx)
}

func (s *Store) getLimiter(key string) *rate.Limiter {
	if key == "" {
		key = "anonymous"
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if limiter, ok := s.limiters[key]; ok {
		limiter.lastSeen = now
		return limiter.limiter
	}

	rl := rate.NewLimiter(s.limit, s.burst)
	s.limiters[key] = &clientLimiter{limiter: rl, lastSeen: now}
	for k, entry := range s.limiters {
		if now.Sub(entry.lastSeen) > s.ttl {
			delete(s.limiters, k)
		}
	}
	return rl
}
