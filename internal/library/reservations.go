package library

import (
	"context"
	"sync"
)

type reservations struct {
	mu      sync.Mutex
	current map[string]chan struct{}
}

func (r *reservations) claim(key string) (release func(), busy <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if done := r.current[key]; done != nil {
		return nil, done
	}
	if r.current == nil {
		r.current = make(map[string]chan struct{})
	}
	done := make(chan struct{})
	r.current[key] = done
	return func() {
		r.mu.Lock()
		delete(r.current, key)
		close(done)
		r.mu.Unlock()
	}, nil
}

func (r *reservations) acquire(ctx context.Context, key string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, done := r.claim(key)
		if done == nil {
			if err := ctx.Err(); err != nil {
				release()
				return nil, err
			}
			return release, nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *reservations) acquirePair(ctx context.Context, first, second string) (func(), error) {
	if second == "" || second == first {
		return r.acquire(ctx, first)
	}
	// Concurrent edits can move apps between the same two addresses.
	if second < first {
		first, second = second, first
	}
	releaseFirst, err := r.acquire(ctx, first)
	if err != nil {
		return nil, err
	}
	releaseSecond, err := r.acquire(ctx, second)
	if err != nil {
		releaseFirst()
		return nil, err
	}
	return func() {
		releaseSecond()
		releaseFirst()
	}, nil
}
