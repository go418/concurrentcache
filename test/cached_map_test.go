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
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/go418/concurrentcache"
	"github.com/go418/concurrentcache/debug"
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
	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		return returnValue{
			requestedKey: key,
			count:        counts[key],
		}, nil
	})

	result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)

	// Get using a different key.
	result = cache.Get(t.Context(), "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)
}

// The multiple Get calls for the same key should result in a single generateMissingValue call.
func TestMapSameKey(t *testing.T) {
	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		return returnValue{
			requestedKey: key,
			count:        counts[key],
		}, nil
	})

	for i := range 10 {
		result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
		require.NoError(t, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Get using a different key.
	result := cache.Get(t.Context(), "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.NoError(t, result.Error)
	require.False(t, result.FromCache)

	// Check that the values were only created once.
	require.Equal(t, map[string]int{"key1": 1, "key2": 1}, counts)
}

// An error returned by generateMissingValue should be cached similarly to a valid value.
func TestMapError(t *testing.T) {
	counts := map[string]int{}
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
		counts[key]++
		rv := returnValue{
			requestedKey: key,
			count:        counts[key],
		}
		return rv, returnError{value: rv}
	})

	for i := range 10 {
		result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
		require.Equal(t, returnError{value: returnValue{requestedKey: "key1", count: 1}}, result.Error)
		require.Equal(t, i > 0, result.FromCache)
	}

	// Get using a different key.
	result := cache.Get(t.Context(), "key2", concurrentcache.AnyVersion)
	require.Equal(t, returnValue{requestedKey: "key2", count: 1}, result.Value)
	require.Equal(t, returnError{value: returnValue{requestedKey: "key2", count: 1}}, result.Error)
	require.False(t, result.FromCache)

	// Check that the values were only created once.
	require.Equal(t, map[string]int{"key1": 1, "key2": 1}, counts)
}

// CacheVersion can be used to force values in the cache to be re-fetched.
// There are 3 mechanisms:
// 1. set minVersion=AnyVersion, will return a cached or non-cached value
// 2. set minVersion=NonCachedVersion, will force a non-cached value
// 3. set minVersion=result.NextVersion, will return a cached result only if it is newer than the previous result
func TestMapCacheVersion(t *testing.T) {
	t.Run("repeated calls with minVersion=AnyVersion should result in one call to generateMissingValue", func(t *testing.T) {
		counts := map[string]int{}
		cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
			counts[key]++
			return returnValue{
				requestedKey: key,
				count:        counts[key],
			}, nil
		})

		for i := range 10 {
			result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)
			require.Equal(t, returnValue{requestedKey: "key1", count: 1}, result.Value)
			require.NoError(t, result.Error)
			require.Equal(t, i > 0, result.FromCache)
		}

		// Get the value again.
		result := cache.Get(t.Context(), "key1", concurrentcache.NonCachedVersion)
		require.Equal(t, returnValue{requestedKey: "key1", count: 2}, result.Value)
		require.NoError(t, result.Error)
		require.False(t, result.FromCache)
	})

	t.Run("minVersion=lastResult.NextVersion should result in a single new call to generateMissingValue", func(t *testing.T) {
		counts := map[string]int{}
		cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
			counts[key]++
			return returnValue{
				requestedKey: key,
				count:        counts[key],
			}, nil
		})

		result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)

		for i := range 10 {
			result := cache.Get(t.Context(), "key1", result.NextVersion)
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

		for i := range 10 {
			result := cache.Get(t.Context(), "key1", concurrentcache.AnyVersion)
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

	group, gctx := errgroup.WithContext(t.Context())

	for range 5000 {
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
		maingroup.Go(func() error {
			// We run this test multiple times, every time we run 'nrConcurrentGetCalls' Get calls in parallel,
			// they should all result in a single generateMissingValue call per run. Then we run the test again.
			// This will result in a second generateMissingValue call, and so on.
			for expectedCount := 1; expectedCount <= nrRepeats; expectedCount++ {
				startingGetCalls := nrConcurrentGetCalls
				allWaiting := make(chan struct{}) // Block until all Get calls are waiting.
				debugContext := debug.OnStartedWaiting(t.Context(), func() {
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
				for range nrConcurrentGetCallsNonCanceled {
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
							panic(fmt.Errorf("invalid value received: %v", result.Value))
						}
						if result.Error != nil {
							panic(fmt.Errorf("unexpected error received: %s", result.Error))
						}

						return nil
					})
				}

				groupCancelled := errgroup.Group{}
				for range nrConcurrentGetCallsCanceled {
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

// Weak cache should allow values to be garbage collected and removed from the cache.
func TestMapWeakCache(t *testing.T) {
	type testCase struct {
		capacity int
		run      func(t *testing.T, cache *concurrentcache.CachedMap[string, returnValue])
	}

	getKey := func(
		t *testing.T,
		cache *concurrentcache.CachedMap[string, returnValue],
		minVersion concurrentcache.CacheVersion,
		key string, count int, cached bool,
	) returnValue {
		result := cache.Get(t.Context(), key, minVersion)
		require.Equal(t, returnValue{requestedKey: key, count: count}, result.Value)
		require.NoError(t, result.Error)
		require.Equal(t, cached, result.FromCache)
		return result.Value
	}

	for i, tc := range []testCase{
		{
			capacity: 1,
			run: func(t *testing.T, cache *concurrentcache.CachedMap[string, returnValue]) {
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 1, false)
				getKey(t, cache, concurrentcache.AnyVersion, "key2", 2, false)

				// Force GC and wait until the weakly cached value is collected and removed.
				for range 5 {
					runtime.GC()
					time.Sleep(5 * time.Millisecond)
				}

				getKey(t, cache, concurrentcache.AnyVersion, "key2", 2, true) // last generated value should still be cached
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 3, false)
			},
		},
		{
			capacity: 1,
			run: func(t *testing.T, cache *concurrentcache.CachedMap[string, returnValue]) {
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 1, false)
				getKey(t, cache, concurrentcache.AnyVersion, "key2", 2, false)
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 1, true)

				// Force GC and wait until the weakly cached value is collected and removed.
				for range 5 {
					runtime.GC()
					time.Sleep(5 * time.Millisecond)
				}

				getKey(t, cache, concurrentcache.AnyVersion, "key1", 1, true) // last accessed value should still be cached
				getKey(t, cache, concurrentcache.AnyVersion, "key2", 3, false)
			},
		},
		{
			capacity: 2,
			run: func(t *testing.T, cache *concurrentcache.CachedMap[string, returnValue]) {
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 1, false)
				getKey(t, cache, concurrentcache.AnyVersion, "key2", 2, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key2", 3, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key1", 4, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key2", 5, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key2", 6, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key2", 7, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key1", 8, false)
				getKey(t, cache, concurrentcache.NonCachedVersion, "key1", 9, false)

				// Force GC and wait until the weakly cached value is collected and removed.
				for range 5 {
					runtime.GC()
					time.Sleep(5 * time.Millisecond)
				}

				// Both values should still be cached as capacity is 2.
				getKey(t, cache, concurrentcache.AnyVersion, "key1", 9, true)
				getKey(t, cache, concurrentcache.AnyVersion, "key2", 7, true)
			},
		},
	} {
		t.Run(fmt.Sprintf("test-case-%d", i), func(t *testing.T) {
			count := 0
			cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (returnValue, error) {
				count++
				return returnValue{
					requestedKey: key,
					count:        count,
				}, nil
			}, concurrentcache.WithCapacity(tc.capacity))

			tc.run(t, cache)
		})
	}
}

