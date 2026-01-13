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

package lru

// inspired by "container/list"

type Element[V any] struct {
	next, prev *Element[V]
	value      V
}

type LRU[V any] struct {
	root              Element[V]
	remainingCapacity int
}

func New[V any](capacity int) *LRU[V] {
	list := new(LRU[V])

	list.root.next = &list.root
	list.root.prev = &list.root
	list.remainingCapacity = capacity

	return list
}

func (l *LRU[V]) insert(e, at *Element[V]) *Element[V] {
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	l.remainingCapacity--
	return e
}

func (l *LRU[V]) remove(e *Element[V]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil // avoid memory leaks
	e.prev = nil // avoid memory leaks
	var zero V
	e.value = zero // drop reference
	l.remainingCapacity++
}

func (l *LRU[V]) move(e, at *Element[V]) {
	if e == at {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev

	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
}

func (l *LRU[V]) MoveToFront(e *Element[V], value V) *Element[V] {
	if e == nil {
		// insert new element with the value
		e = &Element[V]{value: value}
	} else {
		e.value = value
	}

	// already at front
	if l.root.next == e {
		return e
	}

	if e.next == nil || e.prev == nil {
		// evict last element if at capacity
		if l.remainingCapacity == 0 && l.root.prev != &l.root {
			l.remove(l.root.prev)
		}

		// if still at capacity (LRU has 0 capacity), return
		if l.remainingCapacity == 0 {
			return nil
		}

		// element not in list, insert it
		return l.insert(e, &l.root)
	}

	// remove and re-insert at front
	l.move(e, &l.root)
	return e
}
