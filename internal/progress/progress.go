package progress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"harness/internal/events"
)

const (
	NovelAction       = "novel_action"
	ResultRepetition  = "result_repetition"
	RepeatedTimeouts  = "repeated_timeouts"
	ModelSaysStuck    = "model_says_stuck"
	ErrorSuccessRatio = "error_success_ratio"
	BaselineDeviation = "baseline_deviation"
	AuxProgress       = "aux_progress"
)

var DetectorNames = []string{NovelAction, ResultRepetition, RepeatedTimeouts, ModelSaysStuck, ErrorSuccessRatio, BaselineDeviation, AuxProgress}

type Thresholds struct {
	NoNovelTurns       int     `json:"no_novel_turns"`
	RepeatCount        int     `json:"repeat_count"`
	RepeatWindow       int     `json:"repeat_window"`
	TimeoutCount       int     `json:"timeout_count"`
	TimeoutWindow      int     `json:"timeout_window"`
	StuckAdmissions    int     `json:"stuck_admissions"`
	ErrorWindow        int     `json:"error_window"`
	ErrorRatio         float64 `json:"error_ratio"`
	BaselineTurns      int     `json:"baseline_turns"`
	DurationMultiplier float64 `json:"duration_multiplier"`
	AuxEveryTurns      int     `json:"aux_every_turns"`
}

var GuessedThresholds = Thresholds{5, 5, 8, 3, 5, 2, 10, .60, 3, 2, 10}

type Record struct {
	Detector  string         `json:"detector"`
	Armed     bool           `json:"armed"`
	Available bool           `json:"available"`
	WouldFire bool           `json:"would_fire"`
	Guess     bool           `json:"threshold_is_guess"`
	Threshold any            `json:"threshold"`
	Values    map[string]any `json:"values"`
}

type runState struct {
	armed          map[string]bool
	firstFire      map[string]int
	seenCalls      map[string]bool
	seenReads      map[string]bool
	novelTurns     map[int]bool
	turnTools      map[int]map[string]bool
	results        []string
	timeouts       []bool
	errors         []bool
	reasoningKnown bool
	reasoningOn    bool
	reasoning      string
	durations      map[int]float64
	auxKnown       bool
	auxStuck       bool
	maxTurn        int
}

type Manager struct {
	bus  *events.Bus
	mu   sync.Mutex
	runs map[string]*runState
	stop func()
}

func New(bus *events.Bus) *Manager { return &Manager{bus: bus, runs: map[string]*runState{}} }

func ArmedSet(hasAux bool) []string {
	result := append([]string(nil), DetectorNames[:6]...)
	if hasAux {
		result = append(result, AuxProgress)
	}
	return result
}

func (m *Manager) Start() {
	ch, stop := m.bus.Subscribe()
	m.stop = stop
	go func() {
		for event := range ch {
			m.Consume(event)
		}
	}()
}

func (m *Manager) Close() {
	if m.stop != nil {
		m.stop()
	}
}

func key(event events.Event) string { return event.SessionID + "\x00" + event.RunID }

func dataMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if value, ok := value.(map[string]any); ok {
		return value
	}
	encoded, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(encoded, &result)
	return result
}

func intValue(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		result, _ := strconv.Atoi(value.String())
		return result
	}
	return 0
}

func boolValue(value any) bool { result, _ := value.(bool); return result }

