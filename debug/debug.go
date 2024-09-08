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

package debug

import (
	debuginternal "github.com/go418/concurrentcache/internal/debug"
)

// OnStartedWaiting is a callback that is called when a Get operation starts
// waiting for the generator function to finish. This should only be used for
// debugging purposes and is subject to change.
var OnStartedWaiting = debuginternal.OnStartedWaiting
