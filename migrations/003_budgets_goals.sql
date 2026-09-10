-- =============================================================================
-- 003_budgets_goals.sql — the budget engine and the savings-goal system
-- =============================================================================
-- The product spec draws a sharp line between three different numbers, and the
-- schema keeps them separate so the dashboard can never confuse them:
--
--   planned_income   what the user expects to receive   (budgets.planned_income)
--   planned limit    what they intend to spend per      (budget_limits.limit_amount)
--                    category, some of it fixed
--   actual spending  what they really spent             (expenses.amount)
--
-- "Remaining" and "budget health" are always derived from those three at read
-- time. Nothing is cached in a column that could silently drift after an
-- expense is edited or deleted.
-- =============================================================================


-- =============================================================================
-- budgets — one row per user per budget cycle
-- =============================================================================
CREATE TABLE IF NOT EXISTS budgets (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- Explicit start/end rather than a "month" column, because a salaried
    -- user's cycle can run from the 7th to the 6th (users.month_start_day).
    period_start   date        NOT NULL,
    period_end     date        NOT NULL,
    period_type    varchar(10) NOT NULL DEFAULT 'monthly'
        CHECK (period_type IN ('weekly','monthly','yearly','custom')),

    planned_income bigint      NOT NULL DEFAULT 0 CHECK (planned_income >= 0),
    -- Optional explicit savings target for the cycle. Whatever is left after
    -- limits and this target is the discretionary pool.
    savings_target bigint      NOT NULL DEFAULT 0 CHECK (savings_target >= 0),

    title          varchar(120),
    note           varchar(500),
    status         varchar(10) NOT NULL DEFAULT 'active'
        CHECK (status IN ('draft','active','closed')),

    -- Warn at 80% of a limit and call it critical at 100% unless overridden.
    warn_percent   smallint    NOT NULL DEFAULT 80  CHECK (warn_percent BETWEEN 10 AND 100),
    -- Carry unspent money into the next cycle when the user rolls over.
    rollover       boolean     NOT NULL DEFAULT false,

    closed_at      timestamptz,
    deleted_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT budgets_period_ordered CHECK (period_end >= period_start)
);

-- A user cannot have two budgets starting on the same day; the API upserts
-- instead of creating a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS budgets_user_period_uniq
    ON budgets (user_id, period_start) WHERE deleted_at IS NULL;

-- "Which budget covers today?" — the single most frequent budget query.
CREATE INDEX IF NOT EXISTS budgets_user_range_idx
    ON budgets (user_id, period_start DESC, period_end DESC) WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS budgets_set_updated_at ON budgets;
CREATE TRIGGER budgets_set_updated_at BEFORE UPDATE ON budgets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- budget_limits — the per-category plan inside one budget
-- =============================================================================
CREATE TABLE IF NOT EXISTS budget_limits (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    budget_id     uuid        NOT NULL REFERENCES budgets(id)   ON DELETE CASCADE,
    user_id       uuid        NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    category_id   uuid        NOT NULL REFERENCES categories(id) ON DELETE CASCADE,

    limit_amount  bigint      NOT NULL CHECK (limit_amount >= 0),

    -- is_fixed marks rent, instalments and similar commitments. The AI never
    -- suggests cutting these, and "safe to spend today" subtracts them upfront
    -- rather than spreading them across the remaining days.
    is_fixed      boolean     NOT NULL DEFAULT false,

    -- Per-category override of budgets.warn_percent.
    warn_percent  smallint    CHECK (warn_percent IS NULL OR warn_percent BETWEEN 10 AND 100),
    note          varchar(300),

    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT budget_limits_unique_category UNIQUE (budget_id, category_id)
);

-- user_id is denormalised here purely so the generic owner-scoped repository
-- can filter without joining through budgets on every request.
CREATE INDEX IF NOT EXISTS budget_limits_user_idx   ON budget_limits (user_id);
CREATE INDEX IF NOT EXISTS budget_limits_budget_idx ON budget_limits (budget_id);

DROP TRIGGER IF EXISTS budget_limits_set_updated_at ON budget_limits;
CREATE TRIGGER budget_limits_set_updated_at BEFORE UPDATE ON budget_limits
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- goals — savings targets
-- =============================================================================
CREATE TABLE IF NOT EXISTS goals (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    title          varchar(120) NOT NULL,
    description    varchar(500),
    icon           varchar(40)  NOT NULL DEFAULT 'target',
    color          char(7)      NOT NULL DEFAULT '#2E7D32'
        CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),

    target_amount  bigint       NOT NULL CHECK (target_amount > 0),
    -- Maintained by the contribution endpoints inside the same transaction as
    -- the contribution row, so the two can never disagree.
    saved_amount   bigint       NOT NULL DEFAULT 0 CHECK (saved_amount >= 0),

    start_date     date         NOT NULL DEFAULT CURRENT_DATE,
    target_date    date,
    achieved_at    timestamptz,

    kind           varchar(20)  NOT NULL DEFAULT 'savings'
        CHECK (kind IN ('savings','emergency_fund','purchase','debt_payoff','travel','education')),
    priority       smallint     NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 3), -- 1 = highest
    status         varchar(12)  NOT NULL DEFAULT 'active'
        CHECK (status IN ('active','achieved','paused','cancelled')),

    -- What the AI computed the user must save each month to land on time.
    required_monthly bigint     NOT NULL DEFAULT 0 CHECK (required_monthly >= 0),

    deleted_at     timestamptz,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now(),

    CONSTRAINT goals_title_not_blank  CHECK (length(btrim(title)) > 0),
    CONSTRAINT goals_target_after_start CHECK (target_date IS NULL OR target_date >= start_date)
);

CREATE INDEX IF NOT EXISTS goals_user_status_idx ON goals (user_id, status, priority)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS goals_due_idx ON goals (target_date)
    WHERE status = 'active' AND deleted_at IS NULL;

DROP TRIGGER IF EXISTS goals_set_updated_at ON goals;
CREATE TRIGGER goals_set_updated_at BEFORE UPDATE ON goals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- goal_contributions — the ledger behind goals.saved_amount
-- =============================================================================
-- Keeping every deposit and withdrawal as its own row means the progress chart
-- is real history, and a mistaken contribution can be reversed without
-- guessing what the balance used to be.
-- =============================================================================
CREATE TABLE IF NOT EXISTS goal_contributions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id        uuid        NOT NULL REFERENCES goals(id)  ON DELETE CASCADE,
    user_id        uuid        NOT NULL REFERENCES users(id)  ON DELETE CASCADE,

    -- Negative means a withdrawal from the goal. saved_amount itself can never
    -- go below zero (CHECK on goals), so the service validates before writing.
    amount         bigint      NOT NULL CHECK (amount <> 0),
    contributed_at date        NOT NULL DEFAULT CURRENT_DATE,
    note           varchar(300),
    origin         varchar(20) NOT NULL DEFAULT 'manual'
        CHECK (origin IN ('manual','auto','rollover','correction')),

    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS goal_contributions_goal_idx ON goal_contributions (goal_id, contributed_at DESC);
CREATE INDEX IF NOT EXISTS goal_contributions_user_idx ON goal_contributions (user_id, contributed_at DESC);
