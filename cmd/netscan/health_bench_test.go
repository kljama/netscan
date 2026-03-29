package main

import (
	"sync/atomic"
	"testing"
)

func BenchmarkGetRSSMB(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = getRSSMB()
	}
}

func BenchmarkAtomicLoad(b *testing.B) {
	var val atomic.Uint64
	for i := 0; i < b.N; i++ {
		_ = val.Load()
	}
}
