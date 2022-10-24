package diffsize

import (
	"bytes"
	"testing"
	"time"
)

func TestBufferAllocClose(t *testing.T) {
	t.Parallel()

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

func TestResetBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		cap  int
		len  int
		want int
	}{
		{"cap is 0", 0, 0, 0},
		{"cap == len", 10, 10, 10},
		{"cap > len", 10, 5, 10},
	}

	for _, test := range tests {
		buff := Buffer[[]byte]{
			Buffer: make([]byte, test.len, test.cap),
		}

		got := buff.reset()
		if got != test.want {
			t.Errorf("TestResetBytes(%s): buff.reset(): got %d want %d", test.desc, got, test.want)
		}

		if len(buff.Buffer) != test.want {
			t.Errorf("TestResetBytes(%s): buff.reset(): .Buffer length: got %d want %d", test.desc, len(buff.Buffer), test.want)
		}
	}
}

func TestResetPtrBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		cap  int
		len  int
		want int
	}{
		{"cap is 0", 0, 0, 0},
		{"cap == len", 10, 10, 10},
		{"cap > len", 10, 5, 10},
	}

	for _, test := range tests {
		b := make([]byte, test.len, test.cap)
		buff := Buffer[*[]byte]{
			Buffer: &b,
		}

		got := buff.reset()
		if got != test.want {
			t.Errorf("TestResetPtrBytes(%s): buff.reset(): got %d want %d", test.desc, got, test.want)
		}

		if len(*buff.Buffer) != test.want {
			t.Errorf("TestResetPtrBytes(%s): buff.reset(): .Buffer length: got %d want %d", test.desc, len(*buff.Buffer), test.want)
		}
	}
}

func TestResetBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		cap  int
		want int
	}{
		{"cap is 0", 0, 0},
		{"cap == len", 10, 10},
		{"cap > len", 10, 10},
	}

	for _, test := range tests {
		b := make([]byte, test.cap)

		buff := Buffer[*bytes.Buffer]{
			Buffer: bytes.NewBuffer(b),
		}

		got := buff.reset()
		if got != test.want {
			t.Errorf("TestResetBuffer(%s): buff.reset(): got %d want %d", test.desc, got, test.want)
		}

		if buff.Buffer.Cap() != test.want {
			t.Errorf("TestResetBuffer(%s): buff.reset(): .Buffer.Cap(): got %d want %d", test.desc, buff.Buffer.Cap(), test.want)
		}
		if buff.Buffer.Len() != 0 {
			t.Errorf("TestResetBuffer(%s): buff.reset(): .Buffer.Len(): got %d want %d", test.desc, buff.Buffer.Len(), test.want)
		}
	}
}

func TestSizeValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		size Size
		err  bool
	}{
		{
			desc: "Err: Size is zero",
			size: Size{
				ConstBuff:     2,
				SyncPool:      true,
				DisallowAlloc: false,
			},
			err: true,
		},
		{
			desc: "Err: ConstBuff <= 0 && no sync.Pool",
			size: Size{
				Size:          8,
				ConstBuff:     0,
				SyncPool:      false,
				DisallowAlloc: false,
			},
			err: true,
		},
		{
			desc: "Err: DisallowAlloc and sync.Pool set ",
			size: Size{
				Size:          8,
				ConstBuff:     2,
				SyncPool:      true,
				DisallowAlloc: true,
			},
			err: true,
		},
		{
			desc: "Err: MaxSize < Size",
			size: Size{
				Size:          8,
				ConstBuff:     2,
				SyncPool:      true,
				DisallowAlloc: false,
				MaxSize:       7,
			},
			err: true,
		},
		{
			desc: "Success: SyncPool only",
			size: Size{
				Size:     8,
				SyncPool: true,
				MaxSize:  8,
			},
		},
		{
			desc: "Success: ConstBuff only",
			size: Size{
				Size:          8,
				ConstBuff:     100,
				SyncPool:      false,
				DisallowAlloc: true,
				MaxSize:       8,
			},
		},
		{
			desc: "Success: ConstBuff and SyncPool",
			size: Size{
				Size:      8,
				ConstBuff: 100,
				SyncPool:  true,
				MaxSize:   8,
			},
		},
	}

	for _, test := range tests {
		err := test.size.validate()
		switch {
		case err == nil && test.err:
			t.Errorf("TestSizeValidate(%s): got err == nil, want err != nil", test.desc)
			continue
		case err != nil && !test.err:
			t.Errorf("TestSizeValidate(%s): got err == %s, want err == nil", test.desc, err)
			continue
		case err != nil:
			continue
		}
	}
}

