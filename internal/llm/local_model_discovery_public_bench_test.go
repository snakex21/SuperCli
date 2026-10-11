package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// This file deliberately uses no new flight implementation symbols. The exact
// frozen provider_runtime_context.go can be overlaid to compare real public
// ListLocalNativeModelInfos calls, rather than a synthetic coalescing helper.
const publicLocalDiscoveryFixture = `{"models":[{"key":"opaque-model","max_context_length":262144,"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["off","on"]}},"loaded_instances":[{"id":"opaque-instance","config":{"context_length":8192}}]}]}`

type publicLocalDiscoveryJoinContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *publicLocalDiscoveryJoinContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func startPublicLocalDiscovery(ctx context.Context, base, key string) <-chan []ModelInfo {
	done := make(chan []ModelInfo, 1)
	go func() { done <- ListLocalNativeModelInfos(ctx, base, key) }()
	return done
}

func joinPublicLocalDiscovery(base, key string) <-chan []ModelInfo {
	ctx := &publicLocalDiscoveryJoinContext{Context: context.Background(), joined: make(chan struct{})}
	done := startPublicLocalDiscovery(ctx, base, key)
	<-ctx.joined
	return done
}

func TestListLocalNativeModelInfosOverlappingPublicContract(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(publicLocalDiscoveryFixture))
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }); srv.Close() })
	base := srv.URL + "/v1"
	results := []<-chan []ModelInfo{startPublicLocalDiscovery(context.Background(), base, " fixture-key ")}
	<-entered
	for i := 1; i < 8; i++ {
		results = append(results, joinPublicLocalDiscovery(base+"/", "fixture-key"))
	}
	once.Do(func() { close(release) })
	want, err := parseProviderModelInfos([]byte(publicLocalDiscoveryFixture))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots [][]ModelInfo
	for _, done := range results {
		got := <-done
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("public discovery=%+v, want complete records %+v", got, want)
		}
		snapshots = append(snapshots, got)
	}
	if calls.Load() != 1 {
		t.Fatalf("public native GETs=%d, want 1", calls.Load())
	}
	snapshots[0][0].ID, snapshots[0][0].RuntimeContextLength, snapshots[0][0].Notes = "mutated", 1, "mutated"
	snapshots[0][1].Reasoning, snapshots[0][1].Vision = false, false
	for _, snapshot := range snapshots[1:] {
		if !reflect.DeepEqual(snapshot, want) {
			t.Fatal("public results share mutable records")
		}
	}
	// There is no completed TTL: the next user operation must query again.
	next := ListLocalNativeModelInfos(context.Background(), base, "fixture-key")
	if calls.Load() != 2 || !reflect.DeepEqual(next, want) {
		t.Fatalf("fresh next discovery: GETs=%d models=%+v", calls.Load(), next)
	}
}

func BenchmarkListLocalNativeModelInfosOverlappingRequests(b *testing.B) {
	for _, callers := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("Callers%d", callers), func(b *testing.B) {
			var mu sync.Mutex
			var gate chan struct{}
			entered := make(chan struct{}, callers)
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				mu.Lock()
				current := gate
				mu.Unlock()
				entered <- struct{}{}
				select {
				case <-current:
					_, _ = w.Write([]byte(publicLocalDiscoveryFixture))
				case <-r.Context().Done():
				}
			}))
			defer srv.Close()
			want, err := parseProviderModelInfos([]byte(publicLocalDiscoveryFixture))
			if err != nil {
				b.Fatal(err)
			}
			base := srv.URL + "/v1"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				before := calls.Load()
				mu.Lock()
				gate = make(chan struct{})
				current := gate
				mu.Unlock()
				results := []<-chan []ModelInfo{startPublicLocalDiscovery(context.Background(), base, "")}
				<-entered
				for j := 1; j < callers; j++ {
					results = append(results, joinPublicLocalDiscovery(base, ""))
				}
				close(current)
				for _, done := range results {
					if got := <-done; !reflect.DeepEqual(got, want) {
						b.Fatalf("public result changed: %+v", got)
					}
				}
				observed := calls.Load() - before
				if observed < 1 || observed > int64(callers) {
					b.Fatalf("unexpected GET count=%d", observed)
				}
				// Each completed handler emitted one receipt before writing its
				// response. Consume exactly those receipts, never poll for progress.
				for j := int64(1); j < observed; j++ {
					<-entered
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(calls.Load())/float64(b.N), "native-GET/group")
			b.ReportMetric(float64(callers), "callers/group")
			b.ReportMetric(float64(len(publicLocalDiscoveryFixture)), "response-B/GET")
		})
	}
}
