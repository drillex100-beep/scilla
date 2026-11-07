// Package nodeheal heals nodes that stay down: it watches the same health state the
// liveness of nodesync keeps (node_state "health/<id>"), and when a remote node has been
// down for the policy's sustain time it asks the node's host to restart the containers
// (the same hostchan restart the admin's button uses), then watches for the recovery the
// hysteresis reports. A failed incident is retried after the cooldown, and enough failed
// incidents open the circuit: the node is left alone until an admin resets it.
//
// The decision itself is pure (advance) and tested without a database; the engine only
// reads state, performs the restart and writes what happened back: the heal state in
// node_state "heal/<id>", an audit entry and a node.heal.* event for every step.
package nodeheal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"scilla/internal/nodeapi"
	"scilla/internal/panel/audit"
	"scilla/internal/panel/events"
	"scilla/internal/panel/events/feed"
	"scilla/internal/panel/settings"
	"scilla/internal/panel/store"
	"scilla/internal/panel/store/db"
)

// KeyPolicy is the setting that keeps the healing policy.
const KeyPolicy = "node_heal"

const (
	stateHealPrefix   = "heal/"
	defaultInterval   = 15 * time.Second
	retryAfterAfter   = 2 * time.Minute
	historyKeep       = 10
	stateWriteFailLog = "node heal: state is not kept"
)

// HealPolicy is how the engine treats a down node. Disabled by default: the admin turns it on.
type HealPolicy struct {
	Enabled          bool  `json:"enabled"`
	DownForSec       int64 `json:"down_for_sec"`       // continuous downtime before the first restart
	CooldownSec      int64 `json:"cooldown_sec"`       // pause between incidents of one node
	MaxAttempts      int   `json:"max_attempts"`       // restarts per incident
	VerifySec        int64 `json:"verify_sec"`         // how long recovery may take after a restart
	BreakerMax       int   `json:"breaker_max"`        // failed incidents in the window that open the circuit
	BreakerWindowSec int64 `json:"breaker_window_sec"` // how far back failed incidents count
}

// DefaultPolicy is what a panel that never touched the setting gets.
func DefaultPolicy() HealPolicy {
	return HealPolicy{
		Enabled:          false,
		DownForSec:       180,
		CooldownSec:      1800,
		MaxAttempts:      2,
		VerifySec:        300,
		BreakerMax:       3,
		BreakerWindowSec: 86400,
	}
}

// Normalize returns the policy with defaults filled in; the API stores what it answers.
func (p HealPolicy) Normalize() HealPolicy { return p.withDefaults() }

// withDefaults fills what a stored policy leaves out and clamps what is out of range.
func (p HealPolicy) withDefaults() HealPolicy {
	d := DefaultPolicy()
	if p.DownForSec <= 0 {
		p.DownForSec = d.DownForSec
	}
	if p.CooldownSec <= 0 {
		p.CooldownSec = d.CooldownSec
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = d.MaxAttempts
	}
	if p.VerifySec <= 0 {
		p.VerifySec = d.VerifySec
	}
	if p.BreakerMax <= 0 {
		p.BreakerMax = d.BreakerMax
	}
	if p.BreakerWindowSec <= 0 {
		p.BreakerWindowSec = d.BreakerWindowSec
	}
	return p
}

// Phase is where the node's healing stands.
type Phase string

const (
	PhaseWatching    Phase = "watching"     // down, waiting for the sustain time or the cooldown
	PhaseActive      Phase = "active"       // restarts asked, waiting for the recovery
	PhaseCircuitOpen Phase = "circuit_open" // too many failed incidents: left alone until reset
)

// HealIncident is one closed healing story, kept in the state's history.
type HealIncident struct {
	DownAt    int64  `json:"down_at"`
	StartedAt int64  `json:"started_at"`
	Attempts  int    `json:"attempts"`
	Result    string `json:"result"` // "healed" or "failed"
	EndAt     int64  `json:"end_at"`
}

