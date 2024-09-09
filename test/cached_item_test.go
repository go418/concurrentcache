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

package concurrentcache_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/go418/concurrentcache"
	"github.com/go418/concurrentcache/debug"
)

// The multiple Get calls for the same key should result in a single generateMissingValue call.
func TestItemMultiple(t *testing.T) {
	rootCtx := context.Background()

	count := 0
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
		count++
		return returnValue{
			count: count,
		}, nil
	})

	for i := 0; i < 10; i++ {
		result := cache.Get(rootCtx, concurrentcache.AnyVersion)
		require.Equal(t, returnValue{count: 1}, result.Value)
		require.NoError(t, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Check that the values were only created once.
	require.Equal(t, 1, count)
}

// The cache should be constructable directly (without calling NewCachedItem).
func TestItemConstructable(t *testing.T) {
	rootCtx := context.Background()

	count := 0
	cache := concurrentcache.CachedItem[returnValue]{
		Generate: func(ctx context.Context) (returnValue, error) {
			count++
			return returnValue{
				count: count,
			}, nil
		},
	}

	for i := 0; i < 10; i++ {
		result := cache.Get(rootCtx, concurrentcache.AnyVersion)
		require.Equal(t, returnValue{count: 1}, result.Value)
		require.NoError(t, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Check that the values were only created once.
	require.Equal(t, 1, count)
}

// An error returned by generateMissingValue should be cached similarly to a valid value.
func TestItemError(t *testing.T) {
	rootCtx := context.Background()

	count := 0
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
		count++
		rv := returnValue{
			count: count,
		}
		return rv, returnError{value: rv}
	})

	for i := 0; i < 10; i++ {
		result := cache.Get(rootCtx, concurrentcache.AnyVersion)
		require.Equal(t, returnValue{count: 1}, result.Value)
		require.Equal(t, returnError{value: returnValue{count: 1}}, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Check that the values were only created once.
	require.Equal(t, 1, count)
}

// CacheVersion can be used to force values in the cache to be re-fetched.
// There are 3 mechanisms:
// 1. set minVersion=AnyVersion, will return a cached or non-cached value
// 2. set minVersion=NonCachedVersion, will force a non-cached value
// 3. set minVersion=result.Newer(), will return a cached result only if it is newer than the previous result
func TestItemCacheVersion(t *testing.T) {
	rootCtx := context.Background()

	t.Run("repeated calls with minVersion=AnyVersion should result in one call to generateMissingValue", func(t *testing.T) {
		count := 0
		cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
			count++
			return returnValue{
				count: count,
			}, nil
		})

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, concurrentcache.AnyVersion)
			require.Equal(t, returnValue{count: 1}, result.Value)
			require.NoError(t, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}

		// Get the value again.
		result := cache.Get(rootCtx, concurrentcache.NonCachedVersion)
		require.Equal(t, returnValue{count: 2}, result.Value)
		require.NoError(t, result.Error)
		require.False(t, result.FromCache)
	})

	t.Run("minVersion=lastResult.Newer() should result in a single new call to generateMissingValue", func(t *testing.T) {
		count := 0
		cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
			count++
			return returnValue{
				count: count,
			}, nil
		})

		result := cache.Get(rootCtx, concurrentcache.AnyVersion)

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, result.NextVersion)
			require.Equal(t, returnValue{count: 2}, result.Value)
			require.NoError(t, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}
	})

	t.Run("the error returned by generateMissingValue should be returned", func(t *testing.T) {
		count := 0
		cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
			count++
			rv := returnValue{
				count: count,
			}
			return rv, returnError{value: rv}
		})

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, concurrentcache.AnyVersion)
			require.Equal(t, returnValue{count: 1}, result.Value)
			require.Equal(t, returnError{value: returnValue{count: 1}}, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}
	})
}

func TestItemParralel(t *testing.T) {
	count := 0
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
		count++
		time.Sleep(10 * time.Millisecond)
		return returnValue{
			count: count,
		}, nil
	})

	rootCtx := context.Background()
	group, gctx := errgroup.WithContext(rootCtx)

	for i := 0; i < 5000; i++ {
		group.Go(func() error {
			result := cache.Get(gctx, concurrentcache.NonCachedVersion)
			require.NoError(t, result.Error)
			return nil
		})
	}

	require.NoError(t, group.Wait())
}

