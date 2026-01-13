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

type cacheOptions struct {
	// newWorkerContext is a function that creates a new context for the worker.
	// It receives the context passed to the Get call (without the cancel) and
	// returns the context for the worker and a function that will be called when
	// the Get call is canceled before the worker finishes.
	// This function is optional and will by default return the context passed to it.
	newWorkerContext NewWorkerContext
}

type mapCacheOptions struct {
	cacheOptions
}

type itemCacheOptions struct {
	cacheOptions
}

type ItemCacheOption interface {
	applyItemCache(*itemCacheOptions)
}

type MapCacheOption interface {
	applyMapCache(*mapCacheOptions)
}

func WithMapNewWorkerContext(fn NewWorkerContext) interface {
	MapCacheOption
	ItemCacheOption
} {
	return cacheOptionFunc(func(opts *cacheOptions) {
		opts.newWorkerContext = fn
	})
}

// cacheOptionFunc is a helper type to create cache options.

type cacheOptionFunc func(*cacheOptions)

var _ ItemCacheOption = cacheOptionFunc(nil)
var _ MapCacheOption = cacheOptionFunc(nil)

func (f cacheOptionFunc) applyItemCache(opts *itemCacheOptions) {
	f(&opts.cacheOptions)
}

func (f cacheOptionFunc) applyMapCache(opts *mapCacheOptions) {
	f(&opts.cacheOptions)
}
