# MCP originator runtime invalidation

Audit found runtime coordinator fingerprints contained config and selected environments but omitted the new explicit originator. Added originator to the structured environment/identity fingerprint, including turns with no environments. A coordinator regression proves identical identity reuses the service without update, while desktop -> guardian triggers exactly one update. Existing workspace-root fingerprint regression passes too.

Targeted tests passed 0.107s; go vet ./appserver exited 0. Full session39822 remains running and predates this change, so a later affected-package regression is needed. Overall synchronization remains incomplete; no commit/push.
