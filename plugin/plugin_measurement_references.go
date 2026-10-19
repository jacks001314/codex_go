package plugin

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
)

// MaxCachedMeasurementReferences bounds the thread-scoped measurement cache,
// including active preparations (Rust #46542).
const MaxCachedMeasurementReferences = 4

// PluginMeasurementTarget identifies the executor's requested plugin
// measurement files. The executor hint follows its own filesystem's case rules.
type PluginMeasurementTarget struct {
	PluginName string
	Version    string
	// WindowsPathConvention selects case-insensitive plugin-name and historical
	// version matching. The authenticated bundle identity stays case-exact.
	WindowsPathConvention bool
}

// PluginMeasurementBundle is one authenticated, immutable plugin release that
// can materialize trusted roots into an isolated home.
type PluginMeasurementBundle struct {
	RemotePluginID string
	PluginID       *PluginId
	Version        string
	// Materialize installs the validated bundle under home and returns the
	// verified roots. It must not reuse the shared plugin store.
	Materialize func(ctx context.Context, home string) (TrustedPluginRoots, error)
}

// PluginMeasurementBundleFetcher rechecks installation authorization and
// resolves the current release for one target. It returns (nil, nil) when no
// uniquely matching enabled GLOBAL plugin exists.
type PluginMeasurementBundleFetcher interface {
	FetchMeasurementBundle(ctx context.Context, target PluginMeasurementTarget) (*PluginMeasurementBundle, error)
}

// PluginMeasurementIdentity scopes cached references to the authenticated
// account and endpoint that produced them.
type PluginMeasurementIdentity struct {
	AuthIdentity string
	Endpoint     string
}

// PluginMeasurementReference owns the immutable files backing its roots. Keep
// it alive for the full command or turn that consumes those roots.
type PluginMeasurementReference struct {
	roots TrustedPluginRoots
	home  string
	once  sync.Once
	entry *measurementReferenceEntry
	cache *RemotePluginMeasurementCache
}

// Roots returns the verified plugin roots. The reference keeps them valid.
func (r *PluginMeasurementReference) Roots() TrustedPluginRoots {
	if r == nil {
		return TrustedPluginRoots{}
	}
	return r.roots
}

// Close releases the reference. Files are removed once no live reference or
// cache entry retains them.
func (r *PluginMeasurementReference) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.cache == nil || r.entry == nil {
			_ = os.RemoveAll(r.home)
			return
		}
		r.cache.release(r.entry)
	})
}

type measurementReferenceKey struct {
	authIdentity string
	endpoint     string
	remoteID     string
	pluginID     string
	version      string
}

type measurementReferenceEntry struct {
	key     measurementReferenceKey
	done    chan struct{}
	ref     *PluginMeasurementReference
	err     error
	refs    int
	retired bool
}

func (e *measurementReferenceEntry) ready() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// RemotePluginMeasurementCache prepares trusted measurement declarations for
// one installed plugin without loading capabilities or publishing to the shared
// plugin catalog. It is separate from the installed plugin/capability store.
type RemotePluginMeasurementCache struct {
	mu      sync.Mutex
	entries []*measurementReferenceEntry
}

// NewRemotePluginMeasurementCache returns an empty thread-scoped cache.
func NewRemotePluginMeasurementCache() *RemotePluginMeasurementCache {
	return &RemotePluginMeasurementCache{}
}

// Prepare rechecks authorization through the fetcher, then prepares only the
// requested version. Previously authenticated versions stay reusable after a
// release update; a cache miss downloads the current release.
//
// Concurrent callers share one preparation. A caller cancelled while waiting
// stops waiting; the shared download continues for the other callers. When all
// entries are actively preparing, Prepare returns (nil, nil).
func (c *RemotePluginMeasurementCache) Prepare(ctx context.Context, identity PluginMeasurementIdentity, fetcher PluginMeasurementBundleFetcher, target PluginMeasurementTarget) (*PluginMeasurementReference, error) {
	if c == nil || fetcher == nil || strings.TrimSpace(target.PluginName) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bundle, err := fetcher.FetchMeasurementBundle(ctx, target)
	if err != nil {
		return nil, sanitizeMeasurementPreparationError(err)
	}
	if bundle == nil || bundle.PluginID == nil || strings.TrimSpace(bundle.PluginID.Key()) == "" {
		return nil, nil
	}
	version := strings.TrimSpace(bundle.Version)
	if version == "" || strings.TrimSpace(bundle.RemotePluginID) == "" || bundle.Materialize == nil {
		return nil, nil
	}
	currentVersion := measurementVersionMatches(target, version)
	keyVersion := target.Version
	if currentVersion {
		keyVersion = version
	}
	key := measurementReferenceKey{
		authIdentity: strings.TrimSpace(identity.AuthIdentity),
		endpoint:     strings.TrimSpace(identity.Endpoint),
		remoteID:     strings.TrimSpace(bundle.RemotePluginID),
		pluginID:     bundle.PluginID.Key(),
		version:      keyVersion,
	}

	c.mu.Lock()
	c.retireStaleEntriesLocked(key)
	if entry := c.matchingEntryLocked(key, target, currentVersion); entry != nil {
		entry.refs++
		c.mu.Unlock()
		return c.waitForEntry(ctx, entry)
	}
	if len(c.entries) >= MaxCachedMeasurementReferences {
		index := c.evictionIndexLocked()
		if index < 0 {
			c.mu.Unlock()
			return nil, nil
		}
		entry := c.entries[index]
		c.entries = append(c.entries[:index], c.entries[index+1:]...)
		entry.retired = true
		if entry.refs == 0 {
			if entry.ref != nil {
				_ = os.RemoveAll(entry.ref.home)
			}
		}
	}
	entry := &measurementReferenceEntry{key: key, done: make(chan struct{}), refs: 1}
	c.entries = append(c.entries, entry)
	c.mu.Unlock()

	reference, err := materializeMeasurementReference(ctx, key, bundle)
	c.mu.Lock()
	entry.ref = reference
	entry.err = err
	if reference != nil {
		reference.entry = entry
		reference.cache = c
	}
	close(entry.done)
	refs := entry.refs
	c.mu.Unlock()
	if err != nil {
		return nil, sanitizeMeasurementPreparationError(err)
	}
	if refs <= 0 {
		// Every waiter cancelled before the preparation finished; do not leak
		// the isolated home.
		c.mu.Lock()
		entry.retired = true
		c.mu.Unlock()
		reference.Close()
		return nil, nil
	}
	return reference, nil
}

