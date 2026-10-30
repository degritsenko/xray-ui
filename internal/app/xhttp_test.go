package app

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func xhttpURI(security string) string {
	u, _ := url.Parse(sampleURI)
	q := u.Query()
	q.Set("type", "xhttp")
	q.Del("flow")
	q.Set("security", security)
	q.Set("path", "/vpn?token=a&b=2")
	q.Set("host", "cdn.example.com")
	q.Set("mode", "packet-up")
	if security != "reality" {
		q.Del("pbk")
		q.Del("sid")
	}
	if security == "tls" {
		q.Set("alpn", "h2,http/1.1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func TestXHTTPImportAndRender(t *testing.T) {
	for _, security := range []string{"reality", "tls", "none"} {
		t.Run(security, func(t *testing.T) {
			nodes, skipped, err := ParseSubscription([]byte(sampleURI + "\n" + xhttpURI(security)))
			if err != nil || skipped != 0 || len(nodes) != 2 {
				t.Fatalf("XHTTP omitted: nodes=%d skipped=%d err=%v", len(nodes), skipped, err)
			}
			n := nodes[1]
			for _, probe := range []bool{false, true} {
				var data []byte
				if probe {
					data, err = RenderProbeConfig(n, "192.0.2.1", "eth0", 1080)
				} else {
					data, err = RenderConfig(n, "192.0.2.1", "eth0")
				}
				if err != nil {
					t.Fatal(err)
				}
				var cfg struct {
					Outbounds []struct {
						StreamSettings struct {
							Network, Security string
							XHTTPSettings     struct{ Host, Path, Mode string }
							TLSSettings       struct {
								ServerName string
								ALPN       []string
							}
							Sockopt struct{ Interface string }
						}
					}
				}
				if err = json.Unmarshal(data, &cfg); err != nil {
					t.Fatal(err)
				}
				s := cfg.Outbounds[0].StreamSettings
				if s.Network != "xhttp" || s.Security != security || s.XHTTPSettings.Path != "/vpn?token=a&b=2" || s.XHTTPSettings.Host != "cdn.example.com" || s.XHTTPSettings.Mode != "packet-up" || s.Sockopt.Interface != "eth0" {
					t.Fatalf("wrong transport: %s", data)
				}
				if security == "tls" && (s.TLSSettings.ServerName != "example.com" || len(s.TLSSettings.ALPN) != 2) {
					t.Fatalf("missing TLS settings: %s", data)
				}
			}
			data, _ := json.Marshal(n)
			var restored Node
			_ = json.Unmarshal(data, &restored)
			a, _ := RenderConfig(n, "192.0.2.1", "eth0")
			b, err := RenderConfig(restored, "192.0.2.1", "eth0")
			if err != nil || string(a) != string(b) {
				t.Fatal("transport lost after saving state")
			}
		})
	}
}

func TestXHTTPIdentityAndValidation(t *testing.T) {
	uri := xhttpURI("reality")
	n, err := parseNode(uri)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ key, value string }{{"path", "/other"}, {"host", "other.example.com"}, {"mode", "stream-up"}, {"extra", `{"noGRPCHeader":true}`}} {
		u, _ := url.Parse(uri)
		q := u.Query()
		q.Set(change.key, change.value)
		u.RawQuery = q.Encode()
		other, err := parseNode(u.String())
		if err != nil {
			t.Fatal(err)
		}
		if n.ID == other.ID {
			t.Fatalf("%s missing from server identity", change.key)
		}
	}
	for _, change := range []struct{ key, value string }{{"mode", "invalid"}, {"extra", "[]"}, {"extra", `{"downloadSettings":{"address":"other.example.com"}}`}, {"flow", "xtls-rprx-vision"}} {
		u, _ := url.Parse(uri)
		q := u.Query()
		q.Set(change.key, change.value)
		u.RawQuery = q.Encode()
		if _, err := parseNode(u.String()); err == nil {
			t.Fatalf("invalid %s accepted", change.key)
		}
	}
	if _, err := parseNode(strings.Replace(sampleURI, "type=tcp", "type=tcp&path=%2Fignored", 1)); err == nil {
		t.Fatal("silently ignored transport parameters")
	}
}

func TestXHTTPExtraCanonicalization(t *testing.T) {
	var ids []string
	for _, extra := range []string{`{"noGRPCHeader":true,"xmux":{"maxConcurrency":"4-8"}}`, `{ "xmux": {"maxConcurrency":"4-8"}, "noGRPCHeader": true }`} {
		u, _ := url.Parse(xhttpURI("tls"))
		q := u.Query()
		q.Set("extra", extra)
		u.RawQuery = q.Encode()
		n, err := parseNode(u.String())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
		data, err := RenderProbeConfig(n, "192.0.2.1", "eth0", 1080)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Outbounds []struct {
				StreamSettings struct{ XHTTPSettings XHTTPSettings }
			}
		}
		if err = json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err = json.Unmarshal(cfg.Outbounds[0].StreamSettings.XHTTPSettings.Extra, &settings); err != nil || settings["noGRPCHeader"] != true {
			t.Fatal("extra lost", err)
		}
	}
	if ids[0] != ids[1] {
		t.Fatal("JSON formatting changes server identity")
	}
	u, _ := url.Parse(xhttpURI("tls"))
	q := u.Query()
	q.Del("sni")
	q.Del("path")
	q.Del("mode")
	q.Set("type", "splithttp")
	u.RawQuery = q.Encode()
	n, err := parseNode(u.String())
	if err != nil {
		t.Fatal(err)
	}
	data, err := RenderConfig(n, "192.0.2.1", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []struct {
			StreamSettings struct {
				TLSSettings   struct{ ServerName string }
				XHTTPSettings XHTTPSettings
			}
		}
	}
	_ = json.Unmarshal(data, &cfg)
	if cfg.Outbounds[0].StreamSettings.TLSSettings.ServerName != "cdn.example.com" || n.XHTTP.Path != "/" || n.XHTTP.Mode != "auto" {
		t.Fatal("wrong transport defaults")
	}
}