func TestStorageImplCapSize(t *testing.T) {
	t.Parallel()

	const size = 8
	s, err := newStorageImpl[[]byte](Size{Size: size, ConstBuff: 1})
	if err != nil {
		panic(err)
	}
	if s.cap() != size {
		t.Fatalf("TestStorageImplCapSize(%d): s.cap(): got %d, want %d", size, s.cap(), size)
	}
}

func TestNewPool(t *testing.T) {
	tests := []struct {
		desc  string
		sizes Sizes
		err   bool
	}{
		{
			desc: "Err: len(Sizes) == 0",
			err:  true,
		},
		{
			desc: "Err: 2 Sizes that have the same Size",
			sizes: Sizes{
				Size{Size: 8, ConstBuff: 1},
				Size{Size: 16, ConstBuff: 1},
				Size{Size: 8, ConstBuff: 1},
			},
			err: true,
		},
		{
			desc: "Success",
			sizes: Sizes{
				Size{Size: 8, ConstBuff: 1},
				Size{Size: 16, ConstBuff: 1},
				Size{Size: 32, ConstBuff: 1},
			},
		},
	}

	for _, test := range tests {
		_, err := New[[]byte](test.sizes)
		switch {
		case err == nil && test.err:
			t.Errorf("TestNewPool(%s): got err == nil, want err != nil", test.desc)
			continue
		case err != nil && !test.err:
			t.Errorf("TestNewPool(%s): got err == %s, want err == nil", test.desc, err)
			continue
		case err != nil:
			continue
		}
	}
}

func TestPoolGet(t *testing.T) {
	p, err := New[[]byte](
		Sizes{
			Size{Size: 8, ConstBuff: 1},
			Size{Size: 16, ConstBuff: 1},
			Size{Size: 32, ConstBuff: 1},
		},
	)

	if err != nil {
		panic(err)
	}

	tests := []struct {
		atLeast       int
		belongsToPool int
		want          int
	}{
		{
			atLeast:       2,
			belongsToPool: 8,
			want:          8,
		},
		{
			atLeast:       15,
			belongsToPool: 16,
			want:          16,
		},
		{
			atLeast:       31,
			belongsToPool: 32,
			want:          32,
		},
		{
			atLeast:       33,
			belongsToPool: 32,
			// Because this isn't coming from a pool because it is too big, it will
			// be the exact size requested. However, it will still belong to the
			// largest pool.
			want: 33,
		},
	}

	for _, test := range tests {
		b := p.Get(test.atLeast)
		if b.belongsToPool != test.belongsToPool {
			t.Errorf("TestPoolGet(atLeast==%d): belongsToPool: got %d, want %d", test.atLeast, b.belongsToPool, test.belongsToPool)
		}
		if len(b.Buffer) != test.want {
			t.Errorf("TestPoolGet(atLeast==%d): len(Buffer.Buffer): got %d, want %d", test.atLeast, len(b.Buffer), test.want)
		}
	}
}

func TestPoolGetStore(t *testing.T) {
	p, err := New[[]byte](
		Sizes{
			Size{Size: 8, ConstBuff: 1},
			Size{Size: 16, ConstBuff: 1},
			Size{Size: 32, ConstBuff: 1},
		},
	)
	if err != nil {
		panic(err)
	}

	tests := []struct {
		desc    string
		atLeast int
		want    int
	}{
		{
			atLeast: 2,
			want:    8,
		},
		{
			atLeast: 15,
			want:    16,
		},
		{
			atLeast: 31,
			want:    32,
		},
		{
			atLeast: 33,
			want:    0,
		},
	}

	for _, test := range tests {
		s := p.getStore(test.atLeast)
		if s == nil {
			if test.want != 0 {
				t.Errorf("TestGetStore(%s): got store == nil, want %d", test.desc, test.want)
			}
			continue
		}

		if s.capSize != test.want {
			t.Errorf("TestGetStore(%s): getStore(%d): got capacity %d, want capacity %d", test.desc, test.atLeast, s.capSize, test.want)
		}
	}

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
