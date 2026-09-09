-- SPDX-License-Identifier: Apache-2.0
CREATE TABLE control_key_acks (
 node_id text PRIMARY KEY REFERENCES control_nodes(node_id),
 generation bigint NOT NULL CHECK(generation>0 AND generation<9007199254740990),
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 observed_at timestamptz NOT NULL
);
CREATE TABLE control_key_operations (
 generation bigint PRIMARY KEY CHECK(generation>0),
 phase text NOT NULL CHECK(phase IN ('staged','active','stable')),
 digest text NOT NULL CHECK(digest ~ '^[a-f0-9]{64}$'),
 completed_at timestamptz NOT NULL
);
