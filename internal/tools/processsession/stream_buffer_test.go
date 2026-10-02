package processsession

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestStreamBufferWrapPreservesExactCursorAndOmissions(t *testing.T) {
	for _, capacity := range []int{0, 1, 5, 31, 1024} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			buffer := newStreamBuffer(capacity)
			rng := rand.New(rand.NewSource(42))
			var output []byte
			for step := 0; step < 300; step++ {
				chunk := make([]byte, rng.Intn(capacity*3+7))
				_, _ = rng.Read(chunk)
				if n, err := buffer.Write(chunk); err != nil || n != len(chunk) {
					t.Fatalf("write: n=%d err=%v", n, err)
				}
				output = append(output, chunk...)
				base := max(0, len(output)-capacity)
				for _, cursor := range []int{0, base, max(0, base-2), (base + len(output)) / 2, len(output) + 3} {
					limit := rng.Intn(capacity + 7)
					start := min(max(cursor, base), len(output))
					end := min(start+limit, len(output))
					got, next, omitted := buffer.readFrom(int64(cursor), limit)
					if !bytes.Equal(got, output[start:end]) || next != int64(end) || omitted != int64(max(0, base-cursor)) {
						t.Fatalf("step=%d cursor=%d limit=%d got=%x next=%d omitted=%d want=%x/%d/%d", step, cursor, limit, got, next, omitted, output[start:end], end, max(0, base-cursor))
					}
				}
				if len(buffer.data) > capacity || cap(buffer.data) > capacity {
					t.Fatalf("storage exceeded cap: len=%d cap=%d max=%d", len(buffer.data), cap(buffer.data), capacity)
				}
			}
		})
	}
}

func TestStreamBufferSnapshotsAreDetachedFromFutureWrites(t *testing.T) {
	buffer := newStreamBuffer(8)
	_, _ = buffer.Write([]byte("12345678"))
	first, _, _ := buffer.readFrom(0, 8)
	_, _ = buffer.Write([]byte("abcd"))
	wrapped, _, _ := buffer.readFrom(4, 8)
	_, _ = buffer.Write([]byte("ijklmnop"))
	if string(first) != "12345678" || string(wrapped) != "5678abcd" {
		t.Fatalf("old snapshots were mutated: %q / %q", first, wrapped)
	}
}

func TestStreamBufferAllocatesLazilyAndReusesFullCapacity(t *testing.T) {
	buffer := newStreamBuffer(maxBufferBytes)
	if buffer.data != nil {
		t.Fatal("empty stream eagerly allocated the output cap")
	}
	_, _ = buffer.Write([]byte("short output"))
	if cap(buffer.data) >= maxBufferBytes {
		t.Fatal("short stream eagerly allocated the output cap")
	}
	chunk := bytes.Repeat([]byte("x"), 1024)
	_, _ = buffer.Write(bytes.Repeat(chunk, 64))
	if allocations := testing.AllocsPerRun(100, func() { _, _ = buffer.Write(chunk) }); allocations != 0 {
		t.Fatalf("full-buffer writes allocated: %g per write", allocations)
	}
	large := bytes.Repeat(chunk, 1024)
	_, _ = buffer.Write(large)
	if len(buffer.data) != maxBufferBytes || cap(buffer.data) != maxBufferBytes {
		t.Fatalf("large write retained transient excess: len=%d cap=%d", len(buffer.data), cap(buffer.data))
	}
}

func TestStreamBufferConcurrentReadersAndWriter(t *testing.T) {
	buffer := newStreamBuffer(31)
	var wg sync.WaitGroup
	ready := make(chan struct{})
	wg.Add(4)
	go func() {
		defer wg.Done()
		<-ready
		for i := 0; i < 1000; i++ {
			_, _ = buffer.Write([]byte("xxxxxxxxx"))
		}
	}()
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			<-ready
			var cursor int64
			for i := 0; i < 1000; i++ {
				got, next, omitted := buffer.readFrom(cursor, 7)
				if next < cursor || next-cursor != int64(len(got))+omitted || !bytes.Equal(got, bytes.Repeat([]byte("x"), len(got))) {
					t.Errorf("concurrent snapshot: %q next=%d cursor=%d omitted=%d", got, next, cursor, omitted)
					return
				}
				cursor = next
			}
		}()
	}
	close(ready)
	wg.Wait()
}

// The previous append-and-trim behavior is kept only as a benchmark baseline.
// Both variants retain the same 64 KiB tail for the same incoming bytes.
func BenchmarkStreamBufferSustainedWrites(b *testing.B) {
	for _, chunkSize := range []int{1024, 32 << 10, 128 << 10} {
		chunk := bytes.Repeat([]byte("x"), chunkSize)
		b.Run(fmt.Sprintf("ring/%d", chunkSize), func(b *testing.B) {
			buffer := newStreamBuffer(maxBufferBytes)
			_, _ = buffer.Write(bytes.Repeat([]byte("x"), maxBufferBytes))
			b.SetBytes(int64(chunkSize))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = buffer.Write(chunk)
			}
		})
		b.Run(fmt.Sprintf("append_trim_baseline/%d", chunkSize), func(b *testing.B) {
			data := bytes.Repeat([]byte("x"), maxBufferBytes)
			var mu sync.Mutex
			b.SetBytes(int64(chunkSize))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mu.Lock()
				data = append(data, chunk...)
				if drop := len(data) - maxBufferBytes; drop > 0 {
					data = append([]byte(nil), data[drop:]...)
				}
				mu.Unlock()
			}
			if len(data) != maxBufferBytes {
				b.Fatal("baseline tail changed")
			}
		})
	}
}
