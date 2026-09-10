-- =============================================================================
-- 002_transactions.sql — categories, expenses, incomes and recurring rules
-- =============================================================================
-- Design note on dates: spent_at and received_at are DATE, not timestamptz.
--
-- A user records "I spent 200 taka on Tuesday", not "at 14:32:05.221 UTC". A
-- DATE makes every daily/weekly/monthly roll-up exact and timezone-proof — a
-- user travelling to another country does not silently move yesterday's lunch
-- into today's total. The audit trail of when the row was actually entered
-- lives in created_at.
-- =============================================================================


-- =============================================================================
-- categories
-- =============================================================================
-- A row with user_id IS NULL is a system category available to everyone (Food,
-- Transport, Rent, ...). A row with a user_id is that user's own category.
-- One table for both keeps every join and every filter simple.
-- =============================================================================
CREATE TABLE IF NOT EXISTS categories (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid REFERENCES users(id) ON DELETE CASCADE,
    parent_id      uuid REFERENCES categories(id) ON DELETE SET NULL,

    slug           varchar(60)  NOT NULL,          -- stable key: 'food', 'transport'
    name           varchar(60)  NOT NULL,          -- English label
    name_bn        varchar(60),                    -- Bangla label
    icon           varchar(40)  NOT NULL DEFAULT 'wallet',
    color          char(7)      NOT NULL DEFAULT '#6B7280'
        CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),

    kind           varchar(10)  NOT NULL DEFAULT 'expense'
        CHECK (kind IN ('expense','income')),

    -- Fixed categories (rent, instalments) are excluded from "controllable
    -- spending" advice: telling a user to spend less on rent is useless.
    is_fixed       boolean      NOT NULL DEFAULT false,
    is_system      boolean      NOT NULL DEFAULT false,
    is_active      boolean      NOT NULL DEFAULT true,
    sort_order     smallint     NOT NULL DEFAULT 100,

    -- Optional default monthly cap, copied into a new budget as a starting point.
    default_limit  bigint       CHECK (default_limit IS NULL OR default_limit >= 0),

    deleted_at     timestamptz,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now(),

    CONSTRAINT categories_name_not_blank CHECK (length(btrim(name)) > 0),
    -- A system category can never carry an owner, and vice versa.
    CONSTRAINT categories_system_has_no_owner CHECK (
        (is_system AND user_id IS NULL) OR (NOT is_system AND user_id IS NOT NULL)
    )
);

-- One name per user per kind. Two partial unique indexes rather than one
-- constraint, because NULL user_id (system rows) does not deduplicate in a
-- normal UNIQUE.
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_slug_uniq
    ON categories (user_id, kind, slug) WHERE user_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS categories_system_slug_uniq
    ON categories (kind, slug) WHERE user_id IS NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS categories_owner_idx ON categories (user_id, kind, sort_order)
    WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS categories_set_updated_at ON categories;
CREATE TRIGGER categories_set_updated_at BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- recurring_rules — fixed monthly expenses and salaries
-- =============================================================================
-- The rule is the template; the rows it generates are ordinary expenses/incomes
-- carrying recurring_rule_id. That way a generated row can be edited or deleted
-- like any other, and analytics need no special cases.
-- =============================================================================
CREATE TABLE IF NOT EXISTS recurring_rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id     uuid REFERENCES categories(id) ON DELETE SET NULL,

    kind            varchar(10) NOT NULL CHECK (kind IN ('expense','income')),
    title           varchar(120) NOT NULL,
    amount          bigint      NOT NULL CHECK (amount > 0),
    note            varchar(500),
    payment_method  varchar(20),

    frequency       varchar(10) NOT NULL
        CHECK (frequency IN ('daily','weekly','monthly','yearly')),
    interval_count  smallint    NOT NULL DEFAULT 1 CHECK (interval_count BETWEEN 1 AND 60),
    day_of_month    smallint    CHECK (day_of_month BETWEEN 1 AND 31),
    weekday         smallint    CHECK (weekday BETWEEN 0 AND 6),  -- 0 = Sunday

    start_date      date        NOT NULL,
    end_date        date,
    next_run_on     date        NOT NULL,
    last_run_on     date,
    runs_created    integer     NOT NULL DEFAULT 0,

    -- auto_post=false means the job only creates a "confirm this?" notification,
    -- which is what the product spec asks for: the user's permission first.
    auto_post       boolean     NOT NULL DEFAULT false,
    is_active       boolean     NOT NULL DEFAULT true,

    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT recurring_end_after_start CHECK (end_date IS NULL OR end_date >= start_date)
);

