package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
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

// Providers may duplicate mode and padding inside and outside JSON extra.
// Normalize them to the native Xray fields before computing the server ID.
func parseXHTTPSettings(q url.Values) (*XHTTPSettings, error) {
	x := &XHTTPSettings{Host: q.Get("host"), Path: q.Get("path"), Mode: q.Get("mode")}
	if x.Path == "" {
		x.Path = "/"
	}
	fields := map[string]json.RawMessage{}
	if raw, ok := q["extra"]; ok {
		if len(raw[0]) > 64*1024 || json.Unmarshal([]byte(raw[0]), &fields) != nil || fields == nil {
			return nil, errors.New("invalid XHTTP extra")
		}
	}
	if raw, ok := fields["mode"]; ok {
		var mode string
		if json.Unmarshal(raw, &mode) != nil {
			return nil, errors.New("invalid XHTTP extra mode")
		}
		if x.Mode == "" {
			x.Mode = mode
		}
		delete(fields, "mode")
	}
	if x.Mode == "" {
		x.Mode = "auto"
	}
	if padding, ok := q["x_padding_bytes"]; ok {
		bounds := strings.Split(padding[0], "-")
		if len(bounds) > 2 {
			return nil, errors.New("invalid XHTTP padding range")
		}
		previous := int64(0)
		for _, bound := range bounds {
			value, err := strconv.ParseInt(bound, 10, 32)
			if err != nil || value <= 0 || value < previous {
				return nil, errors.New("invalid XHTTP padding range")
			}
			previous = value
		}
		if _, present := fields["xPaddingBytes"]; !present {
			fields["xPaddingBytes"], _ = json.Marshal(padding[0])
		}
	}
	if len(fields) > 0 {
		raw, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		x.Extra, err = canonicalXHTTPExtra(raw)
		if err != nil {
			return nil, err
		}
	}
	return x, nil
}
