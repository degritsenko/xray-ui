package app

import (
	"context"
	"reflect"
	"testing"
)

func TestProbeHistoryKeepsFiveCompletedChecks(t *testing.T) {
	m, _, _, id := testManager(t)
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	node := m.stateCopy().Subscriptions[0].Nodes[0]
	p := &fakeProber{failed: map[string]bool{}}
	m.SetProber(p)
	key := id + "/" + node.ID
	outcomes := []bool{true, false, true, false, false, true, true}
	var first []bool
	for i, ok := range outcomes {
		p.failed[node.ID] = !ok
		m.checkNode(context.Background(), id, node)
		if i == 0 {
			first = m.probes[key].History
		}
	}
	if got := m.probes[key].History; !reflect.DeepEqual(got, outcomes[2:]) {
		t.Fatalf("history = %v", got)
	}
	if !reflect.DeepEqual(first, []bool{true}) {
		t.Fatalf("old snapshot mutated: %v", first)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.checkNode(ctx, id, node)
	if got := m.probes[key].History; !reflect.DeepEqual(got, outcomes[2:]) {
		t.Fatalf("cancellation changed history: %v", got)
	}
}
