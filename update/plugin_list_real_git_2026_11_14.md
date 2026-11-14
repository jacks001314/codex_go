# Real Git plugin/list refresh

Added an appserver public plugin/list regression using two real local Git repositories. Seeded cached 1.0.0 plugin checkouts, published changed same-version docs and new-version release, called plugin/list, awaited the batch effective-plugin callback epoch, and checked docs retained production while release became staging. Uses registered PluginService installations; configured-marketplace git-subdir discovery remains a separate scenario.

Found a production gap: typed plugin Git sources rejected file:// URLs although the Git materializer and upstream #61129 fixture use them. The fix is scoped to parsedMarketplacePluginSource; marketplace CLI parsing remains unchanged. Managed source policy validation remains downstream. A broader initial parser change was rejected by existing TestMarketplaceSourceParsing and was reverted.

Final targeted CLI-parser/cache/real-Git entry tests passed: plugin 0.165s, appserver 1.386s. Full plugin tests passed 2.129s; go vet ./plugin ./appserver exited 0. Logs: plugin_list_real_git_before_2026_11_14.log (actual source rejection), plugin_list_real_git_final_2026_11_14.log, plugin_file_source_2026_11_14.log, plugin_file_source_vet_2026_11_14.log.

Full appserver session 64036 also finished exit 1; complete log and summary are synced for follow-up. It predates this plugin fix. Overall synchronization incomplete; no commit/push.
