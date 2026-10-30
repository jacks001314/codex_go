package auth

import (
	"reflect"
	"sync"
)

// chatGPTRefreshProgress mirrors Rust #58355 `AuthManager::unsettled_chatgpt_refresh`:
// a token generation is unsettled from the start of its OAuth exchange until the
// refreshed credentials are persisted, so callers can withhold it. Cancellation
// while waiting for the refresh guard or reading storage does not record a
// generation, and a completed failure restores the previous record because it
// cannot settle an earlier cancelled attempt.
var chatGPTRefreshProgress = struct {
	sync.Mutex
	byHome map[string]map[string]any
}{byHome: map[string]map[string]any{}}

// beginChatGPTRefresh records the generation whose exchange is about to start
// and returns the record it replaced.
func beginChatGPTRefresh(codexHome string, tokens map[string]any) map[string]any {
	key := authStoreKey(codexHome)
	chatGPTRefreshProgress.Lock()
	defer chatGPTRefreshProgress.Unlock()
	previous := chatGPTRefreshProgress.byHome[key]
	chatGPTRefreshProgress.byHome[key] = cloneAnyMap(tokens)
	return previous
}

// settleChatGPTRefresh clears the record once refreshed credentials were
// persisted.
func settleChatGPTRefresh(codexHome string) {
	key := authStoreKey(codexHome)
	chatGPTRefreshProgress.Lock()
	defer chatGPTRefreshProgress.Unlock()
	delete(chatGPTRefreshProgress.byHome, key)
}

// restoreChatGPTRefresh restores the record a completed failure replaced.
func restoreChatGPTRefresh(codexHome string, previous map[string]any) {
	key := authStoreKey(codexHome)
	chatGPTRefreshProgress.Lock()
	defer chatGPTRefreshProgress.Unlock()
	if previous == nil {
		delete(chatGPTRefreshProgress.byHome, key)
		return
	}
	chatGPTRefreshProgress.byHome[key] = cloneAnyMap(previous)
}

// ChatGPTRefreshUnsettled reports whether this credential's refresh exchange or
// persistence started but did not finish (Rust #58355
// `AuthManager::is_chatgpt_refresh_unsettled`). Executor auth withholds such a
// generation until different credentials appear; replacement credentials and
// successfully completed refreshes are not withheld.
func ChatGPTRefreshUnsettled(codexHome string, tokens map[string]any) bool {
	key := authStoreKey(codexHome)
	chatGPTRefreshProgress.Lock()
	defer chatGPTRefreshProgress.Unlock()
	unsettled := chatGPTRefreshProgress.byHome[key]
	return unsettled != nil && reflect.DeepEqual(unsettled, tokens)
}
