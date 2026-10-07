package plugin

import (
	"crypto/sha256"
	"sync"

	"codex_go/utils"
)

const (
	// ManifestCacheCapacity is Rust core-plugins/src/manifest/manifest_cache.rs
	// CAPACITY (#49099): the cache keeps the most recently used revisions.
	ManifestCacheCapacity = 128
	// ManifestCacheMaxInputBytes is Rust MAX_CACHABLE_INPUT_LEN (#49099): a
	// revision whose manifest plus overlay exceeds it is never cached.
	ManifestCacheMaxInputBytes = 64 * 1024
)

// manifestCache reuses parsed manifest revisions within an explicitly owned
// plugin store (Rust #49099 ManifestCache). Copies of a store share one cache
// because the store holds a pointer; independently created stores never share
// entries. A nil cache is the disabled variant and always parses.
type manifestCache struct {
	// mu serializes cache misses as well as cache hits: parsing is synchronous
	// and does no filesystem I/O, so holding it across a miss keeps concurrent
	// loaders from validating the same revision twice (Rust holds its state
	// mutex across `parse`).
	mu      sync.Mutex
	entries *utils.Cache[manifestCacheKey, manifestCacheEntry]
}

type manifestCacheKey struct {
	root     string
	manifest string
}

func newManifestCache() *manifestCache {
	return &manifestCache{entries: utils.New[manifestCacheKey, manifestCacheEntry](ManifestCacheCapacity)}
}

// disabledManifestCache mirrors Rust `ManifestCache::disabled`.
func disabledManifestCache() *manifestCache { return nil }

// manifestCachePathKey mirrors Rust's `PathUri` cache keys. A path with no URI
// representation falls back to its normalized text, so Windows case and
// separator spellings still collapse to one key (utils.PathIdentityKey, #51482).
func manifestCachePathKey(value string) string {
	if key, ok := utils.PathIdentityKey(value); ok {
		return key
	}
	return "text\x00" + value
}

// manifestCacheEntry carries the revision the parse was made from, so a changed
// manifest or overlay never serves a stale parse.
type manifestCacheEntry struct {
	contentsDigest [32]byte
	overlayPath    string
	overlayDigest  [32]byte
	hasOverlay     bool
	parsed         *resolvedPluginManifest
}

// parse returns the cached parse for an unchanged revision, otherwise runs parse
// once and caches a successful result. Failed parses are not cached, so the next
// load retries them.
func (c *manifestCache) parse(pluginRoot string, manifestPath string, contents []byte, overlayPath string, overlay []byte, parse func() (*resolvedPluginManifest, error)) (*resolvedPluginManifest, error) {
	if c == nil {
		return parse()
	}
	key := manifestCacheKey{
		root:     manifestCachePathKey(pluginRoot),
		manifest: manifestCachePathKey(manifestPath),
	}
	if len(contents)+len(overlay) > ManifestCacheMaxInputBytes {
		// Oversized inputs must not serialize unrelated parses behind the lock.
		c.mu.Lock()
		if c.entries != nil {
			c.entries.Remove(key)
		}
		c.mu.Unlock()
		return parse()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	contentsDigest := sha256.Sum256(contents)
	overlayDigest := sha256.Sum256(overlay)
	hasOverlay := len(overlay) > 0
	if entry, ok := c.entries.Get(key); ok &&
		entry.contentsDigest == contentsDigest &&
		entry.hasOverlay == hasOverlay &&
		entry.overlayPath == overlayPath &&
		entry.overlayDigest == overlayDigest {
		return cloneResolvedPluginManifest(entry.parsed), nil
	}
	parsed, err := parse()
	if err != nil {
		return nil, err
	}
	c.entries.Insert(key, manifestCacheEntry{
		contentsDigest: contentsDigest,
		overlayPath:    overlayPath,
		overlayDigest:  overlayDigest,
		hasOverlay:     hasOverlay,
		parsed:         cloneResolvedPluginManifest(parsed),
	})
	return parsed, nil
}

// cloneResolvedPluginManifest keeps cached entries independent from the value a
// caller receives, mirroring Rust's clone-on-hit.
func cloneResolvedPluginManifest(resolved *resolvedPluginManifest) *resolvedPluginManifest {
	if resolved == nil {
		return nil
	}
	clone := *resolved
	clone.Manifest = clonePluginManifestFile(resolved.Manifest)
	return &clone
}

func clonePluginManifestFile(manifest pluginManifestFile) pluginManifestFile {
	clone := manifest
	clone.Keywords = append([]string(nil), manifest.Keywords...)
	clone.Apps = cloneAppSummaries(manifest.Apps)
	clone.AppTemplates = cloneAppTemplateSummaries(manifest.AppTemplates)
	clone.AppTemplatesSnake = cloneAppTemplateSummaries(manifest.AppTemplatesSnake)
	if manifest.Interface != nil {
		interfaceClone := *manifest.Interface
		clone.Interface = &interfaceClone
	}
	return clone
}
