/*
Package diffsize provides a pool that holds different sizes of buffer space so
that you can retrieve buffers of a certain size.

If the buffer capacity is changed before putting it back in the Pool, it will move
to the next catagory of size.

The Pool can have a static capacity of buffers or use a sync.Pool or both.

A Pool is created to support a BufferType, which can be a *bytes.Buffer, []byte or *[]byte.
To avoid extra allocations in sync.Pool, it is best to use *bytes.Buffer or *[]byte.

Example:

	pool, err := New[*[]byte](
		Sizes{
			{Size: 1, ConstBuff: 1000, SyncPool: true},
			{Size: 2, ConstBuff: 1000, SyncPool: true},
			{Size: 4, ConstBuff: 1000, SyncPool: true},
			{Size: 8, ConstBuff: 1000, SyncPool: true},
			{Size: 16, ConstBuff: 0, SyncPool: true},
		},
	)
	if err != nil {
		panic(err)
	}

	b := pool.Get(8)

	// The buffer can be bigger than what you requested. If that is the
	// case, you *may* want to change the length to what you need.
	*b.Buffer = (*b.Buffer)[:8]

	... Do some stuff with the buffer
	...
	...

	b.Close() // Puts the buffer back in the pool.
*/
package diffsize

import (
	"bytes"
	"fmt"
	"log"
	"sort"
	"sync"
	"unsafe"
)

// BufferType details the types of buffers that are available to use.
type BufferType interface {
	*bytes.Buffer | []byte | *[]byte
}

// Buffer is a container for a BufferType. This should never be created by the
// user, it should always be give by pool.Get().
type Buffer[B BufferType] struct {
	// Buffer is the buffer that is used.
	Buffer B

	// belongsToPool indicates what size pool this DID belong to.
	belongsToPool int
	// fromSyncPool indicates that this buffer came from the SyncPool.
	fromSyncPool bool

	pool *Pool[B]
}

// Close can be called when you are done with Buffer and want it to go back
// into the Pool.
func (b *Buffer[B]) Close() {
	if b.pool != nil {
		b.pool.put(b)
	} else {
		log.Println("bug: Buffer.Close() called that had no pool to return to")
	}
}

// alloc allocates the Buffer.Buffer at "size".
func (b *Buffer[B]) alloc(size int) {
	switch any(b.Buffer).(type) {
	case []byte:
		buff := make([]byte, size)
		ptr := unsafe.Pointer(&buff)
		b.Buffer = *(*B)(ptr)
		return
	case *[]byte:
		buff := make([]byte, size)
		x := &buff
		ptr := unsafe.Pointer(&x)
		b.Buffer = *(*B)(ptr)
		return
	case *bytes.Buffer:
		buff := make([]byte, size)
		x := bytes.NewBuffer(buff)
		ptr := unsafe.Pointer(&x)
		b.Buffer = *(*B)(ptr)
		return
	}
	panic(fmt.Sprintf("bug: unsupported type %T", b.Buffer))
}

// cap returns the capacity of the Buffer.
func (b *Buffer[B]) cap() int {
	switch t := any(b.Buffer).(type) {
	case []byte:
		return cap(t)
	case *[]byte:
		return cap(*t)
	case *bytes.Buffer:
		return t.Cap()
	}
	panic(fmt.Sprintf("bug: unsupported type %T", b))
}

func (b *Buffer[B]) reset() int {
	switch t := any(b.Buffer).(type) {
	case []byte:
		if cap(t) == 0 {
			return 0
		}
		if cap(t) == len(t) {
			return len(t)
		}
		if len(t) != cap(t) {
			t = t[:cap(t)]
		}

		ptr := unsafe.Pointer(&t)
		b.Buffer = *(*B)(ptr)
		return cap(t)
	case *[]byte:
		if cap(*t) == 0 {
			return 0
		}
		if cap(*t) == len(*t) {
			return len(*t)
		}

		*t = (*t)[:cap(*t)]
		ptr := unsafe.Pointer(&t)
		b.Buffer = *(*B)(ptr)
		return cap(*t)
	case *bytes.Buffer:
		t.Reset()
		return t.Cap()
	}
	panic(fmt.Sprintf("bug: unsupported type %T", b))
}

