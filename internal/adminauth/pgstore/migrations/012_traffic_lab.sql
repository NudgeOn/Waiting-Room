-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE traffic_lab_runs (
    id text PRIMARY KEY CHECK(id ~ '^[A-Za-z0-9_-]{43}$'),
    preset text NOT NULL CHECK(preset IN ('quick-20','smoke-1k')),
    state text NOT NULL CHECK(state IN ('queued','running','cancelling','passed','failed','cancelled','interrupted')),
    actor_id text NOT NULL,
    config_revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    deadline_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '120 seconds',
    report jsonb,
    CHECK(report IS NULL OR (jsonb_typeof(report)='object' AND octet_length(report::text)<=32768))
);
CREATE UNIQUE INDEX traffic_lab_one_active ON traffic_lab_runs ((true)) WHERE state IN ('queued','running','cancelling');
CREATE INDEX traffic_lab_recent ON traffic_lab_runs(created_at DESC);
