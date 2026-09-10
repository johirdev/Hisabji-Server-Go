-- =============================================================================
-- 005_ai_notifications.sql — AI output, forecasts, notifications and feedback
-- =============================================================================
-- Every AI answer is PERSISTED, not streamed and forgotten. Three reasons:
--
--   1. The user paid credits for it. Re-opening last month's coach report must
--      not cost again.
--   2. Regenerating the identical analysis from unchanged data is pure waste;
--      cache_key makes the repeat call free.
--   3. "Was this helpful?" feedback is only meaningful when it points at the
--      exact text the user actually read.
-- =============================================================================


-- =============================================================================
-- ai_insights
-- =============================================================================
CREATE TABLE IF NOT EXISTS ai_insights (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature_code   varchar(60) NOT NULL REFERENCES features(code),

    period_type    varchar(10) NOT NULL DEFAULT 'month'
        CHECK (period_type IN ('day','week','month','year','custom')),
    period_start   date        NOT NULL,
    period_end     date        NOT NULL,

    title          varchar(200) NOT NULL,
    title_bn       varchar(200),
    summary        text         NOT NULL,
    summary_bn     text,

    -- The structured half of the answer: metrics, per-category findings,
    -- recommended actions, projections. The client renders cards from this;
    -- `summary` is only the prose fallback.
    payload        jsonb        NOT NULL DEFAULT '{}'::jsonb,

    severity       varchar(10)  NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info','warning','critical')),

    -- Provenance. Without it you cannot tell whether last month's bad advice
    -- came from a model change or a prompt change.
    model          varchar(60),
    prompt_version varchar(20),
    credits_spent  integer      NOT NULL DEFAULT 0 CHECK (credits_spent >= 0),
    input_tokens   integer      NOT NULL DEFAULT 0,
    output_tokens  integer      NOT NULL DEFAULT 0,
    latency_ms     integer      NOT NULL DEFAULT 0,

    -- Hash of (user, feature, period, underlying data fingerprint). A repeat
    -- request with the same key returns this row instead of calling the model.
    cache_key      char(64),
    expires_at     timestamptz,

    -- Set to false when an expense inside the period is edited, so the client
    -- can offer "your data changed — regenerate?".
    is_current     boolean      NOT NULL DEFAULT true,

    created_at     timestamptz  NOT NULL DEFAULT now(),

    CONSTRAINT ai_insights_period_ordered CHECK (period_end >= period_start)
);

