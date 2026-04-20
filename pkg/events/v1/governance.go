// Package eventsv1 governance policy — machine-enforced where possible.
//
// The event contract is the only cross-service dependency inside the
// SleepJiraBot DDD split; everything else is owned by one bounded
// context. Once a vN package is published to consumers we treat its
// wire format as frozen.
//
// Rules
//
//  1. Additive-only inside vN.
//     - New events: allowed. New producers/consumers can be added
//     without coordination.
//     - New optional fields on existing events: allowed, but must be
//     tagged `omitempty` and must decode safely on older consumers
//     (TestContract_AdditiveCompatibility verifies decoders tolerate
//     unknown fields).
//     - Renaming, removing, or changing the type of an existing field:
//     forbidden in vN. These are breaking changes.
//
//  2. Breaking changes require a new major version.
//     - Create pkg/events/v2 with the updated shape.
//     - Dual-publish on v1 and v2 for one release cycle so consumers
//     can migrate independently of producers.
//     - After the migration window, retire the v1 publisher and delete
//     the v1 package.
//
//  3. Subject scheme is part of the contract.
//     - Subjects follow `sjb.<context>.<aggregate>.<event>.vN`.
//     - A breaking change to a subject (e.g. splitting one subject
//     into two) is a v2-level change, even if the payload is
//     unchanged.
//
//  4. Envelope is part of the contract.
//     - The six keys (id, subject, published_at, schema_version,
//     trace_id, payload) are pinned by TestContract_EnvelopeShape.
//     - Adding a new envelope-level field bumps Envelope.SchemaVersion
//     and requires a migration note.
//
// Mechanical enforcement
//
//	contract_test.go — pins on-wire JSON for all 14 current events via
//	golden strings + assert.JSONEq. Rename/remove/type-change fails CI.
//	Adding an optional omitempty field is safe — the absent-key test
//	in TestContract_OmitemptyFields catches accidental required
//	downgrades.
//
// Governance-by-review
//
//	Changes under pkg/events/v1 should be treated as API changes even
//	when they look like local edits. Reviewers check: is this additive?
//	Does it roundtrip under the golden test? Does any new field have
//	omitempty?
//
// This file intentionally contains no code — it is the executable
// expression of the contract policy, read alongside contract_test.go.
package eventsv1
