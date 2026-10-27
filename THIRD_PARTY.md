# Third-party software

The image downloads the official [Xray-core](https://github.com/XTLS/Xray-core) v26.9.9 release at build time and verifies its SHA-256 checksum. Xray-core is licensed under [MPL-2.0](https://github.com/XTLS/Xray-core/blob/v26.9.9/LICENSE). Its license is included in the image at `/usr/share/licenses/xray/LICENSE`.

Alpine Linux packages retain their upstream licenses. Go is used only in the build stage; the panel uses the Go standard library without additional module dependencies.
