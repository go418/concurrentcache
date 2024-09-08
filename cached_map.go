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
	"math/rand/v2"
	"sync"
)

type CachedMap[K comparable, V any] struct {
	mu                   sync.Mutex
	generateMissingValue GenerateMissingMapValueFunc[K, V]
	items                map[K]cacheItem[V]
}

type cacheItem[V any] struct {
	// Unique identifier for this item, used to make sure CacheVersions correspond to this item.
	// This value is 0 until the first generator function call starts executing.
	itemId uint64

	cachedValue versionedValue[V]
	worker      *cacheWorker[V]
}

type GenerateMissingMapValueFunc[K comparable, V any] func(ctx context.Context, key K) (V, error)

func NewCachedMap[K comparable, V any](generateMissingValue GenerateMissingMapValueFunc[K, V]) *CachedMap[K, V] {
	return &CachedMap[K, V]{
		generateMissingValue: generateMissingValue,
		items:                make(map[K]cacheItem[V]),
	}
}

func (c *CachedMap[K, V]) Get(ctx context.Context, key K, minVersion CacheVersion) Result[V] {
	debugger := debuggerFromContext(ctx)

	c.mu.Lock()
	item := c.items[key]

	if !minVersion.matchesItem(item.itemId) {
		c.mu.Unlock()
		panic("[programming error]: provided minVersion does not correspond to the current (cache, key) combo; don't mix CacheVersions across items")
	}

	var nextVersion CacheVersion
	// Return the cached value if it is at least as new as the minimum version.
	{
		cachedValue := item.cachedValue
		if !cachedValue.isZero() && cachedValue.hasMinimumVersion(minVersion) {
			defer c.mu.Unlock() // Unlock after reading the cached value.
			return cachedValue.toResult(item.itemId, true)
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

		// If the item does not have an itemId, generate a random one.
		if item.itemId == 0 {
			item.itemId = rand.Uint64()
		}

		worker = &cacheWorker[V]{
			cachedValue: versionedValue[V]{
				version: nextVersion.version,
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

	return worker.cachedValue.toResult(item.itemId, false)
}

func (c *CachedMap[K, V]) run(ctx context.Context, worker *cacheWorker[V], key K) {
	defer close(worker.done)
	result, error := c.generateMissingValue(ctx, key)

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
