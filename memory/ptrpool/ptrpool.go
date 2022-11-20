/*
Package ptrpool provides a combination freelist and sync.Pool for storing
pointers to types. The Pool can be set to either of these or a combination of
both that has the strength of both.

In addition, a growth function can be provided to allow the freelist to grow
and shrink as needed at intervals.

It also provides a Pool.Stats() method to allow introspecting the Pool to determine
Pool behavior.

If the type to be stored is *[]byte or *bytes.Buffer, you should use
github.com/johnsiilver/pools/memory/buffers/diffsize instead. It has better
handling of these special types.

Here are some examples of different Pool configurations:

Example: Freelist only

	pool, err := New[Record](
		FreeList{Base: 100},
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

Example: Freelist with no allocations beyond freelist, aka it blocks.

	pool, err := New[Record](
		FreeList{Base: 100, DisableAllocs: true},
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

	... // The rest is the same

Example: Freelist with sync.Pool for backup

	pool, err := New[Record](
		FreeList{Base: 10},
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

Example: Freelist with sync.Pool and growth function, no maximum list size

	pool, err := New[Record](
		FreeList{Base: 10: Grower: BasicGrower},
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
	"log"
	"reflect"
	"runtime"
	"sync"
	"time"

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
	freelist atomic.Pointer[chan *A]
	pool     *sync.Pool

	freelistSettings FreeList
	newer            NewValue[A]
	reset            ResetValue[A]

	stats *stats

	close chan struct{}
}

// FreeList are arguements for the freelist. A zero value here disables the
// freelist.
type FreeList struct {
	// Base is the base amount of values in the freelist. This is the
	// minimum that the freelist will hold. This must be >= 0.
	// a FreeList can only have 0 as a Base if the FreeList is the zero value,
	// Grow is set, or the Pool does not have DisableSyncPool.
	Base int

	// DisableAllocs disables allocations when a freelist does not have
	// enough capacity. This cannot be used if the option DisableSyncPool()
	// is not provided to New(). Using this also prevents Grow from being set.
	// CAUTION: Using this requires always calling Value.Close() on any value
	// checked out of the Pool. Otherwise you will reach Pool exhaustion and
	// the Pool will block on new Get() calls indefinetly.
	DisableAllocs bool

	// Grow provides settings for growing your freelist. A zero value here
	// diables growth.
	Grow Grow
}

func (f FreeList) validate(haveSyncPool bool) error {
	if reflect.ValueOf(f).IsZero() {
		return nil
	}

	zeroGrow := reflect.ValueOf(f.Grow).IsZero()

	if f.DisableAllocs && !zeroGrow {
		return fmt.Errorf("cannot have FreeList.DisableAllocs(true) and FreeList.Grow set")
	}

	if f.DisableAllocs && haveSyncPool {
		return fmt.Errorf("cannot have FreeList.DisableAllocs(true) and not DisableSyncPool()")
	}

	if f.Base < 0 {
		return fmt.Errorf("FreeList.Base cannot be < 0")
	}

	if f.Base == 0 && zeroGrow && !haveSyncPool {
		return fmt.Errorf("FreeList.Base == 0 && f.Grow == nil && DisableSyncPool() set, this is invalid")
	}

	return f.Grow.validate()
}

// GrowthArgs are arguments to a growth function to decide if the freelist
// should grow, shrink or stay the same.
type GrowthArgs struct {
	// CurrentSize is the current size of the freelist.
	CurrentSize int
	// PoolPulls is how many times we pulled from the sync.Pool during the
	// MeasurementSpan.
	PoolPulls int
	// MeasurementSpan is the timespan these measurements occurred in.
	MeasurementSpan time.Duration
}

// Grow are instructions on how to Grow your freelist. Growing your freelist
// is generally a good idea when you have some amount of sustained use. This
// allows you to ramp up until you almost never allocate and values lasts past
// GC cycles (unlike a sync.Pool). Small lulls won't reclaim use. This comes
// at the cost of holding memory for longer periods of time.
type Grow struct {
	// Maximum indicates the maximum size the freelist can grow.
	// <= 0 indicates the freelist can grow to any size.
	Maximum int

	// MeasurementSpan is how long we should measure before checking the
	// trigger for growth. This must be > 1 second.
	MeasurementSpan time.Duration

	// Grower is a function that determines the new capacity of the freelist.
	// It receives the current size of the freelist and how many
	// items in the MeasurementSpan were allocated outside the freelist.
	// This must return a positive value.
	Grower func(args GrowthArgs) int
}

func (g Grow) validate() error {
	if reflect.ValueOf(g).IsZero() {
		return nil
	}

	if g.MeasurementSpan < 1*time.Second {
		return fmt.Errorf("FreeList.Grow.MeasurementSpan can not be set to < 1 second")
	}

	if g.Grower == nil {
		return fmt.Errorf("FreeList.Grow.Grower cannot be nil")
	}
	return nil
}

// BasicGrower provides a simple growth function that grows and shrinks the
// freelist.. It will grow the freelist
// by the average number of values that were missed per second. It will shrink
// by 10% whenever there have been no misses for 4 consecutive periods.
// Recommend if using this a 30 second MeasurementSpan.
type BasicGrower struct {
	noGrowth int
}

// Grower implements Growth.Grower.
func (b *BasicGrower) Grower(args GrowthArgs) int {
	secs := args.MeasurementSpan / time.Second
	if secs == 0 { // Protection from divide by 0.
		return args.CurrentSize
	}
	if args.PoolPulls > 0 {
		overagePerSec := args.PoolPulls / int(secs)
		if overagePerSec > 0 { // Growth
			return args.CurrentSize + overagePerSec
		}
		return args.CurrentSize // Not enough to trigger growth.
	}

	b.noGrowth++
	if b.noGrowth >= 4 {
		shrinkBy := float64(args.CurrentSize) * .10
		b.noGrowth = 0
		return args.CurrentSize - int(shrinkBy)
	}
	return args.CurrentSize
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
func New[A any](freelist FreeList, newer NewValue[A], options ...Option[A]) (*Pool[A], error) {
	if newer == nil {
		return nil, fmt.Errorf("newer cannot be nil")
	}

	p := &Pool[A]{
		pool:             &sync.Pool{},
		newer:            newer,
		freelistSettings: freelist,
		stats:            &stats{},
		close:            make(chan struct{}),
	}

	syncNewWrap := func() any {
		v := newer()
		p.stats.NewAlloc.Add(1)
		p.stats.SyncPoolMiss.Add(1)
		return v
	}
	p.pool.New = syncNewWrap

	if freelist.Base > 0 {
		fl := make(chan *A, freelist.Base)
		p.freelist.Store(&fl)
	}

	for _, o := range options {
		o(p)
	}

	if err := freelist.validate(p.pool != nil); err != nil {
		return nil, err
	}

	// If we cannot allocate, then we need to fill the static buffer before any
	// uses instead of doing lazy buffer init.
	if freelist.DisableAllocs {
		fl := p.freelist.Load()
		for i := 0; i < cap(*fl); i++ {
			v := p.newer()
			*fl <- v
		}
		p.freelist.Store(fl)
	}

	if !reflect.ValueOf(freelist.Grow).IsZero() {
		go p.grower()
	}

	return p, nil
}

// Close closes the Pool.
func (p *Pool[A]) Close() {
	close(p.close)
}

// Get retrieves a Value from the Pool.
func (p *Pool[A]) Get() Value[A] {
	fl := p.freelist.Load()
	switch {
	case fl != nil && p.pool != nil:
		select {
		case x := <-*fl:
			p.stats.InUse.Add(1)
			p.stats.FreelListAllocated.Add(1)
			return Value[A]{V: x, pool: p}
		default:
			p.stats.InUse.Add(1)
			p.stats.SyncPoolAllocated.Add(1)
			return Value[A]{V: p.pool.Get().(*A), pool: p}
		}
	case fl != nil && p.pool == nil:
		if !p.freelistSettings.DisableAllocs {
			select {
			case x := <-*fl:
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
		// we are going to wait here until we receive a value.
		t := time.NewTicker(100 * time.Nanosecond)
	repeat:
		select {
		case x := <-*fl:
			t.Stop()
			p.stats.InUse.Add(1)
			p.stats.FreelListAllocated.Add(1)
			return Value[A]{V: x, pool: p}
		case <-t.C:
			fl = p.freelist.Load()
			runtime.Gosched()
			t.Reset(100 * time.Nanosecond)
			goto repeat
		}
	case *fl == nil:
		p.stats.InUse.Add(1)
		p.stats.SyncPoolAllocated.Add(1)
		return Value[A]{V: p.pool.Get().(*A), pool: p}
	default:
		panic(fmt.Sprintf("unknown Pool condition: p.static(%v), p.pool(%v", p.freelist.Load() != nil, p.pool != nil))
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
		fl := p.freelist.Load()
		select {
		case *fl <- v.V:
			return
		default:
			p.pool.Put(v.V)
		}
		return
	}
	fl := p.freelist.Load()
	select {
	case *fl <- v.V:
	default:
		// Drop the buffer.
	}
}

// grower handles all the growing and shrinking that might need to occur.
func (p *Pool[A]) grower() {
	lastStats := p.stats.toStats()

	ticker := time.NewTicker(p.freelistSettings.Grow.MeasurementSpan)
	for {
		select {
		case <-ticker.C:
		case <-p.close:
			return
		}
		stats := p.stats.toStats()
		l := len(*p.freelist.Load())
		allocated := stats.SyncPoolAllocated - lastStats.SyncPoolAllocated
		v := p.freelistSettings.Grow.Grower(
			GrowthArgs{
				CurrentSize:     l,
				PoolPulls:       int(allocated),
				MeasurementSpan: p.freelistSettings.Grow.MeasurementSpan,
			},
		)
		lastStats = stats
		switch {
		case v == l:
			continue
		case v <= 0:
			log.Println("ignoring a pool freelist size of <= 0")
			continue
		case p.freelistSettings.Grow.Maximum <= 0:
			// Do nothing we have no growth limit.
		case v > p.freelistSettings.Grow.Maximum:
			v = p.freelistSettings.Grow.Maximum
			if v == l {
				continue
			}
		}
		if v < p.freelistSettings.Base {
			v = p.freelistSettings.Base
		}

		n := make(chan *A, v)
		old := p.freelist.Swap(&n)
		close(*old)
		for e := range *old {
			select {
			case n <- e:
				continue
			default:
			}
			break
		}
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
