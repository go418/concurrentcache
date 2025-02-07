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
	"testing"

	"github.com/go-logr/logr"
	"github.com/go418/concurrentcache"
	"github.com/go418/concurrentcache/debug"
	"github.com/go418/concurrentcache/logger"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2/ktesting"
)

func TestMapLogger(t *testing.T) {
	rootCtx := context.Background()
	testLogger := ktesting.NewLogger(t, ktesting.NewConfig(
		ktesting.BufferLogs(true),
	))
	rootCtx = logr.NewContext(rootCtx, testLogger)

	step1 := make(chan struct{})
	step2 := make(chan struct{})
	step3 := make(chan struct{})
	step4 := make(chan struct{})
	cache := concurrentcache.NewCachedMap(func(ctx context.Context, key string) (struct{}, error) {
		logger := logr.FromContextOrDiscard(ctx)

		logger.Info("test log 1", "key", "value")
		close(step1)
		<-step3
		logger.Info("test log 2", "key", "value")

		return struct{}{}, nil
	})
	cache.NewWorkerContext = logger.NewWorkerContextWithLogger

	get1Ctx, get1CtxCancel := context.WithCancel(rootCtx)
	go func() {
		result := cache.Get(get1Ctx, "key1", concurrentcache.AnyVersion)
		require.ErrorContains(t, result.Error, "context canceled")
		close(step3)
	}()
	go func() {
		<-step1
		result := cache.Get(debug.OnStartedWaiting(rootCtx, func() { close(step2) }), "key1", concurrentcache.AnyVersion)
		require.NoError(t, result.Error)
		close(step4)
	}()

	<-step2
	get1CtxCancel()
	<-step4

	if testingLogger, ok := testLogger.GetSink().(ktesting.Underlier); ok {
		buffer := testingLogger.GetBuffer()
		require.Equal(t, `INFO test log 1 worker="attached" key="value"
INFO test log 2 worker="detached" key="value"
`, buffer.String())
	}
}

func TestItemLogger(t *testing.T) {
	rootCtx := context.Background()
	testLogger := ktesting.NewLogger(t, ktesting.NewConfig(
		ktesting.BufferLogs(true),
	))
	rootCtx = logr.NewContext(rootCtx, testLogger)

	step1 := make(chan struct{})
	step2 := make(chan struct{})
	step3 := make(chan struct{})
	step4 := make(chan struct{})
	cache := concurrentcache.NewCachedItem(func(ctx context.Context) (struct{}, error) {
		logger := logr.FromContextOrDiscard(ctx)

		logger.Info("test log 1", "key", "value")
		close(step1)
		<-step3
		logger.Info("test log 2", "key", "value")

		return struct{}{}, nil
	})
	cache.NewWorkerContext = logger.NewWorkerContextWithLogger

	get1Ctx, get1CtxCancel := context.WithCancel(rootCtx)
	go func() {
		result := cache.Get(get1Ctx, concurrentcache.AnyVersion)
		require.ErrorContains(t, result.Error, "context canceled")
		close(step3)
	}()
	go func() {
		<-step1
		result := cache.Get(debug.OnStartedWaiting(rootCtx, func() { close(step2) }), concurrentcache.AnyVersion)
		require.NoError(t, result.Error)
		close(step4)
	}()

	<-step2
	get1CtxCancel()
	<-step4

	if testingLogger, ok := testLogger.GetSink().(ktesting.Underlier); ok {
		buffer := testingLogger.GetBuffer()
		require.Equal(t, `INFO test log 1 worker="attached" key="value"
INFO test log 2 worker="detached" key="value"
`, buffer.String())
	}
}
