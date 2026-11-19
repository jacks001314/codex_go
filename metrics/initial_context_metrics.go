package metrics

// PluginRecommendationsWaitMetric measures how long initial context setup waits
// for the plugin recommendation catalog before it renders the recommendation
// instructions (Rust #62326,
// `codex.initial_context.plugin_recommendations_wait_ms`).
const PluginRecommendationsWaitMetric = "codex.initial_context.plugin_recommendations_wait_ms"