-- The worker's only query: "which rules are due today?"
CREATE INDEX IF NOT EXISTS recurring_due_idx ON recurring_rules (next_run_on)
    WHERE is_active AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS recurring_user_idx ON recurring_rules (user_id, kind)
    WHERE deleted_at IS NULL;

DROP TRIGGER IF EXISTS recurring_rules_set_updated_at ON recurring_rules;
CREATE TRIGGER recurring_rules_set_updated_at BEFORE UPDATE ON recurring_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- expenses
-- =============================================================================
CREATE TABLE IF NOT EXISTS expenses (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id       uuid REFERENCES categories(id) ON DELETE SET NULL,
    recurring_rule_id uuid REFERENCES recurring_rules(id) ON DELETE SET NULL,

    amount            bigint      NOT NULL CHECK (amount > 0),
    currency          char(3)     NOT NULL DEFAULT 'BDT',
    spent_at          date        NOT NULL,

    note              varchar(500),
    merchant          varchar(120),
    payment_method    varchar(20) NOT NULL DEFAULT 'cash'
        CHECK (payment_method IN ('cash','card','bkash','nagad','rocket','bank','due','other')),
    tags              text[]      NOT NULL DEFAULT '{}',
    attachment_path   text,

    -- Where the row came from, so the UI can badge auto-generated rows and the
    -- AI can weight manual entries differently.
    source            varchar(20) NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual','recurring','import','ai')),

    -- Set by the leak detector; lets the client highlight a row without
    -- recomputing the analysis.
    is_anomaly        boolean     NOT NULL DEFAULT false,

    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    -- A date far in the future is always a typo (2206 instead of 2026) and
    -- would wreck every projection.
    CONSTRAINT expenses_date_sane CHECK (spent_at BETWEEN DATE '2000-01-01' AND DATE '2100-01-01')
);

COMMENT ON COLUMN expenses.amount   IS 'Positive amount in paisa. Refunds are recorded as income, never as a negative expense.';
COMMENT ON COLUMN expenses.spent_at IS 'The day the money left the user, in their own timezone.';

-- The list endpoint's exact access pattern: one user, newest first.
CREATE INDEX IF NOT EXISTS expenses_user_date_idx     ON expenses (user_id, spent_at DESC, id DESC)
    WHERE deleted_at IS NULL;
-- Category breakdowns for the dashboard and monthly report.
CREATE INDEX IF NOT EXISTS expenses_user_cat_date_idx ON expenses (user_id, category_id, spent_at DESC)
    WHERE deleted_at IS NULL;
-- Free-text search over notes and merchants.
CREATE INDEX IF NOT EXISTS expenses_note_trgm_idx     ON expenses USING gin (note gin_trgm_ops);
CREATE INDEX IF NOT EXISTS expenses_merchant_trgm_idx ON expenses USING gin (merchant gin_trgm_ops);
CREATE INDEX IF NOT EXISTS expenses_tags_idx          ON expenses USING gin (tags);
CREATE INDEX IF NOT EXISTS expenses_recurring_idx     ON expenses (recurring_rule_id)
    WHERE recurring_rule_id IS NOT NULL;

DROP TRIGGER IF EXISTS expenses_set_updated_at ON expenses;
CREATE TRIGGER expenses_set_updated_at BEFORE UPDATE ON expenses
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- incomes
-- =============================================================================
CREATE TABLE IF NOT EXISTS incomes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id       uuid REFERENCES categories(id) ON DELETE SET NULL,
    recurring_rule_id uuid REFERENCES recurring_rules(id) ON DELETE SET NULL,

    amount            bigint      NOT NULL CHECK (amount > 0),
    currency          char(3)     NOT NULL DEFAULT 'BDT',
    received_at       date        NOT NULL,

    source            varchar(120),                  -- 'Salary', 'Tuition', 'Shop sales'
    note              varchar(500),
    payment_method    varchar(20) NOT NULL DEFAULT 'cash'
        CHECK (payment_method IN ('cash','card','bkash','nagad','rocket','bank','other')),

    origin            varchar(20) NOT NULL DEFAULT 'manual'
        CHECK (origin IN ('manual','recurring','import','ai')),

    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT incomes_date_sane CHECK (received_at BETWEEN DATE '2000-01-01' AND DATE '2100-01-01')
);

CREATE INDEX IF NOT EXISTS incomes_user_date_idx   ON incomes (user_id, received_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS incomes_source_trgm_idx ON incomes USING gin (source gin_trgm_ops);

DROP TRIGGER IF EXISTS incomes_set_updated_at ON incomes;
CREATE TRIGGER incomes_set_updated_at BEFORE UPDATE ON incomes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
