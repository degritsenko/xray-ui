#!/bin/sh
set -eu
[ -x /opt/xray-manager ] || { echo 'RED: panel manager is not present in the legacy image'; exit 1; }
TEST_ROOT=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
TMP=$(mktemp -d)
export SUBSCRIPTION_URL=https://198.51.100.20:8443/sub SUBSCRIPTION_NAME="Subscription 1" SUBSCRIPTION_REFRESH_SECONDS=0
mkdir -p "$TMP/runtime"
export TMPDIR="$TMP/runtime" PANEL_ADDRESS=0.0.0.0:8080 PANEL_PASSWORD=1234
cleanup() {
    [ -z "${MANAGER:-}" ] || kill "$MANAGER" 2>/dev/null || true
    [ -z "${VPN_SERVER:-}" ] || kill "$VPN_SERVER" 2>/dev/null || true
    [ -z "${FIXTURE:-}" ] || kill "$FIXTURE" 2>/dev/null || true
    ip netns del fixture 2>/dev/null || true
    rm -rf "$TMP"
}
trap cleanup EXIT
ip netns add fixture
ip link add wan0 type veth peer name fixture0
ip link set fixture0 netns fixture
ip addr add 10.200.0.2/30 dev wan0
ip link set wan0 up
ip netns exec fixture ip addr add 10.200.0.1/30 dev fixture0
ip netns exec fixture ip link set fixture0 up
ip netns exec fixture ip link set lo up
ip netns exec fixture ip addr add 198.51.100.20/32 dev lo
ip route replace default via 10.200.0.1 dev wan0
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$TMP/key.pem" -out "$TMP/cert.pem" -days 1 \
    -subj /CN=fixture -addext 'subjectAltName=IP:198.51.100.20' >/dev/null 2>&1
export SSL_CERT_FILE="$TMP/cert.pem"
/opt/xray/xray x25519 > "$TMP/reality-keys"
PRIVATE_KEY=$(sed -n 's/^PrivateKey: //p' "$TMP/reality-keys")
PUBLIC_KEY=$(sed -n 's/^Password.*: //p' "$TMP/reality-keys")
[ -n "$PRIVATE_KEY" ] && [ -n "$PUBLIC_KEY" ]
export PRIVATE_KEY
python3 - "$TMP" <<'SERVER'
import json, os, pathlib, sys
root=pathlib.Path(sys.argv[1])
config={"log":{"loglevel":"debug"},"inbounds":[{"listen":"0.0.0.0","port":443,"protocol":"vless","settings":{"clients":[{"id":"11111111-1111-4111-8111-111111111111"}],"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"target":"127.0.0.1:8444","serverNames":["fixture"],"privateKey":os.environ["PRIVATE_KEY"],"shortIds":["abcdef"]}}}],"outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","network":"tcp","ip":["198.51.100.20/32"],"port":"8443"}]}}]}
(root/'server.json').write_text(json.dumps(config))
SERVER
URI="vless://11111111-1111-4111-8111-111111111111@198.51.100.20:443?security=reality&type=tcp&pbk=$PUBLIC_KEY&fp=chrome&sni=fixture&sid=abcdef&support-x25519mlkem768=true#Test"
export PROBE_URL=https://198.51.100.20:8443/generate_204
printf '%s\n%s\n%s\n' "$URI" 'wireguard://secret@host' 'tuic://secret@host' > "$TMP/body"
ip netns exec fixture python3 "$TEST_ROOT/https-fixture.py" "$TMP" & FIXTURE=$!
ip netns exec fixture /opt/xray/xray run -config "$TMP/server.json" > "$TMP/server.log" 2>&1 & VPN_SERVER=$!
sleep 1
/opt/xray-manager > "$TMP/log" 2>&1 & MANAGER=$!
if ! ip netns exec fixture python3 "$TEST_ROOT/panel-client.py" "$TMP" first; then cat "$TMP/log" "$TMP/server.log"; exit 1; fi
ip -j route show default | jq -e 'any(.[]; .dev == "tun0")' >/dev/null
ip -j route get 198.51.100.20 oif wan0 | jq -e '.[0].dev == "wan0"' >/dev/null
kill "$MANAGER"; wait "$MANAGER"; MANAGER=
/opt/xray-manager >> "$TMP/log" 2>&1 & MANAGER=$!
if ! ip netns exec fixture python3 "$TEST_ROOT/panel-client.py" "$TMP" second; then cat "$TMP/log" "$TMP/server.log"; exit 1; fi
ip -j route get 198.51.100.20 | jq -e '.[0].dev == "tun0"' >/dev/null
kill "$MANAGER"; wait "$MANAGER"; MANAGER=
printf '%s\n%s\n%s\n' "$URI" 'wireguard://secret@host' 'tuic://secret@host' > "$TMP/body"
export SUBSCRIPTION_URL=https://198.51.100.20:8443/changed
/opt/xray-manager >> "$TMP/log" 2>&1 & MANAGER=$!
if ! ip netns exec fixture python3 "$TEST_ROOT/panel-client.py" "$TMP" source-change; then cat "$TMP/log" "$TMP/server.log"; exit 1; fi
rm -f "$TMP/request-started"
ip netns exec fixture python3 "$TEST_ROOT/panel-client.py" "$TMP" slow & CLIENT=$!
count=0
until [ -f "$TMP/request-started" ]; do count=$((count+1)); [ "$count" -le 30 ] || { cat "$TMP/log"; exit 1; }; sleep .1; done
kill "$MANAGER"
count=0
while kill -0 "$MANAGER" 2>/dev/null; do count=$((count+1)); [ "$count" -le 50 ] || { cat "$TMP/log"; exit 1; }; sleep .1; done
wait "$MANAGER"; MANAGER=
wait "$CLIENT"
kill "$VPN_SERVER"; wait "$VPN_SERVER" || true; VPN_SERVER=
if pgrep -x xray >/dev/null; then echo 'orphaned Xray'; exit 1; fi
[ -z "$(find "$TMP/runtime" -mindepth 1 -print -quit)" ]
echo 'panel runtime integration passed'
