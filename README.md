# xray-panel

Lightweight VLESS REALITY control panel for MikroTik.

A small dark web panel for selecting servers, refreshing subscriptions and checking VPN latency. Uses Xray with native TUN. Subscription URLs are configured only through container environment variables.

Platforms: **Linux AMD64, ARM64 and ARMv7**. Alpine 3.24.3, Xray 26.9.9.

## Run on MikroTik

Requires RouterOS 7 with the container package enabled, a working container network and enough storage. The example assumes `xray-veth` is already configured as `172.18.20.6/30`, with gateway/DNS `172.18.20.5`, and VPN traffic is routed to it.

Add the environment variables:

```routeros
/container/envs/add list=xray-panel key=SUBSCRIPTION_URL value="https://provider.example/sub/YOUR_TOKEN"
/container/envs/add list=xray-panel key=PANEL_PASSWORD value="REPLACE_WITH_A_LONG_RANDOM_PASSWORD"
```

Optional second subscription:

```routeros
/container/envs/add list=xray-panel key=SUBSCRIPTION_URL_2 value="https://another-provider.example/sub/YOUR_TOKEN"
```

Create the container after building and publishing the image below:

```routeros
/container/config/set registry-url=https://registry-1.docker.io
/container/add name=xray-panel remote-image=gritsenko/xray-panel:test interface=xray-veth envlists=xray-panel dns=172.18.20.5 root-dir=/docker/xray-panel logging=yes start-on-boot=yes
/container/start [find where name="xray-panel"]
```

Open **http://172.18.20.6:8080** and sign in with `PANEL_PASSWORD`. Use a password of at least 12 characters. Keep the panel accessible only from your trusted network.

Edit existing entries instead of duplicating them. These commands assume an existing container network; they do not configure MikroTik firewall, NAT or VPN traffic routing. Mount persistent storage at `/opt/xray/state` to retain the password and selected server when replacing the container.

## Settings

| Variable | Default / purpose |
|---|---|
| `SUBSCRIPTION_URL`, `SUBSCRIPTION_URL_2` … `_16` | HTTPS subscription URLs |
| `PANEL_PASSWORD` | 12–256 characters; if omitted, printed once in the first-start container log |
| `SUBSCRIPTION_REFRESH_SECONDS` | `43200` (12 hours); `0` disables scheduled refresh |
| `SERVER_NAME` or `SERVER_ID` | Optional preferred initial server; set only one |
| `PANEL_ADDRESS` | `0.0.0.0:8080` |
| `PROBE_URL` | `https://www.gstatic.com/generate_204` |
| `SUBSCRIPTION_USER_AGENT` | `xray-mikrotik` (retained for provider compatibility) |
| `STATE_DIR` | `/opt/xray/state` |

Supports plain-text and Base64 `vless://` subscriptions: **IPv4, TCP/RAW + REALITY**, with optional `xtls-rprx-vision`. Other protocols/transports are skipped. Source names come from `profile-title`, falling back to the subscription hostname.

Startup connects to the saved server if reachable, otherwise the first working server. Explicit Stop remains stopped across restarts. Failed updates retain the previous configuration. Switching servers can interrupt connections. Latency is an HTTPS request through the VPN, not a bandwidth test.

## Build your own image

Requires Docker with Buildx. Xray is downloaded from its official release and checked against `build/xray-checksums.txt`; no binary archives are stored in this repository.

Build one platform locally (replace `linux/arm64` as needed):

```sh
docker buildx build --platform linux/arm64 --target final \
  --provenance=false --sbom=false \
  -t gritsenko/xray-panel:test --load .
```

Publish all three platforms under one tag. Replace the image name with your Docker Hub repository, then use that same name in `remote-image` on MikroTik:

```sh
docker login
docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 \
  --target final --provenance=false --sbom=false \
  -t gritsenko/xray-panel:test --push .
```

ARMv5 devices such as hEX Refresh are not supported by this Alpine image.

## Tests

```sh
go test -race ./...
go vet ./...
```

Optional network integration test inside a disposable privileged container:

```sh
docker buildx build --platform linux/arm64 --target test -t xray-panel-test --load .
docker run --rm --privileged --platform linux/arm64 xray-panel-test
```

The final runtime image contains no Go compiler, Python, curl, jq or web-server dependency. See [third-party notices](THIRD_PARTY.md).
