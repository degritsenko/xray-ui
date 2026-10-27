package app

import (
	"context"
	"errors"
	"time"
)

func (m *Manager) checkNode(ctx context.Context, subID string, node Node) bool {
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
func (m *Manager) ProbeAll(ctx context.Context) error {
	if m.prober == nil {
		return errors.New("server probing is unavailable")
	}
	if !m.probeMu.TryLock() {
		return errors.New("server check is already running")
	}
	defer m.probeMu.Unlock()
	m.mu.Lock()
	m.probing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.probing = false; m.mu.Unlock() }()
	for _, sub := range m.stateCopy().Subscriptions {
		for _, node := range sub.Nodes {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.checkNode(ctx, sub.ID, node)
		}
	}
	return ctx.Err()
}
func (m *Manager) bootstrapProbed(ctx context.Context) error {
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	m.mu.Lock()
	m.probing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.probing = false; m.mu.Unlock() }()
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
	for _, sub := range m.stateCopy().Subscriptions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.refresh(ctx, sub.ID, connected); err != nil {
			m.SetError(err.Error())
		}
	}
	m.op.Lock()
	s := m.stateCopy()
	manual := m.intent != intent
	m.op.Unlock()
	stopped := s.Active != nil && !s.Active.Enabled
	type candidate struct {
		sub  string
		node Node
	}
	var candidates []candidate
	seen := map[string]bool{}
	add := func(id string, n Node) {
		key := id + "/" + n.ID
		if !seen[key] {
			seen[key] = true
			candidates = append(candidates, candidate{id, n})
		}
	}
	if !connected && !stopped && !manual {
		if s.Active != nil {
			n := s.Active.Node
			if i, err := findSub(s, s.Active.SubscriptionID); err == nil {
				var updated Node
				if s.Active.ByName {
					updated, err = SelectNode(s.Subscriptions[i].Nodes, n.Name, "")
				} else {
					updated, err = SelectNode(s.Subscriptions[i].Nodes, "", n.ID)
				}
				if err == nil {
					n = updated
				}
			}
			add(s.Active.SubscriptionID, n)
		}
		for _, sub := range s.Subscriptions {
			if sub.BootstrapName != "" || sub.BootstrapID != "" {
				if n, err := SelectNode(sub.Nodes, sub.BootstrapName, sub.BootstrapID); err == nil {
					add(sub.ID, n)
				}
			}
		}
		for _, sub := range s.Subscriptions {
			for _, n := range sub.Nodes {
				add(sub.ID, n)
			}
		}
		for _, c := range candidates {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.op.Lock()
			changed := m.intent != intent
			m.op.Unlock()
			if changed {
				manual = true
				break
			}
			if check(c.sub, c.node) && activate(c.sub, c.node) {
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
	manual = manual || m.intent != intent
	stopped = latest.Active != nil && !latest.Active.Enabled
	m.op.Unlock()
	if !connected && !stopped && !manual && len(s.Subscriptions) > 0 {
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
