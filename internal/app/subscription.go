package app

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxSubscriptionBytes = 2 * 1024 * 1024
const MaxNodes = 2048

var allowedParameters = map[string]bool{"security": true, "type": true, "flow": true, "pbk": true, "fp": true, "sni": true, "sid": true, "spx": true, "encryption": true, "headerType": true, "support-x25519mlkem768": true}

func validText(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func parseNode(line string) (Node, error) {
	bad := errors.New("unsupported or invalid entry")
	u, err := url.Parse(line)
	if err != nil || u.Scheme != "vless" || u.User == nil || u.Opaque != "" || u.Path != "" {
		return Node{}, bad
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return Node{}, bad
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Node{}, bad
	}
	for _, part := range strings.Split(u.RawQuery, "&") {
		if part != "" && !strings.Contains(part, "=") {
			return Node{}, bad
		}
	}
	for k, v := range q {
		if !allowedParameters[k] || len(v) != 1 || !validText(v[0]) {
			return Node{}, bad
		}
	}
	if q.Get("security") != "reality" {
		return Node{}, bad
	}
	network := q.Get("type")
	if network != "" && network != "tcp" && network != "raw" {
		return Node{}, bad
	}
	if header := q.Get("headerType"); header != "" && header != "none" {
		return Node{}, bad
	}
	if flag, ok := q["support-x25519mlkem768"]; ok && flag[0] != "true" && flag[0] != "false" && flag[0] != "1" && flag[0] != "0" {
		return Node{}, bad
	}
	port := 0
	for _, c := range u.Port() {
		if c < '0' || c > '9' {
			return Node{}, bad
		}
		port = port*10 + int(c-'0')
		if port > 65535 {
			return Node{}, bad
		}
	}
	n := Node{Name: u.Fragment, Server: u.Hostname(), Port: port, UUID: u.User.Username(), Encryption: q.Get("encryption"), Flow: q.Get("flow"), Fingerprint: q.Get("fp"), SNI: q.Get("sni"), PublicKey: q.Get("pbk"), ShortID: q.Get("sid"), SpiderX: q.Get("spx")}
	if n.Name == "" {
		n.Name = n.Server
	}
	if n.Encryption == "" {
		n.Encryption = "none"
	}
	if n.Fingerprint == "" {
		n.Fingerprint = "chrome"
	}
	if !validText(n.Name) || !validText(n.UUID) || validateNode(n) != nil {
		return Node{}, bad
	}
	n.ID = nodeID(n)
	return n, nil
}
func ParseSubscription(data []byte) ([]Node, int, error) {
	if len(data) > MaxSubscriptionBytes {
		return nil, 0, errors.New("subscription exceeds 2 MiB")
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if !strings.Contains(text, "://") {
		encoded := strings.Join(strings.Fields(text), "")
		var decoded []byte
		var err error
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err = encoding.DecodeString(encoded)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, 0, errors.New("expected VLESS links or a Base64 subscription")
		}
		text = string(decoded)
	}
	var nodes []Node
	skipped := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := parseNode(line)
		if err != nil {
			skipped++
			continue
		}
		nodes = append(nodes, n)
		if len(nodes) > MaxNodes {
			return nil, skipped, errors.New("too many servers in subscription")
		}
	}
	if len(nodes) == 0 {
		return nil, skipped, errors.New("subscription has no supported VLESS TCP REALITY servers")
	}
	return nodes, skipped, nil
}
func SelectNode(nodes []Node, name, id string) (Node, error) {
	if (name == "") == (id == "") {
		return Node{}, errors.New("select a server by name or ID")
	}
	var selected Node
	count := 0
	for _, n := range nodes {
		if (id != "" && n.ID == id) || (id == "" && n.Name == name) {
			selected = n
			count++
		}
	}
	if count == 0 {
		return Node{}, errors.New("selected server not found; current connection retained")
	}
	if count != 1 {
		return Node{}, errors.New("ambiguous selection: use a unique server ID")
	}
	return selected, nil
}
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 8192 || !validText(raw) {
		return errors.New("provide a valid HTTPS subscription URL")
	}
	return nil
}
