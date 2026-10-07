package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// VersionForTOML returns the canonical fingerprint of a TOML-derived configuration
// value.
//
// Mirrors Rust codex-rs/config/src/fingerprint.rs::version_for_toml as simplified by
// #49295 (upstream cf12c86dc5 "Simplify configuration fingerprint canonicalization"):
//
//	let mut json = serde_json::to_value(value).unwrap_or(JsonValue::Null);
//	json.sort_all_objects();
//	let serialized = serde_json::to_vec(&json).unwrap_or_default();
//	// sha256(serialized) rendered as "sha256:<hex>"
//
// The canonical form is JSON with every object's keys sorted recursively -- tables
// nested inside arrays included -- while array element order and scalar values are left
// untouched:
//
//	serde_json::Value::sort_all_objects():
//	    Value::Object(map) => { map.sort_keys(); values_mut().for_each(sort_all_objects) }
//	    Value::Array(list) => list.iter_mut().for_each(sort_all_objects)
//
// encoding/json already writes every object with its keys in ascending order at every
// depth, exactly like sort_all_objects(), so Go needs no hand-rolled recursive
// canonicalizer: encoding the value *is* the canonical form.
//
// The encoder is configured to stay as close to serde_json's bytes as Go's encoder
// allows: HTML escaping is off (`<`, `>`, `&` are written verbatim, like serde_json's
// ESCAPE table), and `\b`, `\f`, `\n`, `\r`, `\t` use the same short escapes. Two
// encoder-level differences remain and are deliberately not papered over: Go writes an
// integral-valued float as `1` where serde_json writes `1.0` (and `1e+21` where
// serde_json writes `1e21`), and Go escapes U+2028/U+2029 as `\u2028` where serde_json
// writes them raw. Both only move the fingerprint of configurations that carry such
// values.
func VersionForTOML(value any) string {
	sum := sha256.Sum256(canonicalConfigJSON(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalConfigJSON returns the canonical JSON bytes hashed by VersionForTOML.
func canonicalConfigJSON(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		// Mirrors Rust's `serde_json::to_value(value).unwrap_or(JsonValue::Null)`:
		// a value that has no JSON representation canonicalizes to `null`.
		return []byte("null")
	}
	// json.Encoder terminates the value with a newline; serde_json::to_vec does not.
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
}
