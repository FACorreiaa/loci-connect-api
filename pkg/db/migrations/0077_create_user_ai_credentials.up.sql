-- +goose Up
-- +goose StatementBegin

-- The AI provider credential a user brought themselves.
--
-- One row per user, not one per provider. The product question is "who serves
-- my itineraries", and it has exactly one answer at a time; a second row would
-- need a second concept — which of them is active — bought at the price of a
-- composite key, for nothing.
CREATE TABLE user_ai_credentials (
    user_id       uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,

    -- Narrower than pkg/config's AI_PROVIDER values, and deliberately not
    -- CHECK-constrained against a literal list here: the valid set is the
    -- catalogue in pkg/ai/providers, and repeating it in SQL would make two
    -- places to edit and one of them to forget. The service validates.
    provider      text        NOT NULL,

    -- AES-256-GCM, sealed by pkg/secret with the user id as additional data —
    -- so a row copied to another user fails to open rather than quietly
    -- serving one person from another's account. Never logged, never rendered,
    -- never selected into anything that reaches a response.
    --
    -- The length floor catches a truncated blob: a sealed value is at least
    -- version + key id + nonce + tag, which is 30 bytes. It cannot catch a
    -- plaintext key, and nothing at this layer could — that guarantee lives in
    -- the repository, which has no method that accepts an unsealed one.
    api_key       bytea       NOT NULL CHECK (length(api_key) BETWEEN 30 AND 8192),

    -- The last few characters, kept in clear so the settings page can say
    -- "a key ending a203 is stored" and somebody can tell whether the key they
    -- are looking at is the one they think. Too short to narrow a guess.
    key_hint      text        NOT NULL DEFAULT '' CHECK (length(key_hint) <= 8),

    -- Empty means the catalogue's default for the provider. Storing the
    -- resolved default instead would freeze it: the row would keep naming a
    -- model long after we stopped recommending it.
    model         text        NOT NULL DEFAULT '' CHECK (length(model) <= 500),

    -- Set only for providers whose address the user supplies — today that is
    -- Hermes alone. Validated by providers.ParseGatewayURL before it is stored
    -- and again before it is dialled.
    base_url      text        NOT NULL DEFAULT '' CHECK (length(base_url) <= 500),

    -- Why the last call on this credential failed. It exists so the settings
    -- page can show a provider as failing instead of the account silently
    -- falling back to Loci's own key and the user wondering why their
    -- dashboard says the key is saved.
    --
    -- Bounded because it holds an upstream error message, which is not ours to
    -- trust the length of.
    last_error    text        NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),
    last_error_at timestamptz,

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE user_ai_credentials;
-- +goose StatementEnd
