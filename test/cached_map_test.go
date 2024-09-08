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

	"concurrentcache"
)

type returnValue struct {
	requestedKey string
	count        int
}

type returnError struct {
	err   error
	value returnValue
}

func (value returnError) Error() string {
	underlying := "originated from worker"
	if value.err != nil {
		underlying = value.err.Error()
	}

	return fmt.Sprintf("[from worker - %+v]: %s", value.value, underlying)
}

// Different keys should result in separate generateMissingValue calls.
func TestMapDifferentKeys(t *testing.T) {
	rootCtx := context.Background()

	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		return returnValue{
			requestedKey: key,
			count:        counts[key],
		}, nil
	})

	result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)

	// Get using a different key.
	result = cache.Get(rootCtx, "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)
}

// The multiple Get calls for the same key should result in a single generateMissingValue call.
func TestMapSameKey(t *testing.T) {
	rootCtx := context.Background()

	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		return returnValue{
			requestedKey: key,
			count:        counts[key],
		}, nil
	})

	for i := 0; i < 10; i++ {
		result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
		require.NoError(t, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Get using a different key.
	result := cache.Get(rootCtx, "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)

	// Check that the values were only created once.
	require.Equal(t, map[string]int{"key1": 1, "key2": 1}, counts)
}

// An error returned by generateMissingValue should be cached similarly to a valid value.
func TestMapError(t *testing.T) {
	rootCtx := context.Background()

	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		rv := returnValue{
			requestedKey: key,
			count:        counts[key],
		}
		return rv, returnError{value: rv}
	})

	for i := 0; i < 10; i++ {
		result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
		require.Equal(t, returnError{value: returnValue{requestedKey: "key1", count: 1}}, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Get using a different key.
	result := cache.Get(rootCtx, "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.Equal(t, returnError{value: returnValue{requestedKey: "key2", count: 1}}, result.Error)
	require.False(t, result.FromCache)

	// Check that the values were only created once.
	require.Equal(t, map[string]int{"key1": 1, "key2": 1}, counts)
}

func TestMapPanic(t *testing.T) {
	rootCtx := context.Background()

	cache1 := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (bool, error) {
		return true, nil
	})

	cache2 := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (bool, error) {
		return true, nil
	})

	// Get a value from cache1 to generate a version.
	result1 := cache1.Get(rootCtx, "key1", concurrentcache.AnyVersion)
	require.NoError(t, result1.Error)

	// Attempt to use the version from cache1 on cache2, which should panic.
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic but did not occur")
		}

		require.Equal(t, "[programming error]: provided minVersion does not correspond to the current (cache, key) combo; don't mix CacheVersions across items", r)
	}()

	cache2.Get(rootCtx, "key1", result1.NextVersion)
}

// CacheVersion can be used to force values in the cache to be re-fetched.
// There are 3 mechanisms:
// 1. set minVersion=AnyVersion, will return a cached or non-cached value
// 2. set minVersion=NonCachedVersion, will force a non-cached value
// 3. set minVersion=result.Newer(), will return a cached result only if it is newer than the previous result
func TestMapCacheVersion(t *testing.T) {
	rootCtx := context.Background()

	t.Run("repeated calls with minVersion=AnyVersion should result in one call to generateMissingValue", func(t *testing.T) {
		counts := map[string]int{}
		cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
			counts[key]++
			return returnValue{
				requestedKey: key,
				count:        counts[key],
			}, nil
		})

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)
			require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
			require.NoError(t, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}

		// Get the value again.
		result := cache.Get(rootCtx, "key1", concurrentcache.NonCachedVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 2}, result.Value)
		require.NoError(t, result.Error)
		require.False(t, result.FromCache)
	})

	t.Run("minVersion=lastResult.Newer() should result in a single new call to generateMissingValue", func(t *testing.T) {
		counts := map[string]int{}
		cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
			counts[key]++
			return returnValue{
				requestedKey: key,
				count:        counts[key],
			}, nil
		})

		result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, "key1", result.NextVersion)
			require.Equal(t, returnValue{requestedKey: "key1", count: 2}, result.Value)
			require.NoError(t, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}
	})

	t.Run("the error returned by generateMissingValue should be returned", func(t *testing.T) {
		counts := map[string]int{}
		cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
			counts[key]++
			rv := returnValue{
				requestedKey: key,
				count:        counts[key],
			}
			return rv, returnError{value: rv}
		})

		for i := 0; i < 10; i++ {
			result := cache.Get(rootCtx, "key1", concurrentcache.AnyVersion)
			require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
			require.Equal(t, returnError{value: returnValue{requestedKey: "key1", count: 1}}, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}
	})
}

func TestMapParralel(t *testing.T) {
	count := 0
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		count++
		time.Sleep(10 * time.Millisecond)
		return returnValue{
			requestedKey: key,
			count:        count,
		}, nil
	})

	rootCtx := context.Background()
	group, gctx := errgroup.WithContext(rootCtx)

	for i := 0; i < 5000; i++ {
		group.Go(func() error {
			result := cache.Get(gctx, "key1", concurrentcache.NonCachedVersion)
			require.Equal(t, "key1", result.Value.requestedKey)
			require.NoError(t, result.Error)
			return nil
		})
	}

	require.NoError(t, group.Wait())
}

