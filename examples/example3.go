package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

// example3 demonstrates that some of the requests can be cancelled without
// affecting the other requests.
// NOTE (not shown in this file): The generator function will only be cancelled
// when all requests are cancelled, new requests will ignore this cancelled
// execution and start a new one.
func example3() {
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (bool, error) {
		// Simulate some expensive computation
		time.Sleep(1 * time.Second)

		isCancelled := ctx.Err() != nil

		return isCancelled, nil
	})

	group := errgroup.Group{}

	// Make 5 requests that will be cancelled after 500ms (halfway through the computation)
	for i := 0; i < 5; i++ {
		group.Go(func() error {
			requestCtx, cancel := context.WithTimeout(context.TODO(), 500*time.Millisecond)
			defer cancel()

			result := cache.Get(requestCtx, concurrentcache.AnyVersion)

			// All cancelled requests should return an error
			if !errors.Is(result.Error, context.DeadlineExceeded) {
				panic("expected a context deadline exceeded error")
			}

			return nil
		})
	}

	// Make 5 requests that will succeed
	for i := 0; i < 5; i++ {
		group.Go(func() error {
			result := cache.Get(context.TODO(), concurrentcache.AnyVersion)

			// All successful requests should return false, as the generator function was not cancelled
			if result.Value {
				panic("expected the generator function to not be cancelled")
			}

			return nil
		})
	}

	// Wait for all requests to complete
	if err := group.Wait(); err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
}
