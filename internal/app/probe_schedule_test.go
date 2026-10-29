package app

import (
	"context"
	"testing"
	"time"
)

func TestProbeScopeOnlyChecksRequestedNode(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	nodes := m.stateCopy().Subscriptions[0].Nodes
	p := &fakeProber{}
	m.SetProber(p)
	if err := m.ProbeScope(context.Background(), id, nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 || p.calls[0] != nodes[1].ID {
		t.Fatalf("wrong scope: %v", p.calls)
	}
	if err := m.ProbeScope(context.Background(), "", nodes[0].ID); err == nil {
		t.Fatal("node without subscription accepted")
	}
	if err := m.ProbeScope(context.Background(), id, "missing"); err == nil {
		t.Fatal("missing node accepted")
	}
}

func TestScheduledChecksRespectOffAndNeverSwitchServers(t *testing.T) {
	m, r, c, id := testManager(t)
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	p := &fakeProber{}
	m.SetProber(p)
	if err := m.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	selected := m.stateCopy().Active.Node.ID
	p.calls = nil
	if err := m.SetProbeInterval(300); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(61 * time.Second)
	m.runScheduledProbes(context.Background(), now)
	if len(p.calls) != 1 || p.calls[0] != selected {
		t.Fatalf("active server not checked: %v", p.calls)
	}
	p.calls = nil
	m.runScheduledProbes(context.Background(), time.Now().Add(301*time.Second))
	if len(p.calls) != 2 {
		t.Fatalf("catalog not checked: %v", p.calls)
	}
	if !c.Running() || m.stateCopy().Active.Node.ID != selected {
		t.Fatal("background check changed connection")
	}
	if err := m.SetProbeInterval(0); err != nil {
		t.Fatal(err)
	}
	p.calls = nil
	m.runScheduledProbes(context.Background(), time.Now().Add(time.Hour))
	if len(p.calls) != 0 {
		t.Fatal("Off still performed automatic probes")
	}
	if !m.Snapshot().NextProbeAt.IsZero() {
		t.Fatal("Off retains next scheduled sweep")
	}
	for _, value := range []int64{-1, 1, 60, 301, 999999} {
		if m.SetProbeInterval(value) == nil {
			t.Fatalf("invalid interval accepted: %d", value)
		}
	}
	_ = id
}

func TestProbeScheduleSkipsBusyWorkerAndCancelledContext(t *testing.T) {
	m, _, _, _ := testManager(t)
	p := &fakeProber{}
	m.SetProber(p)
	m.probeMu.Lock()
	m.runScheduledProbes(context.Background(), time.Now().Add(time.Hour))
	m.probeMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.runScheduledProbes(ctx, time.Now().Add(time.Hour))
	if len(p.calls) != 0 {
		t.Fatal("busy or cancelled scheduler ran a check")
	}
}

func TestOffCancelsInFlightAutomaticCheck(t *testing.T) {
	m, _, _, id := testManager(t)
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	p := &blockingProber{entered: make(chan struct{}), release: make(chan struct{})}
	m.SetProber(p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { m.runScheduledProbes(ctx, time.Now().Add(time.Hour)); close(done) }()
	select {
	case <-p.entered:
	case <-ctx.Done():
		t.Fatal("probe did not begin")
	}
	if err := m.SetProbeInterval(0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Off did not cancel automatic check")
	}
	if m.Snapshot().Probing {
		t.Fatal("cancelled worker still marked active")
	}
}

func TestLongSweepRechecksCurrentActiveServerBetweenTargets(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = sampleURI + "\n" + stringsReplaceServer(sampleURI)
	p := &fakeProber{}
	m.SetProber(p)
	if err := m.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := m.stateCopy()
	selected := state.Active.Node.ID
	m.mu.Lock()
	m.nextActiveProbeAt = time.Now().Add(-time.Minute)
	result := m.probes[id+"/"+selected]
	result.CheckedAt = time.Now().Add(-2 * time.Minute)
	m.probes[id+"/"+selected] = result
	m.mu.Unlock()
	p.calls = nil
	if err := m.runProbeTargets(context.Background(), []probeTarget{{id, state.Subscriptions[0].Nodes[1]}}, true); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 2 || p.calls[1] != selected {
		t.Fatalf("long sweep starved active check: %v", p.calls)
	}
}

func TestPreferenceIdentitySeparatesFeedsAndSurvivesFreshRuntimeIDs(t *testing.T) {
	m, _, _, _ := testManager(t)
	m.mu.Lock()
	a := m.state.Subscriptions[0]
	b := a
	b.ID = "different"
	b.URL = a.URL + "/different-feed"
	m.state.Subscriptions = append(m.state.Subscriptions, b)
	m.mu.Unlock()
	first := m.Snapshot().Subscriptions
	if first[0].PreferenceID == "" || first[0].PreferenceID == first[1].PreferenceID {
		t.Fatal("different sources share preferences")
	}
	m.mu.Lock()
	m.state.Subscriptions[0].ID = "new-runtime-id"
	m.mu.Unlock()
	if m.Snapshot().Subscriptions[0].PreferenceID != first[0].PreferenceID {
		t.Fatal("preferences depend on process-local IDs")
	}
}
