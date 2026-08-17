package main

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	rpcReadAttempts       = 4
	publicRPCMinInterval  = 300 * time.Millisecond
	privateRPCMinInterval = 50 * time.Millisecond
)

// rpcRequestCoordinator spaces requests across every wallet using the same
// endpoint. This prevents a multi-wallet collection from bursting a public RPC.
type rpcRequestCoordinator struct {
	mu   sync.Mutex
	next map[string]time.Time
}

var sharedRPCRequests = rpcRequestCoordinator{next: make(map[string]time.Time)}

func (c *rpcRequestCoordinator) wait(ctx context.Context, endpoint string) error {
	interval := privateRPCMinInterval
	if strings.TrimRight(strings.TrimSpace(endpoint), "/") == defaultRobinhoodRPC {
		interval = publicRPCMinInterval
	}
	now := time.Now()
	c.mu.Lock()
	reserved := now
	if c.next[endpoint].After(reserved) {
		reserved = c.next[endpoint]
	}
	c.next[endpoint] = reserved.Add(interval)
	c.mu.Unlock()

	if !reserved.After(now) {
		return nil
	}
	timer := time.NewTimer(reserved.Sub(now))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// rpcRead retries only calls made before signing/broadcast. SendTransaction is
// intentionally never passed here because an interrupted response is ambiguous.
func rpcRead[T any](ctx context.Context, endpoint string, operation func() (T, error)) (T, error) {
	var zero T
	var err error
	for attempt := 0; attempt < rpcReadAttempts; attempt++ {
		if waitErr := sharedRPCRequests.wait(ctx, endpoint); waitErr != nil {
			return zero, waitErr
		}
		result, callErr := operation()
		if callErr == nil {
			return result, nil
		}
		err = callErr
		if !isRetryableRPCError(callErr) || attempt == rpcReadAttempts-1 {
			return zero, callErr
		}
		delay := 250 * time.Millisecond * time.Duration(1<<attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, err
}

func isRetryableRPCError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"429", "too many requests", "rate limit", "-32005",
		"500 internal server error", "502", "503", "504", "bad gateway", "service unavailable", "gateway timeout",
		"timeout", "deadline exceeded", "temporarily unavailable", "connection reset", "connection refused", "unexpected eof",
		"max fee per gas less than block base fee",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
