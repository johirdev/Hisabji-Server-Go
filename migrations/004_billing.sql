-- =============================================================================
-- 004_billing.sql — subscriptions, credits (tokens), features and payments
-- =============================================================================
--
-- THE BILLING MODEL, IN ONE PARAGRAPH
--
-- Everything premium is metered in a single internal currency: CREDITS (the
-- "tokens" of the token plan). There are exactly two ways to get credits:
--
--   1. SUBSCRIBE — a plan grants `monthly_credits` at the start of every
--      billing month and unlocks a set of modules. Predictable monthly cost,
--      best value for a regular user.
--   2. BUY A PACK — a one-off purchase of credits that never expire. No
--      commitment; ideal for a student who wants two AI reports a year.
--
-- Why one meter instead of two separate systems:
--
--   * AI calls cost real money per request. An "unlimited" plan is an open
--     invoice from the model provider; credits bound it without punishing
--     normal use.
--   * A free user can buy a pack and try any premium AI feature without ever
--     subscribing. That is the cheapest possible path from free to paying,
--     and it is impossible if premium is a plan-only flag.
--   * A new premium module ships as ONE ROW in `features` plus its code in a
--     plan's feature list. No billing code changes, ever.
--
-- Spend order is always: plan allowance first, purchased credits second. The
-- user's non-expiring credits are therefore preserved for as long as possible,
-- which is what they would choose themselves.
-- =============================================================================


