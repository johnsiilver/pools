package main

import (
	"log"

	. "github.com/johnsiilver/pools/memory/buffers/diffsize"
)

func main() {
	pool, err := New[*[]byte](
		Sizes{
			{Size: 16, ConstBuff: 10, SyncPool: true},
			{Size: 32, ConstBuff: 10, SyncPool: true},
			{Size: 64, ConstBuff: 10, SyncPool: true},
			{Size: 128, ConstBuff: 10, SyncPool: true},
			{Size: 256, ConstBuff: 10, SyncPool: true},
			{Size: 512, ConstBuff: 10, SyncPool: true},
			{Size: 1024, ConstBuff: 10, SyncPool: true},
		},
	)
	if err != nil {
		panic(err)
	}

	const n = 32 << 10

	data := make([]byte, n)
	data[n-1] = 'x'

	buf := NewPoolBuffer(pool, len(data))
	buf.Write(data)
	_, err = buf.ReadString('x')
	if err != nil {
		log.Fatal(err)
	}
	buf.Close()
}
