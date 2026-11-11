# UXQA model module

Protocol adapters, subscription authorization flows and required supporting
packages were migrated from Magpie (MIT), source revision
`51bdc985bc413c9e87a976ba08bcfa1054a0b699` (v0.1.3050).
The original copyright and permission notice is in LICENSE. Original file hashes
are in UPSTREAM-MANIFEST.json. Module import paths retain the original namespace
for source compatibility; this is not the Magpie application or its executable.

UXQA additions: `cmd/uxqa-core`, `internal/appdir/uxqa.go`,
`internal/provider/uxqa.go` and their tests. Adaptations confine configuration,
cache and credentials to UXQA; disable external account discovery and switching;
prevent automatic CLI installation; use saved accounts as the native pool; and
brand authorization completion pages as UXQA. The host UI and private API belong
to UXQA. The original main, CLI, GUI and management web application are not bundled.

Claude subscriptions retain the upstream reliance on an already-installed
official Claude Code runtime. Real account authorization and vendor entitlement
are required; local fixture tests are not evidence of a working paid subscription.
