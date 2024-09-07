/*
Copyright 2024 The go418 authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
