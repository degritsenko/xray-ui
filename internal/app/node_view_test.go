package app

import "testing"

func TestNodeViewConnectionTypes(t *testing.T) {
	for _, tc := range []struct {
		node                    Node
		network, security, flow string
	}{
		{Node{}, "tcp", "reality", ""},
		{Node{Flow: "xtls-rprx-vision"}, "tcp", "reality", "xtls-rprx-vision"},
		{Node{Network: "xhttp", Security: "tls"}, "xhttp", "tls", ""},
		{Node{Network: "xhttp", Security: "none"}, "xhttp", "none", ""},
	} {
		view := viewNode(tc.node)
		if view.Network != tc.network || view.Security != tc.security || view.Flow != tc.flow {
			t.Fatalf("connection metadata mismatch: %+v", view)
		}
	}
}
