# Live panel implementation plan

**Goal:** Implement the approved compact dark mockup with scoped and scheduled HTTPS checks, without automatic server switching.
**Architecture:** Go Manager owns an in-memory probe schedule and one sequential probe worker. Existing isolated Xray probes keep the active VPN intact. Browser-only favorites, collapse and sorting contain no subscription URLs or credentials.
**Tech stack:** Go standard library, embedded vanilla JavaScript and CSS. No new binaries or packages.
**Spec:** Approved xray-live-list mockup in this conversation.

- [x] Test scoped node/subscription checks, scheduling, Off, invalid settings, cancellation and no server switching.
- [x] Implement protected probe settings/API and process-owned scheduler; reuse probe mutex and retain old readings while checking.
- [x] Implement approved interface: active delay, age/staleness, selectable auto interval, favorites, sort, collapsible groups, individual checks.
- [x] Extend meaningful client tests for filtering/sorting/check state; preserve native login and logout race coverage.
- [x] Run Go race/vet, client tests, real browser visual checks and container integration; request one independent review.
- [x] Update README/screenshot, commit and publish checked changes and all four image platforms.

Constraints: English UI, dark only, no subscription editing, no automatic switching, sequential probes for limited router memory. Intervals Off/5/10/30 min; default 10 min. Active checks roughly 60 seconds when idle. Settings reset at container restart. Browser preferences may remain in local storage.
Review focus: request cancellation and logout; setting Off mid-check; selected-server changes during probes; refreshed catalogs invalidating IDs; stored browser preference validation.

Review: fixed active-check starvation during long sweeps, same-host preference collisions and keyboard focus loss. Removed obsolete mobile grid rules after real-browser screenshots.
