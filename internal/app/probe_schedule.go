package app

import (
	"context"
	"errors"
	"time"
)

type probeTarget struct {
	subscriptionID string
	node           Node
}

func (m *Manager) probeTargets(subID, nodeID string) ([]probeTarget, error) {
	if subID == "" && nodeID != "" {
		return nil, errors.New("select a subscription for this server")
	}
	var targets []probeTarget
	found := subID == ""
	for _, sub := range m.stateCopy().Subscriptions {
		if subID != "" && sub.ID != subID {
			continue
		}
		found = true
		for _, node := range sub.Nodes {
			if nodeID == "" || node.ID == nodeID {
				targets = append(targets, probeTarget{sub.ID, node})
			}
		}
	}
	if !found || nodeID != "" && len(targets) == 0 {
		return nil, errors.New("server or subscription no longer exists; refresh the panel")
	}
	return targets, nil
}
func (m *Manager) runProbeTargets(ctx context.Context, targets []probeTarget, automatic bool) error {
	m.mu.Lock()
	m.probing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.probing = false; m.probingNode = ""; m.mu.Unlock() }()
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.checkNode(ctx, target.subscriptionID, target.node)
		if automatic {
			m.checkActiveDuringSweep(ctx, time.Now())
		}
	}
	return ctx.Err()
}
func (m *Manager) ProbeScope(ctx context.Context, subID, nodeID string) error {
	if m.prober == nil {
		return errors.New("server probing is unavailable")
	}
	if !m.probeMu.TryLock() {
		return errors.New("server check is already running")
	}
	defer m.probeMu.Unlock()
	targets, err := m.probeTargets(subID, nodeID)
	if err != nil {
		return err
	}
	return m.runProbeTargets(ctx, targets, false)
}
func (m *Manager) SetProbeInterval(seconds int64) error {
	if seconds != 0 && seconds != 300 && seconds != 600 && seconds != 1800 {
		return errors.New("choose Off, 5, 10 or 30 minutes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.autoProbeCancel != nil {
		m.autoProbeCancel()
	}
	m.probeIntervalSeconds = seconds
	m.nextProbeAt = time.Time{}
	m.nextActiveProbeAt = time.Time{}
	if seconds > 0 {
		now := time.Now()
		m.nextProbeAt = now.Add(time.Duration(seconds) * time.Second)
		m.nextActiveProbeAt = now.Add(time.Minute)
	}
	return nil
}
func (m *Manager) runScheduledProbes(ctx context.Context, now time.Time) {
	if ctx.Err() != nil || m.prober == nil || !m.probeMu.TryLock() {
		return
	}
	defer m.probeMu.Unlock()
	state := m.stateCopy()
	running := m.core.Running()
	m.mu.Lock()
	interval := m.probeIntervalSeconds
	if interval == 0 {
		m.mu.Unlock()
		return
	}
	all := !now.Before(m.nextProbeAt)
	active := state.Active != nil && state.Active.Enabled && running && !now.Before(m.nextActiveProbeAt)
	if !all && !active {
		m.mu.Unlock()
		return
	}
	var targets []probeTarget
	// Prioritize the active connection before checking the rest of the catalog.
	if state.Active != nil && state.Active.Enabled && running {
		targets = append(targets, probeTarget{state.Active.SubscriptionID, state.Active.Node})
	}
	if all {
		for _, sub := range state.Subscriptions {
			for _, node := range sub.Nodes {
				if len(targets) > 0 && targets[0].subscriptionID == sub.ID && targets[0].node.ID == node.ID {
					continue
				}
				targets = append(targets, probeTarget{sub.ID, node})
			}
		}
		m.nextProbeAt = now.Add(time.Duration(interval) * time.Second)
	}
	m.nextActiveProbeAt = now.Add(time.Minute)
	probeCtx, cancel := context.WithCancel(ctx)
	m.autoProbeCancel = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); m.autoProbeCancel = nil; m.mu.Unlock() }()
	_ = m.runProbeTargets(probeCtx, targets, true)
}
func (m *Manager) RunProbeScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.runScheduledProbes(ctx, now)
		}
	}
}

// A long sequential sweep must not starve checks of the current connection.
func (m *Manager) checkActiveDuringSweep(ctx context.Context, now time.Time) {
	if ctx.Err() != nil || !m.core.Running() {
		return
	}
	m.mu.Lock()
	if m.probeIntervalSeconds == 0 || now.Before(m.nextActiveProbeAt) || m.state.Active == nil || !m.state.Active.Enabled {
		m.mu.Unlock()
		return
	}
	active := *m.state.Active
	recent, ok := m.probes[active.SubscriptionID+"/"+active.Node.ID]
	if ok && now.Sub(recent.CheckedAt) < time.Minute {
		m.nextActiveProbeAt = recent.CheckedAt.Add(time.Minute)
		m.mu.Unlock()
		return
	}
	m.nextActiveProbeAt = now.Add(time.Minute)
	m.mu.Unlock()
	m.checkNode(ctx, active.SubscriptionID, active.Node)
}
