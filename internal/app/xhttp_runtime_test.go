package app

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Run against the same Xray version shipped in the image; no external VPN needed.
func TestXHTTPRuntime(t *testing.T) {
	binary := os.Getenv("XRAY_BINARY")
	if binary == "" {
		t.Skip("set XRAY_BINARY to run local XHTTP integration")
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	fixture.EnableHTTP2 = true
	fixture.StartTLS()
	defer fixture.Close()
	certificate := fixture.TLS.Certificates[0]
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]})
	privateDER, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "xhttp-ok") }))
	defer target.Close()
	for _, security := range []string{"none", "tls", "reality"} {
		for _, mode := range []string{"auto", "packet-up", "stream-up", "stream-one"} {
			// REALITY always uses HTTP/2 and supports all four XHTTP modes.
			t.Run(security+"/"+mode, func(t *testing.T) {
				serverPort := freeXHTTPPort(t)
				stream := map[string]any{"network": "xhttp", "security": security, "xhttpSettings": map[string]any{"path": "/test-xhttp", "mode": "auto"}}
				if security == "tls" {
					stream["tlsSettings"] = map[string]any{"certificates": []any{map[string]any{"certificateFile": certFile, "keyFile": keyFile}}, "alpn": []string{"h2", "http/1.1"}}
				}
				if security == "reality" {
					stream["realitySettings"] = map[string]any{"target": fixture.Listener.Addr().String(), "serverNames": []string{"example.com"}, "privateKey": base64.RawURLEncoding.EncodeToString(private.Bytes()), "shortIds": []string{"abcdef"}}
				}
				server := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": serverPort, "protocol": "vless", "settings": map[string]any{"clients": []any{map[string]any{"id": "11111111-1111-4111-8111-111111111111"}}, "decryption": "none"}, "streamSettings": stream}}, "outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.0/8"}}}}}}}
				serverJSON, _ := json.Marshal(server)
				startXHTTPProcess(t, binary, serverJSON, serverPort, certFile)
				u, _ := url.Parse(xhttpURI(security))
				q := u.Query()
				q.Set("path", "/test-xhttp")
				q.Set("host", "example.com")
				q.Set("mode", mode)
				q.Set("x_padding_bytes", "100-1000")
				q.Set("extra", `{"mode":"auto","xPaddingBytes":"100-1000"}`)
				q.Set("pbk", base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()))
				q.Set("sid", "abcdef")
				u.RawQuery = q.Encode()
				u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(serverPort))
				n, err := parseNode(u.String())
				if err != nil {
					t.Fatal(err)
				}
				native, err := RenderConfig(n, "127.0.0.1", "lo")
				if err != nil {
					t.Fatal(err)
				}
				configPath := filepath.Join(t.TempDir(), "native.json")
				_ = os.WriteFile(configPath, native, 0600)
				if out, err := exec.Command(binary, "run", "-test", "-config", configPath).CombinedOutput(); err != nil {
					t.Fatalf("native config rejected: %v %s", err, out)
				}
				clientPort := freeXHTTPPort(t)
				clientJSON, err := RenderProbeConfig(n, "127.0.0.1", "lo", clientPort)
				if err != nil {
					t.Fatal(err)
				}
				startXHTTPProcess(t, binary, clientJSON, clientPort, certFile)
				proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", clientPort))
				transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
				response, err := client.Get(target.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "xhttp-ok" {
					t.Fatalf("wrong tunneled response: %q %v", body, err)
				}
			})
		}
	}
}
func freeXHTTPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func startXHTTPProcess(t *testing.T, binary string, data []byte, port int, certFile string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "xray.log"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "run", "-config", path)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+certFile)
	if err = cmd.Start(); err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		_ = log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Logf("Xray log: %s", data)
		}
	})
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Xray did not listen")
}