var errWorkerWasCanceled = fmt.Errorf("worker was canceled")

// The cache should be able to handle a Get call context cancellation. The generateMissingValue
// call should be cancelled only after all the Get calls have finished/ been cancelled. This test
// spawns a bunch of Get calls, and cancels the context for 'nrConcurrentGetCallsCanceled' them,
// waits for them to finish, and then unblocks the generateMissingValue call. The generateMissingValue
// call should be called only once, and the result should be returned to all the non-canceled Get calls.
func testMapGet(
	t *testing.T,
	nrConcurrentGetCallsNonCanceled int,
	nrConcurrentGetCallsCanceled int,
	nrKeys int,
	nrRepeats int,
	allAtSameTime bool,
) {
	rootCtx := context.Background()

	nrConcurrentGetCalls := nrConcurrentGetCallsNonCanceled + nrConcurrentGetCallsCanceled

	counts := make([]int, nrKeys)           // Count how many times generateMissingValue was called for each key.
	blocks := make([]chan struct{}, nrKeys) // Block the generateMissingValue call until all "parallel" Get calls are waiting.
	for i := range blocks {
		blocks[i] = make(chan struct{})
	}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key int) (returnValue, error) {
		counts[key]++

		select {
		case <-ctx.Done():
			rv := returnValue{
				count:        counts[key],
				requestedKey: fmt.Sprintf("key%d", key),
			}
			return rv, returnError{err: context.Cause(ctx), value: rv}
		case <-blocks[key]:
		}

		return returnValue{
			count:        counts[key],
			requestedKey: fmt.Sprintf("key%d", key),
		}, nil
	})

	maingroup := errgroup.Group{}
	for key, block := range blocks {
		key := key
		block := block
		maingroup.Go(func() error {
			// We run this test multiple times, every time we run 'nrConcurrentGetCalls' Get calls in parallel,
			// they should all result in a single generateMissingValue call per run. Then we run the test again.
			// This will result in a second generateMissingValue call, and so on.
			for expectedCount := 1; expectedCount <= nrRepeats; expectedCount++ {
				startingGetCalls := nrConcurrentGetCalls
				allWaiting := make(chan struct{}) // Block until all Get calls are waiting.
				debugContext := concurrentcache.OnStartedWaiting(rootCtx, func() {
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
						result := cache.Get(debugContext, key, concurrentcache.NonCachedVersion)

						valueCount := expectedCount
						if !allAtSameTime {
							// Accept any count value
							valueCount = result.Value.count
						}
						if result.Value != (returnValue{
							requestedKey: fmt.Sprintf("key%d", key),
							count:        valueCount,
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
						result := cache.Get(gctxCancelled, key, concurrentcache.NonCachedVersion)

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
								requestedKey: fmt.Sprintf("key%d", key),
								count:        valueCount,
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
				if err := groupCancelled.Wait(); err != nil {
					return err
				}

				// Unblock the current generateMissingValue call.
				close(block)

				// Wait for all the Normal Get calls to finish.
				if err := groupNormal.Wait(); err != nil {
					return err
				}

				block = make(chan struct{})
				blocks[key] = block
			}

			return nil
		})
	}

	require.NoError(t, maingroup.Wait())
}

func TestMapGet(t *testing.T) {
	testMapGet(t, 0, 50, 50, 50, false)
	testMapGet(t, 50, 0, 50, 50, false)
	testMapGet(t, 50, 50, 50, 50, false)
	testMapGet(t, 0, 50, 50, 50, true)
	testMapGet(t, 50, 0, 50, 50, true)
	testMapGet(t, 50, 50, 50, 50, true)
}

func FuzzTestMapGet(f *testing.F) {
	f.Add(0, 50, 50, 50, false)
	f.Add(50, 0, 50, 50, false)
	f.Add(50, 50, 50, 50, false)
	f.Add(0, 50, 50, 50, true)
	f.Add(50, 0, 50, 50, true)
	f.Add(50, 50, 50, 50, true)

	f.Fuzz(func(t *testing.T, nrConcurrentGetCallsNonCanceled, nrConcurrentGetCallsCanceled, nrKeys, nrRepeats int, allAtSameTime bool) {
		if nrConcurrentGetCallsNonCanceled < 0 ||
			nrConcurrentGetCallsCanceled < 0 ||
			nrKeys < 0 ||
			nrRepeats < 0 {
			t.Skip("invalid input")
		}

		if nrConcurrentGetCallsNonCanceled*nrKeys*nrRepeats > 10000 {
			t.Skip("will take too long")
		}

		if nrConcurrentGetCallsCanceled*nrKeys*nrRepeats > 10000 {
			t.Skip("will take too long")
		}

		testMapGet(t, nrConcurrentGetCallsNonCanceled, nrConcurrentGetCallsCanceled, nrKeys, nrRepeats, allAtSameTime)
	})
}
