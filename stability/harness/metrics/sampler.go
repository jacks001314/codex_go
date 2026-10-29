// Package metrics samples process resource usage for stability scenarios.
package metrics

import (
	"context"
	"runtime"
	"time"
)

// Sample is one point-in-time resource observation.
type Sample struct {
	At             time.Time `json:"at"`
	HeapAllocBytes uint64    `json:"heap_alloc_bytes"`
	HeapInuseBytes uint64    `json:"heap_inuse_bytes"`
	HeapSysBytes   uint64    `json:"heap_sys_bytes"`
	HeapObjects    uint64    `json:"heap_objects"`
	Goroutines     int       `json:"goroutines"`
	RSSBytes       uint64    `json:"rss_bytes"`
	RSSKnown       bool      `json:"rss_known"`
	OpenFDs        int       `json:"open_fds"`
	FDsKnown       bool      `json:"fds_known"`
}

// Take captures one sample.
func Take() Sample {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	rss, rssKnown := readRSSBytes()
	fds, fdsKnown := readOpenFDs()
	return Sample{
		At:             time.Now(),
		HeapAllocBytes: memory.HeapAlloc,
		HeapInuseBytes: memory.HeapInuse,
		HeapSysBytes:   memory.HeapSys,
		HeapObjects:    memory.HeapObjects,
		Goroutines:     runtime.NumGoroutine(),
		RSSBytes:       rss,
		RSSKnown:       rssKnown,
		OpenFDs:        fds,
		FDsKnown:       fdsKnown,
	}
}

// Sampler periodically captures samples.
type Sampler struct {
	Interval time.Duration
}

// Run captures an immediate sample and then one per interval until ctx ends.
func (s Sampler) Run(ctx context.Context) <-chan Sample {
	interval := s.Interval
	if interval <= 0 {
		interval = time.Second
	}
	out := make(chan Sample)
	go func() {
		defer close(out)
		if !sendSample(ctx, out) {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !sendSample(ctx, out) {
					return
				}
			}
		}
	}()
	return out
}

func sendSample(ctx context.Context, out chan<- Sample) bool {
	select {
	case out <- Take():
		return true
	case <-ctx.Done():
		return false
	}
}
