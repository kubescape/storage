# CUSTOM_REST_ENABLED: one env var to switch all 11 Phase 4 flags at once

## Summary

Each of the 11 Phase 4 per-resource `rest.Storage` migrations
(`docs/features/generic-rest-storage-phase4.md`,
`docs/features/generic-rest-storage-containerprofile.md`,
`docs/features/generic-rest-storage-remaining-resources.md`) is gated behind
its own `config.Config.Custom<Resource>RestEnabled` boolean, settable only via
`config.json`. That's the right knob for us internally (roll back a single
misbehaving resource without touching the other 10), but it's 11 fields to
explain to a deployment that just wants one on/off switch for the whole
migration -- notably design-partner test environments evaluating the new
code path.

`CUSTOM_REST_ENABLED` is a new env var that, when set, overrides all 11
`Custom<Resource>RestEnabled` flags at once, regardless of what `config.json`
says for the individual flags.

## How it works

- `pkg/config.CustomRestEnabledEnvVar` is the env var name
  (`CUSTOM_REST_ENABLED`).
- `LoadConfig` applies it last, after `config.json` and every per-flag
  default have already been resolved: `applyCustomRestEnabledEnvVar` reads
  the env var via `os.LookupEnv` and, only when it's set to a non-empty
  value, parses it with `strconv.ParseBool` and overwrites all 11
  `Custom<Resource>RestEnabled` fields on the resulting `Config` with that
  value.
- Unset (or empty) is a pure no-op: every flag keeps whatever `config.json`
  (or its default) computed, exactly as before this env var existed. This is
  an override applied on top of the fully-resolved config, never a new
  default, so it can never mask a `config.json` parse error and never changes
  behavior for a deployment that doesn't set it.
- An unparseable value (anything `strconv.ParseBool` rejects, e.g. `"yes"`)
  is a loud `LoadConfig` error, not a silently-ignored one -- consistent with
  this file's existing `hostType` validation, which also fails loudly on an
  invalid value rather than falling back to a default.
- The individual `config.json` flags remain fully functional underneath this
  override: if a design partner's environment doesn't set the env var at
  all, per-resource control still works exactly as it did before this
  change.

## Verification

`pkg/config/config_test.go`'s `TestCustomRestEnabledEnvVar` covers: the env
var unset leaves a deliberately mixed `config.json` (some resources opted
out individually) untouched; setting it to `"true"`/`"false"` overrides
every one of the 11 flags regardless of `config.json`; an invalid value
returns an error rather than being ignored. Confirmed discriminating: the
test doesn't even compile without `CustomRestEnabledEnvVar` defined, and
fails to build against the pre-change code as a result. Full suite passes,
`gofmt`/`go vet` clean.
