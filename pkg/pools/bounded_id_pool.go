// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pools

import (
	"fmt"
	"sync"
)

type boundedIDPool struct {
	sync.Mutex
	used          map[uint32]bool
	maxUsed       uint32
	max           uint32
	currentlyUsed uint32
}

type BoundedIDPool interface {
	Get() (uint32, error)
	Put(id uint32)
	Used() uint32
}

func NewBoundedIDPool(max uint32) BoundedIDPool {
	return &boundedIDPool{
		used:          make(map[uint32]bool),
		max:           max,
		currentlyUsed: 0,
	}
}

// Get returns an ID that is unique among currently active users of this pool.
func (pool *boundedIDPool) Get() (uint32, error) {
	pool.Lock()
	defer pool.Unlock()

	// Pick a value that's been returned, if any.
	for key := range pool.used {
		delete(pool.used, key)
		pool.currentlyUsed++
		return key, nil
	}

	if pool.maxUsed == pool.max {
		return 0, fmt.Errorf("pool capacity reached (max: %d)", pool.max)
	}
	pool.currentlyUsed++
	// No recycled IDs are available, so increase the pool size.
	pool.maxUsed++
	return pool.maxUsed, nil
}

// Put ..
func (pool *boundedIDPool) Put(id uint32) {
	pool.Lock()
	defer pool.Unlock()

	if id < 1 || id > pool.maxUsed {
		panic(fmt.Errorf("IDPool.Put(%v): invalid value, must be in the range [1,%v]", id, pool.maxUsed))
	}

	if pool.used[id] {
		panic(fmt.Errorf("IDPool.Put(%v): can't put value that was already recycled", id))
	}

	pool.currentlyUsed--
	// If we're recycling maxUsed, just shrink the pool.
	if id == pool.maxUsed {
		pool.maxUsed = id - 1
		return
	}
	pool.used[id] = true
}

func (pool *boundedIDPool) Used() uint32 {
	pool.Lock()
	defer pool.Unlock()
	return pool.currentlyUsed
}