-- =============================================================================
-- features — the catalogue of everything that can be gated or metered
-- =============================================================================
CREATE TABLE IF NOT EXISTS features (
    code         varchar(60) PRIMARY KEY,          -- 'ai_monthly_coach'
    name         varchar(120) NOT NULL,
    name_bn      varchar(120),
    description  varchar(500),
    description_bn varchar(500),

    -- 'module'    : access gate only, costs nothing to use (Advanced Reports)
    -- 'ai_action' : costs credits per invocation (AI Monthly Coach)
    kind         varchar(20)  NOT NULL DEFAULT 'module'
        CHECK (kind IN ('module','ai_action')),

    -- Zero means "free to use once you have access".
    credit_cost  integer      NOT NULL DEFAULT 0 CHECK (credit_cost >= 0),

    -- The cheapest tier that includes this feature in a plan. Used only to
    -- render "upgrade to Pro" copy; real access comes from the plan's
    -- feature list, so a promotional plan can include anything.
    min_tier     varchar(20)  NOT NULL DEFAULT 'free'
        CHECK (min_tier IN ('free','plus','pro','business')),

    -- Whether a user without the plan may still pay per use from purchased
    -- credits. This is the switch that makes the hybrid model work.
    payg_allowed boolean      NOT NULL DEFAULT true,

    category     varchar(40)  NOT NULL DEFAULT 'general',
    icon         varchar(40),
    sort_order   smallint     NOT NULL DEFAULT 100,
    is_active    boolean      NOT NULL DEFAULT true,

    created_at   timestamptz  NOT NULL DEFAULT now(),
    updated_at   timestamptz  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  features            IS 'Add a premium module by inserting one row here and listing its code in a plan.';
COMMENT ON COLUMN features.payg_allowed IS 'If true, a non-subscriber may run this feature by spending purchased credits.';

DROP TRIGGER IF EXISTS features_set_updated_at ON features;
CREATE TRIGGER features_set_updated_at BEFORE UPDATE ON features
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- subscription_plans
-- =============================================================================
CREATE TABLE IF NOT EXISTS subscription_plans (
    code             varchar(40) PRIMARY KEY,       -- 'pro_12m'
    name             varchar(120) NOT NULL,
    name_bn          varchar(120),
    tagline          varchar(200),
    tagline_bn       varchar(200),

    tier             varchar(20)  NOT NULL DEFAULT 'free'
        CHECK (tier IN ('free','plus','pro','business')),

    -- 0 means a non-expiring plan (the free tier). 1/3/6/12 are the paid cycles
    -- the product spec asks for.
    period_months    smallint     NOT NULL DEFAULT 0 CHECK (period_months >= 0),

    price            bigint       NOT NULL DEFAULT 0 CHECK (price >= 0),  -- paisa, for the WHOLE period
    -- list_price lets the UI show "৳3600  ৳2400 (save 33%)" without hardcoding
    -- the discount in the app.
    list_price       bigint       NOT NULL DEFAULT 0 CHECK (list_price >= 0),
    currency         char(3)      NOT NULL DEFAULT 'BDT',

    -- Credits granted at the start of every billing MONTH inside the period,
    -- not once for the whole period. A 12-month plan therefore refills monthly.
    monthly_credits  integer      NOT NULL DEFAULT 0 CHECK (monthly_credits >= 0),
    -- One-off bonus credited on purchase, used to sweeten longer commitments.
    signup_credits   integer      NOT NULL DEFAULT 0 CHECK (signup_credits >= 0),

    -- Unused allowance does not roll over by default: it is an allowance, not
    -- an asset. Purchased credits always roll over.
    allowance_rolls_over boolean  NOT NULL DEFAULT false,

    feature_codes    text[]       NOT NULL DEFAULT '{}',
    max_devices      smallint     NOT NULL DEFAULT 3 CHECK (max_devices BETWEEN 1 AND 20),
    trial_days       smallint     NOT NULL DEFAULT 0 CHECK (trial_days >= 0),

    is_active        boolean      NOT NULL DEFAULT true,
    is_popular       boolean      NOT NULL DEFAULT false,
    sort_order       smallint     NOT NULL DEFAULT 100,

    created_at       timestamptz  NOT NULL DEFAULT now(),
    updated_at       timestamptz  NOT NULL DEFAULT now()
);

COMMENT ON COLUMN subscription_plans.price         IS 'Total price for the whole period, in paisa. Divide by period_months for the effective monthly rate.';
COMMENT ON COLUMN subscription_plans.feature_codes IS 'Feature codes unlocked by this plan; each must exist in features.code.';

DROP TRIGGER IF EXISTS subscription_plans_set_updated_at ON subscription_plans;
CREATE TRIGGER subscription_plans_set_updated_at BEFORE UPDATE ON subscription_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- credit_packs — the token plan
-- =============================================================================
CREATE TABLE IF NOT EXISTS credit_packs (
    code          varchar(40) PRIMARY KEY,          -- 'tokens_100'
    name          varchar(120) NOT NULL,
    name_bn       varchar(120),

    credits       integer      NOT NULL CHECK (credits > 0),
    bonus_credits integer      NOT NULL DEFAULT 0 CHECK (bonus_credits >= 0),
    price         bigint       NOT NULL CHECK (price > 0),   -- paisa
    list_price    bigint       NOT NULL DEFAULT 0,
    currency      char(3)      NOT NULL DEFAULT 'BDT',

    -- 0 = never expires. Non-expiring is the honest default for something the
    -- user paid cash for.
    validity_days integer      NOT NULL DEFAULT 0 CHECK (validity_days >= 0),

    is_active     boolean      NOT NULL DEFAULT true,
    is_popular    boolean      NOT NULL DEFAULT false,
    sort_order    smallint     NOT NULL DEFAULT 100,

    created_at    timestamptz  NOT NULL DEFAULT now(),
    updated_at    timestamptz  NOT NULL DEFAULT now()
);

DROP TRIGGER IF EXISTS credit_packs_set_updated_at ON credit_packs;
CREATE TRIGGER credit_packs_set_updated_at BEFORE UPDATE ON credit_packs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- subscriptions — one row per purchase, never updated in place after expiry
-- =============================================================================
CREATE TABLE IF NOT EXISTS subscriptions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_code     varchar(40) NOT NULL REFERENCES subscription_plans(code),

    status        varchar(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','trialing','active','grace','expired','cancelled','refunded')),

    starts_at     timestamptz NOT NULL DEFAULT now(),
    ends_at       timestamptz NOT NULL,
    -- Access continues briefly after ends_at while a renewal payment settles,
    -- so a bank delay does not lock a paying user out of their own data.
    grace_until   timestamptz,

    -- The next monthly allowance refill inside a multi-month period.
    next_grant_at timestamptz,
    grants_made   smallint    NOT NULL DEFAULT 0,

    auto_renew    boolean     NOT NULL DEFAULT false,
    cancelled_at  timestamptz,
    cancel_reason varchar(300),

    -- Set when this row replaced an earlier one (renewal or upgrade), so the
    -- billing history is a chain rather than a set of unrelated rows.
    previous_id   uuid REFERENCES subscriptions(id) ON DELETE SET NULL,
    payment_id    uuid,

    price_paid    bigint      NOT NULL DEFAULT 0,
    currency      char(3)     NOT NULL DEFAULT 'BDT',

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT subscriptions_period_ordered CHECK (ends_at > starts_at)
);

-- A user may hold only ONE live subscription at a time. Enforced by the
-- database, not just by the service, because double-billing is unforgivable.
CREATE UNIQUE INDEX IF NOT EXISTS subscriptions_one_live_per_user
    ON subscriptions (user_id) WHERE status IN ('active','trialing','grace');

CREATE INDEX IF NOT EXISTS subscriptions_user_idx    ON subscriptions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscriptions_expiry_idx  ON subscriptions (ends_at)
    WHERE status IN ('active','trialing','grace');
CREATE INDEX IF NOT EXISTS subscriptions_grant_idx   ON subscriptions (next_grant_at)
    WHERE status = 'active';

DROP TRIGGER IF EXISTS subscriptions_set_updated_at ON subscriptions;
CREATE TRIGGER subscriptions_set_updated_at BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- credit_wallets — one row per user, the current balance
-- =============================================================================
CREATE TABLE IF NOT EXISTS credit_wallets (
    user_id            uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,

    -- Refilled by the subscription; lost at the next refill unless the plan
    -- says allowance_rolls_over.
    allowance_credits  integer     NOT NULL DEFAULT 0 CHECK (allowance_credits >= 0),
    -- Bought with money. Never expires unless the pack said so.
    purchased_credits  integer     NOT NULL DEFAULT 0 CHECK (purchased_credits >= 0),

    allowance_reset_at timestamptz,
    lifetime_granted   integer     NOT NULL DEFAULT 0,
    lifetime_purchased integer     NOT NULL DEFAULT 0,
    lifetime_used      integer     NOT NULL DEFAULT 0,

    -- Guards against a runaway loop or a compromised account burning through
    -- an expensive model. Checked before every debit.
    monthly_spend_cap  integer     NOT NULL DEFAULT 2000,
    month_spent        integer     NOT NULL DEFAULT 0,
    month_started_at   timestamptz NOT NULL DEFAULT date_trunc('month', now()),

    updated_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE credit_wallets IS 'Balances only. Every change is also written to credit_ledger in the same transaction.';

DROP TRIGGER IF EXISTS credit_wallets_set_updated_at ON credit_wallets;
CREATE TRIGGER credit_wallets_set_updated_at BEFORE UPDATE ON credit_wallets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- credit_ledger — append-only history of every credit movement
-- =============================================================================
-- The wallet is a cache of this table. When a user asks "where did my 40
-- credits go?", the answer must be a list of timestamped rows, not a guess.
-- Rows are never updated or deleted.
-- =============================================================================
CREATE TABLE IF NOT EXISTS credit_ledger (
    id               bigserial PRIMARY KEY,
    user_id          uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    delta            integer     NOT NULL CHECK (delta <> 0),   -- negative = spent
    allowance_after  integer     NOT NULL,
    purchased_after  integer     NOT NULL,

    reason           varchar(30) NOT NULL
        CHECK (reason IN ('signup_bonus','plan_grant','plan_signup_bonus','pack_purchase',
                          'feature_use','refund','expiry','admin_adjust','promo')),

    feature_code     varchar(60) REFERENCES features(code) ON DELETE SET NULL,
    reference_type   varchar(30),                              -- 'subscription' | 'payment' | 'ai_insight'
    reference_id     uuid,
    description      varchar(300),

    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS credit_ledger_user_idx    ON credit_ledger (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS credit_ledger_feature_idx ON credit_ledger (feature_code, created_at DESC);
CREATE INDEX IF NOT EXISTS credit_ledger_ref_idx     ON credit_ledger (reference_type, reference_id);


-- =============================================================================
-- payments — every money movement with the outside world
-- =============================================================================
CREATE TABLE IF NOT EXISTS payments (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    kind             varchar(20) NOT NULL CHECK (kind IN ('subscription','credit_pack')),
    reference_code   varchar(40) NOT NULL,             -- plan code or pack code
    quantity         smallint    NOT NULL DEFAULT 1 CHECK (quantity > 0),

    amount           bigint      NOT NULL CHECK (amount >= 0),   -- paisa
    currency         char(3)     NOT NULL DEFAULT 'BDT',

    provider         varchar(30) NOT NULL DEFAULT 'manual'
        CHECK (provider IN ('manual','bkash','nagad','rocket','sslcommerz','stripe','card','bank')),
    -- The gateway's own id. Unique per provider so a replayed webhook cannot
    -- credit the same payment twice.
    provider_ref     varchar(120),
    provider_payload jsonb       NOT NULL DEFAULT '{}'::jsonb,

    status           varchar(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','processing','paid','failed','cancelled','refunded')),
    failure_reason   varchar(300),

    -- Mirrors idempotency_keys for the specific case of a retried checkout.
    idempotency_key  varchar(120),

    paid_at          timestamptz,
    refunded_at      timestamptz,
    refund_amount    bigint      NOT NULL DEFAULT 0 CHECK (refund_amount >= 0),

    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT payments_refund_within_amount CHECK (refund_amount <= amount)
);

CREATE UNIQUE INDEX IF NOT EXISTS payments_provider_ref_uniq
    ON payments (provider, provider_ref) WHERE provider_ref IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS payments_idempotency_uniq
    ON payments (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS payments_user_idx   ON payments (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS payments_status_idx ON payments (status, created_at DESC);

DROP TRIGGER IF EXISTS payments_set_updated_at ON payments;
CREATE TRIGGER payments_set_updated_at BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- Close the loop: a subscription points at the payment that created it.
ALTER TABLE subscriptions
    DROP CONSTRAINT IF EXISTS subscriptions_payment_id_fkey;
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_payment_id_fkey
    FOREIGN KEY (payment_id) REFERENCES payments(id) ON DELETE SET NULL;
