package ptrpool

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestFreeListOnly(t *testing.T) {
	i := 0
	fl := FreeList{Base: 2, DisableAllocs: true}
	p, err := New(fl, func() *int { i++; x := new(int); *x = i; return x }, DisableSyncPool[int]())
	if err != nil {
		panic(err)
	}

	v1 := p.Get()
	if *v1.V != 1 {
		t.Fatalf("TestFreeListOnly: Value: got %d, want %d", *v1.V, 1)
	}

	v2 := p.Get()
	if *v2.V != 2 {
		t.Fatalf("TestFreeListOnly: Value: got %d, want %d", *v2.V, 2)
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		v3 := p.Get()
		if *v3.V != 1 {
			panic(fmt.Sprintf("TestFreeListOnly: Value: got %d, want %d", *v3.V, 3))
		}
	}()

	v1.Close()
	wg.Wait()
}

func TestGrowth(t *testing.T) {
	tests := []struct {
		desc             string
		lastStats        Stats
		currentStats     *stats
		currentFreelist  chan *int
		freelistSettings FreeList
		wantSize         int
	}{
		{
			desc:            "Grower says to Keep same size",
			currentStats:    &stats{},
			currentFreelist: make(chan *int, 10),
			freelistSettings: FreeList{
				Base: 2,
				Grow: Grow{
					Maximum:         100,
					MeasurementSpan: 30 * time.Second,
					Grower: func(args GrowthArgs) int {
						return args.CurrentSize
					},
				},
			},
			wantSize: 10,
		},
		{
			desc:            "Bad grower gives <= 0 answer, keep same size",
			currentStats:    &stats{},
			currentFreelist: make(chan *int, 10),
			freelistSettings: FreeList{
				Base: 2,
				Grow: Grow{
					Maximum:         100,
					MeasurementSpan: 30 * time.Second,
					Grower: func(args GrowthArgs) int {
						return -1
					},
				},
			},
			wantSize: 10,
		},
		{
			desc:            "Says to grow bigger than maximum, so grow maximum",
			currentStats:    &stats{},
			currentFreelist: make(chan *int, 10),
			freelistSettings: FreeList{
				Base: 2,
				Grow: Grow{
					Maximum:         100,
					MeasurementSpan: 30 * time.Second,
					Grower: func(args GrowthArgs) int {
						return 101
					},
				},
			},
			wantSize: 100,
		},
		{
			desc:            "Shrink less than base, so go to base size",
			currentStats:    &stats{},
			currentFreelist: make(chan *int, 10),
			freelistSettings: FreeList{
				Base: 2,
				Grow: Grow{
					Maximum:         100,
					MeasurementSpan: 30 * time.Second,
					Grower: func(args GrowthArgs) int {
						return 1
					},
				},
			},
			wantSize: 2,
		},
	}

	for _, test := range tests {
		p := &Pool[int]{
			freelistSettings: test.freelistSettings,
			stats:            test.currentStats,
			lastStats:        test.lastStats,
		}

		p.freelist.Store(&test.currentFreelist)

		p.growth()

		fl := p.freelist.Load()
		if cap(*fl) != test.wantSize {
			t.Errorf("TestGrowth(%s): got FreeList size %d, want %d", test.desc, cap(*fl), test.wantSize)
		}
	}
}

func TestBGGrowth(t *testing.T) {
	tests := []struct {
		desc     string
		args     GrowthArgs
		noGrowth int
		want     int
	}{
		{
			desc: "measurement time is 0 (even though this shouldn't happen)",
			args: GrowthArgs{
				CurrentSize: 100,
				PoolPulls:   0,
			},
			want: 100,
		},
		{
			desc: "no change",
			args: GrowthArgs{
				CurrentSize:     100,
				PoolPulls:       0,
				MeasurementSpan: 30 * time.Second,
			},
			want: 100,
		},
		{
			desc: "growth, but not enough to trigger",
			args: GrowthArgs{
				CurrentSize:     100,
				PoolPulls:       5,
				MeasurementSpan: 30 * time.Second,
			},
			want: 100,
		},
		{
			desc: "growth, of 1",
			args: GrowthArgs{
				CurrentSize:     100,
				PoolPulls:       30,
				MeasurementSpan: 30 * time.Second,
			},
			want: 101,
		},
		{
			desc: "shrink by 10",
			args: GrowthArgs{
				CurrentSize:     100,
				PoolPulls:       0,
				MeasurementSpan: 30 * time.Second,
			},
			noGrowth: 3,
			want:     90,
		},
	}

	for _, test := range tests {
		bg := &BasicGrower{noGrowth: test.noGrowth}
		got := bg.Grower(test.args)

		if test.want != got {
			t.Errorf("TestBGGrowth(%s): got %d, want %d", test.desc, got, test.want)
		}
	}
}