func stable(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (m *Manager) Consume(event events.Event) {
	if event.SessionID == "" || event.RunID == "" {
		return
	}
	k := key(event)
	data := dataMap(event.Data)
	m.mu.Lock()
	state := m.runs[k]
	if event.Type == events.RunStarted {
		state = newRunState(sliceStrings(data["armed_detectors"]))
		m.runs[k] = state
	}
	if state == nil {
		m.mu.Unlock()
		return
	}
	if event.Type == events.RunStopped {
		delete(m.runs, k)
		recordFirstFires(state)
		records := Evaluate(state)
		m.mu.Unlock()
		for _, record := range records {
			m.bus.Publish(events.New(events.ProgressShadow, event.SessionID, event.RunID, record))
		}
		return
	}
	observe(state, event)
	recordFirstFires(state)
	m.mu.Unlock()
}

func newRunState(armed []string) *runState {
	state := &runState{armed: map[string]bool{}, firstFire: map[string]int{}, seenCalls: map[string]bool{}, seenReads: map[string]bool{}, novelTurns: map[int]bool{}, turnTools: map[int]map[string]bool{}, durations: map[int]float64{}}
	for _, value := range armed {
		state.armed[value] = true
	}
	return state
}

func observe(state *runState, event events.Event) {
	data := dataMap(event.Data)
	turn := intValue(data["turn"])
	if turn > state.maxTurn {
		state.maxTurn = turn
	}
	switch event.Type {
	case events.ModelRequest:
		params := dataMap(data["params"])
		reasoning := dataMap(params["reasoning"])
		state.reasoningKnown, state.reasoningOn = true, boolValue(reasoning["enabled"])
	case events.ModelDelta:
		if data["kind"] == "reasoning" {
			state.reasoning += strings.ToLower(stringValue(data["text"]))
		}
	case events.ToolCallEvent:
		name := stringValue(data["name"])
		args := data["args"]
		signature := name + "\x00" + stable(args)
		novel := !state.seenCalls[signature]
		state.seenCalls[signature] = true
		if name == "read_file" {
			path := strings.ToLower(strings.ReplaceAll(stringValue(dataMap(args)["path"]), "\\", "/"))
			novel = path != "" && !state.seenReads[path]
			state.seenReads[path] = true
		}
		if name == "write_file" || name == "edit_file" {
			novel = true
		}
		if novel {
			state.novelTurns[turn] = true
		}
		if state.turnTools[turn] == nil {
			state.turnTools[turn] = map[string]bool{}
		}
		state.turnTools[turn][name] = true
	case events.ToolResult:
		preview := stringValue(data["preview"])
		state.results = append(state.results, NormalizeResult(preview))
		state.timeouts = append(state.timeouts, regexp.MustCompile(`(?i)timed? out|timeout|deadline exceeded`).MatchString(preview))
		state.errors = append(state.errors, !boolValue(data["ok"]))
	case events.ModelResponse:
		state.durations[turn] = floatValue(data["duration_ms"])
	case events.ProgressAux:
		state.auxKnown = boolValue(data["available"])
		state.auxStuck = strings.EqualFold(stringValue(data["verdict"]), "stuck")
	}
}

// EvaluateEvents runs the same shadow evaluators used by live event consumers.
// Callers remain responsible for selecting exactly one run from a shared tape.
func EvaluateEvents(stream []events.Event, armed []string) []Record {
	state := newRunState(armed)
	for _, event := range stream {
		if event.Type == events.RunStarted || event.Type == events.RunStopped {
			continue
		}
		observe(state, event)
		recordFirstFires(state)
	}
	return Evaluate(state)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(toString(value)), "\x00", ""))
}
func toString(value any) string {
	if value, ok := value.(string); ok {
		return value
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
func floatValue(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return 0
}
func sliceStrings(value any) []string {
	raw, _ := value.([]string)
	if raw != nil {
		return raw
	}
	values, _ := value.([]any)
	result := []string{}
	for _, item := range values {
		result = append(result, stringValue(item))
	}
	return result
}

var (
	rfcTime       = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ][0-9:.+-]+Z?\b`)
	clockTime     = regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}(?:\.\d+)?\b`)
	pidValue      = regexp.MustCompile(`(?i)\b(pid|process(?: id)?)\s*[:=#]?\s*\d+\b`)
	durationValue = regexp.MustCompile(`(?i)\b(?:elapsed|duration|took|time)\s*[:=]?\s*\d+(?:\.\d+)?\s*(?:ms|s|sec(?:onds?)?|m|min(?:utes?)?)\b`)
	offsetValue   = regexp.MustCompile(`(?i)\b(?:next_)?offset\s*[:=]\s*\d+\b|\bbytes\s+\d+[–-]\d+\b`)
	windowsTemp   = regexp.MustCompile(`(?i)[A-Z]:\\[^\r\n"']*\\(?:AppData\\Local\\Temp|Temp)\\[^\s"']+`)
	unixTemp      = regexp.MustCompile(`(?i)/(?:tmp|var/tmp)/[^\s"']+`)
)