// HealState is what node_state "heal/<id>" keeps.
type HealState struct {
	Phase        Phase          `json:"phase"`
	HealIncident int64          `json:"incident"`     // unix seconds the current downtime began; 0: none
	Attempts     int            `json:"attempts"`     // restarts asked for the current incident
	LastAction   int64          `json:"last_action"`  // unix seconds of the last accepted restart
	VerifyUntil  int64          `json:"verify_until"` // recovery is expected before this
	RetryAfter   int64          `json:"retry_after"`  // a refused restart is not asked again before this
	Failures     []int64        `json:"failures"`     // end times of recent failed incidents
	History      []HealIncident `json:"history"`
	UpdatedAt    int64          `json:"updated_at"`
}

// healKey is the node_state key of a node's healing state.
func healKey(id int64) string { return stateHealPrefix + strconv.FormatInt(id, 10) }

// decision is what advance concluded: the state to keep and what the engine must do.
type decision struct {
	state   HealState
	restart bool // ask the host for a restart
	healed  *HealIncident
	failed  *HealIncident
	circuit bool
}

// advance is the whole brain of the engine, pure so it tests without I/O. healthOK says
// the node answers (the hysteresis already said so); downSince is when the failures began.
func advance(st HealState, healthOK bool, downSince int64, pol HealPolicy, now time.Time) decision {
	pol = pol.withDefaults()
	nowSec := now.Unix()
	d := decision{state: st}

	if !healthOK {
		if st.HealIncident == 0 {
			// A new incident begins; the breaker's memory stays.
			d.state = HealState{Phase: PhaseWatching, HealIncident: downSince, Failures: st.Failures, History: st.History, UpdatedAt: nowSec}
			return d
		}
		if st.Phase == PhaseCircuitOpen || !pol.Enabled {
			d.state.UpdatedAt = nowSec
			return d
		}
		if nowSec-st.HealIncident < pol.DownForSec || st.RetryAfter > nowSec {
			d.state.UpdatedAt = nowSec
			return d
		}
		if st.Attempts == 0 {
			// Between incidents the cooldown applies; inside one the verify clock does.
			if st.LastAction > 0 && nowSec-st.LastAction < pol.CooldownSec {
				d.state.UpdatedAt = nowSec
				return d
			}
			return d.askRestart(nowSec, pol)
		}
		if nowSec < st.VerifyUntil {
			d.state.UpdatedAt = nowSec
			return d
		}
		if st.Attempts < pol.MaxAttempts {
			return d.askRestart(nowSec, pol)
		}
		// The incident is lost: record it, count it for the breaker, and either open the
		// circuit or go back to watching (the cooldown keeps the next incident waiting).
		inc := HealIncident{DownAt: st.HealIncident, StartedAt: st.LastAction, Attempts: st.Attempts, Result: "failed", EndAt: nowSec}
		failures := append(append([]int64{}, st.Failures...), nowSec)
		cut := nowSec - pol.BreakerWindowSec
		kept := failures[:0]
		for _, f := range failures {
			if f >= cut {
				kept = append(kept, f)
			}
		}
		phase := PhaseWatching
		circuit := false
		if len(kept) >= pol.BreakerMax {
			phase = PhaseCircuitOpen
			circuit = true
		}
		d.state = HealState{
			Phase:      phase,
			Failures:   kept,
			History:    pushHistory(st.History, inc),
			LastAction: nowSec,
			UpdatedAt:  nowSec,
		}
		d.failed = &inc
		d.circuit = circuit
		return d
	}

	// The node answers.
	if st.Phase == PhaseActive && st.HealIncident != 0 {
		inc := HealIncident{DownAt: st.HealIncident, StartedAt: st.LastAction, Attempts: st.Attempts, Result: "healed", EndAt: nowSec}
		d.state = HealState{
			Phase:      PhaseWatching,
			Failures:   st.Failures,
			History:    pushHistory(st.History, inc),
			LastAction: st.LastAction,
			UpdatedAt:  nowSec,
		}
		d.healed = &inc
		return d
	}
	if st.HealIncident != 0 || st.Phase == PhaseCircuitOpen {
		// Recovered before any restart, or while the circuit was open: the incident is
		// over without a story; the circuit closes so future incidents can be healed.
		d.state = HealState{Phase: PhaseWatching, Failures: st.Failures, History: st.History, LastAction: st.LastAction, UpdatedAt: nowSec}
	}
	return d
}

