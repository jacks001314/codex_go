package plugin

import "sync"

// maxConcurrentPluginLoads mirrors Rust core-plugins/src/loader.rs
// MAX_CONCURRENT_PLUGIN_LOADS (#57819): independent plugin filesystem/skill
// loads overlap, but at most eight at a time.
const maxConcurrentPluginLoads = 8

// loadPluginsConcurrently runs load for indexes [0, count) with at most
// maxConcurrentPluginLoads in flight and returns the results in index order.
// Rust consumes its buffered stream in configured-name order because that order
// decides duplicate MCP precedence and startup hook warning order; keeping the
// results indexed preserves the same guarantee here.
func loadPluginsConcurrently[T any](count int, load func(index int) T) []T {
	results := make([]T, count)
	if count <= 0 {
		return results
	}
	workers := maxConcurrentPluginLoads
	if count < workers {
		workers = count
	}
	var next atomicIndex
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for {
				index, ok := next.take(count)
				if !ok {
					return
				}
				results[index] = load(index)
			}
		}()
	}
	wg.Wait()
	return results
}

// atomicIndex hands out the next work item without a channel allocation.
type atomicIndex struct {
	mu   sync.Mutex
	next int
}

func (a *atomicIndex) take(limit int) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.next >= limit {
		return 0, false
	}
	index := a.next
	a.next++
	return index, true
}
