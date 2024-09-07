package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

// example1 demonstrates how the cache can be used deduplicate calls to the
// generator function, even when made in parallel.
func example1() {
	sharedValue := 0
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (int, error) {
		// Simulate a long running operation.
		time.Sleep(1 * time.Second)

		// Update the shared value, we do not need to lock the value as the cache will
		// ensure that there are no two simultaneous executions of this generator function.
		sharedValue++

		return sharedValue, nil
	})

	// Requesting the value in parallel will only run the generator function once.
	group := errgroup.Group{}
	for i := 0; i < 10; i++ {
		group.Go(func() error {
			result := cache.Get(context.TODO(), concurrentcache.AnyVersion)
			if result.Value != 1 {
				panic("sharedValue should be 1")
			}
			if result.Error != nil {
				panic("the generator function should not have returned an error")
			}

			return nil
		})
	}

	// Wait for all goroutines to finish.
	if err := group.Wait(); err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}

	// Force the cache to refresh the value.
	result := cache.Get(context.TODO(), concurrentcache.NonCachedVersion)
	if result.Value != 2 {
		panic("sharedValue should be 2")
	}
	if result.Error != nil {
		panic("the generator function should not have returned an error")
	}
}