// askRestart moves the decision to asking for a restart.
func (d *decision) askRestart(nowSec int64, pol HealPolicy) decision {
	d.restart = true
	d.state.Phase = PhaseActive
	d.state.Attempts++
	d.state.LastAction = nowSec
	d.state.VerifyUntil = nowSec + pol.VerifySec
	d.state.UpdatedAt = nowSec
	return *d
}

// pushHistory prepends an incident and keeps the newest historyKeep.
func pushHistory(h []HealIncident, inc HealIncident) []HealIncident {
	out := append([]HealIncident{inc}, h...)
	if len(out) > historyKeep {
		out = out[:historyKeep]
	}
	return out
}

// Restarter asks a node's host to restart its containers; nodesync.Manager satisfies it.
type Restarter interface {
	HostRestart(ctx context.Context, id int64) (nodeapi.RestartAccepted, error)
}

// Alerter tells the admin's alerts (Telegram, the group, Discord); infraalerts.Monitor
// satisfies it. A nil alerter keeps the engine silent.
type Alerter interface {
	Notify(ctx context.Context, key, text string)
}

// Engine watches the nodes and heals the ones that stay down.
type Engine struct {
	st        *store.Store
	set       *settings.Settings
	restarter Restarter
	log       *slog.Logger
	now       func() time.Time
	interval  time.Duration
	alerter   Alerter
	lang      func(ctx context.Context) string
}

// New builds the engine. A nil restarter (nodes disabled) makes it a no-op.
func New(st *store.Store, set *settings.Settings, restarter Restarter, log *slog.Logger, now func() time.Time) *Engine {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{st: st, set: set, restarter: restarter, log: log, now: now, interval: defaultInterval}
}

// SetAlerter makes the engine tell the admin's alerts about every step of an incident;
// the language is the admin's Telegram one (tgate.Gateway.Lang). nil alerter: silent.
func (e *Engine) SetAlerter(a Alerter, lang func(ctx context.Context) string) {
	e.alerter = a
	if lang == nil {
		lang = func(context.Context) string { return "ru" }
	}
	e.lang = lang
}

// Run ticks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	if e.restarter == nil {
		return
	}
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

// HealPolicy returns the healing policy with defaults filled in.
func (e *Engine) HealPolicy(ctx context.Context) HealPolicy {
	p, _, err := settings.GetOver[HealPolicy](ctx, e.set, KeyPolicy, DefaultPolicy())
	if err != nil {
		e.log.Warn("node heal: policy is unreadable, defaults are used", "err", err)
		return DefaultPolicy()
	}
	return p.withDefaults()
}

// HealNodeView is one remote node's healing state for the API.
type HealNodeView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	HealState HealState `json:"state"`
}

// Snapshot lists remote nodes with their healing state (zero state when never healed).
func (e *Engine) Snapshot(ctx context.Context) ([]HealNodeView, error) {
	nodes, err := e.st.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]HealNodeView, 0, len(nodes))
	for _, n := range nodes {
		if n.Address == "" {
			continue // the panel's own node restarts with the panel
		}
		v := HealNodeView{ID: n.ID, Name: n.Name}
		if raw, err := e.st.Q.GetNodeState(ctx, healKey(n.ID)); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &v.HealState)
		}
		out = append(out, v)
	}
	return out, nil
}

// Reset closes an open circuit of a node; an admin asks for it after looking at the node.
func (e *Engine) Reset(ctx context.Context, id int64) (bool, error) {
	key := healKey(id)
	raw, err := e.st.Q.GetNodeState(ctx, key)
	if err != nil || raw == "" {
		return false, nil
	}
	var st HealState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return false, err
	}
	if st.Phase != PhaseCircuitOpen {
		return false, nil
	}
	st.Phase = PhaseWatching
	st.Failures = nil
	st.UpdatedAt = e.now().Unix()
	return true, e.st.Q.SetNodeState(ctx, db.SetNodeStateParams{Key: key, Value: mustJSON(st)})
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// tick is one round over all remote nodes.
func (e *Engine) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	pol := e.HealPolicy(ctx)
	nodes, err := e.st.Q.ListNodes(ctx)
	if err != nil {
		e.log.Warn("node heal: nodes are unreadable", "err", err)
		return
	}
	for _, n := range nodes {
		if n.Address == "" {
			continue
		}
		e.tickNode(ctx, pol, n)
	}
}

