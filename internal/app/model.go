package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"time"
)

type Node struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Server      string `json:"server"`
	Port        int    `json:"port"`
	UUID        string `json:"uuid"`
	Encryption  string `json:"encryption"`
	Flow        string `json:"flow"`
	Fingerprint string `json:"fingerprint"`
	SNI         string `json:"sni"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX"`
}
type Subscription struct {
	Title           string    `json:"title,omitempty"`
	EnvironmentName string    `json:"environmentName,omitempty"`
	EnvironmentID   string    `json:"environmentID,omitempty"`
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	IntervalSeconds int64     `json:"intervalSeconds"`
	Nodes           []Node    `json:"nodes"`
	UpdatedAt       time.Time `json:"updatedAt"`
	AttemptedAt     time.Time `json:"attemptedAt"`
	Error           string    `json:"error,omitempty"`
	Skipped         int       `json:"skipped"`
	BootstrapName   string    `json:"bootstrapName,omitempty"`
	BootstrapID     string    `json:"bootstrapID,omitempty"`
}
type Selection struct {
	SubscriptionID string          `json:"subscriptionID"`
	SourceHash     string          `json:"sourceHash"`
	Node           Node            `json:"node"`
	ByName         bool            `json:"byName"`
	Config         json.RawMessage `json:"config"`
	Enabled        bool            `json:"enabled"`
}
type State struct {
	Version       int            `json:"version"`
	Subscriptions []Subscription `json:"subscriptions"`
	Active        *Selection     `json:"active,omitempty"`
}

func cloneState(s State) State {
	data, _ := json.Marshal(s)
	var result State
	_ = json.Unmarshal(data, &result)
	return result
}
func sourceHash(url string) string { s := sha256.Sum256([]byte(url)); return hex.EncodeToString(s[:]) }
func nodeID(n Node) string {
	// Match jq -cS (including its trailing newline) from the original importer.
	fields := map[string]any{"server": n.Server, "port": n.Port, "uuid": n.UUID, "encryption": n.Encryption, "flow": n.Flow, "fingerprint": n.Fingerprint, "sni": n.SNI, "publicKey": n.PublicKey, "shortId": n.ShortID, "spiderX": n.SpiderX}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(fields)
	sum := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(sum[:])
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var shortPattern = regexp.MustCompile(`^([0-9a-fA-F]{2}){0,8}$`)

func validateNode(n Node) error {
	if !uuidPattern.MatchString(n.UUID) || !hostPattern.MatchString(n.Server) || n.Port < 1 || n.Port > 65535 || !keyPattern.MatchString(n.PublicKey) || !shortPattern.MatchString(n.ShortID) || n.SNI == "" || n.Encryption != "none" || (n.Flow != "" && n.Flow != "xtls-rprx-vision") {
		return errors.New("invalid VLESS REALITY parameters")
	}
	return nil
}
func RenderConfig(n Node, ip, iface string) ([]byte, error) {
	if err := validateNode(n); err != nil {
		return nil, err
	}
	if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
		return nil, errors.New("server IPv4 address not found")
	}
	user := map[string]any{"id": n.UUID, "encryption": n.Encryption}
	if n.Flow != "" {
		user["flow"] = n.Flow
	}
	cfg := map[string]any{
		"log":       map[string]any{"access": "none", "loglevel": "warning"},
		"inbounds":  []any{map[string]any{"tag": "tun-in", "port": 0, "protocol": "tun", "settings": map[string]any{"name": "tun0", "mtu": 1500}, "sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}},
		"outbounds": []any{map[string]any{"tag": "proxy", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": ip, "port": n.Port, "users": []any{user}}}}, "streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"fingerprint": n.Fingerprint, "serverName": n.SNI, "publicKey": n.PublicKey, "shortId": n.ShortID, "spiderX": n.SpiderX}, "sockopt": map[string]any{"interface": iface}}}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
func configIP(data []byte) string {
	var cfg struct {
		Outbounds []struct {
			Settings struct{ VNext []struct{ Address string } }
		}
	}
	if json.Unmarshal(data, &cfg) == nil && len(cfg.Outbounds) > 0 && len(cfg.Outbounds[0].Settings.VNext) > 0 {
		return cfg.Outbounds[0].Settings.VNext[0].Address
	}
	return ""
}
func manualNode(env func(string) string) (*Node, error) {
	if env("SERVER_ADDRESS") == "" {
		return nil, nil
	}
	port, err := strconv.Atoi(env("SERVER_PORT"))
	if err != nil {
		return nil, errors.New("SERVER_PORT must be a number")
	}
	n := Node{Name: "Manual configuration", Server: env("SERVER_ADDRESS"), Port: port, UUID: env("USER_ID"), Encryption: env("ENCRYPTION"), Fingerprint: env("FINGERPRINT_FP"), SNI: env("SERVER_NAME_SNI"), PublicKey: env("PUBLIC_KEY_PBK"), ShortID: env("SHORT_ID_SID"), Flow: env("FLOW")}
	if err = validateNode(n); err != nil {
		return nil, err
	}
	n.ID = nodeID(n)
	return &n, nil
}
