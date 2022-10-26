# Diffsize Buffer Pooling

[![GoDoc](https://godoc.org/github.com/johnsiilver/pools/memory/buffers/diffsize?status.svg)](https://pkg.go.dev/github.com/johnsiilver/pools/memory/buffers/diffsize)
[![Go Report Card](https://goreportcard.com/badge/github.com/johnsiilver/pools/memory/buffers/diffsize)](https://goreportcard.com/report/github.com/johnsiilver/pools/memory/buffers/diffsize)

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

