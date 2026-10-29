package app

import (
	"context"
	"errors"
	"time"
)

func (m *Manager) checkNode(ctx context.Context, subID string, node Node) bool {
	m.mu.Lock()
	m.probingNode = subID + "/" + node.ID
	m.mu.Unlock()
	delay, err := m.prober.Probe(ctx, node)
	if ctx.Err() != nil {
		return false
	}
	result := ProbeResult{Milliseconds: max(1, delay.Milliseconds()), Reachable: err == nil, CheckedAt: time.Now().UTC()}
	if err != nil {
		result.Milliseconds = 0
	}
	m.mu.Lock()
	if m.probes == nil {
		m.probes = make(map[string]ProbeResult)
	}
	// Keep only results for the current catalog and never expose credentials.
	for _, sub := range m.state.Subscriptions {
		if sub.ID == subID {
			for _, n := range sub.Nodes {
				if n.ID == node.ID {
					m.probes[subID+"/"+node.ID] = result
				}
			}
		}
	}
	m.mu.Unlock()
	return err == nil
}
func (m *Manager) ProbeAll(ctx context.Context) error { return m.ProbeScope(ctx, "", "") }
func (m *Manager) bootstrapProbed(ctx context.Context) error {
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	m.mu.Lock()
	m.probing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.probing = false; m.probingNode = ""; m.mu.Unlock() }()
	m.op.Lock()
	initial := m.stateCopy()
	intent := m.intent
	m.op.Unlock()
	connected := false
	tested := map[string]bool{}
	check := func(id string, n Node) bool {
		key := id + "/" + n.ID
		if tested[key] {
			return false
		}
		tested[key] = true
		return m.checkNode(ctx, id, n)
	}
	activate := func(id string, n Node) bool {
		m.op.Lock()
		defer m.op.Unlock()
		// A manual action always wins over an in-flight startup measurement.
		if m.intent != intent || ctx.Err() != nil {
			return false
		}
		s := m.stateCopy()
		if s.Active != nil && !s.Active.Enabled {
			return false
		}
		index, err := findSub(s, id)
		if err != nil {
			return false
		}
		if _, err := SelectNode(s.Subscriptions[index].Nodes, "", n.ID); err != nil {
			unchangedAcceptance := false
			if s.Active != nil && s.Active.SubscriptionID == id && s.Active.Node.ID == n.ID {
				if oldIndex, e := findSub(initial, id); e == nil {
					unchangedAcceptance = s.Subscriptions[index].UpdatedAt.Equal(initial.Subscriptions[oldIndex].UpdatedAt)
				}
			}
			if !unchangedAcceptance {
				return false
			}
		}
		selection := Selection{SubscriptionID: id, SourceHash: sourceHash(s.Subscriptions[index].URL), Node: n, ByName: uniqueNodeName(s.Subscriptions[index].Nodes, n.Name), Enabled: true}
		s.Subscriptions[index].BootstrapName = ""
		s.Subscriptions[index].BootstrapID = ""
		if err = m.apply(ctx, s, selection, false); err != nil {
			return false
		}
		m.logs.Add("Automatically connected to an available server")
		return true
	}
	// Restore a working saved VPN before contacting subscription hosts.
	if initial.Active != nil && initial.Active.Enabled {
		if len(initial.Subscriptions) == 0 {
			m.op.Lock()
			err := m.apply(ctx, initial, *initial.Active, false)
			m.op.Unlock()
			return err
		}
		if check(initial.Active.SubscriptionID, initial.Active.Node) {
			connected = activate(initial.Active.SubscriptionID, initial.Active.Node)
		}
	}
	// Try each source as soon as it is loaded. Later slow downloads must not
	// postpone an available connection from an earlier source.
	for _, source := range m.stateCopy().Subscriptions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.refresh(ctx, source.ID, connected); err != nil && !isSubscriptionRefreshError(err) {
			m.SetError(err.Error())
		}
		if connected {
			continue
		}
		m.op.Lock()
		s := m.stateCopy()
		manual := m.intent != intent
		m.op.Unlock()
		if manual || (s.Active != nil && !s.Active.Enabled) {
			continue
		}
		index, err := findSub(s, source.ID)
		if err != nil {
			continue
		}
		sub := s.Subscriptions[index]
		var candidates []Node
		seen := map[string]bool{}
		add := func(n Node) {
			if !seen[n.ID] {
				seen[n.ID] = true
				candidates = append(candidates, n)
			}
		}
		if s.Active != nil && s.Active.SubscriptionID == source.ID {
			n := s.Active.Node
			var updated Node
			if s.Active.ByName {
				updated, err = SelectNode(sub.Nodes, n.Name, "")
			} else {
				updated, err = SelectNode(sub.Nodes, "", n.ID)
			}
			if err == nil {
				n = updated
			}
			add(n)
		}
		if sub.BootstrapName != "" || sub.BootstrapID != "" {
			if n, e := SelectNode(sub.Nodes, sub.BootstrapName, sub.BootstrapID); e == nil {
				add(n)
			}
		}
		for _, n := range sub.Nodes {
			add(n)
		}
		for _, n := range candidates {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.op.Lock()
			changed := m.intent != intent
			m.op.Unlock()
			if changed {
				break
			}
			if check(source.ID, n) && activate(source.ID, n) {
				connected = true
				break
			}
		}
	}
	// Remaining checks never change the accepted server.
	for _, sub := range m.stateCopy().Subscriptions {
		for _, n := range sub.Nodes {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !tested[sub.ID+"/"+n.ID] {
				check(sub.ID, n)
			}
		}
	}
	m.op.Lock()
	latest := m.stateCopy()
	manual := m.intent != intent
	stopped := latest.Active != nil && !latest.Active.Enabled
	m.op.Unlock()
	if !connected && !stopped && !manual && len(latest.Subscriptions) > 0 {
		err := errors.New("no reachable VPN server found; check servers in the panel")
		m.SetError(err.Error())
		return err
	}
	return nil
}
func uniqueNodeName(nodes []Node, name string) bool {
	count := 0
	for _, n := range nodes {
		if n.Name == name {
			count++
		}
	}
	return count == 1
}
