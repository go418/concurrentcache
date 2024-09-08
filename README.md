# concurrentcache

<a href="https://godoc.org/github.com/go418/concurrentcache"><img src="https://godoc.org/github.com/go418/concurrentcache?status.svg"/></a>
<a href="https://goreportcard.com/report/github.com/go418/concurrentcache"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/go418/concurrentcache"/></a>

> concurrentcache is an in-memory [self-populating](#a-self-populating-cache) cache that [deduplicates concurrent requests](#a-concurrent-deduplicating-cache). Resulting
> in a sequential execution of the function that generates the cache value.

## Features:
- **Context-aware cache**: the generator function is canceled when all the requests are canceled.  
*see [A context-aware cache](#a-context-aware-cache)*
- **Version-queryable cache**: you can request (1) any value (possibly cached), (2) a fresh
value (not cached), (3) a value that is newer than a value returned previously.  
*see [A version-queryable cache](#a-version-queryable-cache)*

## Usage

1. The cache can be used deduplicate calls to the generator function, even when made in parallel.

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

func main() {
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
```

2. The version query parameter can be used to only get results that are cached for at most 5 seconds.

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

func main() {
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
```

3. Some of the requests can be cancelled without affecting the other requests.  
**NOTE (not shown in this example):** The generator function will only be cancelled
when all requests are cancelled, new requests will ignore this cancelled
execution and start a new one.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go418/concurrentcache"
	"golang.org/x/sync/errgroup"
)

func main() {
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
```

## A self-populating cache

The cache is a self-populating cache. This means that the cache is responsible for
calling the generator function when there is no value in the cache or when the value
is stale. The cache sits between the requestor and the generator.

```
           requests    calls generator
[ requestor ] -> [ cache ] -> [ generator function ]
```

## A concurrent-deduplicating cache

The cache deduplicates concurrent requests. This means that when multiple requests are
made to the cache before the generator function has completed, only one generator function
will be executed. All other requests will wait for the generator function to complete.
The cache guarantees non-overlapping function calls to the underlying generator function.

```
Generator  |       execute generator    generator returns
goroutine  | -------------+=====================+------------
-----------|
Requestor1 |          GET K=a             GET returns
goroutine  | -------------^---------------------+------------
-----------|
Requestor2 |                 GET K=a       GET returns
goroutine  | -------------------^---------------+------------
```

## A context-aware cache

The cache passes a context to the generator function. Additionally, all requests (Get calls)
are also passed a context. The generator function is canceled when all requests are canceled.
If there is a single request that is not canceled, the generator function will continue to run.
The result of a canceled generator function is not cached. The last request that is canceled
waits for the canceled generator function to complete, it will also receive the result of the
generator function extended with the cancelation Cause error. All earlier canceled requests
will only receive the cancelation Cause error.

Scenario 1: A single request is canceled
```
Generator  |       execute generator    generator returns
goroutine  | -------------+=====================+------------
-----------|
Requestor1 |          GET K=a             GET returns
goroutine  | -------------^---------------------+------------
-----------|
Requestor2 |               GET K=a  GET context
goroutine  |                          canceled
           | -------------------^--------x-------------------
```

Scenario 2: All requests are canceled
```
Generator  |          execute           generator context
goroutine  |         generator               canceled
           | -------------+=====================x------------
-----------|
Requestor1 |          GET K=a              GET context
goroutine  |                                 canceled 
           | -------------^---------------------x------------
-----------|
Requestor2 |                GET K=a  GET context
goroutine  |                          canceled
           | -------------------^--------x-----------------
```

## A version-queryable cache

The cache has a version query parameter that can be used to request a value that is:
- `AnyVersion`: any value, possibly cached.
- `NonCachedVersion`: a fresh value, not cached.
- `NextVersion`: a value that is newer than a value returned previously.

Using this version query parameter, we can implement more advanced caching strategies.

For example, we can query the cache for a value and request a newer value if we detect
that the returned value has been cached for longer than a certain TTL (see example 2).

Or we can use the returned value to perform an action and request a newer value if the
action was unsuccessful (eg. when caching a token that has expired). Lastly, the returned
value also has a `FromCache` field that indicates if the value was cached or not. This
can be used to prevent calling the generator twice when the action is unsuccessful but
we know that the value was up-to-date.

## CachedItem vs CachedMap

The `CachedItem` is a single value cache, while the `CachedMap` is a map cache that
stores multiple values, each with its own key.

## Compared to ...

- **`sync.Map` and `map[..] + sync.Mutex`**: These concurrent maps can be used to cache
results, but they do not deduplicate concurrent requests. This means that if a request takes
a long time to complete and a second request is made, the second request will also run the
generator function. This means that there is no guarantee that the generator function will
be sequentially executed.

- **`sync.Mutex`**: A simple mutex can be used to prevent logic from being executed concurrently.
However, it has no support for context cancellation. This means that if the generator function
takes a long time to complete, requests that are cancelled will be stuck waiting for the
generator function to complete. This can result in non-responsive behavior. Additionally, caching
the result of the generator function has to be done manually.

- `sync.Once...`: These functions can be used to ensure that a function is only executed once. Some
of these functions can cache the result of the function. However, there is no support for requesting
a new execution of the generator function. Also, there is no support for context cancellation, all
requests will wait for the generator function to complete.

- `singleflight.Group`: This package can be used to deduplicate concurrent requests. It also supports
Forgetting a key which will result in a new execution of the generator function. However, caching the
result of the generator function has to be done manually. Also, there is no support for context cancellation.
