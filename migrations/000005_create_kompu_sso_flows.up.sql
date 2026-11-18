-- Single-use browser-SSO login attempts. Only SHA-256(state) is stored; the
-- PKCE verifier is derived from SSO_FLOW_HMAC_KEY and never stored.
CREATE TABLE public.kompu_sso_flows (
    state_hash bytea PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE INDEX idx_kompu_sso_flows_expiry
    ON public.kompu_sso_flows (expires_at)
    WHERE used_at IS NULL;
