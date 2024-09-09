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

package concurrentcache

import (
	"context"
	"errors"
	"sync"

	debuginternal "github.com/go418/concurrentcache/internal/debug"
	versionsinternal "github.com/go418/concurrentcache/internal/versions"
)

// concurrentcache.CachedMap is an in-memory self-populating key-addressable cache
// that deduplicates concurrent requests. Resulting in a sequential execution of the
// function that generates the cache value.
type CachedMap[K comparable, V any] struct {
	mu    sync.Mutex
	items map[K]cacheItem[V]

	// Generator function that is called when the cached value is missing or out-of-date.
	Generate MapGenerator[K, V]
}

type cacheItem[V any] struct {
	cachedValue versionedValue[V]
	worker      *cacheWorker[V]
}

// MapGenerator is a function that generates a value for a CachedMap.
// The function will be called when the cached value is missing or out-of-date.
// The function can safely access shared resources, without the need for additional
// synchronization, as it is guaranteed to be called sequentially.
type MapGenerator[K comparable, V any] func(ctx context.Context, key K) (V, error)

func NewCachedMap[K comparable, V any](generateMissingValue MapGenerator[K, V]) *CachedMap[K, V] {
	return &CachedMap[K, V]{
		Generate: generateMissingValue,
		items:    make(map[K]cacheItem[V]),
	}
}

// Get returns the result generated by the Generate function for the given key.
// If the cached value is newer than the provided minVersion, the cached value is returned.
// If the generator function is already running for the key, Get waits for it to finish.
// If the context is canceled before the generator function finishes, Get returns a failed result.
// If all Get calls are canceled before the generator function finishes, the generator function is canceled
// and the last canceled Get call returns the result generated by the generator function, the value is not cached.
func (c *CachedMap[K, V]) Get(ctx context.Context, key K, minVersion CacheVersion) Result[V] {
	debugger := debuginternal.DebuggerFromContext(ctx)

	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[K]cacheItem[V])
	}
	item := c.items[key]

	var nextVersion CacheVersion
	// Return the cached value if it is at least as new as the minimum version.
	{
		cachedValue := item.cachedValue
		if !cachedValue.isZero() && cachedValue.hasMinimumVersion(minVersion) {
			defer c.mu.Unlock() // Unlock after reading the cached value.
			return cachedValue.toResult(true)
		}
		nextVersion = cachedValue.newer()
	}

	worker := item.worker // This pointer will be used even outside the lock.

	// If there is a worker running and it was canceled, wait for it to finish and
	// call 'Get' again accepting any value that is newer than the current cachedValue.
	if worker != nil && worker.nrGetCallsWaiting == 0 {
		c.mu.Unlock()

		select {
		case <-ctx.Done(): // The context was canceled before the previous worker shut down.
			return newFailedResult[V](context.Cause(ctx), minVersion)
		case <-worker.done: // The previous worker has shut down
		}

		// The previous worker has shut down. We can now start a new worker. We accept any
		// result that is newer than the cached value we found when we locked the mutex.
		result := c.Get(ctx, key, nextVersion)
		// Even if the result says "FromCache", we know it is newer than what we had in
		// cache before, so we can label it as "NotFromCache".
		result.FromCache = false
		return result
	}

	// If there is no worker running, create a new worker.
	if worker == nil {
		workerCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))

		worker = &cacheWorker[V]{
			cachedValue: versionedValue[V]{
				version: versionsinternal.NewGlobalVersion(),
			},

			cancel:            cancel,
			done:              make(chan struct{}),
			nrGetCallsWaiting: 0,
		}
		item.worker = worker

		c.items[key] = item

		go c.run(workerCtx, worker, key)
	}

	// Increment the number of Get calls waiting for the worker to finish.
	// This variable is protected by the cache mutex.
	worker.nrGetCallsWaiting++
	debugger.OnStartedWaiting()

	c.mu.Unlock() // Unlock before waiting for the worker to finish.

	select {
	case <-ctx.Done(): // The context was canceled before the worker finished.
		// The context was canceled. We need to decrement the number of readers
		// and cancel the worker if this was the last reader. The reason we also
		// cancel the worker is because the Get func manages the workers.
		c.mu.Lock()
		worker.nrGetCallsWaiting--
		rc := worker.nrGetCallsWaiting
		c.mu.Unlock()

		// Since we are not the last reader, we don't need to cancel the worker.
		if rc > 0 {
			return newFailedResult[V](context.Cause(ctx), minVersion)
		}

		// The last canceled Get call cancels the worker context and waits
		// for the worker to finish. This is done to prevent leaking the
		// worker goroutine. There is always at least one reader waiting for
		// the worker goroutine to finish.
		worker.cancel(context.Cause(ctx))
		<-worker.done

		// Return the result that was generated by the worker.
		// NOTE: this result will differ from the results received by the earlier
		// Get calls that were canceled. This is because they were canceled before
		// the worker finished. The result returned here is the result that was
		// generated by the worker. The context error is appended to the error
		// that was generated by the worker.
		return Result[V]{
			Value:       worker.cachedValue.value,
			Error:       errors.Join(worker.cachedValue.err, context.Cause(ctx)),
			FromCache:   false,
			NextVersion: minVersion,
		}
	case <-worker.done: // The worker has finished.
	}

	return worker.cachedValue.toResult(false)
}

