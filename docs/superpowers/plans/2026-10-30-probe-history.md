# Five-check history

Approved design: five segments beneath each server address, oldest left, newest right. Green success, red failure, gray unknown. Use existing completed HTTPS probes only. In-memory history resets on restart. No persistence, new requests, settings or dependencies.

Implementation: add a bounded immutable boolean history to ProbeResult in probe.go and manager_probe.go; canceled probes add nothing. Render five accessible segments in app.js and style.css. Test bounded ordering, snapshot isolation and cancellation; run panel tests and browser layout checks. Publish only linux/arm64 as gritsenko/xray-ui:arm64-test.
