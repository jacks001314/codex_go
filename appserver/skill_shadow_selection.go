package appserver

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"codex_go/config"
	promptctx "codex_go/prompt"
	"codex_go/turn"
)

const (
	skillShadowRunMetric        = "codex.skills.shadow_selection"
	skillShadowDurationMetric   = "codex.skills.shadow_selection.duration_ms"
	skillShadowCatalogMetric    = "codex.skills.shadow_selection.catalog_entries"
	skillShadowSelectedMetric   = "codex.skills.shadow_selection.selected_entries"
	skillShadowTermsMetric      = "codex.skills.shadow_selection.query_terms"
	skillShadowReductionMetric  = "codex.skills.shadow_selection.reduction_bps"
	skillShadowInvocationMetric = "codex.skills.shadow_selection.invocation"
	skillShadowMaxQueryBytes    = 16 * 1024
	skillShadowRankingSlots     = 2
)

// skillShadowRankingBudget is the process-wide ranking capacity (Rust
// RANKING_SLOTS: Semaphore::const_new(2)). Admission happens only on the
// background worker, so turn preparation never waits for a ranking slot.
var skillShadowRankingBudget = make(chan struct{}, skillShadowRankingSlots)

// skillShadowRank runs one shadow ranking. Tests replace it to hold the shared
// budget and observe when a worker actually starts ranking (Rust spawn_blocking).
var skillShadowRank = promptctx.RunSkillShadowSelectionWithTaskContext

// skillShadowThreadState is the per-thread shadow-selection state (Rust
// SkillsThreadState): the recent skill-invocation history, the task context,
// and the latest turn's observation target.
type skillShadowThreadState struct {
	mu          sync.Mutex
	recent      []string
	turn        *skillShadowTurn
	taskContext *promptctx.ShadowTaskContext
}

type skillShadowRankedSelection struct {
	method         string
	queryScript    string
	skillResources []string
}

// skillShadowTurn is one turn's shadow-selection observation target (Rust
// ShadowSelectionTurnState): eligibility and the seen set frozen at turn start
// plus the queue of invocations waiting to be scored against the frozen
// rankings.
type skillShadowTurn struct {
	turnID   string
	eligible map[string]bool
	ranked   []skillShadowRankedSelection
	rankedCh chan struct{}
	queue    *skillShadowInvocationQueue
	mu       sync.Mutex
	seen     map[string]bool
}

// setRanked publishes the frozen rankings and unblocks waiters.
func (t *skillShadowTurn) setRanked(ranked []skillShadowRankedSelection) {
	t.mu.Lock()
	t.ranked = ranked
	t.mu.Unlock()
	close(t.rankedCh)
}

// waitRanked blocks until the background ranking finishes and returns its
// frozen selections; tests use it instead of observing metrics as they land.
func (t *skillShadowTurn) waitRanked() []skillShadowRankedSelection {
	<-t.rankedCh
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]skillShadowRankedSelection(nil), t.ranked...)
}

// skillShadowInvocationQueue holds invocations observed for a turn until its
// frozen rankings are ready. Sends never block turn execution, and closing the
// queue lets the worker drain and score everything already queued.
type skillShadowInvocationQueue struct {
	mu     sync.Mutex
	items  []string
	closed bool
	notify chan struct{}
}

func newSkillShadowInvocationQueue() *skillShadowInvocationQueue {
	return &skillShadowInvocationQueue{notify: make(chan struct{}, 1)}
}

func (q *skillShadowInvocationQueue) push(resource string) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, resource)
	q.mu.Unlock()
	q.signal()
}

