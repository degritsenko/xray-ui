package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type HysteriaSettings struct {
	Auth         string `json:"auth"`
	Insecure     bool   `json:"insecure,omitempty"`
	ObfsPassword string `json:"obfsPassword,omitempty"`
	Congestion   string `json:"congestion,omitempty"`
}

func validateHysteria(n Node) error {
	if n.Hysteria != nil && n.Hysteria.Insecure {
		return errors.New("Hysteria2 insecure TLS is not supported by this Xray version")
	}
	if n.Hysteria == nil || n.Hysteria.Auth == "" || len(n.Hysteria.Auth) > 4096 ||
		!validText(n.Hysteria.Auth) || !validText(n.Hysteria.ObfsPassword) ||
		!hostPattern.MatchString(n.Server) || n.Port < 1 || n.Port > 65535 ||
		!validText(n.SNI) || n.Network != "hysteria" || n.Security != "tls" {
		return errors.New("invalid Hysteria2 parameters")
	}
	return nil
}

func parseHysteria(u *url.URL) (Node, error) {
	bad := errors.New("unsupported or invalid Hysteria2 entry")
	if u.User == nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return Node{}, bad
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Node{}, bad
	}
	for k, values := range q {
		if len(values) != 1 || !validText(values[0]) {
			return Node{}, bad
		}
		switch k {
		case "sni", "insecure", "obfs", "obfs-password", "alpn", "security", "fm":
		default:
			return Node{}, bad
		}
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return Node{}, bad
		}
	}
	auth := u.User.Username()
	if password, ok := u.User.Password(); ok {
		auth += ":" + password
	}
	h := &HysteriaSettings{Auth: auth}
	if security := q.Get("security"); security != "" && security != "tls" {
		return Node{}, bad
	}
	if alpn := q.Get("alpn"); alpn != "" && alpn != "h3" {
		return Node{}, bad
	}
	if raw := q.Get("fm"); raw != "" {
		var mask struct {
			QuicParams struct {
				Debug      bool   `json:"debug"`
				Congestion string `json:"congestion"`
			} `json:"quicParams"`
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if len(raw) > 4096 || decoder.Decode(&mask) != nil {
			return Node{}, bad
		}
		switch mask.QuicParams.Congestion {
		case "", "bbr", "reno":
			h.Congestion = mask.QuicParams.Congestion
		default:
			return Node{}, bad
		}
	}
	switch q.Get("insecure") {
	case "", "0", "false":
	case "1", "true":
		return Node{}, errors.New("Hysteria2 insecure TLS is not supported by this Xray version")
	default:
		return Node{}, bad
	}
	if obfs := q.Get("obfs"); obfs != "" {
		if obfs != "salamander" || q.Get("obfs-password") == "" {
			return Node{}, bad
		}
		h.ObfsPassword = q.Get("obfs-password")
	} else if q.Get("obfs-password") != "" {
		return Node{}, bad
	}
	n := Node{Protocol: "hysteria2", Hysteria: h, Network: "hysteria", Security: "tls",
		Name: u.Fragment, Server: u.Hostname(), Port: port, SNI: q.Get("sni")}
	if n.Name == "" {
		n.Name = n.Server
	}
	if !validText(n.Name) || validateHysteria(n) != nil || strings.Contains(u.Host, "@") {
		return Node{}, bad
	}
	n.ID = nodeID(n)
	return n, nil
}

func hysteriaOutbound(n Node, ip, iface string) map[string]any {
	name := n.SNI
	if name == "" {
		name = n.Server
	}
	stream := map[string]any{
		"network": "hysteria", "security": "tls",
		"sockopt":          map[string]any{"interface": iface},
		"tlsSettings":      map[string]any{"serverName": name, "alpn": []string{"h3"}},
		"hysteriaSettings": map[string]any{"version": 2, "auth": n.Hysteria.Auth},
	}
	mask := map[string]any{}
	if n.Hysteria.Congestion != "" {
		mask["quicParams"] = map[string]any{"congestion": n.Hysteria.Congestion}
	}
	if n.Hysteria.ObfsPassword != "" {
		mask["udp"] = []any{map[string]any{
			"type": "salamander", "settings": map[string]any{"password": n.Hysteria.ObfsPassword},
		}}
	}
	if len(mask) > 0 {
		stream["finalmask"] = mask
	}
	return map[string]any{
		"tag": "proxy", "protocol": "hysteria",
		"settings":       map[string]any{"version": 2, "address": ip, "port": n.Port},
		"streamSettings": stream,
	}
}
