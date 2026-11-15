package config

// MCPLaunchCwds carries process-local launch directories for stdio MCP
// servers. The host supplies them on the app-server command line so they are
// never written to shared configuration (Rust #61444).
type MCPLaunchCwds struct {
	// Plugins maps a plugin id to its servers' launch directories.
	Plugins map[string]map[string]string
	// Configured maps a configured server name to its expected executable and
	// launch directory.
	Configured map[string]MCPLaunchCwd
}

// MCPLaunchCwd is one configured server's launch-directory entry.
type MCPLaunchCwd struct {
	Executable string
	Directory  string
}

// Clone returns an independent copy of the launch directories.
func (c *MCPLaunchCwds) Clone() *MCPLaunchCwds {
	if c == nil {
		return nil
	}
	clone := &MCPLaunchCwds{Configured: map[string]MCPLaunchCwd{}}
	if len(c.Plugins) > 0 {
		clone.Plugins = make(map[string]map[string]string, len(c.Plugins))
		for pluginID, servers := range c.Plugins {
			if len(servers) == 0 {
				clone.Plugins[pluginID] = map[string]string{}
				continue
			}
			entries := make(map[string]string, len(servers))
			for server, directory := range servers {
				entries[server] = directory
			}
			clone.Plugins[pluginID] = entries
		}
	}
	for server, entry := range c.Configured {
		clone.Configured[server] = entry
	}
	return clone
}

// PluginDirectory returns the launch directory supplied for one plugin server.
func (c *MCPLaunchCwds) PluginDirectory(pluginID string, server string) (string, bool) {
	if c == nil || len(c.Plugins) == 0 {
		return "", false
	}
	servers := c.Plugins[pluginID]
	if len(servers) == 0 {
		return "", false
	}
	directory, ok := servers[server]
	return directory, ok
}

// ConfiguredDirectory returns the launch directory supplied for one configured
// server, requiring the configured executable to match.
func (c *MCPLaunchCwds) ConfiguredDirectory(server string, executable string) (string, bool) {
	if c == nil || len(c.Configured) == 0 {
		return "", false
	}
	entry, ok := c.Configured[server]
	if !ok || entry.Executable != executable {
		return "", false
	}
	return entry.Directory, true
}
