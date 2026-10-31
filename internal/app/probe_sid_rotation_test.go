package app

import (
	"context"
	"strings"
	"testing"
)

func TestProbeHistorySurvivesXHTTPShortIDRotation(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = xhttpURI("reality")
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	old := m.stateCopy().Subscriptions[0].Nodes[0]
	m.probes = map[string]ProbeResult{id + "/" + old.ID: {Reachable: true, History: []bool{true}}}
	r.body = strings.Replace(r.body, "sid=", "sid=ab", 1)
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	next := m.stateCopy().Subscriptions[0].Nodes[0]
	if old.ID == next.ID {
		t.Fatal("short ID did not change")
	}
	if result, ok := m.probes[id+"/"+next.ID]; !ok || len(result.History) != 1 || !result.History[0] {
		t.Fatal("probe history lost")
	}
}

func TestProbeHistorySurvivesTCPShortIDRotation(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = sampleURI
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	old := m.stateCopy().Subscriptions[0].Nodes[0]
	m.probes = map[string]ProbeResult{id + "/" + old.ID: {Reachable: true, History: []bool{true, true}}}
	r.body = strings.Replace(r.body, "sid=abcdef", "sid=ab", 1)
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	next := m.stateCopy().Subscriptions[0].Nodes[0]
	if old.ID == next.ID {
		t.Fatal("short ID did not change")
	}
	if result, ok := m.probes[id+"/"+next.ID]; !ok || len(result.History) != 2 || !result.History[0] || !result.History[1] {
		t.Fatal("probe history lost")
	}
}
