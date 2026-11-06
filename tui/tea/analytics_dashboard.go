package tea

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/auth"
	"codex_go/chatgptapi"
	codextui "codex_go/tui"
	"codex_go/tui/analytics"
	"codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
	"codex_go/tui/keymsg"
	"codex_go/tui/styles"
	"codex_go/utils"
)

// Full-screen account analytics dashboard opened from the /usage menu
// (Rust #45764 analytics::AnalyticsView). The retained view keeps navigation
// state across reopen while every report loads and fails independently.

// analyticsPollInterval bounds how long a completed report waits for its next
// frame. Loads run on their own goroutines; the dashboard polls them without
// blocking.
const analyticsPollInterval = 60 * time.Millisecond

// analyticsPollMsg asks the open dashboard to poll its pending report loads.
type analyticsPollMsg struct {
	generation int
}

type analyticsDashboard struct {
	active     bool
	generation int

	live        *analytics.Live
	liveFactory func(endDate time.Time) *analytics.Live
	modelNames  map[string]string

	sections             [analytics.AnalyticsSectionCount]analytics.AnalyticsSectionState
	chats                analytics.AnalyticsLoad[analytics.AnalyticsChats]
	chatLister           analytics.AnalyticsChatLister
	tasks                analytics.AnalyticsLoad[analytics.AnalyticsTaskChats]
	taskLister           analytics.AnalyticsTaskDescendantLister
	chatMetric           int
	profile              analytics.AnalyticsLoad[chatgptapi.AccountProfile]
	showZeroCreditGroups bool
	account              analytics.AnalyticsLoad[auth.PlanType]
	reportsStarted       bool

	section         analytics.AnalyticsSection
	zoomed          bool
	showHelp        bool
	scrollOffset    int
	helpScroll      int
	followSelection bool
	viewportHeight  int
	ranges          [3]int
	endDate         time.Time
	tokenModel      *string
	// theme is the active syntax theme, captured each render so the Summary
	// token calendar uses the terminal palette (Rust #46897).
	theme string
}

func newAnalyticsDashboard() *analyticsDashboard {
	dashboard := &analyticsDashboard{
		account:         analytics.UnavailableAnalyticsLoad[auth.PlanType](),
		chats:           analytics.UnavailableAnalyticsLoad[analytics.AnalyticsChats](),
		tasks:           analytics.UnavailableAnalyticsLoad[analytics.AnalyticsTaskChats](),
		profile:         analytics.UnavailableAnalyticsLoad[chatgptapi.AccountProfile](),
		section:         analytics.AnalyticsSectionSummary,
		zoomed:          true,
		followSelection: true,
		viewportHeight:  1,
		endDate:         analyticsDay(time.Now()),
	}
	for index := range dashboard.sections {
		dashboard.sections[index] = analytics.DefaultAnalyticsSectionState()
	}
	dashboard.sections[analytics.AnalyticsSectionUsage].Group = 1
	dashboard.sections[analytics.AnalyticsSectionActivity].Group = 2
	dashboard.sections[analytics.AnalyticsSectionChats].Cursor = 0
	return dashboard
}

