-- SPDX-License-Identifier: Apache-2.0
-- Explicit owner migration; never applied by the serving process.
CREATE TABLE control_delivery (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    generation bigint NOT NULL DEFAULT 0 CHECK(generation >= 0 AND generation < 9007199254740990),
    document jsonb NOT NULL CHECK(jsonb_typeof(document)='object'),
    envelope bytea CHECK(octet_length(envelope) BETWEEN 1 AND 65536),
    issued_at timestamptz,
    expires_at timestamptz,
    CHECK((envelope IS NULL)=(issued_at IS NULL)),
    CHECK((envelope IS NULL)=(expires_at IS NULL))
);
-- Existing unreviewed drafts are never activated during an upgrade.
INSERT INTO control_delivery(singleton,document)
SELECT true,jsonb_build_object('config',jsonb_build_object('schemaVersion',1,'revision',0,
 'profile',COALESCE((SELECT document->>'profile' FROM control_config WHERE singleton),'standard-10k'),
 'regionId',COALESCE((SELECT document->>'regionId' FROM control_config WHERE singleton),'local'),
 'rooms','[]'::jsonb),'runtimes','[]'::jsonb);
CREATE TABLE control_nodes (
    node_id text PRIMARY KEY CHECK(node_id IN ('gateway','coordinator')),
    generation bigint NOT NULL CHECK(generation>0),
    envelope_digest text NOT NULL CHECK(envelope_digest ~ '^[a-f0-9]{64}$'),
    observed_at timestamptz NOT NULL,
    metrics jsonb NOT NULL CHECK(jsonb_typeof(metrics)='array')
);
CREATE TABLE control_events (
    id text PRIMARY KEY CHECK(id ~ '^[A-Za-z0-9_-]{1,80}$'),
    room_id text NOT NULL,
    prequeue_at timestamptz NOT NULL,
    admit_at timestamptz NOT NULL,
    drain_at timestamptz NOT NULL,
    state text NOT NULL CHECK(state IN ('scheduled','running','paused_by_override','cancelled','completed')),
    CHECK(prequeue_at<admit_at AND admit_at<drain_at)
);
CREATE INDEX control_events_room ON control_events(room_id,prequeue_at);
CREATE INDEX control_events_due ON control_events(prequeue_at) WHERE state IN ('scheduled','running');
