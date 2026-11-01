CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    login         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    -- revision is the version of the latest change among the items of the
    -- user. Every item write bumps it under the row lock, so the writes of one
    -- user get their versions one by one and commit in that order: a client
    -- that asks for the changes after the version it has seen misses none.
    revision      BIGINT      NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE items (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The client chooses the ID, so it is unique per user only: two users
    -- cannot learn about each other's items through a taken ID.
    id         UUID        NOT NULL,
    type       SMALLINT    NOT NULL,
    -- data is encrypted by the client; a tombstone has it empty.
    data       BYTEA       NOT NULL,
    version    BIGINT      NOT NULL,
    deleted    BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id)
);

-- Sync asks for the changes of a user after a version.
CREATE INDEX items_user_id_version_idx ON items (user_id, version);
