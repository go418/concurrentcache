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

import "context"

type debugKey struct{}

type debugFns struct {
	onStartedWaiting func()
}

func (d *debugFns) OnStartedWaiting() {
	if d == nil || d.onStartedWaiting == nil {
		return
	}

	d.onStartedWaiting()
}

func OnStartedWaiting(ctx context.Context, onStartedWaiting func()) context.Context {
	return context.WithValue(ctx, debugKey{}, debugFns{
		onStartedWaiting: onStartedWaiting,
	})
}

func debuggerFromContext(ctx context.Context) *debugFns {
	if v, ok := ctx.Value(debugKey{}).(debugFns); ok {
		return &v
	}
	return nil
}
