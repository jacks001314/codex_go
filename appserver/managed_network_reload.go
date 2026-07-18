package appserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/network"

	"github.com/fsnotify/fsnotify"
)

type managedNetworkReloadWatcher struct {
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	done    chan struct{}
}

func (r *RuntimeRouter) startManagedNetworkReloadWatcher() {
	if r == nil || r.services.Config == nil {
		return
	}
	r.managedNetworkReloadMu.Lock()
	defer r.managedNetworkReloadMu.Unlock()
	if r.managedNetworkReload != nil {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("failed to start managed network config watcher", "error", err)
		return
	}
	codexHome := r.services.Config.CodexHome()
	for _, directory := range []string{codexHome, filepath.Join(codexHome, "rules")} {
		if err := watcher.Add(directory); err != nil && !os.IsNotExist(err) {
			_ = watcher.Close()
			slog.Warn("failed to watch managed network config", "directory", directory, "error", err)
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &managedNetworkReloadWatcher{watcher: watcher, cancel: cancel, done: make(chan struct{})}
	r.managedNetworkReload = state
	go r.runManagedNetworkReloadWatcher(ctx, state, codexHome)
}

func (r *RuntimeRouter) watchManagedNetworkProjectConfig(cwd string) {
	if r == nil {
		return
	}
	r.managedNetworkReloadMu.Lock()
	defer r.managedNetworkReloadMu.Unlock()
	state := r.managedNetworkReload
	if state == nil {
		return
	}
	for _, directory := range config.ProjectDotCodexFolders(cwd) {
		if err := state.watcher.Add(directory); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to watch project managed network config", "directory", directory, "error", err)
		}
	}
}

func (r *RuntimeRouter) runManagedNetworkReloadWatcher(ctx context.Context, state *managedNetworkReloadWatcher, codexHome string) {
	defer close(state.done)
	defer state.watcher.Close()
	var timer *time.Timer
	var timerC <-chan time.Time
	schedule := func() {
		if timer == nil {
			timer = time.NewTimer(50 * time.Millisecond)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(50 * time.Millisecond)
		timerC = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-state.watcher.Events:
			if !ok {
				return
			}
			if managedNetworkReloadEvent(event, codexHome) {
				if filepath.Clean(event.Name) == filepath.Join(codexHome, "rules") && event.Op&fsnotify.Create != 0 {
					_ = state.watcher.Add(filepath.Join(codexHome, "rules"))
				}
				schedule()
			}
		case err, ok := <-state.watcher.Errors:
			if ok && err != nil {
				slog.Warn("managed network config watcher error", "error", err)
			}
		case <-timerC:
			timerC = nil
			if err := r.reloadManagedNetworkFromConfig(); err != nil {
				slog.Warn("failed to reload managed network proxy", "error", err)
			}
		}
	}
}

func managedNetworkReloadEvent(event fsnotify.Event, codexHome string) bool {
	name := filepath.Clean(event.Name)
	return name == config.ConfigPath(codexHome) ||
		name == execPolicyDefaultPath(codexHome) ||
		name == filepath.Join(codexHome, "rules") ||
		(filepath.Base(name) == "config.toml" && filepath.Base(filepath.Dir(name)) == ".codex")
}

func execPolicyDefaultPath(codexHome string) string {
	return filepath.Join(codexHome, "rules", "default.rules")
}

func (r *RuntimeRouter) reloadManagedNetworkFromConfig() error {
	if r == nil || r.services.Config == nil {
		return nil
	}
	var firstErr error
	if r.services.ManagedNetwork != nil {
		read, err := r.services.Config.Read(&config.ConfigReadParams{})
		if err != nil {
			firstErr = err
		} else {
			proxyConfig, _, buildErr := r.buildManagedNetworkProxyConfig(read.Config)
			if buildErr != nil {
				firstErr = buildErr
			} else if reloadErr := r.services.ManagedNetwork.ReloadConfig(*proxyConfig); reloadErr != nil {
				firstErr = reloadErr
			}
		}
	}
	if err := r.reloadThreadManagedNetworksFromConfig(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (r *RuntimeRouter) reloadThreadManagedNetworksFromConfig() error {
	type snapshot struct {
		threadID string
		input    managedNetworkReloadInput
		prepared *network.PreparedProxyManagedNetwork
	}
	r.managedNetworksMu.Lock()
	snapshots := make([]snapshot, 0, len(r.managedNetworks))
	for threadID, prepared := range r.managedNetworks {
		input := r.managedNetworkInputs[threadID]
		input.Overrides = cloneAnyMap(input.Overrides)
		snapshots = append(snapshots, snapshot{threadID: threadID, input: input, prepared: prepared})
	}
	r.managedNetworksMu.Unlock()

	var firstErr error
	for _, item := range snapshots {
		cfg, err := config.LoadWithOptions(r.services.Config.CodexHome(), &config.LoadOptions{CWD: item.input.CWD})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		applyRuntimeConfigOverrides(cfg, item.input.Overrides)
		proxyConfig, shouldStart, err := r.buildManagedNetworkProxyConfigForCWD(cfg.Values, item.input.CWD)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !shouldStart {
			continue
		}
		r.scopeManagedNetworkConfigForThread(item.threadID, proxyConfig)
		if err := item.prepared.ReloadConfig(*proxyConfig); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *RuntimeRouter) closeManagedNetworkReloadWatcher() error {
	if r == nil {
		return nil
	}
	r.managedNetworkReloadMu.Lock()
	state := r.managedNetworkReload
	r.managedNetworkReload = nil
	r.managedNetworkReloadMu.Unlock()
	if state == nil {
		return nil
	}
	state.cancel()
	select {
	case <-state.done:
		return nil
	case <-time.After(2 * time.Second):
		return context.DeadlineExceeded
	}
}

func managedNetworkConfigPathMatches(path string, codexHome string) bool {
	path = strings.TrimSpace(path)
	return path != "" && managedNetworkReloadEvent(fsnotify.Event{Name: path}, codexHome)
}
