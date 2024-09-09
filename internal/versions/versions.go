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

package internalversions

import (
	"sync/atomic"
)

var globalVersion atomic.Uint64

func NewGlobalVersion() CacheVersion {
	return CacheVersion{version: globalVersion.Add(1)}
}

// CacheVersion is a version number that can be used to track the
// version of a cache entry. The version number is a monotonically
// increasing number that is globally unique across all caches and entries.
type CacheVersion struct {
	version uint64
}

var (
	// AnyVersion is a special version number that can be used to
	// indicate that the version of a cache entry does not matter.
	// The Get function will return any cached value, regardless of
	// its version and will generate a new value if there is no cached
	// value.
	AnyVersion = CacheVersion{version: 0}
	// NonCachedVersion is a special version number that can be used to
	// indicate to the Get function that we want to generate a new value
	// and do not want to use any cached value.
	NonCachedVersion = CacheVersion{version: ^uint64(0)}
)

func NextVersion(rv CacheVersion) CacheVersion {
	return CacheVersion{version: rv.version + 1}
}

func IsNewerVersion(a, b CacheVersion) bool {
	return a.version > b.version
}

// AtomicLatestVersion is a thread-safe version bookkeeper, which can
// be used to keep track of the latest version of a set of results.
// For example, if a piece of code performs 5 cache lookups, this
// AtomicLatestVersion can be used to keep track of the latest version
// of the results of those lookups and use that latest version as the
// version of the combined result. Then, the next time the code runs,
// it can invalidate all the cache lookups by specifying the latest
// version as the minimum version for each individual cache lookup.
// TODO: this mechanism might introduce some unnecessary cache invalidations.
type AtomicLatestVersion struct {
	version uint64
}

func NewAtomicLatestVersion(rv CacheVersion) AtomicLatestVersion {
	return AtomicLatestVersion(rv)
}

func (r *AtomicLatestVersion) Load() CacheVersion {
	return CacheVersion{version: atomic.LoadUint64(&r.version)}
}

func (r *AtomicLatestVersion) Update(rv CacheVersion) {
	existingValue := atomic.LoadUint64(&r.version)
	for rv.version > existingValue && !atomic.CompareAndSwapUint64(&r.version, existingValue, rv.version) {
		existingValue = atomic.LoadUint64(&r.version)
	}
}
