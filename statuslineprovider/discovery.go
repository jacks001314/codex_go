package statuslineprovider

import (
	"context"
	"sort"
	"strings"
	"time"
)

var inventoryHeadStart = 2 * time.Second

const maxConcurrentProbes = 4

// UpdateKind distinguishes a per-server result from the overall inventory
// failure that ends a refresh.
type UpdateKind string

const (
	UpdateServer          UpdateKind = "server"
	UpdateInventoryFailed UpdateKind = "inventory_failed"
)

// Update is one streaming discovery result.
type Update struct {
	Kind      UpdateKind
	Server    string
	Providers []Provider
	Err       error
}

type probeResult struct {
	name      string
	providers []Provider
	err       error
}

// Refresh streams the first inventory or named lookup result for each server.
// Dropping or canceling the caller context stops outstanding waits. The five
// second deadline starts when Refresh is called.
func Refresh(ctx context.Context, app AppServer, known []string) <-chan Update {
	updates := make(chan Update, 64)
	go func() {
		defer close(updates)
		lookupCtx, cancel := context.WithTimeout(ctx, statuslineLookupTimeout)
		defer cancel()
		discover(lookupCtx, ctx, app, known, updates)
	}()
	return updates
}

func discover(ctx context.Context, parent context.Context, app AppServer, known []string, updates chan<- Update) {
	started := time.Now()
	queued := map[string]bool{}
	for _, name := range known {
		if name = trimServerName(name); name != "" {
			queued[name] = true
		}
	}
	published := map[string]bool{}
	probes := map[string]context.CancelFunc{}
	results := make(chan probeResult, maxConcurrentProbes*2)
	pages := globalPages(ctx, app)
	headStart := time.NewTimer(inventoryHeadStart)
	defer headStart.Stop()
	deadline := time.NewTimer(time.Until(started.Add(statuslineLookupTimeout)))
	defer deadline.Stop()
	headElapsed := false
	listingDone := false
	var listingErr error

	nextQueued := func() string {
		names := make([]string, 0, len(queued))
		for name := range queued {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ""
		}
		return names[0]
	}
	startProbes := func() {
		if !headElapsed {
			return
		}
		for len(probes) < maxConcurrentProbes {
			name := nextQueued()
			if name == "" {
				return
			}
			delete(queued, name)
			if published[name] {
				continue
			}
			probes[name] = startProbe(ctx, app, name, results)
		}
	}

	for {
		if listingDone && len(queued) == 0 && len(probes) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				abortProbes(probes)
				return
			}
			publishTimeouts(parent, updates, queued, probes)
			if !listingDone {
				sendUpdate(ctx, updates, Update{Kind: UpdateInventoryFailed, Err: &LookupError{Kind: LookupTimedOut}})
			}
			return
		case <-deadline.C:
			publishTimeouts(parent, updates, queued, probes)
			if listingErr != nil {
				sendUpdate(ctx, updates, Update{Kind: UpdateInventoryFailed, Err: &LookupError{Kind: LookupRequest, Err: listingErr}})
			} else if !listingDone {
				sendUpdate(ctx, updates, Update{Kind: UpdateInventoryFailed, Err: &LookupError{Kind: LookupTimedOut}})
			}
			return
		case result := <-results:
			if _, active := probes[result.name]; !active {
				continue
			}
			delete(probes, result.name)
			published[result.name] = true
			update := Update{Kind: UpdateServer, Server: result.name, Providers: result.providers}
			if result.err != nil {
				update.Err = &LookupError{Kind: LookupRequest, Err: result.err}
			}
			sendUpdate(ctx, updates, update)
			startProbes()
		case event, ok := <-pages:
			if !ok {
				pages = nil
				listingDone = true
				if ctx.Err() != nil {
					if parent.Err() != nil {
						abortProbes(probes)
						return
					}
					publishTimeouts(parent, updates, queued, probes)
					sendUpdate(parent, updates, Update{Kind: UpdateInventoryFailed, Err: &LookupError{Kind: LookupTimedOut}})
					return
				}
				continue
			}
			if event.Done {
				listingDone = true
				listingErr = event.Err
				continue
			}
			for _, server := range event.Servers {
				name := trimServerName(server.Name)
				if name == "" || published[name] {
					continue
				}
				published[name] = true
				delete(queued, name)
				if cancel := probes[name]; cancel != nil {
					cancel()
					delete(probes, name)
				}
				providers, err := server.Providers()
				update := Update{Kind: UpdateServer, Server: name, Providers: providers}
				if err != nil {
					update.Err = &LookupError{Kind: LookupRequest, Err: err}
				}
				sendUpdate(ctx, updates, update)
			}
			startProbes()
		case <-headStart.C:
			headElapsed = true
			startProbes()
		}
	}
	if listingErr != nil {
		sendUpdate(ctx, updates, Update{Kind: UpdateInventoryFailed, Err: &LookupError{Kind: LookupRequest, Err: listingErr}})
	}
}

func startProbe(ctx context.Context, app AppServer, name string, results chan<- probeResult) context.CancelFunc {
	probeCtx, cancel := context.WithCancel(ctx)
	go func() {
		server, err := inventoryServer(probeCtx, app, name, nil)
		var providers []Provider
		if err == nil && server != nil {
			providers, err = server.Providers()
		}
		result := probeResult{name: name, providers: providers, err: err}
		select {
		case results <- result:
		case <-probeCtx.Done():
		}
	}()
	return cancel
}

func publishTimeouts(ctx context.Context, updates chan<- Update, queued map[string]bool, probes map[string]context.CancelFunc) {
	names := make([]string, 0, len(queued)+len(probes))
	for name := range queued {
		names = append(names, name)
	}
	for name := range probes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sendUpdate(ctx, updates, Update{Kind: UpdateServer, Server: name, Err: &LookupError{Kind: LookupTimedOut}})
	}
	abortProbes(probes)
}

func abortProbes(probes map[string]context.CancelFunc) {
	for _, cancel := range probes {
		cancel()
	}
}

func sendUpdate(ctx context.Context, updates chan<- Update, update Update) {
	select {
	case updates <- update:
	case <-ctx.Done():
	}
}

func trimServerName(name string) string {
	return strings.TrimSpace(name)
}
