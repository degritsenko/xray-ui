package app

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"
)

type Manager struct {
	op                   sync.Mutex
	mu                   sync.RWMutex
	state                State
	store                StateStore
	network              Runtime
	core                 Core
	logs                 *LogBuffer
	lastError            string
	userAgent            string
	prober               Prober
	probeMu              sync.Mutex
	probes               map[string]ProbeResult
	probing              bool
	intent               uint64
	coreVersion          string
	probeIntervalSeconds int64
	nextProbeAt          time.Time
	nextActiveProbeAt    time.Time
	probingNode          string
	autoProbeCancel      context.CancelFunc
}

func NewManager(store StateStore, state State, network Runtime, core Core, logs *LogBuffer) *Manager {
	if logs == nil {
		logs = NewLogBuffer(nil)
	}
	m := &Manager{store: store, state: cloneState(state), network: network, core: core, logs: logs, userAgent: "xray-mikrotik"}
	_ = m.SetProbeInterval(600)
	m.updateSecrets(state)
	return m
}
func (m *Manager) SetProber(p Prober) { m.prober = p }
func (m *Manager) SetUserAgent(ua string) {
	if ua != "" {
		m.userAgent = ua
	}
}
func (m *Manager) stateCopy() State { m.mu.RLock(); defer m.mu.RUnlock(); return cloneState(m.state) }
func (m *Manager) SetError(message string) {
	m.mu.Lock()
	m.lastError = message
	m.mu.Unlock()
	if message != "" {
		m.logs.Add(message)
	}
}
func (m *Manager) updateSecrets(s State) {
	var secrets []string
	for _, sub := range s.Subscriptions {
		secrets = append(secrets, sub.URL)
		for _, n := range sub.Nodes {
			secrets = append(secrets, n.UUID, n.PublicKey)
		}
	}
	if s.Active != nil {
		secrets = append(secrets, s.Active.Node.UUID, s.Active.Node.PublicKey)
	}
	m.logs.SetSecrets(secrets)
}
func (m *Manager) save(s State) error {
	if err := m.store.Save(s); err != nil {
		if errors.Is(err, ErrStateTooLarge) {
			return errors.New("settings exceed size limit; reduce the number of subscriptions or servers")
		}
		return errors.New("could not save settings; previous settings retained")
	}
	m.mu.Lock()
	m.state = cloneState(s)
	valid := map[string]bool{}
	for _, sub := range s.Subscriptions {
		for _, n := range sub.Nodes {
			valid[sub.ID+"/"+n.ID] = true
		}
	}
	for key := range m.probes {
		if !valid[key] {
			delete(m.probes, key)
		}
	}
	m.mu.Unlock()
	m.updateSecrets(s)
	return nil
}
func findSub(s State, id string) (int, error) {
	for i, sub := range s.Subscriptions {
		if sub.ID == id {
			return i, nil
		}
	}
	return 0, errors.New("subscription not found")
}
func (m *Manager) apply(ctx context.Context, s State, selection Selection, force bool) error {
	ip, err := m.network.Resolve(ctx, selection.Node.Server)
	if err != nil && ctx.Err() == nil {
		old := m.stateCopy().Active
		if old != nil && old.SourceHash == selection.SourceHash && old.Node.ID == selection.Node.ID {
			ip = configIP(old.Config)
			if ip != "" {
				err = nil
				m.logs.Add("DNS unavailable: using saved IPv4 address for the selected server")
			}
		}
	}
	if err != nil {
		return err
	}
	config, err := RenderConfig(selection.Node, ip, m.network.Interface())
	if err != nil {
		return err
	}
	selection.Config = config
	s.Active = &selection
	m.updateSecrets(s)
	if selection.Enabled {
		if force {
			err = m.core.Restart(ctx, config, func() error {
				if routeErr := m.network.EnsureTunnel(ctx); routeErr != nil {
					return routeErr
				}
				return m.save(s)
			})
		} else {
			err = m.core.Apply(ctx, config, func() error {
				if routeErr := m.network.EnsureTunnel(ctx); routeErr != nil {
					return routeErr
				}
				return m.save(s)
			})
		}
	} else {
		err = m.save(s)
	}
	if err != nil {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		routeErr := m.network.EnsureTunnel(restoreCtx)
		cancel()
		if routeErr != nil {
			err = errors.New("could not restore TUN route; check logs")
		}
		m.updateSecrets(m.stateCopy())
		m.SetError(err.Error())
		return err
	}
	m.SetError("")
	return nil
}
func (m *Manager) Activate(ctx context.Context, subID, nodeID string) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.intent++
	s := m.stateCopy()
	index, err := findSub(s, subID)
	if err != nil {
		return err
	}
	sub := s.Subscriptions[index]
	node, err := SelectNode(sub.Nodes, "", nodeID)
	if err != nil {
		return err
	}
	sameName := 0
	for _, n := range sub.Nodes {
		if n.Name == node.Name {
			sameName++
		}
	}
	err = m.apply(ctx, s, Selection{SubscriptionID: subID, SourceHash: sourceHash(sub.URL), Node: node, ByName: sameName == 1, Enabled: true}, false)
	if err == nil {
		m.logs.Add("Selected server is active")
	}
	return err
}
func (m *Manager) Refresh(ctx context.Context, id string) error { return m.refresh(ctx, id, true) }
func (m *Manager) refresh(ctx context.Context, id string, apply bool) error {
	m.op.Lock()
	defer m.op.Unlock()
	s := m.stateCopy()
	index, err := findSub(s, id)
	if err != nil {
		return err
	}
	sub := &s.Subscriptions[index]
	sub.AttemptedAt = time.Now().UTC()
	var body []byte
	var title string
	var usage *SubscriptionUsage
	if downloader, ok := m.network.(SubscriptionMetadataDownloader); ok {
		var metadata SubscriptionMetadata
		body, metadata, err = downloader.DownloadSubscriptionMetadata(ctx, sub.URL, m.userAgent)
		title, usage = metadata.Title, metadata.Usage
	} else if downloader, ok := m.network.(SubscriptionDownloader); ok {
		body, title, err = downloader.DownloadSubscription(ctx, sub.URL, m.userAgent)
	} else {
		body, err = m.network.Download(ctx, sub.URL, m.userAgent)
		if err == nil {
			title = subscriptionTitle("", body)
		}
	}
	if err == nil {
		if usage == nil {
			usage = subscriptionUsage("", body)
		}
		sub.Nodes, sub.Skipped, err = ParseSubscription(body)
	}
	if err != nil {
		// Restore the catalog as well as the active process on malformed/download failure.
		old := m.stateCopy()
		old.Subscriptions[index].AttemptedAt = sub.AttemptedAt
		old.Subscriptions[index].Error = err.Error()
		if saveErr := m.save(old); saveErr != nil {
			return saveErr
		}
		m.logs.Add("Subscription refresh failed; current connection retained")
		return &subscriptionRefreshError{cause: err}
	}
	sub.Usage = usage
	sub.Title = decodeProfileTitle(title)
	sub.UpdatedAt = time.Now().UTC()
	sub.Error = ""
	var candidate *Selection
	active := s.Active
	if apply && active != nil && active.SubscriptionID == id && active.SourceHash == sourceHash(sub.URL) {
		var node Node
		if active.ByName {
			node, err = SelectNode(sub.Nodes, active.Node.Name, "")
		} else {
			node, err = SelectNode(sub.Nodes, "", active.Node.ID)
		}
		if err == nil && active.Enabled {
			selected := *active
			selected.Node = node
			candidate = &selected
		} else if err != nil {
			sub.Error = err.Error()
		}
	} else if apply && active == nil && (sub.BootstrapName != "" || sub.BootstrapID != "") {
		node, selectErr := SelectNode(sub.Nodes, sub.BootstrapName, sub.BootstrapID)
		if selectErr != nil {
			sub.Error = selectErr.Error()
			err = selectErr
		} else {
			candidate = &Selection{SubscriptionID: id, SourceHash: sourceHash(sub.URL), Node: node, ByName: sub.BootstrapName != "", Enabled: true}
		}
	}
	bootstrapName, bootstrapID := sub.BootstrapName, sub.BootstrapID
	bootstrapAttempt := s.Active == nil && (bootstrapName != "" || bootstrapID != "")
	if apply && (candidate != nil || !bootstrapAttempt) {
		sub.BootstrapName, sub.BootstrapID = "", ""
	}
	if candidate != nil {
		if applyErr := m.apply(ctx, s, *candidate, false); applyErr != nil {
			// The runner keeps the old process; persist the new catalog without replacing acceptance.
			if bootstrapAttempt {
				s.Subscriptions[index].BootstrapName = bootstrapName
				s.Subscriptions[index].BootstrapID = bootstrapID
			}
			s.Active = m.stateCopy().Active
			s.Subscriptions[index].Error = applyErr.Error()
			if saveErr := m.save(s); saveErr != nil {
				return saveErr
			}
			return applyErr
		}
	} else if saveErr := m.save(s); saveErr != nil {
		return saveErr
	}
	m.logs.Add("Subscription updated")
	if err != nil {
		return &subscriptionRefreshError{cause: err}
	}
	return nil
}
func (m *Manager) Control(ctx context.Context, action string) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.intent++
	s := m.stateCopy()
	if s.Active == nil {
		return errors.New("select a server first")
	}
	switch action {
	case "stop":
		s.Active.Enabled = false
		if err := m.save(s); err != nil {
			return err
		}
		m.core.Stop()
		if routeErr := m.network.EnsureTunnel(ctx); routeErr != nil {
			return routeErr
		}
		m.logs.Add("Xray stopped; panel and subscription updates remain available")
		return nil
	case "start", "restart":

		selection := *s.Active
		selection.Enabled = true
		if err := m.apply(ctx, s, selection, action == "restart"); err != nil {
			return err
		}
		m.logs.Add("Xray started")
		return nil
	default:
		return errors.New("unknown action")
	}
}
func (m *Manager) Bootstrap(ctx context.Context) error {
	if m.prober != nil {
		return m.bootstrapProbed(ctx)
	}
	m.op.Lock()
	s := m.stateCopy()
	var err error
	if s.Active != nil && s.Active.Enabled {
		err = m.apply(ctx, s, *s.Active, false)
	}
	m.op.Unlock()
	if err != nil {
		m.SetError(err.Error())
	}
	// Resume saved acceptance first. A newly imported env selection is applied only
	// through its matching subscription, never through an arbitrary catalog entry.
	for _, sub := range m.stateCopy().Subscriptions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if refreshErr := m.Refresh(ctx, sub.ID); refreshErr != nil {
			err = refreshErr
			if !isSubscriptionRefreshError(err) {
				m.SetError(err.Error())
			}
		}
	}
	return err
}
func (m *Manager) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	refresh := func() {
		for _, sub := range m.stateCopy().Subscriptions {
			if sub.IntervalSeconds > 0 && (sub.AttemptedAt.IsZero() || time.Since(sub.AttemptedAt) >= time.Duration(sub.IntervalSeconds)*time.Second) {
				if ctx.Err() != nil {
					return
				}
				_ = m.Refresh(ctx, sub.ID)
			}
		}
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
func (m *Manager) RefreshAll(ctx context.Context) {
	for _, sub := range m.stateCopy().Subscriptions {
		if ctx.Err() != nil {
			return
		}
		_ = m.Refresh(ctx, sub.ID)
	}
}

// View types deliberately omit URLs, UUIDs, public keys and accepted raw configs.
type NodeView struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Server string       `json:"server"`
	Port   int          `json:"port"`
	Probe  *ProbeResult `json:"probe,omitempty"`
}
type SubscriptionView struct {
	Usage           *SubscriptionUsage `json:"usage,omitempty"`
	PreferenceID    string             `json:"preferenceID"`
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Host            string             `json:"host"`
	IntervalSeconds int64              `json:"intervalSeconds"`
	Nodes           []NodeView         `json:"nodes"`
	UpdatedAt       time.Time          `json:"updatedAt"`
	Error           string             `json:"error"`
	Skipped         int                `json:"skipped"`
}
type ActiveView struct {
	SubscriptionID string   `json:"subscriptionID"`
	Node           NodeView `json:"node"`
	Enabled        bool     `json:"enabled"`
	SourceChanged  bool     `json:"sourceChanged"`
}
type Snapshot struct {
	CoreVersion          string             `json:"coreVersion"`
	Subscriptions        []SubscriptionView `json:"subscriptions"`
	Active               *ActiveView        `json:"active"`
	Running              bool               `json:"running"`
	LastError            string             `json:"lastError"`
	Logs                 []LogEntry         `json:"logs"`
	CSRF                 string             `json:"csrf,omitempty"`
	Version              string             `json:"version"`
	Probing              bool               `json:"probing"`
	ProbingNode          string             `json:"probingNode"`
	ProbeIntervalSeconds int64              `json:"probeIntervalSeconds"`
	NextProbeAt          time.Time          `json:"nextProbeAt"`
}