// analyticsDay normalizes a timestamp to UTC midnight, like chrono's
// NaiveDate.
func analyticsDay(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// openAnalyticsDashboard opens the retained analytics view from the /usage
// menu (Rust AppEvent::OpenAnalytics).
func (m *Model) openAnalyticsDashboard() bubbletea.Cmd {
	if m == nil || !m.hasChatGPTAccount {
		return nil
	}
	modelNames := make(map[string]string, len(m.modelCatalogOpts))
	for _, option := range m.modelCatalogOpts {
		if strings.TrimSpace(option.ID) != "" && strings.TrimSpace(option.Label) != "" {
			modelNames[option.ID] = option.Label
		}
	}
	if m.analyticsDashboard == nil {
		m.analyticsDashboard = newAnalyticsDashboard()
	}
	factory := m.analyticsLive
	if factory == nil {
		codexHome := m.codexHome
		factory = func(endDate time.Time) *analytics.Live {
			return analytics.NewLive(analytics.AnalyticsSessionConfig{CodexHome: codexHome}, endDate)
		}
	}
	m.analyticsDashboard.open(factory, modelNames, m.analyticsChats, m.analyticsTaskThreads)
	return m.analyticsDashboard.nextPollCmd()
}

// openAnalyticsSummary opens the retained dashboard on the Summary tab with an
// optional activity view (Rust AppEvent::OpenAnalytics { view }, #45769).
func (m *Model) openAnalyticsSummary(view *chatwidget.TokenActivityView) bubbletea.Cmd {
	cmd := m.openAnalyticsDashboard()
	if m == nil || m.analyticsDashboard == nil {
		return cmd
	}
	dashboard := m.analyticsDashboard
	dashboard.section = analytics.AnalyticsSectionSummary
	dashboard.zoomed = true
	dashboard.scrollOffset = 0
	if view != nil {
		for index, candidate := range analytics.AnalyticsSummaryViews {
			if candidate == *view {
				dashboard.sections[analytics.AnalyticsSectionSummary].Group = index
				break
			}
		}
	}
	return cmd
}

// open attaches a live session factory and restarts every report. Navigation
// state from a previous open is retained (Rust AnalyticsView::open).
func (d *analyticsDashboard) open(factory func(endDate time.Time) *analytics.Live, modelNames map[string]string, chatLister analytics.AnalyticsChatLister, taskLister analytics.AnalyticsTaskDescendantLister) {
	if d == nil {
		return
	}
	d.active = true
	d.generation++
	d.showHelp = false
	d.liveFactory = factory
	d.modelNames = modelNames
	d.chatLister = chatLister
	d.taskLister = taskLister
	d.refresh()
}

// refresh rebuilds the live client and every load (Rust
// AnalyticsView::refresh).
func (d *analyticsDashboard) refresh() {
	if d == nil {
		return
	}
	d.endDate = analyticsDay(time.Now())
	d.live = nil
	if d.liveFactory != nil {
		d.live = d.liveFactory(d.endDate)
	}
	for index := range d.sections {
		d.sections[index].History.Invalidate()
	}
	d.reportsStarted = false
	d.tokenModel = nil
	d.sections[analytics.AnalyticsSectionChats].Detail = nil
	d.chats.Invalidate()
	d.tasks.Invalidate()
	d.profile.Invalidate()
	d.account.Invalidate()
	if d.live != nil {
		live := d.live
		load := func(ctx context.Context) (*auth.PlanType, error) {
			session, err := live.Session(ctx)
			if err != nil {
				return nil, err
			}
			plan := session.Backend.Account().PlanType
			return &plan, nil
		}
		d.account = *analytics.StartAnalyticsLoad(context.Background(), load, nil)
	}
	d.startReports()
}

// cancelLoads invalidates every report, including loaded data, when the
// retained view is discarded (Rust AnalyticsView::cancel_loads).
func (d *analyticsDashboard) cancelLoads() {
	if d == nil {
		return
	}
	for index := range d.sections {
		d.sections[index].History.Invalidate()
	}
	d.chats.Invalidate()
	d.tasks.Invalidate()
	d.profile.Invalidate()
	d.account.Invalidate()
	d.live = nil
	d.liveFactory = nil
	d.chatLister = nil
	d.taskLister = nil
}

// finish closes the dashboard while keeping its navigation state. Pending
// loads are aborted so a late response can never repaint a closed view
// (Rust AnalyticsView::is_done handling).
func (d *analyticsDashboard) finish() {
	if d == nil {
		return
	}
	d.active = false
	for index := range d.sections {
		if d.sections[index].History.State() == analytics.AnalyticsLoadStateLoading {
			d.sections[index].History.Invalidate()
		}
	}
	if d.chats.State() == analytics.AnalyticsLoadStateLoading {
		d.chats.Invalidate()
	}
	if d.tasks.State() == analytics.AnalyticsLoadStateLoading {
		d.tasks.Invalidate()
	}
	if d.profile.State() == analytics.AnalyticsLoadStateLoading {
		d.profile.Invalidate()
	}
	d.account.Invalidate()
	d.live = nil
}

func (d *analyticsDashboard) loadReport(section analytics.AnalyticsSection) {
	if d == nil {
		return
	}
	if !section.HasReport() {
		return
	}
	if d.live == nil || d.liveFactory == nil {
		d.sections[section].History.Invalidate()
		return
	}
	report := section.Report()
	group := d.sections[section].Group
	rangeIndex := d.ranges[section.RangeGroup(d.business())]
	days := uint32(7)
	if rangeIndex != 0 {
		days = 30
	}
	grouping := analytics.AnalyticsGroupingAt(group)
	live := d.live
	model := d.tokenModel
	load := func(ctx context.Context) (*analytics.AccountAnalyticsHistory, error) {
		if model != nil && report == analytics.AccountAnalyticsReportUsage {
			return live.FilteredHistory(ctx, report, days, grouping, model)
		}
		return live.History(ctx, report, days, grouping)
	}
	d.sections[section].History.Cancel()
	d.sections[section].History = *analytics.StartAnalyticsLoad(context.Background(), load, nil)
}

// loadSummary starts the profile load independently of the report loads
// (Rust AnalyticsView::load_summary, #45769).
func (d *analyticsDashboard) loadSummary() {
	if d == nil {
		return
	}
	if d.live == nil || d.liveFactory == nil {
		d.profile.Invalidate()
		return
	}
	live := d.live
	d.profile = *analytics.StartAnalyticsLoad(context.Background(), func(ctx context.Context) (*chatgptapi.AccountProfile, error) {
		return analytics.LoadAnalyticsProfile(ctx, live)
	}, nil)
}

func (d *analyticsDashboard) business() bool {
	if d == nil {
		return false
	}
	plan, ok := d.account.Ready()
	if !ok {
		return false
	}
	kind := analytics.AccountKindFor(plan)
	return kind == analytics.AccountKindBusiness || kind == analytics.AccountKindEnterprise
}

func (d *analyticsDashboard) consumerAttribution() bool {
	if d == nil || d.business() {
		return false
	}
	var attributed *bool
	if d.live != nil {
		value := d.live.AttributedUsage()
		attributed = &value
	}
	return analytics.AnalyticsConsumerAttribution(false, attributed)
}

func (d *analyticsDashboard) visibleSections() []analytics.AnalyticsSection {
	if d == nil {
		return nil
	}
	plan, ok := d.account.Ready()
	return analytics.AnalyticsVisibleSections(plan, ok)
}

func (d *analyticsDashboard) groupOptions() []int {
	if d == nil || len(d.visibleSections()) == 0 {
		return nil
	}
	var creditGroups []int
	if d.live != nil {
		creditGroups = d.live.CreditGroups()
	}
	return analytics.AnalyticsGroupOptions(d.section, d.business(), d.consumerAttribution(), creditGroups)
}

// pollReports advances every load and starts the reports once the account is
// known (Rust AnalyticsView::poll_reports).
func (d *analyticsDashboard) pollReports() {
	if d == nil {
		return
	}
	for index := range d.sections {
		d.sections[index].History.Poll()
	}
	d.chats.Poll()
	d.tasks.Poll()
	d.profile.Poll()
	d.account.Poll()
	d.startReports()
	if !d.business() && !d.consumerAttribution() {
		group := d.sections[analytics.AnalyticsSectionUsage].Group
		if group != 0 && group != 2 {
			d.sections[analytics.AnalyticsSectionUsage].Group = 0
		}
	}
	if chats, ok := d.chats.Ready(); ok {
		cursor := &d.sections[analytics.AnalyticsSectionChats].Cursor
		*cursor = analyticsMin(*cursor, analyticsMax(len(chats.Rows)-1, 0))
	}
	if tasks, ok := d.tasks.Ready(); ok {
		cursor := &d.sections[analytics.AnalyticsSectionChats].Cursor
		*cursor = analyticsMin(*cursor, analyticsMax(len(tasks.Rows)-1, 0))
	}
}

// startReports loads the account-specific sections exactly once per refresh.
func (d *analyticsDashboard) startReports() {
	if d == nil || d.reportsStarted {
		return
	}
	plan, ok := d.account.Ready()
	if !ok {
		return
	}
	kind := analytics.AccountKindFor(plan)
	d.reportsStarted = true
	visible := d.visibleSections()
	if len(visible) == 0 {
		return
	}
	if !analyticsSectionVisible(visible, d.section) {
		d.section = visible[0]
	}
	usageGroups := []int{1, 2, 0, 3}
	if d.business() {
		usageGroups = []int{6, 2}
	}
	usage := &d.sections[analytics.AnalyticsSectionUsage]
	if !analyticsContainsInt(usageGroups, usage.Group) {
		if kind == analytics.AccountKindBusiness {
			usage.Group = 2
		} else {
			usage.Group = usageGroups[0]
		}
	}
	if d.live != nil {
		if creditGroups := d.live.CreditGroups(); len(creditGroups) > 0 &&
			!analyticsContainsInt(creditGroups, d.sections[analytics.AnalyticsSectionCredits].Group) {
			d.sections[analytics.AnalyticsSectionCredits].Group = creditGroups[0]
		}
	}
	d.loadSummary()
	for _, section := range visible {
		d.loadReport(section)
	}
	if analyticsSectionVisible(visible, analytics.AnalyticsSectionChats) {
		if !d.business() {
			if d.live != nil && d.chatLister != nil && d.taskLister != nil {
				live := d.live
				roots := d.chatLister
				descendants := d.taskLister
				d.tasks = *analytics.StartAnalyticsLoadWithTimeout(context.Background(), func(ctx context.Context) (*analytics.AnalyticsTaskChats, error) {
					return analytics.LoadAnalyticsTasks(ctx, live, roots, descendants)
				}, nil, 120*time.Second)
			} else {
				d.tasks = analytics.UnavailableAnalyticsLoad[analytics.AnalyticsTaskChats]()
			}
		} else if d.live != nil && d.chatLister != nil {
			live := d.live
			lister := d.chatLister
			d.chats = *analytics.StartAnalyticsLoadWithTimeout(context.Background(), func(ctx context.Context) (*analytics.AnalyticsChats, error) {
				return analytics.LoadAnalyticsChats(ctx, live, lister)
			}, nil, 90*time.Second)
		} else {
			d.chats = analytics.UnavailableAnalyticsLoad[analytics.AnalyticsChats]()
		}
	}
}

// changeRange flips the shared 7/30 day range and keeps each section's
// selection on the same calendar day when it is still in range (Rust
// AnalyticsView::change_range).
func (d *analyticsDashboard) changeRange() {
	if d == nil {
		return
	}
	group := d.section.RangeGroup(d.business())
	d.ranges[group] ^= 1
	toThirty := d.ranges[group] == 1
	for _, section := range d.visibleSections() {
		if section.RangeGroup(d.business()) != group {
			continue
		}
		state := &d.sections[section]
		state.Cursor, state.Detail = analytics.AnalyticsShiftSectionSelection(state.Cursor, state.Detail, toThirty)
		d.loadReport(section)
	}
}

func (d *analyticsDashboard) rowCount() int {
	if d == nil {
		return 7
	}
	if d.section == analytics.AnalyticsSectionChats {
		if !d.business() {
			if tasks, ok := d.tasks.Ready(); ok {
				return len(tasks.Rows)
			}
			return 0
		}
		if chats, ok := d.chats.Ready(); ok {
			return len(chats.Rows)
		}
		return 0
	}
	return analytics.AnalyticsSectionDays(d.ranges[d.section.RangeGroup(d.business())])
}

func (d *analyticsDashboard) dayHasDetails(section analytics.AnalyticsSection, cursor int) bool {
	history, ok := d.sections[section].History.Ready()
	if !ok {
		return false
	}
	start := analytics.AnalyticsSectionStartDate(d.endDate, d.ranges[section.RangeGroup(d.business())])
	return analytics.AnalyticsDayHasDetails(&history, start, cursor)
}

func (d *analyticsDashboard) modelName(key string) string {
	if name, ok := d.modelNames[key]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	return key
}

// summaryProfile returns the decoded profile, its load message, readiness and
// the resolved plan for the Summary panel and card.
func (d *analyticsDashboard) summaryProfile() (*chatgptapi.AccountProfile, string, bool, *auth.PlanType) {
	if d == nil {
		return nil, "", false, nil
	}
	profile, ready := d.profile.Ready()
	var loaded *chatgptapi.AccountProfile
	if ready {
		loaded = &profile
	}
	message, _ := d.profile.Message()
	var planPtr *auth.PlanType
	if plan, ok := d.account.Ready(); ok {
		planPtr = &plan
	}
	return loaded, message, ready, planPtr
}

// cycleTokenModel advances the business usage model filter, wrapping to no
// filter after the last model (Rust AnalyticsView's `m` handling).
func (d *analyticsDashboard) cycleTokenModel() {
	var models []string
	if d.live != nil {
		models = d.live.TokenModels()
	}
	var next *string
	switch {
	case d.tokenModel == nil:
		if len(models) > 0 {
			value := models[0]
			next = &value
		}
	default:
		position := -1
		for index, model := range models {
			if model == *d.tokenModel {
				position = index
				break
			}
		}
		switch {
		case position >= 0 && position+1 < len(models):
			value := models[position+1]
			next = &value
		case position < 0 && len(models) > 0:
			value := models[0]
			next = &value
		}
	}
	d.tokenModel = next
	d.loadReport(analytics.AnalyticsSectionUsage)
}

// nextPollCmd schedules the next poll while any load is pending.
func (d *analyticsDashboard) nextPollCmd() bubbletea.Cmd {
	if d == nil || !d.active {
		return nil
	}
	if !d.anyLoading() {
		return nil
	}
	generation := d.generation
	return bubbletea.Tick(analyticsPollInterval, func(time.Time) bubbletea.Msg {
		return analyticsPollMsg{generation: generation}
	})
}

// anyLoading reports whether any account or report load is still pending.
func (d *analyticsDashboard) anyLoading() bool {
	if d == nil {
		return false
	}
	if d.account.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	for index := range d.sections {
		if d.sections[index].History.State() == analytics.AnalyticsLoadStateLoading {
			return true
		}
	}
	if d.chats.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	if d.tasks.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	if d.profile.State() == analytics.AnalyticsLoadStateLoading {
		return true
	}
	return false
}

func analyticsSectionVisible(visible []analytics.AnalyticsSection, section analytics.AnalyticsSection) bool {
	for _, candidate := range visible {
		if candidate == section {
			return true
		}
	}
	return false
}

func analyticsContainsInt(values []int, value int) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func analyticsMax(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func analyticsMin(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// analyticsPlainRune returns a single unmodified rune key.
func analyticsPlainRune(msg keymsg.KeyMsg) (rune, bool) {
	if msg.Type != keymsg.KeyRunes || len(msg.Runes) != 1 || msg.Alt || msg.Paste {
		return 0, false
	}
	return msg.Runes[0], true
}

// analyticsListAction resolves the shared list keymap action for a key.
func (m *Model) analyticsListAction(msg keymsg.KeyMsg) string {
	spec := keySpecFromKeyMsg(msg)
	if spec == "" {
		return ""
	}
	for _, action := range []string{
		"move_up", "move_down", "move_left", "move_right",
		"page_up", "page_down", "jump_top", "jump_bottom",
		"accept", "cancel",
	} {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		for _, binding := range bindings {
			normalized, err := codextui.NormalizeKeybindingSpec(binding)
			if err != nil {
				continue
			}
			if normalized == spec {
				return action
			}
		}
	}
	return ""
}

// analyticsListActionForKey resolves the shared list keymap action for a key,
// falling back to the vim horizontal aliases only when the key itself is
// unbound: explicit bindings win and remapped or unbound arrows stay
// authoritative (Rust AnalyticsView::handle_key, #45772).
func (m *Model) analyticsListActionForKey(msg keymsg.KeyMsg) string {
	if action := m.analyticsListAction(msg); action != "" {
		return action
	}
	runeKey, ok := analyticsPlainRune(msg)
	if !ok {
		return ""
	}
	switch runeKey {
	case 'h':
		return m.analyticsListAction(keymsg.KeyMsg{Type: keymsg.KeyLeft})
	case 'l':
		return m.analyticsListAction(keymsg.KeyMsg{Type: keymsg.KeyRight})
	}
	return ""
}

func (d *analyticsDashboard) selectSection(section analytics.AnalyticsSection) {
	if d.zoomed && section != d.section {
		d.scrollOffset = 0
	}
	d.followSelection = !d.zoomed || section != d.section
	d.section = section
	d.showHelp = false
}

// scrollOffsetForRender reports the report or help offset the next frame
// paints (Rust AnalyticsView::scroll_offset, #46863).
func (d *analyticsDashboard) scrollOffsetForRender() int {
	if d != nil && d.showHelp {
		return d.helpScroll
	}
	if d == nil {
		return 0
	}
	return d.scrollOffset
}

// setScrollOffsetForRender writes the active body's independent scroll offset.
func (d *analyticsDashboard) setScrollOffsetForRender(offset int) {
	if d == nil {
		return
	}
	if d.showHelp {
		d.helpScroll = offset
		return
	}
	d.scrollOffset = offset
}

// summaryAction scrolls the Summary body. A missing action leaves the view
// unchanged (Rust AnalyticsView::summary_action, #45769).
func (d *analyticsDashboard) summaryAction(action string) {
	if d == nil {
		return
	}
	offset := d.scrollOffsetForRender()
	switch action {
	case "move_up":
		offset--
	case "move_down":
		offset++
	case "page_up":
		offset -= d.viewportHeight
	case "page_down":
		offset += d.viewportHeight
	case "jump_top":
		offset = 0
	case "jump_bottom":
		offset = int(^uint(0) >> 1)
	case "cancel":
		d.finish()
		return
	default:
		return
	}
	if offset < 0 {
		offset = 0
	}
	d.setScrollOffsetForRender(offset)
	d.followSelection = false
}

// analyticsControl is a report action shared by keyboard dispatch, painted
// controls, and the help screen (Rust analytics/controls.rs Control, #46863).
type analyticsControl int

const (
	analyticsControlRange analyticsControl = iota
	analyticsControlGroup
	analyticsControlModel
	analyticsControlTaskMetric
	analyticsControlDashboard
	analyticsControlZeroCreditGroups
)

// analyticsControlAvailable applies one eligibility rule to keyboard actions,
// visible controls, and help (Rust AnalyticsView::control_available).
func (d *analyticsDashboard) analyticsControlAvailable(control analyticsControl) bool {
	if d == nil || len(d.visibleSections()) == 0 {
		return false
	}
	switch control {
	case analyticsControlRange:
		return d.section.HasReport()
	case analyticsControlGroup:
		return len(d.groupOptions()) > 1
	case analyticsControlModel:
		return d.section == analytics.AnalyticsSectionUsage && d.business()
	case analyticsControlTaskMetric:
		return d.section == analytics.AnalyticsSectionChats && !d.business()
	case analyticsControlDashboard:
		return true
	case analyticsControlZeroCreditGroups:
		return d.section == analytics.AnalyticsSectionChats && d.business() && d.sections[d.section].Detail != nil
	default:
		return false
	}
}

// activateAnalyticsControl applies a visible action without synthesizing a
// remappable key event.
func (d *analyticsDashboard) activateAnalyticsControl(m *Model, control analyticsControl) bool {
	if !d.analyticsControlAvailable(control) {
		return false
	}
	switch control {
	case analyticsControlRange:
		d.changeRange()
	case analyticsControlGroup:
		options := d.groupOptions()
		next := 0
		for index, group := range options {
			if group == d.sections[d.section].Group {
				next = (index + 1) % len(options)
				break
			}
		}
		d.sections[d.section].Group = options[next]
		d.loadReport(d.section)
	case analyticsControlModel:
		d.cycleTokenModel()
	case analyticsControlTaskMetric:
		tasks, ok := d.tasks.Ready()
		var loaded *analytics.AnalyticsTaskChats
		if ok {
			loaded = &tasks
		}
		metrics := analytics.AnalyticsTaskMetrics(loaded)
		current := 0
		for index, metric := range metrics {
			if metric == d.chatMetric {
				current = index
				break
			}
		}
		d.chatMetric = metrics[(current+1)%len(metrics)]
		d.sections[d.section].Detail = nil
	case analyticsControlDashboard:
		d.zoomed = !d.zoomed
		d.scrollOffset = 0
		d.followSelection = true
	case analyticsControlZeroCreditGroups:
		d.showZeroCreditGroups = !d.showZeroCreditGroups
	}
	return true
}

// analyticsHelpShortcutAvailable keeps a configured binding authoritative when
// it claims ? or shift-? in the list context (Rust
// AnalyticsView::help_shortcut_available).
func (d *analyticsDashboard) analyticsHelpShortcutAvailable(m *Model) bool {
	if m == nil {
		return true
	}
	for _, action := range []string{
		"move_up", "move_down", "move_left", "move_right",
		"page_up", "page_down", "jump_top", "jump_bottom",
		"accept", "cancel",
	} {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		for _, binding := range bindings {
			normalized, err := codextui.NormalizeKeybindingSpec(binding)
			if err != nil {
				continue
			}
			if normalized == "?" || normalized == "shift-?" {
				return false
			}
		}
	}
	return true
}

// analyticsHelpLines renders the scrollable Usage shortcuts screen. Control
// availability matches the keys that actually act on the current report
// (Rust analytics/chrome.rs help_lines).
func (d *analyticsDashboard) analyticsHelpLines(m *Model, width int) []string {
	line := func(action string) string { return analyticsHintLabel(m, action) }
	raw := []string{
		"Tab / Shift+Tab · next / previous report",
		fmt.Sprintf("1–%d · open a report directly", len(d.visibleSections())),
		line("move_left") + " / " + line("move_right") + " · select chart day or plan window",
		line("move_up") + " / " + line("move_down") + " · select chat or plan period; scroll Overview",
		line("accept") + " · expand or collapse details",
		line("page_up") + " / " + line("page_down") + " · scroll report",
	}
	if d.analyticsControlAvailable(analyticsControlRange) {
		raw = append(raw, "r · switch between 7 and 30 days")
	}
	if d.analyticsControlAvailable(analyticsControlGroup) {
		raw = append(raw, "g · change grouping / Overview aggregation")
	}
	if d.analyticsControlAvailable(analyticsControlModel) {
		raw = append(raw, "m · cycle model filter")
	}
	if d.analyticsControlAvailable(analyticsControlTaskMetric) {
		raw = append(raw, "s · change chat sort metric")
	}
	if d.analyticsControlAvailable(analyticsControlZeroCreditGroups) {
		raw = append(raw, "a · show zero-credit groups in expanded details")
	}
	raw = append(raw,
		"R · refresh all reports",
		"z · dashboard / focused report; "+line("accept")+" · focus dashboard card",
		line("cancel")+" · back; q / ctrl+c · close usage",
	)
	if d.analyticsHelpShortcutAvailable(m) {
		raw = append(raw[:len(raw)-1], append([]string{"? · toggle this help"}, raw[len(raw)-1:]...)...)
	}
	if updated := d.analyticsUpdatedLabel(); updated != "" {
		raw = append(raw, "Report updated "+updated)
	}
	return analytics.AnalyticsWrapLines(raw, width)
}

// handleKey applies one key press. It mirrors Rust AnalyticsView::handle_key.
func (d *analyticsDashboard) handleKey(m *Model, msg keymsg.KeyMsg) bool {
	if d == nil || !d.active {
		return false
	}
	if runeKey, ok := analyticsPlainRune(msg); ok && runeKey == 'q' {
		d.finish()
		return true
	}
	if msg.Type == keymsg.KeyCtrlC {
		d.finish()
		return true
	}
	action := ""
	if runeKey, ok := analyticsPlainRune(msg); ok && runeKey == ' ' {
		action = "accept"
	} else {
		action = m.analyticsListActionForKey(msg)
	}
	switch action {
	case "accept", "cancel", "move_left", "move_right", "move_up", "move_down", "jump_top", "jump_bottom":
		d.followSelection = true
	default:
		d.followSelection = false
	}
	runeKey, hasRune := analyticsPlainRune(msg)
	if d.analyticsHelpShortcutAvailable(m) && hasRune && runeKey == '?' {
		d.showHelp = !d.showHelp
		d.helpScroll = 0
		d.followSelection = false
		return true
	}
	if d.showHelp {
		if action == "cancel" {
			d.showHelp = false
			d.followSelection = false
			return true
		}
		d.summaryAction(action)
		return true
	}
	if hasRune && runeKey == 'z' && d.analyticsControlAvailable(analyticsControlDashboard) {
		d.activateAnalyticsControl(m, analyticsControlDashboard)
		return true
	}
	if !d.zoomed && action == "accept" {
		d.activateAnalyticsControl(m, analyticsControlDashboard)
		return true
	}
	if hasRune && runeKey == 'R' {
		d.refresh()
		return true
	}
	if hasRune && runeKey == 'a' && d.analyticsControlAvailable(analyticsControlZeroCreditGroups) {
		d.activateAnalyticsControl(m, analyticsControlZeroCreditGroups)
		return true
	}
	if hasRune && runeKey == 'm' && d.analyticsControlAvailable(analyticsControlModel) {
		d.activateAnalyticsControl(m, analyticsControlModel)
		return true
	}
	if hasRune && runeKey == 'r' && d.analyticsControlAvailable(analyticsControlRange) {
		d.activateAnalyticsControl(m, analyticsControlRange)
		return true
	}
	if hasRune && runeKey == 'g' && d.analyticsControlAvailable(analyticsControlGroup) {
		d.activateAnalyticsControl(m, analyticsControlGroup)
		return true
	}
	if hasRune && runeKey == 's' && d.analyticsControlAvailable(analyticsControlTaskMetric) {
		d.activateAnalyticsControl(m, analyticsControlTaskMetric)
		return true
	}
	visible := d.visibleSections()
	if hasRune && runeKey >= '1' && runeKey <= '7' {
		if index := int(runeKey - '1'); index < len(visible) {
			d.selectSection(visible[index])
		}
		return true
	}
	current := 0
	for index, section := range visible {
		if section == d.section {
			current = index
			break
		}
	}
	switch msg.Type {
	case keymsg.KeyTab:
		if len(visible) > 0 {
			d.selectSection(visible[(current+1)%len(visible)])
		}
		return true
	case keymsg.KeyShiftTab:
		if len(visible) > 0 {
			d.selectSection(visible[(current+len(visible)-1)%len(visible)])
		}
		return true
	}
	if len(visible) == 0 {
		if action == "cancel" {
			d.finish()
		}
		return true
	}
	if !d.zoomed && action == "cancel" {
		d.finish()
		return true
	}
	if d.section == analytics.AnalyticsSectionSummary {
		d.summaryAction(action)
		return true
	}
	if (d.section == analytics.AnalyticsSectionPlugins ||
		d.section == analytics.AnalyticsSectionActivity ||
		d.section == analytics.AnalyticsSectionSkills) &&
		action == "accept" && !d.dayHasDetails(d.section, d.sections[d.section].Cursor) {
		return true
	}
	chart := d.section != analytics.AnalyticsSectionChats
	if !chart && (action == "accept" || action == "move_right") {
		cursor := d.sections[d.section].Cursor
		if d.business() {
			chats, ok := d.chats.Ready()
			if !ok || cursor < 0 || cursor >= len(chats.Rows) || chats.Rows[cursor].Usage == nil {
				return true
			}
		} else {
			tasks, ok := d.tasks.Ready()
			if !ok || cursor < 0 || cursor >= len(tasks.Rows) ||
				analytics.AnalyticsTaskAvailable(tasks.Rows[cursor]) == nil {
				return true
			}
		}
	}
	count := analyticsMax(d.rowCount(), 1)
	state := &d.sections[d.section]
	previousCursor := state.Cursor
	scrollOffset := d.scrollOffsetForRender()
	switch action {
	case "move_up":
		state.Cursor = analyticsMax(state.Cursor-1, 0)
	case "move_down":
		state.Cursor = analyticsMin(state.Cursor+1, count-1)
	case "move_left":
		if chart {
			state.Cursor = analyticsMax(state.Cursor-1, 0)
		} else {
			state.Detail = nil
		}
	case "move_right":
		if chart {
			state.Cursor = analyticsMin(state.Cursor+1, count-1)
		} else if state.Detail != nil && *state.Detail == state.Cursor {
			state.Detail = nil
		} else {
			cursor := state.Cursor
			state.Detail = &cursor
		}
	case "jump_top":
		state.Cursor = 0
	case "jump_bottom":
		state.Cursor = count - 1
	case "accept":
		if state.Detail != nil && *state.Detail == state.Cursor {
			state.Detail = nil
		} else {
			cursor := state.Cursor
			state.Detail = &cursor
		}
	case "cancel":
		if state.Detail == nil {
			d.finish()
			return true
		}
		state.Detail = nil
	case "page_down":
		scrollOffset += d.viewportHeight
		d.followSelection = false
	case "page_up":
		scrollOffset = analyticsMax(scrollOffset-d.viewportHeight, 0)
		d.followSelection = false
	}
	if state.Detail != nil {
		cursor := state.Cursor
		state.Detail = &cursor
	}
	if !chart && state.Cursor != previousCursor {
		state.Detail = nil
	}
	d.setScrollOffsetForRender(scrollOffset)
	return true
}

// render draws the whole dashboard inside the terminal area (Rust
// analytics/render.rs AnalyticsView::render, #46863).
func (d *analyticsDashboard) render(m *Model, width, height int) string {
	d.pollReports()
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	visible := d.visibleSections()
	theme := m.styles()
	var plan *auth.PlanType
	if value, ok := d.account.Ready(); ok {
		plan = &value
	}
	lines := []string{analyticsUsageTitleLine(theme, d.live, plan)}
	if len(visible) > 0 {
		lines = append(lines, analyticsDashboardTabBar(theme, d, innerWidth, visible))
	} else {
		lines = append(lines, "")
	}
	reset := theme.ExecCell.Reset
	lines = append(lines, theme.Chat.DimText+strings.Repeat("─", innerWidth)+reset)
	controlHeight := 2
	if innerWidth < 100 {
		controlHeight = 3
	}
	lines = append(lines, d.analyticsControlsLines(m, innerWidth, controlHeight)...)
	bodyHeight := height - len(lines) - 2
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	d.viewportHeight = analyticsMax(bodyHeight, 1)
	d.theme = m.activeTUITheme()
	var body []string
	selectionStart, selectionEnd := 0, 0
	if d.showHelp {
		body = d.analyticsHelpLines(m, innerWidth)
	} else {
		body, selectionStart, selectionEnd = d.bodyLines(innerWidth, bodyHeight)
	}
	offset := d.scrollOffsetForRender()
	if d.followSelection {
		if selectionStart < offset {
			offset = selectionStart
		} else {
			end := selectionStart + analyticsMin(selectionEnd-selectionStart, d.viewportHeight)
			if end > offset+d.viewportHeight {
				offset = end - d.viewportHeight
			}
		}
	}
	offset = analyticsMin(analyticsMax(offset, 0), analyticsMax(len(body)-d.viewportHeight, 0))
	d.setScrollOffsetForRender(offset)
	d.followSelection = false
	for index := 0; index < bodyHeight; index++ {
		position := offset + index
		if position < len(body) {
			lines = append(lines, body[position])
		} else {
			lines = append(lines, "")
		}
	}
	footer := d.analyticsFooterLines(m, innerWidth, len(body) > d.viewportHeight)
	for len(footer) < 2 {
		footer = append(footer, "")
	}
	lines = append(lines, footer[0], footer[1])
	rendered := make([]string, len(lines))
	for index, line := range lines {
		if line == "" {
			rendered[index] = ""
			continue
		}
		rendered[index] = " " + analyticsTruncateStyled(line, innerWidth)
	}
	if height > 0 && len(rendered) > height {
		rendered = rendered[:height]
	}
	return strings.Join(rendered, "\n")
}

// analyticsUsageTitleLine renders the fixed Usage heading. The plan and
// account live in the heading while report controls sit below the tabs.
func analyticsUsageTitleLine(theme styles.Styles, live *analytics.Live, plan *auth.PlanType) string {
	title := "Usage"
	if plan != nil {
		title += " · " + analyticsHeadingPlanLabel(*plan)
	}
	reset := theme.ExecCell.Reset
	styled := theme.ExecCell.Bold + title + reset
	if live != nil {
		if account := live.AccountLabel(); account != nil && strings.TrimSpace(*account) != "" {
			styled += theme.Chat.DimText + " · " + strings.TrimSpace(*account) + reset
		}
	}
	return styled
}

// analyticsHeadingPlanLabel is the short plan name in the usage heading (Rust
// render.rs plan match; distinct from the Summary's quota SKU label).
func analyticsHeadingPlanLabel(plan auth.PlanType) string {
	switch plan {
	case auth.PlanFree:
		return "Free"
	case auth.PlanGo:
		return "Go"
	case auth.PlanPlus:
		return "Plus"
	case auth.PlanPro, auth.PlanProMax:
		return "Pro"
	case auth.PlanProlite:
		return "Pro Lite"
	case auth.PlanTeam, auth.PlanBusiness, auth.PlanSelfServeBusinessProlite, auth.PlanSelfServeBusinessUsageBased:
		return "Business"
	case auth.PlanEnt26, auth.PlanEnterpriseCBPAutomation, auth.PlanEnterpriseCBPUsageBased, auth.PlanEnterprise:
		return "Enterprise"
	case auth.PlanEdu, auth.PlanEduPlus, auth.PlanEduPro:
		return "Education"
	default:
		return "Account"
	}
}

// analyticsDashboardTabBar paints the compact report tabs. The numbered form
// appears only when the full labels fit the wide layout.
func analyticsDashboardTabBar(theme styles.Styles, d *analyticsDashboard, width int, visible []analytics.AnalyticsSection) string {
	labels := make([]string, 0, len(visible))
	selected := 0
	for index, section := range visible {
		label := analytics.AnalyticsSectionTabLabel(section, d.business())
		if width >= 80 {
			label = fmt.Sprintf("%d %s", index+1, label)
		}
		if section == d.section {
			selected = index
		}
		labels = append(labels, label)
	}
	reset := theme.ExecCell.Reset
	activeStyle := func(text string) string {
		return theme.Dialog.Highlight + theme.ExecCell.Bold + text + reset
	}
	inactiveStyle := func(text string) string {
		return theme.Chat.DimText + text + reset
	}
	return analyticsTruncateStyled(bottompane.FilledTabBarStyledText(labels, selected, width, activeStyle, inactiveStyle), width)
}

// analyticsControlsLines paints the date range, grouping, model, sort, and
// update metadata in rows reserved by terminal width alone.
func (d *analyticsDashboard) analyticsControlsLines(m *Model, width, height int) []string {
	rows := make([][]string, height)
	if height <= 0 {
		return nil
	}
	reset := ""
	theme := m.styles()
	reset = theme.ExecCell.Reset
	key := func(text string) string {
		return analyticsFooterKeyStyle() + theme.ExecCell.Bold + text + reset
	}
	dim := func(text string) string {
		return theme.Chat.DimText + text + reset
	}
	accent := func(text string) string {
		return theme.Dialog.Highlight + theme.ExecCell.Bold + text + reset
	}
	if d.showHelp {
		rows[0] = append(rows[0], theme.ExecCell.Bold+"Usage shortcuts"+reset)
	} else if len(d.visibleSections()) > 0 {
		report := d.analyticsControlAvailable(analyticsControlRange)
		optionRow := 0
		if width < 100 && report {
			optionRow = 1
		}
		if !d.zoomed {
			focus := ""
			if hint := analyticsHintLabel(m, "accept"); hint != "" {
				focus = dim(" · ") + key(hint) + dim(" focus")
			}
			rows[0] = append(rows[0], theme.ExecCell.Bold+"Dashboard"+reset+focus)
		}
		if report {
			rangeIndex := d.ranges[d.section.RangeGroup(d.business())]
			sevenDay := dim("7d")
			thirtyDay := dim("30d")
			if rangeIndex == 0 {
				sevenDay = accent("7d")
			} else {
				thirtyDay = accent("30d")
			}
			dates := analyticsControlDateInterval(d.endDate, rangeIndex, width)
			rows[0] = append(rows[0], key("r")+" "+sevenDay+dim("/")+thirtyDay+dim(" · "+dates))
		}
		if d.analyticsControlAvailable(analyticsControlGroup) {
			rows[optionRow] = append(rows[optionRow],
				key("g")+" "+d.section.GroupLabel(d.sections[d.section].Group, d.consumerAttribution()))
		}
		if d.analyticsControlAvailable(analyticsControlModel) {
			model := "All models"
			if d.tokenModel != nil && strings.TrimSpace(*d.tokenModel) != "" {
				model = strings.TrimSpace(*d.tokenModel)
			}
			rows[optionRow] = append(rows[optionRow], key("m")+" "+model)
		}
		if d.analyticsControlAvailable(analyticsControlTaskMetric) {
			tasks, ok := d.tasks.Ready()
			var loaded *analytics.AnalyticsTaskChats
			if ok {
				loaded = &tasks
			}
			metric := analytics.AnalyticsTaskMetric(d.chatMetric, loaded)
			rows[0] = append(rows[0], key("s")+dim(" sort: ")+analytics.AnalyticsTaskMetricNames[metric])
		}
		rows[height-1] = nil
		if updated := d.analyticsUpdatedLabel(); updated != "" {
			rows[height-1] = append(rows[height-1], dim("Updated · "+updated))
		}
	}
	lines := make([]string, height)
	for index, row := range rows {
		if len(row) == 0 {
			continue
		}
		lines[index] = analyticsTruncateStyled(strings.Join(row, dim(" · ")), width)
	}
	return lines
}

// analyticsControlDateInterval is Rust controls.rs' width-dependent range
// label, including the numeric cross-year fallback on narrow screens.
func analyticsControlDateInterval(endDate time.Time, rangeIndex, width int) string {
	start := analytics.AnalyticsSectionStartDate(endDate, rangeIndex)
	switch {
	case start.Year() != endDate.Year() && width < 50:
		return start.Format("01/02/2006") + "–" + endDate.Format("01/02/2006")
	case start.Year() == endDate.Year():
		return start.Format("Jan 2") + "–" + endDate.Format("Jan 2, 2006")
	default:
		return start.Format("Jan 2, 2006") + "–" + endDate.Format("Jan 2, 2006")
	}
}

// analyticsFooterLines keeps two hint rows regardless of report state.
func (d *analyticsDashboard) analyticsFooterLines(m *Model, width int, scrollable bool) []string {
	type hint struct{ key, label string }
	pair := func(first, second string) string {
		if first == "" && second == "" {
			return ""
		}
		return first + "/" + second
	}
	var actions []hint
	if d.showHelp {
		actions = append(actions, hint{analyticsHintLabel(m, "cancel"), "back"})
	} else if !d.zoomed {
		actions = append(actions,
			hint{analyticsHintLabel(m, "accept"), "focus"},
			hint{"tab", "card"},
		)
	} else {
		switch d.section {
		case analytics.AnalyticsSectionSummary:
		case analytics.AnalyticsSectionChats:
			actions = append(actions, hint{pair(analyticsHintLabel(m, "move_up"), analyticsHintLabel(m, "move_down")), "row"})
		default:
			actions = append(actions, hint{pair(analyticsHintLabel(m, "move_left"), analyticsHintLabel(m, "move_right")), "day"})
		}
		if d.dayHasDetails(d.section, d.sections[d.section].Cursor) || d.chatHasDetails() {
			actions = append(actions, hint{analyticsHintLabel(m, "accept"), "details"})
		}
	}
	if scrollable {
		actions = append(actions, hint{pair(analyticsHintLabel(m, "page_up"), analyticsHintLabel(m, "page_down")), "scroll"})
	}
	if !d.showHelp {
		actions = append(actions, hint{analyticsHintLabel(m, "cancel"), "back"})
	}
	var navigation []hint
	switch {
	case d.showHelp:
		navigation = append(navigation, hint{"?", "close help"})
	case len(d.visibleSections()) == 0:
		navigation = append(navigation, hint{"R", "retry"})
	default:
		reportKey := "tab"
		if width >= 80 {
			reportKey = fmt.Sprintf("tab/1–%d", len(d.visibleSections()))
		}
		zoom := "focus"
		switch {
		case d.zoomed && width < 50:
			zoom = "all"
		case d.zoomed:
			zoom = "dashboard"
		}
		helpLabel := ""
		if width >= 50 {
			helpLabel = "help"
		}
		navigation = append(navigation,
			hint{reportKey, "report"},
			hint{"z", zoom},
			hint{"?", helpLabel},
			hint{"R", "refresh"},
		)
	}
	if !d.analyticsHelpShortcutAvailable(m) {
		kept := navigation[:0]
		for _, item := range navigation {
			if item.key != "?" {
				kept = append(kept, item)
			}
		}
		navigation = kept
	}
	join := func(items []hint) string {
		var builder strings.Builder
		for _, item := range items {
			if item.key == "" {
				continue
			}
			part := item.key
			if item.label != "" {
				part += " " + item.label
			}
			separator := ""
			if builder.Len() > 0 {
				separator = " · "
			}
			if analytics.AnalyticsDisplayWidth(builder.String()+separator+part) > width {
				continue
			}
			builder.WriteString(separator)
			builder.WriteString(part)
		}
		return analyticsStyledControls(m.styles(), builder.String())
	}
	return []string{join(actions), join(navigation)}
}

// chatHasDetails reports whether the selected chat row can open details.
func (d *analyticsDashboard) chatHasDetails() bool {
	if d == nil || d.section != analytics.AnalyticsSectionChats {
		return false
	}
	cursor := d.sections[d.section].Cursor
	if d.business() {
		chats, ok := d.chats.Ready()
		return ok && cursor >= 0 && cursor < len(chats.Rows) && chats.Rows[cursor].Usage != nil
	}
	tasks, ok := d.tasks.Ready()
	return ok && cursor >= 0 && cursor < len(tasks.Rows) && analytics.AnalyticsTaskAvailable(tasks.Rows[cursor]) != nil
}

// bodyLines renders the maximized panel or the overview grid.
func (d *analyticsDashboard) bodyLines(width, height int) ([]string, int, int) {
	if len(d.visibleSections()) == 0 {
		return []string{analytics.AnalyticsStatusLine(d.account)}, 0, 1
	}
	if d.zoomed {
		chartHeight := height / 5
		chartHeight = analyticsMin(analyticsMax(chartHeight, 4), 10)
		panel := d.panel(d.section, width, chartHeight)
		if panel.ChartBands > 0 {
			overhead := len(panel.Lines) - chartHeight*panel.ChartBands
			adjusted := (height - overhead - 1) / panel.ChartBands
			if adjusted < 2 {
				adjusted = 2
			}
			if adjusted != chartHeight {
				panel = d.panel(d.section, width, adjusted)
			}
		}
		_, profileReady := d.profile.Ready()
		centerSummary := d.section == analytics.AnalyticsSectionSummary && profileReady
		if centerSummary {
			// The chrome owns the heading and rule; center the profile in the
			// report body (Rust render.rs center_summary, #46863).
			padding := (height - len(panel.Lines)) / 2
			if padding > 0 {
				insert := make([]string, padding)
				panel.Lines = append(insert, panel.Lines...)
			}
		}
		start, end := 0, len(panel.Lines)
		if len(panel.Lines) > height {
			start, end = panel.SelectionStart, panel.SelectionEnd
		}
		lines := make([]string, 0, len(panel.Lines)+1)
		lines = append(lines, panel.Lines...)
		// Rust #45770 only reserves the trailing blank row when the panel
		// overflows the viewport.
		if !centerSummary && len(panel.Lines) > height {
			lines = append(lines, "")
		}
		return lines, start, end
	}
	groups := [analytics.AnalyticsSectionCount]int{}
	for index := range d.sections {
		groups[index] = d.sections[index].Group
	}
	chats, chatsReady := d.chats.Ready()
	var loadedChats *analytics.AnalyticsChats
	if chatsReady {
		loadedChats = &chats
	}
	chatMessage, _ := d.chats.Message()
	tasks, tasksReady := d.tasks.Ready()
	var loadedTasks *analytics.AnalyticsTaskChats
	if tasksReady {
		loadedTasks = &tasks
	}
	taskMessage, _ := d.tasks.Message()
	profile, profileMessage, profileReady, planPtr := d.summaryProfile()
	lines, start, end := analytics.AnalyticsDashboardLines(analytics.AnalyticsDashboardOptions{
		Width:               width,
		Height:              height,
		Visible:             d.visibleSections(),
		Section:             d.section,
		Ranges:              d.ranges,
		Groups:              groups,
		Business:            d.business(),
		ConsumerAttribution: d.consumerAttribution(),
		TokenModel:          d.tokenModel,
		Chats:               loadedChats,
		ChatMessage:         chatMessage,
		ChatReady:           chatsReady,
		Tasks:               loadedTasks,
		TaskMessage:         taskMessage,
		TaskReady:           tasksReady,
		ChatMetric:          d.chatMetric,
		Profile:             profile,
		ProfileMessage:      profileMessage,
		ProfileReady:        profileReady,
		Plan:                planPtr,
		Theme:               d.theme,
		EndDate:             d.endDate,
		History:             d.historyLines,
	})
	return lines, start, end
}

func (d *analyticsDashboard) panel(section analytics.AnalyticsSection, width, chartHeight int) analytics.AnalyticsPanel {
	history, ok := d.sections[section].History.Ready()
	var loaded *analytics.AccountAnalyticsHistory
	if ok {
		loaded = &history
	}
	chats, chatsReady := d.chats.Ready()
	var loadedChats *analytics.AnalyticsChats
	if chatsReady {
		loadedChats = &chats
	}
	chatMessage, _ := d.chats.Message()
	tasks, tasksReady := d.tasks.Ready()
	var loadedTasks *analytics.AnalyticsTaskChats
	if tasksReady {
		loadedTasks = &tasks
	}
	taskMessage, _ := d.tasks.Message()
	profile, profileMessage, profileReady, planPtr := d.summaryProfile()
	return analytics.AnalyticsPanelLines(analytics.AnalyticsPanelOptions{
		Section:              section,
		State:                d.sections[section],
		History:              loaded,
		Chats:                loadedChats,
		ChatMessage:          chatMessage,
		ChatReady:            chatsReady,
		Tasks:                loadedTasks,
		TaskMessage:          taskMessage,
		TaskReady:            tasksReady,
		ChatMetric:           d.chatMetric,
		Profile:              profile,
		ProfileMessage:       profileMessage,
		ProfileReady:         profileReady,
		Plan:                 planPtr,
		ShowZeroCreditGroups: d.showZeroCreditGroups,
		ViewportHeight:       d.viewportHeight,
		Width:                width,
		ChartHeight:          chartHeight,
		RangeIndex:           d.ranges[section.RangeGroup(d.business())],
		EndDate:              d.endDate,
		Business:             d.business(),
		ConsumerAttribution:  d.consumerAttribution(),
		ModelName:            d.modelName,
		Theme:                d.theme,
	})
}

func (d *analyticsDashboard) historyLines(section analytics.AnalyticsSection, width, height int) []string {
	history, ok := d.sections[section].History.Ready()
	var loaded *analytics.AccountAnalyticsHistory
	if ok {
		loaded = &history
	}
	lines, _ := analytics.AnalyticsHistoryLines(section, d.sections[section], loaded, analytics.AnalyticsHistoryOptions{
		Width:               width,
		ChartHeight:         height,
		ViewportHeight:      d.viewportHeight,
		RangeIndex:          d.ranges[section.RangeGroup(d.business())],
		EndDate:             d.endDate,
		Zoomed:              d.zoomed,
		Business:            d.business(),
		ConsumerAttribution: d.consumerAttribution(),
		ModelName:           d.modelName,
	})
	return lines
}

// analyticsUpdatedLabel is the current report's update time. It lives in the
// fixed controls area above every report and in the help screen (Rust
// analytics/controls.rs render_controls and chrome.rs help_lines, #46863).
func (d *analyticsDashboard) analyticsUpdatedLabel() string {
	if d == nil || !d.section.HasReport() {
		return ""
	}
	history, ok := d.sections[d.section].History.Ready()
	if !ok || history.UpdatedAt == nil || *history.UpdatedAt <= 0 {
		return ""
	}
	return time.Unix(*history.UpdatedAt, 0).UTC().Format("Jan 2 15:04") + " UTC"
}

// analyticsStyledControls dims the footer and highlights each key token.
func analyticsStyledControls(theme styles.Styles, line string) string {
	reset := theme.ExecCell.Reset
	if reset == "" {
		return line
	}
	dim := theme.Chat.DimText
	keyStyle := analyticsFooterKeyStyle() + theme.ExecCell.Bold
	parts := strings.Split(line, " · ")
	var builder strings.Builder
	for index, part := range parts {
		if index > 0 {
			builder.WriteString(" · ")
		}
		keys, description := part, ""
		if at := strings.LastIndex(part, " "); at >= 0 {
			keys = part[:at]
			description = part[at:]
		}
		builder.WriteString(keyStyle)
		builder.WriteString(keys)
		builder.WriteString(reset)
		builder.WriteString(dim)
		builder.WriteString(description)
		builder.WriteString(reset)
	}
	return dim + builder.String() + reset
}

// analyticsFooterKeyStyle keeps footer shortcut keys readable on light
// terminals while leaving dark terminals on their default foreground
// (Rust style::footer_hint_key_style, #45772).
func analyticsFooterKeyStyle() string {
	if background, known := codextui.DefaultBackgroundRGB(); known && codextui.IsLight(background) {
		return "\x1b[30m"
	}
	return ""
}

// analyticsTruncateStyled truncates a styled line by its visible width so the
// escape sequences survive the cut.
func analyticsTruncateStyled(line string, width int) string {
	if width <= 0 || analytics.AnalyticsDisplayWidth(utils.StripANSI(line)) <= width {
		return line
	}
	var builder strings.Builder
	used := 0
	for index := 0; index < len(line); {
		if line[index] == 0x1b {
			end := index + 1
			if end < len(line) && line[end] == '[' {
				end++
				for end < len(line) && line[end] != 'm' {
					end++
				}
				if end < len(line) {
					end++
				}
			}
			builder.WriteString(line[index:end])
			index = end
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		charWidth := analytics.AnalyticsDisplayWidth(string(r))
		if used+charWidth > width {
			break
		}
		builder.WriteString(line[index : index+size])
		used += charWidth
		index += size
	}
	return builder.String()
}

// analyticsHintLabel renders the primary list binding for an action.
func analyticsHintLabel(m *Model, action string) string {
	if m == nil {
		return ""
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
	if len(bindings) == 0 {
		return ""
	}
	normalized, err := codextui.NormalizeKeybindingSpec(bindings[0])
	if err != nil {
		return bindings[0]
	}
	return analyticsKeyLabel(normalized)
}

func analyticsKeyLabel(spec string) string {
	switch spec {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "page-up":
		return "pgup"
	case "page-down":
		return "pgdn"
	case "space":
		return "space"
	}
	return strings.ReplaceAll(spec, "-", "+")
}
