package tui

func ResolveServiceTier(configured string, fallback string) string {
	if configured != "" && configured != "default" {
		return configured
	}
	return fallback
}