func NormalizeResult(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = rfcTime.ReplaceAllString(value, "<timestamp>")
	value = clockTime.ReplaceAllString(value, "<time>")
	value = pidValue.ReplaceAllString(value, "$1=<pid>")
	value = durationValue.ReplaceAllString(value, "elapsed=<duration>")
	value = offsetValue.ReplaceAllString(value, "offset=<byte-offset>")
	value = windowsTemp.ReplaceAllString(value, "<temp-path>")
	value = unixTemp.ReplaceAllString(value, "<temp-path>")
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func ResultHash(value string) string {
	sum := sha256.Sum256([]byte(NormalizeResult(value)))
	return hex.EncodeToString(sum[:])
}

func Evaluate(state *runState) []Record {
	records := evaluateCurrent(state)
	for index := range records {
		if first := state.firstFire[records[index].Detector]; first > 0 {
			records[index].WouldFire = true
			records[index].Values["first_fire_turn"] = first
		}
	}
	return records
}

func recordFirstFires(state *runState) {
	for _, record := range evaluateCurrent(state) {
		if record.Available && record.WouldFire && state.firstFire[record.Detector] == 0 {
			state.firstFire[record.Detector] = max(1, state.maxTurn)
		}
	}
}

func evaluateCurrent(state *runState) []Record {
	t := GuessedThresholds
	armed := func(name string) bool { return state.armed[name] }
	noNovel := 0
	for turn := state.maxTurn; turn > 0 && !state.novelTurns[turn]; turn-- {
		noNovel++
	}
	windowResults := tailStrings(state.results, t.RepeatWindow)
	counts := map[string]int{}
	maxRepeat := 0
	for _, result := range windowResults {
		counts[result]++
		if counts[result] > maxRepeat {
			maxRepeat = counts[result]
		}
	}
	timeouts := countTrue(tailBools(state.timeouts, t.TimeoutWindow))
	errors := countTrue(tailBools(state.errors, t.ErrorWindow))
	errorWindow := min(len(state.errors), t.ErrorWindow)
	ratio := 0.0
	if errorWindow > 0 {
		ratio = float64(errors) / float64(errorWindow)
	}
	stuckCount := 0
	lower := state.reasoning
	for _, phrase := range []string{"i'm stuck", "i am stuck", "tried several", "doesn't seem to be working", "not making progress"} {
		stuckCount += strings.Count(lower, phrase)
	}
	turns := sortedTurns(state.durations)
	baseline, recent := averageTurns(state.durations, headInts(turns, t.BaselineTurns)), averageTurns(state.durations, tailInts(turns, t.BaselineTurns))
	diversity := map[string]bool{}
	for _, turn := range tailInts(turns, t.BaselineTurns) {
		for name := range state.turnTools[turn] {
			diversity[name] = true
		}
	}
	deviation := len(turns) >= 2*t.BaselineTurns && baseline > 0 && recent >= baseline*t.DurationMultiplier && len(diversity) <= 1
	records := []Record{
		{NovelAction, armed(NovelAction), true, noNovel >= t.NoNovelTurns, true, map[string]any{"consecutive_turns": t.NoNovelTurns}, map[string]any{"consecutive_without_novel_action": noNovel}},
		{ResultRepetition, armed(ResultRepetition), true, maxRepeat >= t.RepeatCount, true, map[string]any{"count": t.RepeatCount, "window": t.RepeatWindow}, map[string]any{"largest_repeat": maxRepeat, "observed": len(windowResults)}},
		{RepeatedTimeouts, armed(RepeatedTimeouts), true, timeouts >= t.TimeoutCount, true, map[string]any{"count": t.TimeoutCount, "window": t.TimeoutWindow}, map[string]any{"timeouts": timeouts, "observed": min(len(state.timeouts), t.TimeoutWindow)}},
		{ModelSaysStuck, armed(ModelSaysStuck), state.reasoningKnown && state.reasoningOn, stuckCount >= t.StuckAdmissions, true, map[string]any{"admissions": t.StuckAdmissions}, map[string]any{"admissions": stuckCount, "reasoning_enabled": state.reasoningOn}},
		{ErrorSuccessRatio, armed(ErrorSuccessRatio), true, errorWindow >= t.ErrorWindow && ratio >= t.ErrorRatio, true, map[string]any{"ratio": t.ErrorRatio, "window": t.ErrorWindow}, map[string]any{"errors": errors, "observed": errorWindow, "ratio": ratio}},
		{BaselineDeviation, armed(BaselineDeviation), len(turns) >= 2*t.BaselineTurns, deviation, true, map[string]any{"baseline_turns": t.BaselineTurns, "duration_multiplier": t.DurationMultiplier, "recent_tool_diversity_max": 1}, map[string]any{"baseline_duration_ms": baseline, "recent_duration_ms": recent, "recent_tool_diversity": len(diversity)}},
		{AuxProgress, armed(AuxProgress), armed(AuxProgress) && state.auxKnown, state.auxKnown && state.auxStuck, true, map[string]any{"every_turns": t.AuxEveryTurns, "soft": true}, map[string]any{"verdict_available": state.auxKnown, "verdict_stuck": state.auxStuck}},
	}
	return records
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func countTrue(values []bool) int {
	n := 0
	for _, v := range values {
		if v {
			n++
		}
	}
	return n
}
func tailStrings(values []string, n int) []string {
	if len(values) > n {
		return values[len(values)-n:]
	}
	return values
}
func tailBools(values []bool, n int) []bool {
	if len(values) > n {
		return values[len(values)-n:]
	}
	return values
}
func sortedTurns(values map[int]float64) []int {
	result := make([]int, 0, len(values))
	for turn := range values {
		result = append(result, turn)
	}
	sort.Ints(result)
	return result
}
func headInts(values []int, n int) []int {
	if len(values) > n {
		return values[:n]
	}
	return values
}
func tailInts(values []int, n int) []int {
	if len(values) > n {
		return values[len(values)-n:]
	}
	return values
}
func averageTurns(values map[int]float64, turns []int) float64 {
	if len(turns) == 0 {
		return 0
	}
	total := 0.0
	for _, turn := range turns {
		total += values[turn]
	}
	return total / float64(len(turns))
}