func (q *skillShadowInvocationQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// drain returns the queued resources and whether the queue is closed; the
// caller keeps scoring until closed and empty.
func (q *skillShadowInvocationQueue) drain() ([]string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items, q.closed
}

func (q *skillShadowInvocationQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// skillShadowSelectionInput is the turn-start state frozen before the turn can
// contribute new invocation history (Rust ShadowSelectionInput).
type skillShadowSelectionInput struct {
	query          promptctx.ShadowQuery
	documents      []promptctx.SkillSelectionDocument
	resources      []string
	recentSkillIDs []int
	taskQuery      promptctx.ShadowQuery
	taskRecentIDs  []int
}

func (r *RuntimeRouter) skillShadowThreadStateFor(threadID string) *skillShadowThreadState {
	r.skillShadowMu.Lock()
	defer r.skillShadowMu.Unlock()
	if r.skillShadowState == nil {
		r.skillShadowState = map[string]*skillShadowThreadState{}
	}
	state := r.skillShadowState[threadID]
	if state == nil {
		state = &skillShadowThreadState{taskContext: promptctx.NewShadowTaskContext()}
		r.skillShadowState[threadID] = state
	}
	return state
}

// runSkillShadowSelection freezes the turn's ranking inputs and hands the
// ranking to a background worker (Rust ShadowSelectionExperiment::start,
// #49812). Turn preparation never waits for the shared ranking budget, and
// invocations recorded from now on queue up for scoring once rankings exist.
func (r *RuntimeRouter) runSkillShadowSelection(threadID string, turnID string, cfg *config.Config, params *turn.TurnStartParams, groups ...[]promptctx.InstructionsSkillMetadata) {
	if r == nil || cfg == nil || !cfg.SkillShadowSelectionEnabled() {
		return
	}
	query, queryTruncated := skillShadowQuery(params)
	explicitlySelected := skillShadowExplicitNames(params)
	documents := make([]promptctx.SkillSelectionDocument, 0)
	resources := make([]string, 0)
	for _, group := range groups {
		for _, skill := range group {
			if !skill.AllowsImplicitInvocation() || explicitlySelected[strings.ToLower(strings.TrimSpace(skill.Name))] {
				continue
			}
			documents = append(documents, promptctx.SkillSelectionDocument{
				ID: len(documents), Name: skill.Name, ShortDescription: skill.ShortDescription,
				Description: skill.Description, RoutingMetadata: skill.RoutingMetadata,
				Dependencies: skillShadowDependencies(skill.Dependencies),
			})
			resources = append(resources, skillShadowResourceKey(skill))
		}
	}
	state := r.skillShadowThreadStateFor(threadID)
	shadowQuery := promptctx.ShadowQuery{Text: query, Truncated: queryTruncated}
	snapshot := state.taskContext.BeginTurn(turnID, shadowQuery, skillShadowIsSubstantive(query, params))
	eligible := make(map[string]bool, len(resources))
	eligibleByResource := make(map[string]int, len(resources))
	for index, resource := range resources {
		eligible[resource] = true
		eligibleByResource[resource] = index
	}
	recentSkillIDs := make([]int, 0)
	state.mu.Lock()
	taskContext := state.taskContext
	for _, resource := range state.recent {
		if index, ok := eligibleByResource[resource]; ok {
			recentSkillIDs = append(recentSkillIDs, index)
		}
	}
	turn := &skillShadowTurn{
		turnID: turnID, eligible: eligible, seen: map[string]bool{},
		rankedCh: make(chan struct{}), queue: newSkillShadowInvocationQueue(),
	}
	// The new turn becomes the observation target before it can record
	// anything; the previous turn's worker keeps draining what it observed.
	previous := state.turn
	state.turn = turn
	state.mu.Unlock()
	if previous != nil {
		previous.queue.close()
	}
	taskRecentIDs := make([]int, 0, len(snapshot.RecentSkills))
	for _, resource := range snapshot.RecentSkills {
		if index, ok := eligibleByResource[resource]; ok {
			taskRecentIDs = append(taskRecentIDs, index)
		}
	}
	// Explicit intent is a relevance signal for future turns even when the
	// subsequent prompt read fails; predictions were frozen before recording it
	// (Rust ShadowSelectionExperiment::start).
	for _, group := range groups {
		for _, skill := range group {
			if !skill.AllowsImplicitInvocation() {
				continue
			}
			if explicitlySelected[strings.ToLower(strings.TrimSpace(skill.Name))] {
				taskContext.Record(turnID, skillShadowResourceKey(skill))
			}
		}
	}
	r.skillShadowWG.Add(1)
	go r.evaluateSkillShadowSelection(turn, skillShadowSelectionInput{
		query: shadowQuery, documents: documents, resources: resources,
		recentSkillIDs: recentSkillIDs, taskQuery: snapshot.Query, taskRecentIDs: taskRecentIDs,
	})
}

// evaluateSkillShadowSelection ranks a turn's frozen inputs off the turn
// preparation path, then scores every invocation observed for the turn,
// including ones queued before the turn state was dropped (Rust evaluate).
func (r *RuntimeRouter) evaluateSkillShadowSelection(turn *skillShadowTurn, input skillShadowSelectionInput) {
	defer r.skillShadowWG.Done()
	skillShadowRankingBudget <- struct{}{}
	observations := skillShadowRank(input.query, input.documents, input.recentSkillIDs, input.taskQuery, input.taskRecentIDs)
	// Release capacity only once the ranking itself finished, then keep scoring
	// queued observations without holding a slot (Rust drops the permit when
	// the blocking worker returns).
	<-skillShadowRankingBudget
	ranked := make([]skillShadowRankedSelection, 0, len(observations))
	for _, observation := range observations {
		selected := skillShadowSanitizeSelectedIDs(observation.CandidateIDs, len(input.documents))
		ranked = append(ranked, skillShadowRankedSelection{
			method:         observation.Method,
			queryScript:    observation.QueryScript,
			skillResources: skillShadowResourcesForIDs(selected, input.resources),
		})
	}
	turn.setRanked(ranked)
	r.recordSkillShadowRankMetrics(input.query.Truncated, input.documents, observations, ranked)
	r.drainSkillShadowInvocations(turn, ranked)
}

func (r *RuntimeRouter) recordSkillShadowRankMetrics(queryTruncated bool, documents []promptctx.SkillSelectionDocument, observations []promptctx.SkillShadowSelectionObservation, ranked []skillShadowRankedSelection) {
	if r == nil || r.services.SkillShadowMetrics == nil {
		return
	}
	for index, observation := range observations {
		selectedCount := len(ranked[index].skillResources)
		status := "selected"
		if observation.QueryTermCount == 0 {
			status = "no_query_terms"
		} else if selectedCount == 0 {
			status = "no_matches"
		}
		tags := map[string]string{
			"method": observation.Method, "status": status, "query_script": observation.QueryScript,
			"query_truncated":         strconv.FormatBool(queryTruncated || observation.QueryTruncated),
			"candidate_set_truncated": strconv.FormatBool(observation.CandidateSetTruncated),
		}
		r.services.SkillShadowMetrics.Counter(skillShadowRunMetric, 1, tags)
		r.services.SkillShadowMetrics.RecordDuration(skillShadowDurationMetric, observation.Duration, tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowCatalogMetric, len(documents), tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowSelectedMetric, selectedCount, tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowTermsMetric, observation.QueryTermCount, tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowReductionMetric, skillShadowReductionBPS(len(documents), selectedCount), tags)
	}
}

// drainSkillShadowInvocations scores queued invocations against the turn's
// frozen rankings until the queue is closed and empty (Rust evaluate's
// invocation loop).
func (r *RuntimeRouter) drainSkillShadowInvocations(turn *skillShadowTurn, ranked []skillShadowRankedSelection) {
	metrics := r.services.SkillShadowMetrics
	for {
		items, closed := turn.queue.drain()
		for _, resource := range items {
			recordSkillShadowInvocationMetrics(metrics, ranked, resource)
		}
		if closed {
			return
		}
		<-turn.queue.notify
	}
}

func recordSkillShadowInvocationMetrics(metrics SkillShadowMetricSink, ranked []skillShadowRankedSelection, resource string) {
	if metrics == nil {
		return
	}
	for _, selection := range ranked {
		rank := 0
		for index, candidate := range selection.skillResources {
			if candidate == resource {
				rank = index + 1
				break
			}
		}
		metrics.Counter(skillShadowInvocationMetric, 1, map[string]string{
			"method":       selection.method,
			"hit":          strconv.FormatBool(rank > 0),
			"rank":         skillShadowRankBucket(rank),
			"query_script": selection.queryScript,
		})
	}
}

// recordSkillShadowInvocation mirrors Rust ShadowSelectionTurnState
// record_invocation: the invoked skill advances the per-thread relevance
// history immediately for future turns, while its rank metrics are scored once
// the turn's frozen rankings are ready.
func (r *RuntimeRouter) recordSkillShadowInvocation(threadID string, turnID string, skill promptctx.InstructionsSkillMetadata) {
	if r == nil {
		return
	}
	resource := skillShadowResourceKey(skill)
	if resource == "" {
		return
	}
	state := r.skillShadowThreadStateFor(threadID)
	state.mu.Lock()
	turn := state.turn
	if turn == nil || turn.turnID != turnID || !turn.eligible[resource] {
		state.mu.Unlock()
		return
	}
	turn.mu.Lock()
	duplicate := turn.seen[resource]
	if !duplicate {
		turn.seen[resource] = true
	}
	turn.mu.Unlock()
	if duplicate {
		state.mu.Unlock()
		return
	}
	state.recent = skillShadowRecordRecent(state.recent, resource)
	taskContext := state.taskContext
	state.mu.Unlock()
	taskContext.Record(turnID, resource)
	turn.queue.push(resource)
}

// closeSkillShadowSelection closes every thread's turn queue and waits for the
// background workers to drain, so invocations observed before shutdown are
// still scored (Rust drops the turn states and their observers drain the queue).
func (r *RuntimeRouter) closeSkillShadowSelection() {
	r.skillShadowMu.Lock()
	turns := make([]*skillShadowTurn, 0, len(r.skillShadowState))
	for _, state := range r.skillShadowState {
		state.mu.Lock()
		if state.turn != nil {
			turns = append(turns, state.turn)
		}
		state.mu.Unlock()
	}
	r.skillShadowMu.Unlock()
	for _, turn := range turns {
		turn.queue.close()
	}
	_ = waitForWaitGroup(&r.skillShadowWG, 5*time.Second)
}

// skillShadowRecordRecent pushes a resource to the front of the per-thread
// recent list, deduplicating and evicting beyond the shadow result limit
// (Rust RecentSkillInvocations::record).
func skillShadowRecordRecent(recent []string, resource string) []string {
	recent = append([]string{resource}, skillShadowWithout(recent, resource)...)
	if len(recent) > promptctx.SkillShadowSelectionMaxResults {
		recent = recent[:promptctx.SkillShadowSelectionMaxResults]
	}
	return recent
}

func skillShadowWithout(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func skillShadowResourceKey(skill promptctx.InstructionsSkillMetadata) string {
	value := skill.Path
	if isResourceBackedSkill(skill) {
		value = firstNonEmpty(skill.ResourceID, skill.PackageID, skill.LocatorPath)
	}
	if strings.TrimSpace(value) == "" {
		value = skill.Name
	}
	return normalizeSkillShadowResource(value)
}

func normalizeSkillShadowResource(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
}

func skillShadowSanitizeSelectedIDs(candidateIDs []int, eligibleCount int) []int {
	seen := make(map[int]bool, min(len(candidateIDs), promptctx.SkillShadowSelectionMaxResults))
	out := make([]int, 0, min(len(candidateIDs), promptctx.SkillShadowSelectionMaxResults))
	for _, id := range candidateIDs {
		if id < 0 || id >= eligibleCount || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= promptctx.SkillShadowSelectionMaxResults {
			break
		}
	}
	return out
}

func skillShadowResourcesForIDs(ids []int, resources []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id >= 0 && id < len(resources) {
			out = append(out, resources[id])
		}
	}
	return out
}

// skillShadowRankBucket mirrors Rust shadow_selection_experiment rank_bucket.
func skillShadowRankBucket(rank int) string {
	switch {
	case rank == 1:
		return "1"
	case rank >= 2 && rank <= 5:
		return "2_5"
	case rank >= 6 && rank <= 10:
		return "6_10"
	case rank >= 11 && rank <= 20:
		return "11_20"
	case rank >= 21 && rank <= promptctx.SkillShadowSelectionMaxResults:
		return "21_50"
	default:
		return "miss"
	}
}

func skillShadowExplicitNames(params *turn.TurnStartParams) map[string]bool {
	out := map[string]bool{}
	if params == nil {
		return out
	}
	for _, input := range params.Input {
		if input.Type != "skill" && input.Type != "mention" {
			continue
		}
		if name := strings.ToLower(strings.TrimSpace(input.Name)); name != "" {
			out[name] = true
		}
	}
	return out
}

func skillShadowDependencies(dependencies []promptctx.InstructionsSkillDependency) string {
	parts := make([]string, 0, min(len(dependencies), 32)*2)
	for _, dependency := range dependencies[:min(len(dependencies), 32)] {
		parts = append(parts, nonEmptyStrings(dependency.Value, dependency.Description)...)
	}
	return strings.Join(parts, " ")
}

func skillShadowQuery(params *turn.TurnStartParams) (string, bool) {
	if params == nil {
		return "", false
	}
	parts := make([]string, 0, len(params.Input)+1)
	if strings.TrimSpace(params.Prompt) != "" {
		parts = append(parts, params.Prompt)
	}
	for _, input := range params.Input {
		part := input.Text
		if strings.TrimSpace(part) == "" && (input.Type == "skill" || input.Type == "mention") {
			part = input.Name
		}
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	query := strings.Join(parts, " ")
	if len(query) <= skillShadowMaxQueryBytes {
		return query, false
	}
	end := skillShadowMaxQueryBytes
	for end > 0 && query[end]&0xc0 == 0x80 {
		end--
	}
	return query[:end], true
}

// skillShadowIsSubstantive mirrors Rust ShadowTaskContext::is_substantive
// (#39008): explicit skill/mention intents are always substantive, otherwise the
// normalized request text must not be a bare continuation/acknowledgement.
func skillShadowIsSubstantive(query string, params *turn.TurnStartParams) bool {
	if params != nil {
		for _, input := range params.Input {
			if (input.Type == "skill" || input.Type == "mention") && strings.TrimSpace(input.Name) != "" {
				return true
			}
		}
	}
	normalized := skillShadowNormalizeSubstantiveText(query)
	return !skillShadowSubstantiveIgnores[normalized]
}

func skillShadowNormalizeSubstantiveText(text string) string {
	parts := make([]string, 0)
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if part != "" {
			parts = append(parts, strings.ToLower(part))
		}
	}
	return strings.Join(parts, " ")
}

var skillShadowSubstantiveIgnores = map[string]bool{
	"": true, "yes": true, "yep": true, "yeah": true, "ok": true, "okay": true,
	"sure": true, "go": true, "go ahead": true, "continue": true, "please continue": true,
	"proceed": true, "do it": true, "do that": true, "try again": true, "retry": true,
	"thanks": true, "thank you": true,
}

func skillShadowReductionBPS(catalog, selected int) int {
	if catalog <= 0 {
		return 0
	}
	return 10000 - selected*10000/catalog
}

func selectSkillMetadata(cfg *config.Config, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) []promptctx.InstructionsSkillMetadata {
	if cfg == nil || !cfg.SkillSelectionEnabled() || len(skills) == 0 {
		return skills
	}
	query, _ := skillShadowQuery(params)
	documents := make([]promptctx.SkillSelectionDocument, 0, len(skills))
	for i, skill := range skills {
		if skill.AllowsImplicitInvocation() {
			documents = append(documents, promptctx.SkillSelectionDocument{ID: i, Name: skill.Name, ShortDescription: skill.Description, Description: skill.Description})
		}
	}
	selection := promptctx.SelectSkillsMultiQueryLexical(query, documents, promptctx.SkillShadowSelectionMaxResults)
	if selection.QueryTermCount == 0 {
		return skills
	}
	selected := make(map[int]bool, len(selection.CandidateIDs))
	for _, id := range selection.CandidateIDs {
		selected[id] = true
	}
	out := make([]promptctx.InstructionsSkillMetadata, 0, len(selection.CandidateIDs))
	for i, skill := range skills {
		if selected[i] || !skill.AllowsImplicitInvocation() {
			out = append(out, skill)
		}
	}
	return out
}
