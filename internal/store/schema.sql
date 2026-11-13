CREATE TABLE IF NOT EXISTS admins(id text PRIMARY KEY, username text UNIQUE NOT NULL, password bytea NOT NULL, totp bytea NOT NULL, last_totp bigint NOT NULL DEFAULT 0, role text NOT NULL DEFAULT 'admin');
CREATE TABLE IF NOT EXISTS sessions(hash text PRIMARY KEY, admin_id text REFERENCES admins(id) ON DELETE CASCADE, expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS groups(id text PRIMARY KEY, name text UNIQUE NOT NULL);
INSERT INTO groups(id,name) VALUES ('default','پیش‌فرض') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS users(id text PRIMARY KEY, name text NOT NULL UNIQUE, enabled boolean NOT NULL, expires_at timestamptz NOT NULL, quota_bytes bigint NOT NULL CHECK(quota_bytes>=0), used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0), group_id text NOT NULL REFERENCES groups(id), token text NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS nodes(id text PRIMARY KEY, name text NOT NULL, host text NOT NULL UNIQUE, domain text NOT NULL UNIQUE, group_id text NOT NULL REFERENCES groups(id), enabled boolean NOT NULL DEFAULT true, ready boolean NOT NULL DEFAULT false, verified jsonb NOT NULL DEFAULT '[]', last_seen timestamptz, applied_version text NOT NULL DEFAULT '', last_error text NOT NULL DEFAULT '', reality_target text NOT NULL, reality_sni text NOT NULL, reality_private bytea NOT NULL, reality_public text NOT NULL, short_id text NOT NULL, ws_path text NOT NULL, tls_cert bytea, tls_key bytea, tls_expiry timestamptz, client_serial text NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS credentials(user_id text REFERENCES users(id) ON DELETE CASCADE, node_id text REFERENCES nodes(id) ON DELETE CASCADE, uuid text NOT NULL, password bytea NOT NULL, PRIMARY KEY(user_id,node_id));
CREATE TABLE IF NOT EXISTS jobs(id text PRIMARY KEY,node_id text REFERENCES nodes(id) ON DELETE CASCADE,status text NOT NULL,step text NOT NULL,error text NOT NULL DEFAULT '',updated_at timestamptz NOT NULL DEFAULT now());
CREATE UNIQUE INDEX IF NOT EXISTS one_active_job ON jobs(node_id) WHERE status IN ('queued','running');
CREATE TABLE IF NOT EXISTS reports(node_id text REFERENCES nodes(id) ON DELETE CASCADE, id text NOT NULL,run_id text NOT NULL,sequence bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(node_id,id), UNIQUE(node_id,run_id,sequence));
CREATE TABLE IF NOT EXISTS audit(id bigserial PRIMARY KEY, admin_id text NOT NULL, action text NOT NULL, target text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS settings(id text PRIMARY KEY, value bytea NOT NULL);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS previous_serial text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS previous_until timestamptz;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS client_cert bytea;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS client_key bytea;
-- Keep legacy columns for compatibility, but erase the retired second-factor material.
ALTER TABLE admins ALTER COLUMN totp SET DEFAULT ''::bytea;
UPDATE admins SET totp=''::bytea,last_totp=0 WHERE octet_length(totp)>0 OR last_totp<>0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS shared_host boolean NOT NULL DEFAULT false;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_port integer NOT NULL DEFAULT 443 CHECK(reality_port BETWEEN 1 AND 65535);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS websocket_port integer NOT NULL DEFAULT 8443 CHECK(websocket_port BETWEEN 1 AND 65535);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS trojan_port integer NOT NULL DEFAULT 2053 CHECK(trojan_port BETWEEN 1 AND 65535);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS stats_port integer NOT NULL DEFAULT 10085 CHECK(stats_port BETWEEN 1 AND 65535);
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS reality_behind_nginx boolean NOT NULL DEFAULT false;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS route_quality jsonb NOT NULL DEFAULT '{}';
CREATE TABLE IF NOT EXISTS bot_plans (
    id text PRIMARY KEY, name text NOT NULL, days integer NOT NULL CHECK(days BETWEEN 1 AND 3650),
    quota_bytes bigint NOT NULL CHECK(quota_bytes>=0), group_id text NOT NULL REFERENCES groups(id),
    stars integer NOT NULL CHECK(stars>=0), enabled boolean NOT NULL
);
CREATE TABLE IF NOT EXISTS bot_links (
    telegram_id bigint PRIMARY KEY, user_id text NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS bot_codes (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    hash text NOT NULL UNIQUE, expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS bot_orders (
    id text PRIMARY KEY, telegram_id bigint NOT NULL, user_id text,
    plan_id text NOT NULL REFERENCES bot_plans(id), plan_name text NOT NULL,
    group_id text NOT NULL REFERENCES groups(id), days integer NOT NULL, quota_bytes bigint NOT NULL,
    stars integer NOT NULL, status text NOT NULL, charge_id text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE bot_orders ADD COLUMN IF NOT EXISTS checkout_id text;
ALTER TABLE bot_plans ADD COLUMN IF NOT EXISTS purchase_limit integer NOT NULL DEFAULT 0 CHECK(purchase_limit BETWEEN 0 AND 2);
ALTER TABLE bot_orders ADD COLUMN IF NOT EXISTS fulfilled_at timestamptz;
ALTER TABLE bot_orders ADD COLUMN IF NOT EXISTS approved_at timestamptz;
CREATE INDEX IF NOT EXISTS bot_orders_plan_buyer ON bot_orders(plan_id,telegram_id);
CREATE INDEX IF NOT EXISTS bot_orders_plan_user ON bot_orders(plan_id,user_id);
CREATE INDEX IF NOT EXISTS bot_orders_created_at ON bot_orders(created_at DESC);
CREATE TABLE IF NOT EXISTS bulk_requests (
    admin_id text NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    key text NOT NULL, digest text NOT NULL, PRIMARY KEY(admin_id,key)
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_token_hash text;
CREATE UNIQUE INDEX IF NOT EXISTS account_token_hash_unique ON users(account_token_hash);
CREATE TABLE IF NOT EXISTS user_node_activity (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id text NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    sampled_at timestamptz NOT NULL,
    PRIMARY KEY(user_id,node_id)
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_days integer NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS next_reset_at timestamptz;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_reset_at timestamptz;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS accounting_sample_at timestamptz;
CREATE INDEX IF NOT EXISTS users_reset_due ON users(next_reset_at) WHERE next_reset_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS webhook_config (
    singleton boolean PRIMARY KEY CHECK(singleton), url text NOT NULL, secret bytea NOT NULL
);
CREATE TABLE IF NOT EXISTS webhook_state (
    kind text NOT NULL, target text NOT NULL, status text NOT NULL, PRIMARY KEY(kind,target)
);
CREATE TABLE IF NOT EXISTS webhook_events (
    id text PRIMARY KEY, kind text NOT NULL, target text NOT NULL, status text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(), next_attempt timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0, delivery text NOT NULL DEFAULT 'pending'
);
CREATE INDEX IF NOT EXISTS webhook_due ON webhook_events(next_attempt) WHERE delivery='pending';
CREATE OR REPLACE FUNCTION observe_user_webhook() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_kind text;
    event_status text;
    previous_status text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM webhook_config) THEN RETURN NEW; END IF;
    PERFORM pg_advisory_xact_lock_shared(503812);
    IF NOT EXISTS (SELECT 1 FROM webhook_config) THEN RETURN NEW; END IF;
    FOR event_kind,event_status IN
        SELECT 'user.enabled', CASE WHEN NEW.enabled THEN 'enabled' ELSE 'suspended' END
        UNION ALL SELECT 'user.quota', CASE WHEN NEW.quota_bytes>0 AND NEW.used_bytes>=NEW.quota_bytes THEN 'exhausted' ELSE 'available' END
    LOOP
        SELECT status INTO previous_status FROM webhook_state WHERE kind=event_kind AND target=NEW.id FOR UPDATE;
        IF previous_status IS NOT NULL AND previous_status<>event_status AND event_status NOT IN ('valid','available') THEN
            INSERT INTO webhook_events(id,kind,target,status) VALUES(gen_random_uuid()::text,event_kind,NEW.id,event_status);
        END IF;
        INSERT INTO webhook_state(kind,target,status) VALUES(event_kind,NEW.id,event_status)
            ON CONFLICT(kind,target) DO UPDATE SET status=excluded.status WHERE webhook_state.status<>excluded.status;
    END LOOP;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE TRIGGER user_webhook_changes
AFTER INSERT OR UPDATE OF enabled,expires_at,quota_bytes,used_bytes ON users
FOR EACH ROW EXECUTE FUNCTION observe_user_webhook();
