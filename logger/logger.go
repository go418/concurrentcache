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

package logger

import (
	"context"
	"sync/atomic"

	"github.com/go-logr/logr"
)

// NewWorkerContextWithLogger is a worker context constructor that can be passed to CachedMap or
// CachedItem to create a new context for the worker based on the context passed to the Get call.
// This implementation will modify the logger found in the context of the Get call to include
// a new key-value pair that indicates if the worker is still attached to the Get call. The worker
// is considered detached if the Get call was canceled and has returned before the worker logged
// the message.
func NewWorkerContextWithLogger(getCtxWithoutCancel context.Context) (context.Context, func()) {
	logger, err := logr.FromContext(getCtxWithoutCancel)
	if err != nil {
		return getCtxWithoutCancel, func() {}
	}

	workerLogger := &loggerWrapper{LogSink: logger.GetSink()}
	return logr.NewContext(
			getCtxWithoutCancel,
			logger.WithSink(workerLogger),
		), func() {
			workerLogger.detached.Store(true)
		}
}

type loggerWrapper struct {
	logr.LogSink
	detached atomic.Bool
}

func (lw *loggerWrapper) Info(level int, msg string, keysAndValues ...any) {
	newKeysAndValues := make([]any, 0, len(keysAndValues)+2)
	if lw.detached.Load() {
		newKeysAndValues = append(newKeysAndValues, "worker", "detached")
	} else {
		newKeysAndValues = append(newKeysAndValues, "worker", "attached")
	}
	newKeysAndValues = append(newKeysAndValues, keysAndValues...)
	lw.LogSink.Info(level, msg, newKeysAndValues...)
}

func (lw *loggerWrapper) Error(err error, msg string, keysAndValues ...any) {
	newKeysAndValues := make([]any, 0, len(keysAndValues)+2)
	if lw.detached.Load() {
		newKeysAndValues = append(newKeysAndValues, "worker", "detached")
	} else {
		newKeysAndValues = append(newKeysAndValues, "worker", "attached")
	}
	newKeysAndValues = append(newKeysAndValues, keysAndValues...)
	lw.LogSink.Error(err, msg, newKeysAndValues...)
}
