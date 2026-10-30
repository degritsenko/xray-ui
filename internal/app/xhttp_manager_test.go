package app

import (
	"context"
	"encoding/json"
	"testing"
)

func TestXHTTPRefreshSelectAndRestart(t *testing.T) {
	m, r, _, id := testManager(t)
	r.body = sampleURI + "\n" + xhttpURI("tls")
	if err := m.Refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	view := m.Snapshot()
	if len(view.Subscriptions[0].Nodes) != 2 {
		t.Fatal("panel omitted XHTTP server")
	}
	node := m.stateCopy().Subscriptions[0].Nodes[1]
	if err := m.Activate(context.Background(), id, node.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []struct{ StreamSettings struct{ Network string } }
	}
	if err = json.Unmarshal(saved.Active.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Outbounds[0].StreamSettings.Network != "xhttp" || saved.Active.Node.XHTTP.Path != "/vpn?token=a&b=2" {
		t.Fatal("XHTTP selection not persisted")
	}
}
