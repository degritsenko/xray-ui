package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type XHTTPSettings struct {
	Host  string          `json:"host"`
	Path  string          `json:"path"`
	Mode  string          `json:"mode"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

// Empty fields retain the meaning of nodes saved before XHTTP support.
func (n Node) transport() string {
	if n.Network == "" || n.Network == "raw" {
		return "tcp"
	}
	return n.Network
}
func (n Node) security() string {
	if n.Security == "" {
		return "reality"
	}
	return n.Security
}

func canonicalXHTTPExtra(raw []byte) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 64*1024 || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errors.New("XHTTP extra must be a JSON object")
	}
	// Separate download endpoints need their own DNS, route pinning and interface
	// binding. Reject them until the runtime can manage both endpoints.
	allowed := strings.Fields("headers xPaddingBytes xPaddingObfsMode xPaddingKey xPaddingHeader xPaddingPlacement xPaddingMethod uplinkHTTPMethod sessionIDPlacement sessionIDKey sessionIDTable sessionIDLength seqPlacement seqKey uplinkDataPlacement uplinkDataKey uplinkChunkSize noGRPCHeader noSSEHeader scMaxEachPostBytes scMinPostsIntervalMs scMaxBufferedPosts scStreamUpServerSecs serverMaxHeaderBytes xmux")
	for key, value := range fields {
		if key == "downloadSettings" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(fields, key)
			continue
		}
		ok := false
		for _, name := range allowed {
			if key == name {
				ok = true
				break
			}
		}
		if !ok {
			return nil, errors.New("unsupported XHTTP extra field")
		}
	}
	// Decode with UseNumber so large integers survive canonicalization exactly.
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	delete(normalized.(map[string]any), "downloadSettings")
	return json.Marshal(normalized)
}

func validateTransport(n Node) error {
	bad := errors.New("invalid or unsupported VLESS transport")
	if n.transport() == "tcp" {
		if n.security() != "reality" || n.XHTTP != nil || len(n.ALPN) > 0 || n.Flow != "" && n.Flow != "xtls-rprx-vision" {
			return bad
		}
		return nil
	}
	if n.transport() != "xhttp" || n.Flow != "" || n.XHTTP == nil {
		return bad
	}
	if n.security() != "reality" && n.security() != "tls" && n.security() != "none" {
		return bad
	}
	x := n.XHTTP
	if !validText(x.Host) || !validText(x.Path) || !strings.HasPrefix(x.Path, "/") {
		return bad
	}
	switch x.Mode {
	case "auto", "packet-up", "stream-up", "stream-one":
	default:
		return bad
	}
	if len(x.Extra) > 0 {
		if _, err := canonicalXHTTPExtra(x.Extra); err != nil {
			return err
		}
	}
	if len(n.ALPN) > 0 && n.security() != "tls" {
		return bad
	}
	for _, protocol := range n.ALPN {
		if protocol == "" || !validText(protocol) || len(protocol) > 255 {
			return bad
		}
	}
	return nil
}

func streamSettings(n Node, iface string) map[string]any {
	stream := map[string]any{"network": n.transport(), "security": n.security(), "sockopt": map[string]any{"interface": iface}}
	switch n.security() {
	case "reality":
		stream["realitySettings"] = map[string]any{"fingerprint": n.Fingerprint, "serverName": n.SNI, "publicKey": n.PublicKey, "shortId": n.ShortID, "spiderX": n.SpiderX}
	case "tls":
		name := n.SNI
		if name == "" && n.XHTTP != nil {
			name = n.XHTTP.Host
		}
		if name == "" {
			name = n.Server
		}
		settings := map[string]any{"serverName": name, "fingerprint": n.Fingerprint}
		if len(n.ALPN) > 0 {
			settings["alpn"] = n.ALPN
		}
		stream["tlsSettings"] = settings
	}
	if n.transport() == "xhttp" {
		stream["xhttpSettings"] = n.XHTTP
	}
	return stream
}