func TestMapWeakCacheStress(t *testing.T) {
	const capacity = 100

	count := int64(0)
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (int64, error) {
		return atomic.AddInt64(&count, 1), nil
	}, concurrentcache.WithCapacity(capacity))

	for _, minVersion := range []concurrentcache.CacheVersion{
		concurrentcache.AnyVersion,
		concurrentcache.NonCachedVersion,
	} {
		// Add 5000 unique entries in parallel
		{
			group, gctx := errgroup.WithContext(t.Context())
			for i := range 5000 {
				group.Go(func() error {
					key := fmt.Sprintf("key-%d", i+1)
					result := cache.Get(gctx, key, minVersion)
					require.NoError(t, result.Error)
					return nil
				})
			}
			require.NoError(t, group.Wait())
		}

		// Re-access the first entries that fit within capacity in parallel
		{
			group, gctx := errgroup.WithContext(t.Context())
			for i := range 5000 {
				group.Go(func() error {
					key := fmt.Sprintf("key-%d", (i%capacity)+1)
					result := cache.Get(gctx, key, minVersion)
					require.NoError(t, result.Error)
					return nil
				})
			}

			// Perform GC while the goroutines are running.
			for range 100 {
				runtime.GC()
				time.Sleep(1 * time.Millisecond)
			}

			require.NoError(t, group.Wait())
		}

		// Force GC and wait until the weakly cached value is collected and removed.
		for range 5 {
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
		}

		// All keys that we fetched last (within capacity) should be cached.
		for i := range capacity {
			key := fmt.Sprintf("key-%d", i+1)
			result := cache.Get(t.Context(), key, concurrentcache.AnyVersion)
			require.NoError(t, result.Error)
			require.True(t, result.FromCache)
		}
	}
}

// Ensure the memory footprint of a weak cache remains bounded after GC.
func TestMapWeakCacheMemory(t *testing.T) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) ([]byte, error) {
		return make([]byte, 1*1024*1024 /* 1MB per value */), nil
	}, concurrentcache.WithCapacity(0))

	for i := range 1000 {
		cache.Get(t.Context(), fmt.Sprintf("k%d", i), concurrentcache.AnyVersion)
	}

	// Force GC and measure memory used by the process after weak values are collectible.
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Allow a reasonable headroom (10MB) for allocations unrelated to the cache.
	const threshold = uint64(10 << 20)
	used := after.HeapAlloc
	base := before.HeapAlloc
	if used > base+threshold {
		t.Fatalf("weak cache memory footprint too large: before=%d after=%d diff=%d", base, used, used-base)
	}
}
