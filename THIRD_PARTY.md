# Third-party software

The image downloads the official [Xray-core](https://github.com/XTLS/Xray-core) v26.9.9 release at build time and verifies its SHA-256 checksum. Xray-core is licensed under [MPL-2.0](https://github.com/XTLS/Xray-core/blob/v26.9.9/LICENSE). Its license is included in the image at `/usr/share/licenses/xray/LICENSE`.

AMD64, ARM64 and ARMv7 images use Alpine Linux packages, which retain their upstream licenses.

The ARMv5 image uses the official BusyBox glibc image and selected ARM EABI libraries, iproute2, CA certificates and time-zone data from Debian 13 (Trixie). Debian package copyright notices and license texts are included under `/usr/share/licenses` and `/usr/share/common-licenses`. BusyBox is licensed under GPL-2.0; its source and image build instructions are available at [BusyBox](https://busybox.net/) and [docker-library/busybox](https://github.com/docker-library/busybox).

Go is used only in the build stage; the panel uses the Go standard library without additional module dependencies.
