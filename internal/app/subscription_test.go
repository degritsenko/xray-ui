package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const sampleURI = "vless://11111111-1111-4111-8111-111111111111@vpn.example.com:443?security=reality&type=tcp&flow=xtls-rprx-vision&pbk=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG&fp=chrome&sni=example.com&sid=abcdef#Germany%20%F0%9F%87%A9%F0%9F%87%AA"

func TestParseSubscriptionFormats(t *testing.T) {
	for _, data := range []string{sampleURI, base64.StdEncoding.EncodeToString([]byte(sampleURI)), base64.RawURLEncoding.EncodeToString([]byte(sampleURI))} {
		nodes, skipped, err := ParseSubscription([]byte(data))
		if err != nil || len(nodes) != 1 || skipped != 0 {
			t.Fatalf("parse failed: %v, nodes=%d, skipped=%d", err, len(nodes), skipped)
		}
		n := nodes[0]
		if n.Name != "Germany 🇩🇪" || n.Port != 443 || n.Flow != "xtls-rprx-vision" {
			t.Fatalf("wrong node: %#v", n)
		}
		renamed, _, _ := ParseSubscription([]byte(strings.Split(sampleURI, "#")[0] + "#Renamed"))
		if n.ID != renamed[0].ID || len(n.ID) != 64 {
			t.Fatal("ID changes on rename")
		}
	}
}
func TestMixedProviderMetadata(t *testing.T) {
	uri := strings.ReplaceAll(strings.ReplaceAll(sampleURI, "flow=xtls-rprx-vision&", ""), "fp=chrome", "fp=qq&support-x25519mlkem768=true")
	nodes, skipped, err := ParseSubscription([]byte("wireguard://secret@host\n" + uri + "\ntuic://secret@host"))
	if err != nil || len(nodes) != 1 || skipped != 2 || nodes[0].Fingerprint != "qq" || nodes[0].Flow != "" {
		t.Fatal("mixed provider import failed", err)
	}
}
func TestRejectUnsupportedAndMalformed(t *testing.T) {
	for _, uri := range []string{
		strings.Replace(sampleURI, "type=tcp", "type=ws", 1),
		strings.Replace(sampleURI, ":443?", ":99999?", 1),
		strings.Replace(sampleURI, "#Germany", "&unsupported#Germany", 1),
		strings.Replace(sampleURI, "#Germany", "&unknown=value#Germany", 1),
		strings.Replace(sampleURI, "#Germany", "&fp=firefox#Germany", 1),
		strings.Replace(sampleURI, "#Germany", "&support-x25519mlkem768=bad#Germany", 1),
		strings.Replace(sampleURI, "#Germany%20%F0%9F%87%A9%F0%9F%87%AA", "#Bad%00Name", 1),
		"<html>server error</html>",
	} {
		if _, _, err := ParseSubscription([]byte(uri)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, _, err := ParseSubscription(make([]byte, MaxSubscriptionBytes+1)); err == nil {
		t.Fatal("oversized subscription accepted")
	}
}
func TestSelectNode(t *testing.T) {
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	if _, err := SelectNode(nodes, "", ""); err == nil {
		t.Fatal("implicit selection")
	}
	if _, err := SelectNode(nodes, "missing", ""); err == nil {
		t.Fatal("missing selection accepted")
	}
	if _, err := SelectNode(append(nodes, nodes[0]), nodes[0].Name, ""); err == nil {
		t.Fatal("ambiguous name accepted")
	}
	if n, err := SelectNode(nodes, "", nodes[0].ID); err != nil || n.ID != nodes[0].ID {
		t.Fatal("ID selection failed")
	}
}
func TestRenderConfig(t *testing.T) {
	nodes, _, _ := ParseSubscription([]byte(sampleURI))
	data, err := RenderConfig(nodes[0], "192.0.2.1", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds  []struct{ Protocol string }
		Outbounds []struct {
			Settings struct {
				VNext []struct {
					Address string
					Users   []struct{ Flow string }
				}
			}
			StreamSettings struct{ Sockopt struct{ Interface string } }
		}
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Inbounds[0].Protocol != "tun" || cfg.Outbounds[0].Settings.VNext[0].Address != "192.0.2.1" || cfg.Outbounds[0].Settings.VNext[0].Users[0].Flow != "xtls-rprx-vision" || cfg.Outbounds[0].StreamSettings.Sockopt.Interface != "eth0" {
		t.Fatal("wrong config")
	}
}
func TestStableIDsMatchLegacyImporter(t *testing.T) {
	for _, sample := range []struct{ uri, id string }{
		{sampleURI, "3e451b855d0f18e97aec74490c017e9474082ad6a039ce49f6ce6e3935a26632"},
		{strings.Replace(sampleURI, "#Germany", "&spx=%2F%3Fa%3D1%26b%3D2#Germany", 1), "eeb8ade750687cdee6849a87efc906d0d7c6dd0c9ea823aa24b126b3a35277ac"},
	} {
		nodes, _, err := ParseSubscription([]byte(sample.uri))
		if err != nil || nodes[0].ID != sample.id {
			t.Fatal("legacy server ID changed", err)
		}
	}
}

func TestSubscriptionDeduplicatesEquivalentConnections(t *testing.T) {
	alias := strings.Split(sampleURI, "#")[0] + "#Alias"
	nodes, skipped, err := ParseSubscription([]byte(sampleURI + "\n" + alias + "\n" + strings.Replace(sampleURI, ":443?", ":8443?", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || skipped != 0 {
		t.Fatalf("expected two unique connections, got %d (skipped %d)", len(nodes), skipped)
	}
	if nodes[0].Name != "Germany 🇩🇪" {
		t.Fatal("first alias was not retained")
	}
	if _, err = SelectNode(nodes, "", nodes[0].ID); err != nil {
		t.Fatal("deduplicated connection cannot be selected", err)
	}
}
