package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

// example2 demonstrates how the version query parameter can be used to
// only get results that are cached for at most 5 seconds.
func example2() {
	nrExecutions := 0
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (time.Time, error) {
		nrExecutions++
		return time.Now(), nil
	})

	ttl := 5 * time.Second

	// Request the value once to cache it and wait for the TTL to expire.
	_ = cache.Get(context.TODO(), concurrentcache.AnyVersion)
	time.Sleep(ttl + 1*time.Second)

	// Requesting the value in parallel will only run the generator function once.
	group := errgroup.Group{}
	for i := 0; i < 10; i++ {
		group.Go(func() error {
			result := cache.Get(context.TODO(), concurrentcache.AnyVersion)
			if !result.FromCache {
				return nil // The value was not cached, so it falls within the TTL.
			}

			// The value was cached, request a newer value if the cached value is older than the TTL.
			if time.Since(result.Value) > ttl {
				result = cache.Get(context.TODO(), result.NextVersion)
			}

			if time.Since(result.Value) > ttl {
				panic("the value should have been refreshed")
			}

			return nil
		})
	}

	// Wait for all goroutines to finish.
	if err := group.Wait(); err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}

	// We were super efficient, the generator function was only called twice (once for the initial value and once for the refresh).
	if nrExecutions != 2 {
		panic("the generator function should have been called twice")
	}
}
