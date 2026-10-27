package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type StateStore interface {
	Load() (State, error)
	Save(State) error
}
type Store struct{ Dir string }

func NewStore(dir string) *Store { return &Store{Dir: dir} }
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func readJSON(path string, target any, max int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > max {
		return errors.New("state file too large")
	}
	return json.Unmarshal(b, target)
}

const MaxStateBytes = 16 * 1024 * 1024

var ErrStateTooLarge = errors.New("state too large")

func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func atomicJSON(path string, value any) error {
	data, err := encodeJSON(value)
	if err != nil {
		return err
	}
	return atomicBytes(path, data)
}
func atomicBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}
func (s *Store) Load() (State, error) {
	var state State
	err := readJSON(filepath.Join(s.Dir, "manager.json"), &state, MaxStateBytes)
	if err == nil && state.Version != 1 {
		err = errors.New("unsupported state version")
	}
	return state, err
}
func (s *Store) Save(state State) error {
	data, err := encodeJSON(state)
	if err != nil {
		return err
	}
	if len(data) > MaxStateBytes {
		return ErrStateTooLarge
	}
	return atomicBytes(filepath.Join(s.Dir, "manager.json"), data)
}

// LoadOrImport reconciles sources from environment on EVERY boot. The panel
// persists node choice and accepted state, but can never define source URLs.
func (s *Store) LoadOrImport(env func(string) string) (State, error) {
	state, err := s.Load()
	fresh := errors.Is(err, os.ErrNotExist)
	if err != nil && !fresh {
		return state, err
	}
	if fresh {
		state = State{Version: 1, Subscriptions: []Subscription{}}
	}
	previous := cloneState(state)
	configured := []Subscription{}
	seen := map[string]bool{}
	for index := 1; index <= 16; index++ {
		suffix := ""
		if index > 1 {
			suffix = fmt.Sprintf("_%d", index)
		}
		source := strings.TrimSpace(env("SUBSCRIPTION_URL" + suffix))
		if source == "" {
			continue
		}
		if err = ValidateURL(source); err != nil {
			return state, err
		}
		if seen[source] {
			return state, errors.New("duplicate subscription URL in environment variables")
		}
		seen[source] = true
		name := strings.TrimSpace(env("SUBSCRIPTION_NAME" + suffix))
		if name == "" {
			name = fmt.Sprintf("Subscription %d", index)
		}
		if !validText(name) || len(name) > 160 {
			return state, errors.New("invalid subscription name in environment variables")
		}
		interval := int64(600)
		value := env("SUBSCRIPTION_REFRESH_SECONDS" + suffix)
		if value == "" {
			value = env("SUBSCRIPTION_REFRESH_SECONDS")
		}
		if value != "" {
			interval, err = strconv.ParseInt(value, 10, 64)
			if err != nil || interval < 0 || interval > 2147483647 {
				return state, errors.New("invalid subscription refresh interval")
			}
		}
		sub := Subscription{ID: randomID(), Name: name, URL: source, IntervalSeconds: interval, Nodes: []Node{}}
		existed := false
		for _, old := range previous.Subscriptions {
			if old.URL == source {
				sub = old
				existed = true
				break
			}
		}
		sub.Name, sub.URL, sub.IntervalSeconds = name, source, interval
		desiredName, desiredID := "", ""
		if len(configured) == 0 {
			desiredName, desiredID = env("SERVER_NAME"), env("SERVER_ID")
		}
		if desiredName != "" && desiredID != "" {
			return state, errors.New("set either SERVER_NAME or SERVER_ID, not both")
		}
		selectorChanged := existed && (sub.EnvironmentName != desiredName || sub.EnvironmentID != desiredID)
		sub.EnvironmentName, sub.EnvironmentID = desiredName, desiredID
		if !existed || selectorChanged {
			sub.BootstrapName, sub.BootstrapID = desiredName, desiredID
		}
		if selectorChanged && (desiredName != "" || desiredID != "") && state.Active != nil && state.Active.SubscriptionID == sub.ID {
			state.Active = nil
		}
		if fresh && state.Active == nil {
			s.importLegacy(&state, &sub)
		}
		configured = append(configured, sub)
	}
	state.Subscriptions = configured
	if len(configured) > 0 {
		if state.Active != nil {
			matched := false
			for _, sub := range configured {
				if state.Active.SubscriptionID == sub.ID && state.Active.SourceHash == sourceHash(sub.URL) {
					matched = true
					break
				}
			}
			if !matched {
				state.Active = nil
			}
		}
		if state.Active == nil {
			for index := range state.Subscriptions {
				sub := &state.Subscriptions[index]
				if sub.EnvironmentName != "" || sub.EnvironmentID != "" {
					sub.BootstrapName, sub.BootstrapID = sub.EnvironmentName, sub.EnvironmentID
					break
				}
			}
		}
	} else {
		manual, manualErr := manualNode(env)
		if manualErr != nil {
			return state, manualErr
		}
		if manual == nil {
			state.Active = nil
		} else if state.Active == nil || state.Active.SubscriptionID != "" || state.Active.Node.ID != manual.ID {
			state.Active = &Selection{Node: *manual, SourceHash: "manual", Enabled: true}
		}
	}
	if err = s.Save(state); err != nil {
		return state, err
	}
	return state, nil
}
func (s *Store) importLegacy(state *State, sub *Subscription) {
	var legacy struct {
		Source   string `json:"source"`
		Selector struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"selector"`
		Node Node `json:"node"`
	}
	if readJSON(filepath.Join(s.Dir, "last-good.json"), &legacy, MaxSubscriptionBytes) != nil || legacy.Source != sourceHash(sub.URL) || legacy.Selector.Name != sub.BootstrapName || legacy.Selector.ID != sub.BootstrapID || validateNode(legacy.Node) != nil {
		return
	}
	config, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if err != nil || len(config) > MaxSubscriptionBytes || !json.Valid(config) {
		return
	}
	sub.Nodes = []Node{legacy.Node}
	state.Active = &Selection{SubscriptionID: sub.ID, SourceHash: sourceHash(sub.URL), Node: legacy.Node, ByName: sub.BootstrapName != "", Config: config, Enabled: true}
}
