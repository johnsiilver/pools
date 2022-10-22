package diffsize

import (
	"testing"
	"time"
)

func TestBufferAllocClose(t *testing.T) {
	pool, err := New[*[]byte](
		Sizes{
			{Size: 16, ConstBuff: 1, SyncPool: false, DisallowAlloc: true},
		},
	)
	if err != nil {
		panic(err)
	}

	b := pool.Get(8)
	if b.cap() != 16 { // We asked for at least 8, but we only issue size 16.
		t.Fatalf("TestBufferAllocClose: cap(): got %d, want %d", b.cap(), 8)
	}
	*b.Buffer = append(*b.Buffer, 0, 0, 0)
	b.Close()

	b = pool.Get(16)
	// This should be the same buffer we got last time, the pool has a capacity of 1
	// and cannot alloc new buffers. We had expanded the capacity of the old one to hold
	// at least 19. Go's allocator doubles the capacity to 32.
	if b.cap() != 32 {
		t.Fatalf("TestBufferAllocClose: cap(): got %d, want %d", b.cap(), 8)
	}

	completed := make(chan struct{})
	go func() {
		defer close(completed)
		n := pool.Get(16)
		n.Close()
	}()

	select {
	case <-time.After(1 * time.Second):
		b.Close()
	case <-completed:
		t.Fatalf("TestBufferAllocClose: retrieved buffer when that should have been impossible")
	}

	<-completed
}

func TestPool(t *testing.T) {
	pool, err := New[*[]byte](
		Sizes{
			{Size: 1, ConstBuff: 10, SyncPool: true},
			{Size: 2, ConstBuff: 10, SyncPool: true},
			{Size: 4, ConstBuff: 10, SyncPool: true},
			{Size: 8, ConstBuff: 10, SyncPool: true},
			{Size: 16, ConstBuff: 0, SyncPool: true},
		},
	)
	if err != nil {
		panic(err)
	}

	for _, size := range []int{1, 2, 4, 8, 16} {
		b := pool.Get(size)
		if len(*b.Buffer) != size {
			t.Errorf("TestPool(%d)got size %d, want size %d", size, len(*b.Buffer), size)
		}
		defer func() { b.Close() }()
	}
}
