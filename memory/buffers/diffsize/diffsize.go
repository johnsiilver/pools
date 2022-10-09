/*
Package diffsize provides a pool that holds different sizes of buffer space so
that you can retrieve buffers of a certain size.

If the buffer capacity is changed before putting it back in the Pool, it will move
to the next catagory of size.

The Pool can have a static capcity of buffers or use a sync.Pool or both.

A Pool is created to support a BufferType, which can be a *bytes.Buffer, []byte or *[]byte.
To avoid extra allocations, it is best to use *bytes.Buffer or *[]byte.

Example:

	pool, err := New[*[]byte](
		[]Sizes{
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

	b.Close() // Puts it back in the pool, you can also do pool.Put(b)
*/
package diffsize

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/google/btree"
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
	b.pool.Put(b)
}

// alloc allocates a new Buffer.Buffer of "size" and returns a new Buffer.
// The original Buffer stays the same.
func (b *Buffer[B]) alloc(size int) {
	switch any(b.Buffer).(type) {
	case []byte:
		buff := make([]byte, size)
		ptr := unsafe.Pointer(&buff)
		b.Buffer = *(*B)(ptr)
	case *[]byte:
		buff := make([]byte, size)
		x := &buff
		ptr := unsafe.Pointer(&x)
		b.Buffer = *(*B)(ptr)
	case *bytes.Buffer:
		buff := make([]byte, size)
		x := bytes.NewBuffer(buff)
		ptr := unsafe.Pointer(&x)
		b.Buffer = *(*B)(ptr)
	}
	panic(fmt.Sprintf("bug: unsupported type %T", b))
}

// cap returns the capacity of the Buffer.
func (b *Buffer[B]) cap() int {
	switch t := any(b).(type) {
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
	size := 0
	switch t := any(b.Buffer).(type) {
	case []byte:
		if cap(t) == 0 {
			return size
		}
		if cap(t) == len(t) {
			return len(t)
		}
		if len(t) != cap(t) {
			t = t[:cap(t)] // I'm sure this is wrong, fix when internet is back.
		}

		ptr := unsafe.Pointer(&t)
		b.Buffer = *(*B)(ptr)
		return cap(t)
	case *[]byte:
		if cap(*t) == 0 {
			return size
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

// Pool holds a pool of []bytes
type Pool[B BufferType] struct {
	tree        *btree.BTreeG[storage]
	largestSize int
}

// Sizes is a slice of Size arguments.
type Sizes []Size

// Size details a set of pool sizes that will be have their own buffer reuse space.
// This allows you to retrieve buffers that have a least the size you are looking for
// to minimize allocations.
type Size struct {
	// Size is the size of the buffer you want to create a pool for.
	Size int
	// ConstBuff is how many buffer entries that are never collected
	// should be in this pool.
	ConstBuff int
	// SyncPool indicates that the bool is backed by a sync.Pool.
	SyncPool bool
}

func (s Size) validate() error {
	if s.Size == 0 {
		return fmt.Errorf("cannot create a size of 0")
	}

	if s.ConstBuff <= 0 && !s.SyncPool {
		return fmt.Errorf("cannot have a Size with ConstBuff not set and SyncPool off")
	}
	return nil
}

type storage interface {
	cap() int
}

type storageImpl[B BufferType] struct {
	capSize int
	buff    chan *Buffer[B]
	pool    *sync.Pool
}

func (s *storageImpl[B]) cap() int {
	return s.capSize
}

func (s *storageImpl[B]) insert(b *Buffer[B]) {
	b.fromSyncPool = false
	b.belongsToPool = s.capSize

	select {
	case s.buff <- b:
		return
	default:
	}
	b.fromSyncPool = true
	s.pool.Put(b)
}

func (s *storageImpl[B]) get() *Buffer[B] {
	select {
	case x := <-s.buff:
		return x
	default:
	}
	if s.pool != nil {
		return s.pool.Get().(*Buffer[B])
	}

	var b = &Buffer[B]{}
	b.alloc(s.capSize)
	b.belongsToPool = s.capSize

	return b
}

type find int

func (f find) cap() int {
	return int(f)
}

func lessFunc[S storage](a, b S) bool {
	return a.cap() < b.cap()
}

// New creates a new Pool.
func New[B BufferType](sizes Sizes) (*Pool[B], error) {
	if len(sizes) == 0 {
		return nil, fmt.Errorf("must specify sizes")
	}

	tree := btree.NewG(2, lessFunc[storage])

	largestSize := 0
	for _, size := range sizes {
		if err := size.validate(); err != nil {
			return nil, err
		}
		if size.Size > largestSize {
			largestSize = size.Size
		}
		si := &storageImpl[B]{capSize: size.Size}
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
		}
		tree.ReplaceOrInsert(si)
	}
	return &Pool[B]{tree: tree, largestSize: largestSize}, nil
}

// Get returns a Buffer from the pool.
func (p *Pool[B]) Get(atLeast int) *Buffer[B] {
	var store *storageImpl[B]

	var iter btree.ItemIteratorG[storage] = func(item storage) bool {
		a := item.(*storageImpl[B])
		store = a
		return false
	}

	p.tree.AscendGreaterOrEqual(
		find(atLeast),
		iter,
	)

	if store != nil {
		b := store.get()
		b.pool = p
		return b
	}

	item, _ := p.tree.Max()
	store = item.(*storageImpl[B])
	b := &Buffer[B]{}
	b.alloc(atLeast)
	b.belongsToPool = store.capSize
	b.pool = p
	return b
}

// Put puts a Buffer into the pool.
func (p *Pool[B]) Put(b *Buffer[B]) {
	size := b.reset()

	// The buffer didn't change size, so it can go back to the same place it
	// started at.
	if size == b.belongsToPool {
		item, ok := p.tree.Get(find(size))
		if ok {
			store := item.(*storageImpl[B])
			b.fromSyncPool = false
			select {
			case store.buff <- b:
				return
			default:
			}
			b.fromSyncPool = true
			store.pool.Put(b)
			return
		}
		log.Printf("bug: should have been able to find tree entry(%d), but didn't", size)
	}

	// Okay, this exceeds our largest buffer size, put it in whatever is our
	// largest storage buffer.
	if size >= p.largestSize {
		item, ok := p.tree.Get(find(p.largestSize))
		if !ok {
			panic(fmt.Sprintf("something is terribly wrong here: can't find largest size(%d) tree entry", p.largestSize))
		}

		store := item.(*storageImpl[B])
		b.belongsToPool = store.capSize
		store.insert(b)
		return
	}

	// Okay, this goes in some mid tier storage. Find it and store it there.
	item, ok := p.tree.Get(find(b.belongsToPool))
	if !ok {
		panic(fmt.Sprintf("something is terribly wrong here: can't find largest size(%d) tree entry", p.largestSize))
	}
	store := item.(*storageImpl[B])

	var iter btree.ItemIteratorG[storage] = func(item storage) bool {
		a := item.(*storageImpl[B])
		if a.capSize > size {
			return false
		}
		store = a
		return true
	}

	p.tree.AscendGreaterOrEqual(
		find(size),
		iter,
	)
	b.belongsToPool = store.capSize
	store.insert(b)
}
