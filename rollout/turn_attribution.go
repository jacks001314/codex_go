package rollout

import "codex_go/session"

// TurnAttribution is the regular-turn provenance persisted on the
// `turn_started` rollout event (Rust #51402). The canonical definition lives in
// the `session` package so a reconstructed record can carry it without an
// import cycle; this alias keeps the rollout-facing name.
type TurnAttribution = session.TurnAttribution