CREATE INDEX IF NOT EXISTS ai_insights_user_idx    ON ai_insights (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ai_insights_feature_idx ON ai_insights (user_id, feature_code, period_start DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ai_insights_cache_uniq
    ON ai_insights (user_id, cache_key) WHERE cache_key IS NOT NULL;


-- =============================================================================
-- forecasts — the prediction modules from the product spec
-- =============================================================================
-- Stored separately from ai_insights because most forecasts are ARITHMETIC,
-- not model output: month-end projection, cash runway and savings pace are
-- computed from the user's own numbers, cost no credits, and must stay
-- available on the free plan. Only the narrative explanation costs credits.
-- =============================================================================
CREATE TABLE IF NOT EXISTS forecasts (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    kind             varchar(30) NOT NULL
        CHECK (kind IN ('month_end_spend','month_end_balance','category_spend',
                        'budget_risk','cash_runway','savings','goal','yearly')),

    category_id      uuid REFERENCES categories(id) ON DELETE CASCADE,
    goal_id          uuid REFERENCES goals(id)      ON DELETE CASCADE,

    period_start     date        NOT NULL,
    period_end       date        NOT NULL,

    projected_amount bigint      NOT NULL DEFAULT 0,
    actual_amount    bigint,                        -- filled in after the period closes
    -- 0-100. Low when there are few data points; the UI must show estimates as
    -- estimates, never as promises.
    confidence       smallint    NOT NULL DEFAULT 50 CHECK (confidence BETWEEN 0 AND 100),
    -- Days of runway, or days until the goal — whichever the kind implies.
    projected_days   integer,

    -- The inputs the number came from, so the client can explain the maths.
    basis            jsonb       NOT NULL DEFAULT '{}'::jsonb,

    generated_at     timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL DEFAULT now() + interval '6 hours'
);

CREATE INDEX IF NOT EXISTS forecasts_user_kind_idx ON forecasts (user_id, kind, period_start DESC);
CREATE INDEX IF NOT EXISTS forecasts_cleanup_idx   ON forecasts (expires_at);


-- =============================================================================
-- notifications
-- =============================================================================
CREATE TABLE IF NOT EXISTS notifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    type        varchar(40)  NOT NULL,   -- 'budget_warning', 'recurring_due', 'goal_behind'
    title       varchar(200) NOT NULL,
    title_bn    varchar(200),
    body        varchar(1000) NOT NULL,
    body_bn     varchar(1000),

    -- Deep-link target: {"screen":"expense","id":"..."}
    data        jsonb        NOT NULL DEFAULT '{}'::jsonb,

    channel     varchar(10)  NOT NULL DEFAULT 'in_app'
        CHECK (channel IN ('in_app','push','sms','email')),
    priority    varchar(10)  NOT NULL DEFAULT 'normal'
        CHECK (priority IN ('low','normal','high')),

    read_at     timestamptz,
    sent_at     timestamptz,
    -- Keeps the notification list from becoming an archaeological dig.
    expires_at  timestamptz  NOT NULL DEFAULT now() + interval '60 days',

    created_at  timestamptz  NOT NULL DEFAULT now()
);

-- The badge count query: unread, newest first.
CREATE INDEX IF NOT EXISTS notifications_unread_idx ON notifications (user_id, created_at DESC)
    WHERE read_at IS NULL;
CREATE INDEX IF NOT EXISTS notifications_user_idx   ON notifications (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notifications_cleanup_idx ON notifications (expires_at);


-- =============================================================================
-- user_devices — push targets
-- =============================================================================
CREATE TABLE IF NOT EXISTS user_devices (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    push_token   text        NOT NULL,
    platform     varchar(10) NOT NULL CHECK (platform IN ('android','ios','web')),
    device_name  varchar(120),
    app_version  varchar(20),
    locale       varchar(5),

    is_active    boolean     NOT NULL DEFAULT true,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT user_devices_token_uniq UNIQUE (push_token)
);

CREATE INDEX IF NOT EXISTS user_devices_user_idx ON user_devices (user_id) WHERE is_active;


-- =============================================================================
-- feedback — the product growth loop
-- =============================================================================
-- One table for all of it: was the report helpful, did the recommendation
-- work, what feature is missing, why did you cancel. Analysed together, this
-- is what tells the roadmap what to build next.
-- =============================================================================
CREATE TABLE IF NOT EXISTS feedback (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    kind           varchar(30) NOT NULL
        CHECK (kind IN ('insight','recommendation','feature_request','cancel_reason',
                        'bug','category_correction','general')),

    reference_type varchar(30),                     -- 'ai_insight' | 'expense' | 'subscription'
    reference_id   uuid,

    helpful        boolean,
    rating         smallint CHECK (rating IS NULL OR rating BETWEEN 1 AND 5),
    message        varchar(2000),

    -- For a category correction: what the model guessed vs what the user chose.
    suggested_value varchar(120),
    corrected_value varchar(120),

    -- Did the user act on the advice, and did it help? This is the only honest
    -- measure of whether the AI layer is worth its cost.
    followed       boolean,
    outcome        varchar(20) CHECK (outcome IS NULL OR outcome IN ('improved','no_change','worse')),

    status         varchar(20) NOT NULL DEFAULT 'new'
        CHECK (status IN ('new','reviewed','planned','shipped','declined')),

    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS feedback_user_idx   ON feedback (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS feedback_kind_idx   ON feedback (kind, status, created_at DESC);
CREATE INDEX IF NOT EXISTS feedback_ref_idx    ON feedback (reference_type, reference_id);