func (c *CachedMap[K, V]) run(ctx context.Context, worker *cacheWorker[V], key K) {
	defer close(worker.done)
	result, error := c.Generate(ctx, key)

	// set the result on the worker
	worker.cachedValue.value = result
	worker.cachedValue.err = error

	// update the cache item
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.items[key]

	// Set the worker to nil to indicate that it is done.
	item.worker = nil

	// Update the cache value if the worker was not canceled
	if worker.nrGetCallsWaiting > 0 {
		item.cachedValue = worker.cachedValue
	}

	c.items[key] = item
}

// For advanced usecases only.
// This function allows you to write a value directly to the cache instead of using the
// Generate function to generate the value. This is useful eg. for initializing the cache
// with values that are already available/ were stored somewhere.
// The provided version must be newer than the current version of the cached value otherwise
// the value will not be set. Version AnyVersion will always set the value, while NonCachedVersion
// is not allowed and will panic.
func (c *CachedMap[K, V]) Set(key K, value V, version CacheVersion) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setLocked(key, value, version)
}

// For advanced usecases only.
// This function allows you to write a set of values directly to the cache instead of using the
// Generate function to generate the value. This is useful eg. for initializing the cache
// with values that are already available/ were stored somewhere.
// The provided version must be newer than the current version of the cached value otherwise
// the value will not be set. Version AnyVersion will always set the value, while NonCachedVersion
// is not allowed and will panic.
func (c *CachedMap[K, V]) SetAll(values map[K]V, version CacheVersion) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, value := range values {
		c.setLocked(key, value, version)
	}
}

func (c *CachedMap[K, V]) setLocked(key K, value V, version CacheVersion) {
	item := c.items[key]

	// If the version is NonCachedVersion, panic.
	if version == NonCachedVersion {
		panic("concurrentcache: NonCachedVersion is not allowed when setting a value directly in the cache")
	}

	// If the version is AnyVersion, generate a new version. But make sure it is equally as old
	// as an results that will be generated by the Generate function.
	if version == AnyVersion {
		if item.worker != nil {
			version = item.worker.cachedValue.sameAge()
		} else {
			version = versionsinternal.NewGlobalVersion()
		}
	}

	// If the cached value is already at least as new as the new version, do nothing.
	if item.cachedValue.hasMinimumVersion(version) {
		return
	}

	item.cachedValue = versionedValue[V]{
		value:   value,
		version: version,
	}
	c.items[key] = item
}
