package ptrpool

/*
This file holds end to end test for the various types of pools that can be
constructed to make sure that in the most basic use cases that each pool is
working.
*/

import (
	"testing"
	"time"
)

func Must[A any](freelist FreeList, newer NewValue[A], options ...Option[A]) *Pool[A] {
	p, err := New(freelist, newer, options...)
	if err != nil {
		panic(err)
	}
	return p
}

type Record struct {
	ID int
}

func (r *Record) Reset() {
	r.ID = 0
}

func TestETOE(t *testing.T) {

	tests := []struct {
		desc  string
		pool  *Pool[Record]
		sleep time.Duration
	}{
		{
			desc: "Freelist only",
			pool: Must(
				FreeList{Base: 1},
				func() *Record {
					return &Record{}
				},
				Reseter(
					func(r *Record) {
						r.Reset()
					},
				),
				DisableSyncPool[Record](),
			),
		},
		{
			desc: "Freelist with no allocations",
			pool: Must(
				FreeList{Base: 100, DisableAllocs: true},
				func() *Record {
					return &Record{}
				},
				Reseter(
					func(r *Record) {
						r.Reset()
					},
				),
				DisableSyncPool[Record](),
			),
		},
		{
			desc: "Freelist with sync.Pool backup",
			pool: Must(
				FreeList{Base: 1},
				func() *Record {
					return &Record{}
				},
				Reseter(
					func(r *Record) {
						r.Reset()
					},
				),
			),
		},
		{
			desc: "Freelist with sync.Pool and growth function",
			pool: Must(
				FreeList{
					Base: 1,
					Grow: Grow{
						Maximum:         1000,
						MeasurementSpan: 1 * time.Second,
						Grower:          (&BasicGrower{}).Grower,
					},
				},
				func() *Record {
					return &Record{}
				},
				Reseter(
					func(r *Record) {
						r.Reset()
					},
				),
			),
			sleep: 1 * time.Second,
		},
	}

	for _, test := range tests {
		defer test.pool.Close()

		ch := make(chan Value[Record], 100)

		// Get a *Record from the pool, set the ID and send it for printing.
		go func() {
			defer close(ch)
			for i := 0; i < 100000; i++ {
				if i%10000 == 0 {
					time.Sleep(test.sleep)
				}
				v := test.pool.Get()
				if v.V.ID != 0 {
					panic("received v.V.ID that was not reset")
				}
				v.V.ID = i
				ch <- v
			}
		}()

		var last int
		// Print all values received and send it back to the freelist.
		for v := range ch {
			last = v.V.ID
			v.Close()
		}
		if last != 99999 {
			t.Errorf("TestETOE(%s): did not get expected end value, got %d", test.desc, last)
		}
	}
}