// tickNode advances one node by its current health and keeps what was decided.
func (e *Engine) tickNode(ctx context.Context, pol HealPolicy, n db.Node) {
	health, err := e.st.Q.GetNodeState(ctx, "health/"+strconv.FormatInt(n.ID, 10))
	if err != nil {
		return // never told: nothing to heal
	}
	ok, downSince := parseHealth(health)
	key := healKey(n.ID)
	raw, err := e.st.Q.GetNodeState(ctx, key)
	if err != nil {
		return
	}
	var st HealState
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			e.log.Warn(stateWriteFailLog, "node", n.ID, "err", err)
			return
		}
	}
	d := advance(st, ok, downSince, pol, e.now())
	if d.restart {
		e.doRestart(ctx, n, d)
		return
	}
	e.finish(ctx, n, d)
}

// parseHealth reads the liveness value: "up", "down:<unix>", or "" (never told, healthy).
func parseHealth(v string) (ok bool, downSince int64) {
	if strings.HasPrefix(v, "down:") {
		since, err := strconv.ParseInt(strings.TrimPrefix(v, "down:"), 10, 64)
		if err == nil {
			return false, since
		}
	}
	return true, 0
}

// doRestart asks the host and only then keeps the decision; a refusal pauses the node
// briefly instead of burning attempts.
func (e *Engine) doRestart(ctx context.Context, n db.Node, d decision) {
	acc, err := e.restarter.HostRestart(ctx, n.ID)
	now := e.now()
	if err != nil {
		e.log.Warn("node heal: restart is refused", "node", n.ID, "err", err)
		d.state.Attempts--
		d.state.LastAction = 0
		d.state.Phase = PhaseActive
		d.state.VerifyUntil = now.Unix()
		d.state.RetryAfter = now.Add(retryAfterAfter).Unix()
		d.state.UpdatedAt = now.Unix()
		e.finish(ctx, n, d)
		return
	}
	e.writeAudit(ctx, "node_heal.restart", n, map[string]any{"attempt": d.state.Attempts, "request": acc.ID})
	e.emit(ctx, events.NodeHealStarted, events.Heal{NodeID: n.ID, Name: n.Name, Attempts: d.state.Attempts})
	e.notifyStarted(ctx, n, d.state.Attempts)
	e.finish(ctx, n, d)
}

// finish keeps the decided state and tells what the incident came to, if it closed.
func (e *Engine) finish(ctx context.Context, n db.Node, d decision) {
	if err := e.writeState(ctx, healKey(n.ID), d.state); err != nil {
		e.log.Warn(stateWriteFailLog, "node", n.ID, "err", err)
	}
	switch {
	case d.healed != nil:
		dur := d.healed.EndAt - d.healed.DownAt
		e.writeAudit(ctx, "node_heal.healed", n, map[string]any{"duration_sec": dur, "attempts": d.healed.Attempts})
		e.emit(ctx, events.NodeHealed, events.Heal{NodeID: n.ID, Name: n.Name, Attempts: d.healed.Attempts, DurationSec: dur})
		e.notifyHealed(ctx, n, dur, d.healed.Attempts)
	case d.failed != nil:
		details := map[string]any{"attempts": d.failed.Attempts, "circuit_open": d.circuit}
		e.writeAudit(ctx, "node_heal.failed", n, details)
		e.emit(ctx, events.NodeHealFailed, events.Heal{NodeID: n.ID, Name: n.Name, Attempts: d.failed.Attempts, CircuitOpen: d.circuit})
		e.notifyFailed(ctx, n, d.failed.Attempts, d.circuit)
	}
}

