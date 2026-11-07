package nodeheal

import (
	"testing"
	"time"
)

func TestAdvanceLifecycle(t *testing.T) {
	pol := HealPolicy{Enabled: true, DownForSec: 180, CooldownSec: 1800, MaxAttempts: 2, VerifySec: 300, BreakerMax: 3, BreakerWindowSec: 86400}
	now := time.Unix(1_800_000_000, 0)
	down := now.Add(-10 * time.Minute).Unix()

	// New incident starts watching; nothing happens before the sustain time.
	d := advance(HealState{}, false, down, pol, now)
	if d.restart || d.state.HealIncident != down || d.state.Phase != PhaseWatching {
		t.Fatalf("new incident: %+v", d)
	}
	// HealPolicy disabled: no restart ever.
	d = advance(HealState{HealIncident: down}, false, down, HealPolicy{Enabled: false}, now)
	if d.restart {
		t.Fatal("disabled policy restarted")
	}
	// Sustain not reached.
	soon := time.Unix(down+100, 0)
	d = advance(HealState{HealIncident: down, Phase: PhaseWatching}, false, down, pol, soon)
	if d.restart {
		t.Fatal("restarted before DownForSec")
	}
	// Sustain reached: first restart.
	d = advance(HealState{HealIncident: down, Phase: PhaseWatching}, false, down, pol, now)
	if !d.restart || d.state.Attempts != 1 || d.state.Phase != PhaseActive {
		t.Fatalf("first restart: %+v", d)
	}
	if d.state.VerifyUntil != now.Unix()+pol.VerifySec {
		t.Fatalf("verify window: %d", d.state.VerifyUntil)
	}
	// Recovery during verification closes the incident as healed.
	d = advance(d.state, true, 0, pol, now.Add(30*time.Second))
	if d.healed == nil || d.healed.Result != "healed" || d.healed.Attempts != 1 {
		t.Fatalf("healed: %+v", d.healed)
	}
	if d.state.Phase != PhaseWatching || d.state.HealIncident != 0 {
		t.Fatalf("state after heal: %+v", d.state)
	}
}

func TestAdvanceRetriesAndBreaker(t *testing.T) {
	pol := HealPolicy{Enabled: true, DownForSec: 60, CooldownSec: 1800, MaxAttempts: 2, VerifySec: 100, BreakerMax: 2, BreakerWindowSec: 86400}
	now := time.Unix(1_800_000_000, 0)
	down := now.Add(-time.Hour).Unix()

	// Two failed attempts, then the incident fails.
	st := HealState{HealIncident: down, Phase: PhaseActive, Attempts: 2, LastAction: now.Unix() - 200, VerifyUntil: now.Unix() - 1}
	d := advance(st, false, down, pol, now)
	if d.failed == nil || d.failed.Result != "failed" {
		t.Fatalf("expected failed incident: %+v", d)
	}
	if len(d.state.Failures) != 1 || d.state.Phase != PhaseWatching {
		t.Fatalf("state after fail: %+v", d.state)
	}
	// Cooldown blocks the next incident's restart.
	d = advance(HealState{HealIncident: down, Phase: PhaseWatching, LastAction: now.Unix()}, false, down, pol, now.Add(10*time.Second))
	if d.restart {
		t.Fatal("restarted during cooldown")
	}
	// After cooldown a new attempt starts; enough failures open the circuit.
	st2 := HealState{HealIncident: down, Phase: PhaseWatching, Failures: []int64{now.Unix() - 100}, LastAction: now.Unix() - 2000}
	d = advance(st2, false, down, pol, now)
	if !d.restart {
		t.Fatal("no restart after cooldown")
	}
	// Second failed incident opens the circuit (BreakerMax=2).
	st3 := HealState{HealIncident: down, Phase: PhaseActive, Attempts: 2, LastAction: now.Unix(), VerifyUntil: now.Unix() - 1,
		Failures: []int64{now.Unix() - 100}}
	d = advance(st3, false, down, pol, now.Add(200*time.Second))
	if d.failed == nil || !d.circuit || d.state.Phase != PhaseCircuitOpen {
		t.Fatalf("circuit: %+v", d)
	}
	// Circuit stays open while down, closes when the node answers.
	d = advance(d.state, false, down, pol, now.Add(300*time.Second))
	if d.restart {
		t.Fatal("restarted with open circuit")
	}
	d = advance(d.state, true, 0, pol, now.Add(400*time.Second))
	if d.state.Phase != PhaseWatching {
		t.Fatalf("circuit did not close on recovery: %+v", d.state)
	}
}

func TestParseHealth(t *testing.T) {
	ok, _ := parseHealth("up")
	if !ok {
		t.Fatal("up is not ok")
	}
	ok, _ = parseHealth("")
	if !ok {
		t.Fatal("never told is ok")
	}
	ok, since := parseHealth("down:12345")
	if ok || since != 12345 {
		t.Fatalf("down parse: %v %d", ok, since)
	}
}
