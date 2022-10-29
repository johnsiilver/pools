# Diffsize Buffer Pooling

[![GoDoc](https://godoc.org/github.com/johnsiilver/pools/memory/buffers/diffsize?status.svg)](https://pkg.go.dev/github.com/johnsiilver/pools/memory/buffers/diffsize)
[![Go Report Card](https://goreportcard.com/badge/github.com/johnsiilver/pools)](https://goreportcard.com/report/github.com/johnsiilver/pools)

## Introduction

This package provides a multi-size buffer cache and sync.Pool to reduce overhead for the `*bytes.Buffer`, `*[]byte` and `[]byte` types.

This allows you to pull buffers that can hold some amount of data you specify. This can greatly reduce the amount of allocations that are needed, prevent a lot of oversized buffers for your needs and provide a static cache that are never reclaimed.

In a very basic benchmarks you can see a 5x reduction in bytes allocated, almost 2x reduction in allocations at the cost of around a 17% increase in allocation speed.

Your results may vary depending on tuning of the pool and use case.

## Basic use

```go
// Allocate a pool that has a static cache for byte sizes
// 1, 2, 4, 8 and 16, each with 10 static buffers for byte sizes 1-8
// and all can use a sync.Pool for additional allocations and reuse.
// Note: A buffer request larger than 16 will be allocated and will
// go back into the pool for size 16. If that is undesirable or you want
// to limit how big that can be, you can set MaxSize on that pool size.
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

// Retrieve a buffer that can hold at least 8 bytes.
b := pool.Get(8)

// The buffer can be bigger than what you requested. If that is the
// case, you *may* want to change the length to what you need.
*b.Buffer = (*b.Buffer)[:8]

... Do some stuff with the buffer
...
...

b.Close() // Puts the buffer back in a pool.
```

There are other settings where you can prevent buffers greater than a size from being put in the pool or prevent allocations so that you have a defined memory cap.

## PoolBuffer

Since we are providing a package that pools buffers, I thought it might be nice to be able to have a `Pool[*[]byte]` that could share its buffers with a `bytes.Buffer` when doing resizes.

Introducing `PoolBuffer` which allows you to use the same underlying buffer `Pool` for both. On the surface, there are only a few differences between it and `bytes.Buffer`:

* Cannot use a naked `PoolBuffer`
* `NewPoolBuffer()` does not allow you to provide a `[]byte`
* `PoolBuffer.Close()` must be used to return the underlying `*[]byte` to the `Pool`

Benchmarks are between `PoolBuffer` and `bytes.Buffer` are borrowed from the standard library (and `PoolBuffer` is almost identical in code).  These are pretty simplistic benchmarks.

In all cases where Benchmarks for `bytes.Buffer` win, it is usually because the asked for buffer was larger than are our largest buffer and an allocation was made in a `sync.Pool` for storage or the test didn't amoritize the cost of Pooling with multiple pulls of similar size. In longer term runs I'd expect the average case to always beat `bytes.Buffer` unless your `Sizes` provided to the pool were way off.
```
PoolBuffer
BenchmarkReadString-10                            238203              5103 ns/op        6421.08 MB/s       65706 B/op          5 allocs/op

StdLib(winnder)
BenchmarkReadString-10                            515664              2113 ns/op        15507.97 MB/s      32768 B/op          1 allocs/op
--

PoolBuffer(winner)
BenchmarkWriteByte-10                             138075              8747 ns/op         468.29 MB/s           0 B/op          0 allocs/op

StdLib
BenchmarkWriteRune-10                              75733             15780 ns/op         778.72 MB/s           0 B/op          0 allocs/op
--

PoolBuffer(tie)
BenchmarkWriteRune-10                              77050             15528 ns/op         791.37 MB/s           0 B/op          0 allocs/op

StdLib(tie)
BenchmarkWriteRune-10                              75733             15780 ns/op         778.72 MB/s           0 B/op          0 allocs/op
--

PoolBuffer(winner)
BenchmarkPoolBufferNotEmptyWriteRead-10             7635            156482 ns/op              81 B/op          1 allocs/op

StdLib
BenchmarkBufferNotEmptyWriteRead-10                 7431            160532 ns/op            3520 B/op          3 allocs/op
--

PoolBuffer
BenchmarkPoolBufferFullSmallReads-10               27093             43658 ns/op           65705 B/op          4 allocs/op

StdLib (winner)
BenchmarkBufferFullSmallReads-10                   36812             32609 ns/op            3072 B/op          2 allocs/op
--

PoolBuffer (winner)
BenchmarkPoolBufferWriteBlock/N4096-10           4990893               238.8 ns/op           118 B/op          1 allocs/op

StdLib
BenchmarkBufferWriteBlock/N4096-10               1508166               743.5 ns/op          7168 B/op          3 allocs/op
--

PoolBuffer (winner)
BenchmarkPoolBufferWriteBlock/N65536-10           249820              4461 ns/op           65728 B/op          4 allocs/op

StdLib
BenchmarkBufferWriteBlock/N65536-10               131604              8797 ns/op          130048 B/op          7 allocs/op
--

PoolBuffer (winner)
BenchmarkPoolBufferWriteBlock/N1048576-10          13966             82614 ns/op         2032136 B/op         16 allocs/op

StdLib
BenchmarkBufferWriteBlock/N1048576-10              10000            110334 ns/op         2096132 B/op         11 allocs/op
--
```