func (e *Engine) writeState(ctx context.Context, key string, st HealState) error {
	st.UpdatedAt = e.now().Unix()
	return e.st.Q.SetNodeState(ctx, db.SetNodeStateParams{Key: key, Value: mustJSON(st)})
}

func (e *Engine) writeAudit(ctx context.Context, action string, n db.Node, details map[string]any) {
	details["name"] = n.Name
	if err := e.st.Tx(ctx, func(q *db.Queries) error {
		return audit.Write(ctx, q, e.now(), audit.Entry{
			AdminID:    0, // the engine acts as the system
			Action:     action,
			TargetType: "node",
			TargetID:   strconv.FormatInt(n.ID, 10),
			Details:    details,
		})
	}); err != nil {
		e.log.Warn("node heal: audit is not written", "action", action, "err", err)
	}
}

// notifyStarted tells the admin that the engine started treating a node.
func (e *Engine) notifyStarted(ctx context.Context, n db.Node, attempt int) {
	e.tell(ctx, healText(e.langOf(ctx), healStartedText, n.Name, attempt, 0, false))
}

// notifyHealed tells the admin that the node is back.
func (e *Engine) notifyHealed(ctx context.Context, n db.Node, durSec int64, attempts int) {
	e.tell(ctx, healText(e.langOf(ctx), healHealedText, n.Name, attempts, durSec, false))
}

// notifyFailed tells the admin that the incident is lost, and when the circuit opened.
func (e *Engine) notifyFailed(ctx context.Context, n db.Node, attempts int, circuit bool) {
	e.tell(ctx, healText(e.langOf(ctx), healFailedText, n.Name, attempts, 0, circuit))
}

func (e *Engine) langOf(ctx context.Context) string {
	if e.lang == nil {
		return "ru"
	}
	return e.lang(ctx)
}

func (e *Engine) tell(ctx context.Context, text string) {
	if e.alerter == nil {
		return
	}
	e.alerter.Notify(ctx, "", text)
}

const (
	healStartedText = "Авто-лечение: нода «%s» не отвечает — перезапускаю её контейнеры (попытка %d)."
	healHealedText  = "Авто-лечение: нода «%s» восстановлена за %s (попыток: %d)."
	healFailedText  = "Авто-лечение: нода «%s» не восстановилась после %d попыток.%s"
)

// healText renders one alert in the admin's language; ru is the fallback.
func healText(lang, tmpl, name string, attempts int, durSec int64, circuit bool) string {
	switch tmpl {
	case healStartedText:
		if lang == "en" {
			return fmt.Sprintf("Auto-heal: node %q is not answering — restarting its containers (attempt %d).", name, attempts)
		}
		return fmt.Sprintf(healStartedText, name, attempts)
	case healHealedText:
		if lang == "en" {
			return fmt.Sprintf("Auto-heal: node %q is back after %s (attempts: %d).", name, durText(durSec, lang), attempts)
		}
		return fmt.Sprintf(healHealedText, name, durText(durSec, lang), attempts)
	default:
		if lang == "en" {
			out := fmt.Sprintf("Auto-heal: node %q did not come back after %d attempts.", name, attempts)
			if circuit {
				out += " Auto-heal is off for this node until an admin resets it."
			}
			return out
		}
		out := fmt.Sprintf(healFailedText, name, attempts, "")
		if circuit {
			out += " Авто-лечение отключено для ноды до сброса админом."
		}
		return out
	}
}

// durText renders a duration the way the alerts say it: "2м 15с" / "2m 15s".
func durText(sec int64, lang string) string {
	m, s := sec/60, sec%60
	if lang == "en" {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%dм %dс", m, s)
}

func (e *Engine) emit(ctx context.Context, typ string, h events.Heal) {
	if err := feed.Emit(ctx, e.st.Q, e.now(), typ, 0, 0, h, typ+":"+strconv.FormatInt(h.NodeID, 10)+":"+strconv.FormatInt(e.now().Unix(), 10)); err != nil {
		e.log.Warn("node heal: event is not told", "event", typ, "err", err)
	}
}
