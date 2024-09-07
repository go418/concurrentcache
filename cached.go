package concurrentcache

type CacheVersion struct {
	version uint64
	cacheId uint64
}

func (cv CacheVersion) matchesCache(cacheId uint64) bool {
	// If the cacheId in CacheVersion is 0, we are working with a general case
	// version (eg. AnyVersion or NonCachedVersion) these work for all caches.
	if cv.cacheId == 0 {
		return true
	}

	// Make sure the cacheIds match
	return cv.cacheId == cacheId
}

var (
	AnyVersion       = CacheVersion{version: 0}
	NonCachedVersion = CacheVersion{version: ^uint64(0)}
)

type Result[V any] struct {
	// Value is the value that was generated by the worker.
	Value V
	// Error is the error that was generated by the worker or
	// in the case of a canceled Get call, the context cause error.
	Error error
	// FromCache is true if the value was returned from the cache instead of
	// being generated by the worker.
	FromCache bool

	// NextVersion is the version that is newer than the version of the result.
	// If the result is from a canceled Get call, the NextVersion is the version
	// that was provided as the minVersion argument.
	NextVersion CacheVersion
}

func newFailedResult[V any](err error, minVersion CacheVersion) Result[V] {
	return Result[V]{
		Error:     err,
		FromCache: false,

		// For a canceled Get call, the NextVersion is the version that was provided
		// as the minVersion argument.
		NextVersion: minVersion,
	}
}

type versionedValue[V any] struct {
	value   V
	err     error
	version uint64
}

func (vv versionedValue[V]) isZero() bool {
	return vv.version == 0
}

func (vv versionedValue[V]) hasMinimumVersion(minVersion CacheVersion) bool {
	return vv.version >= minVersion.version
}

func (vv versionedValue[V]) newer() CacheVersion {
	return CacheVersion{version: vv.version + 1}
}

func (vv versionedValue[V]) toResult(cacheId uint64, isFromCache bool) Result[V] {
	return Result[V]{
		Value:     vv.value,
		Error:     vv.err,
		FromCache: isFromCache,

		// For a result that is from the cache, the NextVersion is the version
		// of the result plus one, making it newer than the current version.
		NextVersion: CacheVersion{
			cacheId: cacheId,
			version: vv.version + 1,
		},
	}
}

type cacheWorker[V any] struct {
	// 'cachedValue' is a copy of the value that was generated by the worker. It
	// is used to return the value to the waiting Get calls. It is safe to read
	// once the done channel is closed.
	cachedValue versionedValue[V]

	// cancel is called by the last Get call that is canceled. It cancels the
	// worker context.
	cancel func(cause error)

	// 'done' is closed when the worker is done. It is used to signal the Get
	// calls that are waiting for the worker to finish.
	done chan struct{}

	// 'nrGetCallsWaiting' is the number of Get calls that are waiting for the
	// worker to finish.
	// If this number is 0, the worker was canceled and we need to wait for
	// it to finish before calling 'Get' again.
	// The cache mutex is used to protect this field.
	nrGetCallsWaiting int64
}