// Pool holds a pool of *Buffer. Note: It is unsafe and unpredictable behavior to put
// Buffers from one pool in another pool.
type Pool[B BufferType] struct {
	stores      []*storage[B]
	largestSize int
}

// Sizes is a slice of Size arguments.
type Sizes []Size

func (s Sizes) validate() error {
	seen := map[int]bool{}
	for _, size := range s {
		if seen[size.Size] {
			return fmt.Errorf("Size(%d) was defined more than once", size.Size)
		}
		seen[size.Size] = true
	}
	return nil
}

// Size details a set of pool sizes that will have their own buffer reuse space.
// This allows you to retrieve buffers that have a least the size you are looking for
// to minimize allocations.
type Size struct {
	// Size is the size of the buffer you want to create a pool for.
	Size int
	// ConstBuff is how many buffer entries that are never collected
	// should be in this pool.
	ConstBuff int
	// SyncPool indicates that the bool is backed by a sync.Pool when ConstBuff
	// has no available buffers.
	SyncPool bool
	// DisallowAlloc indicates that if there are no allocation in the ConstBuff
	// and SyncPool is false, a Get() should block until ConstBuff has an available
	// buffer. If SyncPool is true, this will cause an error with New().
	DisallowAlloc bool
	// MaxSize will cause a buffer that is entering this pool
	// that is larger than MaxSize to be thrown away. This prevents large buffers
	// from being reusued when that is undesirable. This is ignored if <= 0.
	MaxSize int
}

func (s Size) validate() error {
	if s.Size == 0 {
		return fmt.Errorf("cannot create a size of 0")
	}

	if s.ConstBuff <= 0 && !s.SyncPool {
		return fmt.Errorf("cannot have a Size with ConstBuff not set and SyncPool off")
	}

	if s.DisallowAlloc && s.SyncPool {
		return fmt.Errorf("cannot have DisallowAlloc and SyncPool both set to true")
	}

	if s.MaxSize > 0 {
		if s.MaxSize < s.Size {
			return fmt.Errorf("cannot have MaxSize(%d) <= Size(%d", s.MaxSize, s.Size)
		}
	}

	return nil
}

// storage implements storage.
type storage[B BufferType] struct {
	// capSize is the capacity this is supposed to store. It may hold larger sizes.
	capSize int
	// maxSize is the maximum size of a Buffer that can be stored in this.
	maxSize int
	// disallowAlloc indicates if we can allocate a new Buffer.
	disallowAlloc bool
	// buff is a static allocation of Buffers that won't go away once allocated.
	buff chan *Buffer[B]
	// pool is an overflow holding of Buffers.
	pool *sync.Pool
}

// newStorage creates a new storage of size Size.
func newStorage[B BufferType](size Size) (*storage[B], error) {
	if err := size.validate(); err != nil {
		return nil, err
	}

	si := &storage[B]{capSize: size.Size, maxSize: size.MaxSize, disallowAlloc: size.DisallowAlloc}
	if size.ConstBuff > 0 {
		si.buff = make(chan *Buffer[B], size.ConstBuff)
	}

	if size.SyncPool {
		si.pool = &sync.Pool{
			New: func() any {
				b := &Buffer[B]{belongsToPool: size.Size}
				b.alloc(size.Size)
				return b
			},
		}
	} else {
		// If we don't have a sync.Pool, we need to pre-allocate.
		for i := 0; i < size.ConstBuff; i++ {
			b := &Buffer[B]{}
			b.alloc(size.Size)
			si.buff <- b
		}
	}

	return si, nil
}

func (s *storage[B]) cap() int {
	return s.capSize
}

func (s *storage[B]) insert(b *Buffer[B]) {
	if s.maxSize > 0 && b.cap() > s.maxSize {
		return
	}

	b.fromSyncPool = false
	b.belongsToPool = s.capSize
	select {
	case s.buff <- b:
	default:
		// This can happen when we are putting a resized buffers into
		// a higher capacity pool and that pool is full.
	}
	if s.pool == nil {
		return
	}
	b.fromSyncPool = true
	s.pool.Put(b)
}

