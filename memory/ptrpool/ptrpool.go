/*
Package ptrpool provides a combination freelist and sync.Pool for storing
pointers to types. The Pool can be set to either of these or a combination of
both that has the strength of both.

It also provides a Pool.Stats() method to allow introspecting the Pool to determine
Pool behavior.

If the type to be stored is *[]byte or *bytes.Buffer, you should use
github.com/johnsiilver/pools/memory/buffers/diffsize instead.

Here are some examples of different Pool configurations:

Example: Freelist only

	pool, err := New[Record](
		100,
		func() *Record {
			return &Record{}
		},
		ResetValue(
			func(r *Record) {
				r.Reset()
			},
		),
		DisableSyncPool(),
	)
	if err != nil {
		// Do something
	}
	defer p.pool.Close()

	ch := make(chan Value[*Record], 1000)

	// Get a *Record from the pool, set the ID and send it for printing.
	go func() {
		defer close(ch)
		for i := 0; i < 100000; i++ {
			v := pool.Get()
			v.V.Id = i
			ch <- v
		}
	}()

	// Print all values received and send it back to the freelist.
	for v := range ch {
		fmt.Println("Record ID: %d", v.V.Id)
		v.Close()
	}

Example: Freelist with no allocations beyond freelist

	pool, err := New[Record](
		100,
		func() *Record {
			return &Record{}
		},
		ResetValue(
			func(r *Record) {
				r.Reset()
			},
		),
		DisableSyncPool(),
		DisableAllocs(),
	)
	if err != nil {
		// Do something
	}
	defer p.pool.Close()

	... // The rest is the same

Example: Freelist with sync.Pool for backup

	pool, err := New[Record](
		10,
		func() *Record {
			return &Record{}
		},
		ResetValue(
			func(r *Record) {
				r.Reset()
			},
		),

	if err != nil {
		// Do something
	}
	defer p.pool.Close()

	... // The rest is the same
*/
package ptrpool

import (
	"fmt"
	"sync"

	"sync/atomic"
)

// Value is a value that is stored in the Pool.
type Value[A any] struct {
	// V is the value.
	V *A

	pool *Pool[A]
}

// Close puts Value.V into the Pool it came from. This Value should not be used again.
func (v Value[A]) Close() {
	v.pool.put(v)
}

// NewValue is a function that returns a new value that can be used in a Value
// returned by a Pool when the Pool is empty.
type NewValue[A any] func() *A

// ResetValue resets a value before the value is reinserted into the pool.
type ResetValue[A any] func(*A)

// Pool provides a pool of reusable values that can come from a freelist or
// a sync.Pool or combinations of both.
type Pool[A any] struct {
	static chan *A
	pool   *sync.Pool

	newer  NewValue[A]
	reset  ResetValue[A]
	allocs bool

	stats *stats
}

// Option is an option to the New() constructor.
type Option[A any] func(p *Pool[A])

// DisableSyncPool disables the sync.Pool in the Pool. Reusable memory will
// only come from the static buffer.
func DisableSyncPool[A any]() func(p *Pool[A]) {
	return func(p *Pool[A]) {
		p.pool = nil
	}
}

// DisableAllocs prevents any allocations if the Pool is empty. This can
// only be used with DisableSyncPool() and an existing static buffer.
// CAUTION: Using this requires always calling Value.Close() on any value
// checked out of the Pool. Otherwise you will reach Pool exhaustion and
// the Pool will block on new Get() calls indefinetly.
func DisableAllocs[A any]() func(p *Pool[A]) {
	return func(p *Pool[A]) {
		p.allocs = false
	}
}

// Reseter provides a ResetValue function that will be called before a Value
// is put into the Pool. This is used to reset the value to a reusable state.
func Reseter[A any](reset ResetValue[A]) func(p *Pool[A]) {
	return func(p *Pool[A]) {
		p.reset = reset
	}
}

// New creates a new Pool of Values. freelist is how large your static buffer is.
// freelist values never get garbage collected. newer is the function that creates
// new values for use in the Pool when an allocation is required.
func New[A any](freelist int, newer NewValue[A], options ...Option[A]) (*Pool[A], error) {
	if newer == nil {
		return nil, fmt.Errorf("newer cannot be nil")
	}

	p := &Pool[A]{
		pool:   &sync.Pool{},
		newer:  newer,
		allocs: true,
		stats:  &stats{},
	}

	syncNewWrap := func() any {
		v := newer()
		p.stats.NewAlloc.Add(1)
		p.stats.SyncPoolMiss.Add(1)
		return v
	}
	p.pool.New = syncNewWrap

	if freelist > 0 {
		staticBuff := make(chan *A, freelist)
		p.static = staticBuff
	}

	for _, o := range options {
		o(p)
	}

	if freelist == 0 && p.pool == nil {
		return nil, fmt.Errorf("cannot have a Pool with no static buffer and disabled sync.Pool")
	}
	if !p.allocs && p.pool != nil {
		return nil, fmt.Errorf("cannot have a Pool with no allocations allowed and a non-disabled sync.Pool")
	}

	// If we cannot allocate, then we need to fill the static buffer before any
	// uses instead of doing lazy buffer init.
	if !p.allocs {
		for i := 0; i < len(p.static); i++ {
			p.put(Value[A]{V: p.newer(), pool: p})
		}
	}

	return p, nil
}