func viewNode(n Node) NodeView {
	return NodeView{ID: n.ID, Name: n.Name, Server: n.Server, Port: n.Port}
}
func (m *Manager) Snapshot() Snapshot {
	s := m.stateCopy()
	view := Snapshot{Subscriptions: []SubscriptionView{}, Running: m.core.Running(), Logs: m.logs.Entries(), Version: "ui-10"}
	m.mu.RLock()
	view.LastError = m.lastError
	view.CoreVersion = m.coreVersion
	view.Probing = m.probing
	view.ProbingNode = m.probingNode
	view.ProbeIntervalSeconds = m.probeIntervalSeconds
	view.NextProbeAt = m.nextProbeAt
	results := make(map[string]ProbeResult, len(m.probes))
	for k, v := range m.probes {
		results[k] = v
	}
	m.mu.RUnlock()
	for _, sub := range s.Subscriptions {
		u, _ := url.Parse(sub.URL)
		host := ""
		if u != nil {
			host = u.Host
		}
		name := sub.Title
		if name == "" && u != nil {
			name = u.Hostname()
		}
		if name == "" {
			name = sub.Name
		}
		sv := SubscriptionView{Usage: sub.Usage, PreferenceID: sourceHash("panel-preferences/" + sub.URL), ID: sub.ID, Name: name, Host: host, IntervalSeconds: sub.IntervalSeconds, Nodes: []NodeView{}, UpdatedAt: sub.UpdatedAt, Error: sub.Error, Skipped: sub.Skipped}
		for _, n := range sub.Nodes {
			nv := viewNode(n)
			if result, ok := results[sub.ID+"/"+n.ID]; ok {
				nv.Probe = &result
			}
			sv.Nodes = append(sv.Nodes, nv)
		}
		view.Subscriptions = append(view.Subscriptions, sv)
	}
	if s.Active != nil {
		view.Active = &ActiveView{SubscriptionID: s.Active.SubscriptionID, Node: viewNode(s.Active.Node), Enabled: s.Active.Enabled}
		if result, ok := results[s.Active.SubscriptionID+"/"+s.Active.Node.ID]; ok {
			view.Active.Node.Probe = &result
		}
		if index, err := findSub(s, s.Active.SubscriptionID); err == nil {
			view.Active.SourceChanged = sourceHash(s.Subscriptions[index].URL) != s.Active.SourceHash
		}
	}
	return view
}
