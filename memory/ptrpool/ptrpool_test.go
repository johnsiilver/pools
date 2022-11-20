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