func materializeMeasurementReference(ctx context.Context, key measurementReferenceKey, bundle *PluginMeasurementBundle) (*PluginMeasurementReference, error) {
	home, err := os.MkdirTemp("", "codex-plugin-measurement-")
	if err != nil {
		return nil, err
	}
	roots, err := bundle.Materialize(ctx, home)
	if err != nil {
		_ = os.RemoveAll(home)
		return nil, err
	}
	return &PluginMeasurementReference{roots: roots, home: home}, nil
}

func (c *RemotePluginMeasurementCache) waitForEntry(ctx context.Context, entry *measurementReferenceEntry) (*PluginMeasurementReference, error) {
	select {
	case <-entry.done:
		if entry.err != nil {
			return nil, sanitizeMeasurementPreparationError(entry.err)
		}
		if entry.ref == nil {
			return nil, nil
		}
		return entry.ref, nil
	case <-ctx.Done():
		c.mu.Lock()
		entry.refs--
		if entry.refs <= 0 && entry.ready() {
			entry.retired = true
			if entry.ref != nil {
				_ = os.RemoveAll(entry.ref.home)
			}
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *RemotePluginMeasurementCache) release(entry *measurementReferenceEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.refs > 0 {
		entry.refs--
	}
	if entry.refs == 0 && entry.retired && entry.ref != nil {
		_ = os.RemoveAll(entry.ref.home)
		entry.ref.home = ""
	}
}

// retireStaleEntriesLocked drops completed entries from another auth identity
// or endpoint, and entries whose plugin identity changed upstream. Active
// preparations are retained until they finish because they can never match the
// new key.
func (c *RemotePluginMeasurementCache) retireStaleEntriesLocked(key measurementReferenceKey) {
	kept := c.entries[:0]
	for _, entry := range c.entries {
		stale := entry.ready() && (entry.key.authIdentity != key.authIdentity || entry.key.endpoint != key.endpoint ||
			(entry.key.pluginID == key.pluginID && entry.key.remoteID != key.remoteID))
		if !stale {
			kept = append(kept, entry)
			continue
		}
		entry.retired = true
		if entry.refs == 0 && entry.ref != nil {
			_ = os.RemoveAll(entry.ref.home)
		}
	}
	c.entries = kept
}

func (c *RemotePluginMeasurementCache) matchingEntryLocked(key measurementReferenceKey, target PluginMeasurementTarget, currentVersion bool) *measurementReferenceEntry {
	var matched *measurementReferenceEntry
	for _, entry := range c.entries {
		if entry.key.authIdentity != key.authIdentity || entry.key.endpoint != key.endpoint ||
			entry.key.remoteID != key.remoteID || entry.key.pluginID != key.pluginID {
			continue
		}
		if currentVersion {
			if entry.key != key {
				continue
			}
		} else if !measurementVersionMatches(target, entry.key.version) {
			continue
		}
		if matched != nil {
			// Historical Windows hints must not select between case-distinct
			// releases.
			return nil
		}
		matched = entry
	}
	return matched
}

func (c *RemotePluginMeasurementCache) evictionIndexLocked() int {
	for index, entry := range c.entries {
		if entry.ready() {
			return index
		}
	}
	return -1
}

func measurementVersionMatches(target PluginMeasurementTarget, version string) bool {
	if target.WindowsPathConvention {
		return strings.EqualFold(strings.TrimSpace(target.Version), strings.TrimSpace(version))
	}
	return strings.TrimSpace(target.Version) == strings.TrimSpace(version)
}

// sanitizeMeasurementPreparationError keeps authenticated URLs and response
// bodies out of diagnostics (Rust #46542).
func sanitizeMeasurementPreparationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errMeasurementPreparationFailed
}

var errMeasurementPreparationFailed = &measurementPreparationError{}

type measurementPreparationError struct{}

func (*measurementPreparationError) Error() string {
	return "plugin measurement catalog lookup failed"
}