func (s *storage[B]) get() *Buffer[B] {
	if s.disallowAlloc {
		return <-s.buff
	}

	// Try from our constant buffer first.
	select {
	case x := <-s.buff:
		return x
	default:
	}
	// If we have a pool, pull from the pool
	if s.pool != nil {
		return s.pool.Get().(*Buffer[B])
	}

	var b = &Buffer[B]{}
	b.alloc(s.capSize)
	b.belongsToPool = s.capSize

	return b
}

// New creates a new Pool.
func New[B BufferType](sizes Sizes) (*Pool[B], error) {
	if len(sizes) == 0 {
		return nil, fmt.Errorf("must specify sizes")
	}
	if err := sizes.validate(); err != nil {
		return nil, err
	}

	pool := &Pool[B]{}

	largestSize := 0
	for _, size := range sizes {
		if size.Size > largestSize {
			largestSize = size.Size
		}
		si, err := newStorage[B](size)
		if err != nil {
			return nil, err
		}
		pool.stores = append(pool.stores, si)
	}
	pool.largestSize = largestSize
	sortStorageImpl(pool.stores)

	return pool, nil
}

// Get returns a Buffer from the pool that is at least "atLeast" in capacity.
// If the type is []byte or *[]byte it's size will be set to its capacity (which may be
// bigger than you requested). If it is a *bytes.Buffer, it will have had .Reset() called
// on it. If this size is larger than the biggest pool storage, then a new Buffer
// will be returned that meets the size requirement. When DisallowAlloc is set,
// this condition bypasses that in order to prevent a issue where we cannot
// allocate what is requested. This will cause a log message.
func (p *Pool[B]) Get(atLeast int) *Buffer[B] {
	store := p.getStore(atLeast)

	if store != nil && store.capSize >= atLeast {
		b := store.get()
		b.belongsToPool = store.capSize
		b.pool = p
		return b
	}

	store = p.findStore(p.largestSize)

	// We allocate a new Buffer because that is larger than any of our storage.
	// We associate the Buffer with the largest storage, however it may not end
	// up there depending on the MaxSize setting for that pool.
	b := &Buffer[B]{}
	b.alloc(atLeast)
	if !store.disallowAlloc {
		b.belongsToPool = store.capSize
		b.pool = p
	} else {
		log.Printf("diffsize.Pool.Get(%d): exceeds pool storage sizes and largest store has disallowAlloc set, your code is bugged", atLeast)
	}
	return b
}

func (p *Pool[B]) getStore(atLeast int) *storage[B] {
	if atLeast > p.largestSize {
		return nil
	}
	l := len(p.stores)

	i := sort.Search(
		l,
		func(i int) bool {
			return p.stores[i].capSize >= atLeast
		},
	)
	if i == l {
		return nil
	}
	return p.stores[i]
}

func (p *Pool[B]) findStore(size int) *storage[B] {
	l := len(p.stores)
	i := sort.Search(
		l,
		func(i int) bool {
			return p.stores[i].capSize >= size
		},
	)
	if i == l || p.stores[i].capSize != size {
		return nil
	}
	return p.stores[i]
}

// Put puts a Buffer into the pool.
func (p *Pool[B]) put(b *Buffer[B]) {
	size := b.reset()

	// The buffer didn't change size, so it can go back to the same place it
	// started at.
	if b.reset() == b.belongsToPool {
		store := p.findStore(size)
		if store != nil {
			store.insert(b)
			return
		}
		log.Printf("bug: should have been able to find tree entry(%d), but didn't", b.reset())
	}

	// Okay, this exceeds our largest buffer size, put it in whatever is our
	// largest storage buffer.
	if size >= p.largestSize {
		store := p.findStore(p.largestSize)
		b.belongsToPool = store.capSize
		store.insert(b)
		return
	}

	// Okay, this goes in some mid tier storage. Find it and store it there.
	store := p.findStore(b.belongsToPool)
	if store == nil {
		panic(fmt.Sprintf("something is terribly wrong here: can't find largest size(%d) tree entry", p.largestSize))
	}

	b.belongsToPool = store.capSize
	store.insert(b)
}

func sortStorageImpl[B BufferType](s []*storage[B]) {
	sort.Slice(s, func(i, j int) bool {
		return s[i].capSize < s[j].capSize
	})
}