// Close closes the Pool.
func (p *Pool[A]) Close() {
	// At this time, we don't need to do anything.
	// This future proofs us if we decide to make some changes that requires
	// a Close() function.
}

// Get retrieves a Value from the Pool.
func (p *Pool[A]) Get() Value[A] {
	switch {
	case p.static != nil && p.pool != nil:
		select {
		case x := <-p.static:
			p.stats.InUse.Add(1)
			p.stats.FreelListAllocated.Add(1)
			return Value[A]{V: x, pool: p}
		default:
			p.stats.InUse.Add(1)
			p.stats.SyncPoolAllocated.Add(1)
			return Value[A]{V: p.pool.Get().(*A), pool: p}
		}
	case p.static != nil && p.pool == nil:
		if p.allocs {
			select {
			case x := <-p.static:
				p.stats.InUse.Add(1)
				p.stats.FreelListAllocated.Add(1)
				return Value[A]{V: x, pool: p}
			default:
				p.stats.InUse.Add(1)
				p.stats.NewAlloc.Add(1)
				return Value[A]{V: p.newer(), pool: p}
			}
		}
		// Since we don't allow allocating when the freelist is empty,
		// we are going to wait here indefinitely.
		x := <-p.static
		p.stats.InUse.Add(1)
		p.stats.FreelListAllocated.Add(1)
		return Value[A]{V: x, pool: p}
	case p.static == nil:
		p.stats.InUse.Add(1)
		p.stats.SyncPoolAllocated.Add(1)
		return Value[A]{V: p.pool.Get().(*A), pool: p}
	default:
		panic(fmt.Sprintf("unknown Pool condition: p.static(%v), p.pool(%v", p.static != nil, p.pool != nil))
	}
}

// put puts the value held by Value in our freelist or sync.Pool or drops it if
// neither have space.
func (p *Pool[A]) put(v Value[A]) {
	if p.reset != nil {
		p.reset(v.V)
	}
	p.stats.InUse.Add(-1)

	if p.pool != nil {
		select {
		case p.static <- v.V:
			return
		default:
			p.pool.Put(v.V)
		}
		return
	}
	select {
	case p.static <- v.V:
	default:
		// Drop the buffer.
	}
}

// Stats returns the stats for the Pool.
func (p *Pool[A]) Stats() Stats {
	return p.stats.toStats()
}

type stats struct {
	InUse atomic.Int64

	FreelListAllocated, SyncPoolAllocated, SyncPoolMiss,
	NewAlloc, TotalAllocated atomic.Uint64
}

func (s *stats) toStats() Stats {
	stats := Stats{
		InUse:              s.InUse.Load(),
		FreelListAllocated: s.FreelListAllocated.Load(),
		SyncPoolAllocated:  s.SyncPoolAllocated.Load(),
		NewAlloc:           s.NewAlloc.Load(),
		SyncPoolMiss:       s.SyncPoolMiss.Load(),
	}
	stats.TotalAllocated = stats.FreelListAllocated + stats.SyncPoolAllocated + stats.NewAlloc
	return stats
}

// Stats are stats for the Pool.
type Stats struct {
	// InUse is how many pointers are currently checked out of the Pool.
	// If your use case includes not returning values to the pool, this is
	// value is not useful.
	InUse int64

	// FreelListAllocated is how many allocations have come from the freelist.
	// SyncPoolAllocated is how many allocations came from the sync.Pool.
	// NewAlloc is how many allocation were new allocations by the allocator, including
	// sync.Pool.New() calls.
	// TotalAllocated is the total amount of allocations by the Pool.
	FreelListAllocated, SyncPoolAllocated, NewAlloc, TotalAllocated uint64

	// SyncPoolMiss is how many times the internal sync.Pool had to allocate
	// becasue it didn't have any pool values to use.
	SyncPoolMiss uint64

	// Dropped is how many values were dropped by the Pool. This is only valid
	// when using this as a freelist only. We don't have visibility into the
	// sync.Pool in order to see how many values have been freed there.
	Dropped uint64
}