// The cache should be able to handle a Get call context cancellation. The generateMissingValue
// call should be cancelled only after all the Get calls have finished/ been cancelled. This test
// spawns a bunch of Get calls, and cancels the context for 'nrConcurrentGetCallsCanceled' them,
// waits for them to finish, and then unblocks the generateMissingValue call. The generateMissingValue
// call should be called only once, and the result should be returned to all the non-canceled Get calls.
func testItemGet(
	t *testing.T,
	nrConcurrentGetCallsNonCanceled int,
	nrConcurrentGetCallsCanceled int,
	nrRepeats int,
	allAtSameTime bool,
) {
	rootCtx := context.Background()

	nrConcurrentGetCalls := nrConcurrentGetCallsNonCanceled + nrConcurrentGetCallsCanceled

	count := 0                   // Count how many times generateMissingValue was called for each key.
	block := make(chan struct{}) // Block the generateMissingValue call until all "parallel" Get calls are waiting.
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (returnValue, error) {
		count++

		select {
		case <-ctx.Done():
			rv := returnValue{
				count: count,
			}
			return rv, returnError{err: context.Cause(ctx), value: rv}
		case <-block:
		}

		return returnValue{
			count: count,
		}, nil
	})

	// We run this test multiple times, every time we run 'nrConcurrentGetCalls' Get calls in parallel,
	// they should all result in a single generateMissingValue call per run. Then we run the test again.
	// This will result in a second generateMissingValue call, and so on.
	for expectedCount := 1; expectedCount <= nrRepeats; expectedCount++ {
		startingGetCalls := nrConcurrentGetCalls
		allWaiting := make(chan struct{}) // Block until all Get calls are waiting.
		debugContext := debug.OnStartedWaiting(rootCtx, func() {
			startingGetCalls--

			if startingGetCalls == 0 {
				close(allWaiting)
			}

			if startingGetCalls < 0 {
				panic("unexpected number of calls")
			}
		})
		gctxCancelled, cancel := context.WithCancelCause(debugContext)

		groupNormal := errgroup.Group{}
		for i := 0; i < nrConcurrentGetCallsNonCanceled; i++ {
			groupNormal.Go(func() error {
				result := cache.Get(debugContext, concurrentcache.NonCachedVersion)

				valueCount := expectedCount
				if !allAtSameTime {
					// Accept any count value
					valueCount = result.Value.count
				}
				if result.Value != (returnValue{
					count: valueCount,
				}) {
					panic(fmt.Errorf("invalid value received: %v", result))
				}
				if result.Error != nil {
					panic(fmt.Errorf("unexpected error received: %s", result.Error))
				}

				return nil
			})
		}

		groupCancelled := errgroup.Group{}
		for i := 0; i < nrConcurrentGetCallsCanceled; i++ {
			groupCancelled.Go(func() error {
				result := cache.Get(gctxCancelled, concurrentcache.NonCachedVersion)

				if isCanceledEarly := result.Error == errWorkerWasCanceled; isCanceledEarly {
					if result.Value != (returnValue{}) {
						panic(fmt.Errorf("invalid value received: %v", result))
					}
					if result.Error != errWorkerWasCanceled {
						panic(fmt.Errorf("invalid error received: %s", result.Error))
					}
				} else {
					valueCount := expectedCount
					if !allAtSameTime {
						// Accept any count value
						valueCount = result.Value.count
					}
					rv := returnValue{
						count: valueCount,
					}

					if result.Value != rv {
						panic(fmt.Errorf("invalid value received: %v", result))
					}
					if !errors.Is(result.Error, returnError{err: errWorkerWasCanceled, value: rv}) ||
						!errors.Is(result.Error, errWorkerWasCanceled) {
						panic(fmt.Errorf("invalid error received: %s", result.Error))
					}
				}
				return nil
			})
		}

		// Wait for all the Get calls to be waiting.
		if nrConcurrentGetCalls > 0 && allAtSameTime {
			<-allWaiting
		}

		// Cancel half of the contexts.
		cancel(errWorkerWasCanceled)

		// Wait for all the Canceled Get calls to finish.
		require.NoError(t, groupCancelled.Wait())

		// Unblock the current generateMissingValue call.
		close(block)

		// Wait for all the Normal Get calls to finish.
		require.NoError(t, groupNormal.Wait())

		block = make(chan struct{})
	}
}

func TestItemGet(t *testing.T) {
	testItemGet(t, 0, 50, 50, false)
	testItemGet(t, 50, 0, 50, false)
	testItemGet(t, 50, 50, 50, false)
	testItemGet(t, 0, 50, 50, true)
	testItemGet(t, 50, 0, 50, true)
	testItemGet(t, 50, 50, 50, true)
}

func FuzzTestItemGet(f *testing.F) {
	f.Add(0, 50, 50, false)
	f.Add(50, 0, 50, false)
	f.Add(50, 50, 50, false)
	f.Add(0, 50, 50, true)
	f.Add(50, 0, 50, true)
	f.Add(50, 50, 50, true)

	f.Fuzz(func(t *testing.T, nrConcurrentGetCallsNonCanceled, nrConcurrentGetCallsCanceled, nrRepeats int, allAtSameTime bool) {
		if nrConcurrentGetCallsNonCanceled < 0 ||
			nrConcurrentGetCallsCanceled < 0 ||
			nrRepeats < 0 {
			t.Skip("invalid input")
		}

		if nrConcurrentGetCallsNonCanceled*nrRepeats > 10000 {
			t.Skip("will take too long")
		}

		if nrConcurrentGetCallsCanceled*nrRepeats > 10000 {
			t.Skip("will take too long")
		}

		testItemGet(t, nrConcurrentGetCallsNonCanceled, nrConcurrentGetCallsCanceled, nrRepeats, allAtSameTime)
	})
}
