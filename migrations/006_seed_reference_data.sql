-- =============================================================================
-- 006_seed_reference_data.sql — system categories, features, plans and packs
-- =============================================================================
-- Every insert here is ON CONFLICT DO NOTHING. Reference data is seeded ONCE;
-- after that it belongs to the admin API, because prices and credit costs are
-- business decisions that must be changeable without a deployment. A seed that
-- overwrote them would silently revert a price change on the next deploy.
-- =============================================================================


-- -----------------------------------------------------------------------------
-- System expense categories (available to every user, user_id IS NULL)
-- -----------------------------------------------------------------------------
INSERT INTO categories (slug, name, name_bn, icon, color, kind, is_fixed, is_system, sort_order) VALUES
    ('food',           'Food & Dining',      'খাবার',              'utensils',    '#EF6C00', 'expense', false, true, 10),
    ('groceries',      'Groceries',          'বাজার',              'shopping-basket','#43A047','expense', false, true, 20),
    ('transport',      'Transport',          'যাতায়াত',            'bus',         '#1E88E5', 'expense', false, true, 30),
    ('rent',           'House Rent',         'বাসা ভাড়া',          'home',        '#5E35B1', 'expense', true,  true, 40),
    ('utilities',      'Utilities',          'ইউটিলিটি বিল',        'zap',         '#FB8C00', 'expense', false, true, 50),
    ('mobile_internet','Mobile & Internet',  'মোবাইল ও ইন্টারনেট',  'wifi',        '#00897B', 'expense', false, true, 60),
    ('installment',    'Installment / Loan', 'কিস্তি / ঋণ',         'credit-card', '#C62828', 'expense', true,  true, 70),
    ('education',      'Education',          'শিক্ষা',              'graduation-cap','#3949AB','expense', false, true, 80),
    ('health',         'Health & Medicine',  'স্বাস্থ্য',            'heart-pulse', '#D81B60', 'expense', false, true, 90),
    ('clothing',       'Clothing',           'পোশাক',              'shirt',       '#8E24AA', 'expense', false, true, 100),
    ('shopping',       'Shopping',           'কেনাকাটা',            'shopping-bag','#F4511E', 'expense', false, true, 110),
    ('entertainment',  'Entertainment',      'বিনোদন',             'film',        '#6D4C41', 'expense', false, true, 120),
    ('personal_care',  'Personal Care',      'ব্যক্তিগত যত্ন',       'sparkles',    '#00ACC1', 'expense', false, true, 130),
    ('family',         'Family',             'পরিবার',             'users',       '#7CB342', 'expense', false, true, 140),
    ('gifts',          'Gifts & Donation',   'উপহার ও দান',         'gift',        '#EC407A', 'expense', false, true, 150),
    ('travel',         'Travel',             'ভ্রমণ',               'plane',       '#039BE5', 'expense', false, true, 160),
    ('insurance',      'Insurance',          'বীমা',                'shield',      '#546E7A', 'expense', true,  true, 170),
    ('savings_out',    'Savings & Deposit',  'সঞ্চয় ও জমা',        'piggy-bank',  '#2E7D32', 'expense', false, true, 180),
    ('business_exp',   'Business Expense',   'ব্যবসায়িক খরচ',       'briefcase',   '#455A64', 'expense', false, true, 190),
    ('pocket',         'Pocket Money',       'হাত খরচ',             'wallet',      '#FDD835', 'expense', false, true, 200),
    ('other_expense',  'Other',              'অন্যান্য',            'more-horizontal','#9E9E9E','expense', false, true, 999)
ON CONFLICT DO NOTHING;

-- -----------------------------------------------------------------------------
-- System income categories
-- -----------------------------------------------------------------------------
INSERT INTO categories (slug, name, name_bn, icon, color, kind, is_fixed, is_system, sort_order) VALUES
    ('salary',        'Salary',          'বেতন',            'banknote',    '#2E7D32', 'income', true,  true, 10),
    ('business_inc',  'Business Income', 'ব্যবসার আয়',      'store',       '#1565C0', 'income', false, true, 20),
    ('freelance',     'Freelance',       'ফ্রিল্যান্স',      'laptop',      '#00838F', 'income', false, true, 30),
    ('tuition',       'Tuition',         'টিউশন',           'book-open',   '#6A1B9A', 'income', false, true, 40),
    ('allowance',     'Allowance',       'হাত খরচ / ভাতা',   'hand-coins',  '#EF6C00', 'income', false, true, 50),
    ('investment',    'Investment',      'বিনিয়োগ',         'trending-up', '#00695C', 'income', false, true, 60),
    ('rental_income', 'Rental Income',   'ভাড়া বাবদ আয়',    'building',    '#4E342E', 'income', false, true, 70),
    ('bonus',         'Bonus',           'বোনাস',           'award',       '#F9A825', 'income', false, true, 80),
    ('gift_income',   'Gift',            'উপহার',           'gift',        '#EC407A', 'income', false, true, 90),
    ('other_income',  'Other',           'অন্যান্য',        'more-horizontal','#9E9E9E','income', false, true, 999)
ON CONFLICT DO NOTHING;


-- =============================================================================
-- features — the gate/meter catalogue
-- =============================================================================
-- kind='module'    -> access gate, free to use once unlocked
-- kind='ai_action' -> costs credit_cost per invocation
--
-- credit_cost is deliberately proportional to how much work the model does:
-- a one-line answer costs 1, a full monthly coaching report costs 5, a yearly
-- projection over twelve months of data costs 8.
-- =============================================================================
INSERT INTO features (code, name, name_bn, description, kind, credit_cost, min_tier, payg_allowed, category, icon, sort_order) VALUES
    -- Always-on modules, included in the free plan ---------------------------
    ('basic_analytics',   'Basic Analytics',      'সাধারণ বিশ্লেষণ',
        'Daily, weekly, monthly and yearly charts of your spending.',
        'module', 0, 'free', true, 'analytics', 'bar-chart', 10),
    ('safe_to_spend',     'Safe to Spend',        'আজ কত খরচ নিরাপদ',
        'How much you can spend today and stay inside your budget.',
        'module', 0, 'free', true, 'budget', 'shield-check', 20),
    ('custom_categories', 'Custom Categories',    'নিজের ক্যাটাগরি',
        'Create your own spending categories and rules.',
        'module', 0, 'free', true, 'core', 'tags', 30),
    ('budget_alerts',     'Budget Alerts',        'বাজেট সতর্কতা',
        'Warnings when a category crosses its limit.',
        'module', 0, 'free', true, 'budget', 'bell', 40),

    -- Paid modules ----------------------------------------------------------
    ('smart_recurring',   'Smart Recurring',      'স্মার্ট রিকারিং',
        'Automatic monthly entries for rent, instalments and salary.',
        'module', 0, 'plus', false, 'core', 'repeat', 100),
    ('advanced_reports',  'Advanced Reports',     'অ্যাডভান্সড রিপোর্ট',
        'Deeper breakdowns, comparisons and printable summaries.',
        'module', 0, 'plus', false, 'analytics', 'file-text', 110),
    ('data_export',       'Data Export',          'ডেটা এক্সপোর্ট',
        'Export your transactions to CSV or PDF.',
        'module', 0, 'plus', false, 'core', 'download', 120),
    ('goal_tracker',      'Goal Tracker',         'লক্ষ্য ট্র্যাকার',
        'Savings goals with progress tracking and reminders.',
        'module', 0, 'plus', false, 'savings', 'target', 130),
    ('business_mode',     'Business Mode',        'ব্যবসা মোড',
        'A separate workspace for shop sales, supplier payments and cash flow.',
        'module', 0, 'business', false, 'business', 'store', 140),
    ('family_sharing',    'Family Sharing',       'পারিবারিক শেয়ারিং',
        'Share one household budget between family members.',
        'module', 0, 'pro', false, 'core', 'users', 150),
    ('priority_support',  'Priority Support',     'অগ্রাধিকার সাপোর্ট',
        'Faster replies from the support team.',
        'module', 0, 'pro', false, 'support', 'life-buoy', 160),

    -- Metered AI actions ----------------------------------------------------
    ('ai_quick_answer',   'Ask Hisabji',          'হিসাবজিকে জিজ্ঞাসা',
        'Ask one question about your own money and get a direct answer.',
        'ai_action', 1, 'free', true, 'ai', 'message-circle', 200),
    ('cash_runway',       'Cash Runway',          'টাকা কতদিন চলবে',
        'How many days your current balance will last at this spending pace.',
        'ai_action', 1, 'free', true, 'ai', 'timer', 210),
    ('budget_risk',       'Budget Risk Prediction','বাজেট ঝুঁকি পূর্বাভাস',
        'The chance you will overrun this month, and which category will cause it.',
        'ai_action', 2, 'plus', true, 'ai', 'alert-triangle', 220),
    ('ai_weekly_coach',   'AI Weekly Coach',      'সাপ্তাহিক কোচ',
        'What happened to your money last week and what to change this week.',
        'ai_action', 2, 'plus', true, 'ai', 'calendar-days', 230),
    ('goal_forecast',     'Goal Forecast',        'লক্ষ্য পূর্বাভাস',
        'When you will reach a savings goal at your current pace.',
        'ai_action', 2, 'plus', true, 'ai', 'flag', 240),
    ('spending_leak',     'Spending Leak Detector','খরচের ফাঁক শনাক্ত',
        'The small, repeated expenses that quietly add up to a large amount.',
        'ai_action', 3, 'plus', true, 'ai', 'search', 250),
    ('category_forecast', 'Category Forecast',    'ক্যাটাগরি পূর্বাভাস',
        'Which categories are trending up and what they will cost next month.',
        'ai_action', 3, 'pro', true, 'ai', 'trending-up', 260),
    ('savings_planner',   'Savings Planner',      'সঞ্চয় পরিকল্পনা',
        'A realistic monthly savings target built from your actual behaviour.',
        'ai_action', 3, 'pro', true, 'ai', 'piggy-bank', 270),
    ('ai_monthly_coach',  'AI Monthly Coach',     'মাসিক কোচ',
        'A full monthly report: mistakes, comparisons and the three actions that matter most.',
        'ai_action', 5, 'pro', true, 'ai', 'sparkles', 280),
    ('business_cashflow', 'Business Cash Flow',   'ব্যবসার ক্যাশ ফ্লো',
        'Cash-in versus cash-out, profit estimate and low-cash warnings.',
        'ai_action', 5, 'business', true, 'business', 'activity', 290),
    ('yearly_forecast',   'Yearly Forecast',      'বাৎসরিক পূর্বাভাস',
        'A twelve-month projection of expenses, savings and trends.',
        'ai_action', 8, 'pro', true, 'ai', 'calendar-range', 300)
ON CONFLICT (code) DO NOTHING;


-- =============================================================================
-- subscription_plans
-- =============================================================================
-- Pricing rationale (BDT, Bangladesh market):
--
--   * The monthly plan exists as a low-commitment entry point and is
--     deliberately the WORST value per month. Its job is to convert doubters.
--   * 3 / 6 / 12 month plans step the effective monthly price down, so the
--     12-month plan is the obvious choice — which is also the plan that gives
--     us the runway to pay for a year of model calls up front.
--   * monthly_credits is what actually costs us money, so it grows more slowly
--     than the price. A user who needs more buys a pack.
-- =============================================================================
INSERT INTO subscription_plans
    (code, name, name_bn, tagline, tier, period_months, price, list_price, currency,
     monthly_credits, signup_credits, allowance_rolls_over, feature_codes,
     max_devices, trial_days, is_active, is_popular, sort_order)
VALUES
    ('free', 'Free', 'ফ্রি',
     'Track everything. Try AI three times.',
     'free', 0, 0, 0, 'BDT',
     0, 3, false,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts'],
     2, 0, true, false, 10),

    ('plus_1m', 'Plus Monthly', 'প্লাস মাসিক',
     'Full AI coaching, one month at a time.',
     'plus', 1, 19900, 19900, 'BDT',
     50, 0, false,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts',
           'smart_recurring','advanced_reports','data_export','goal_tracker',
           'ai_quick_answer','cash_runway','budget_risk','ai_weekly_coach','goal_forecast','spending_leak'],
     3, 7, true, false, 20),

    ('pro_3m', 'Pro — 3 Months', 'প্রো — ৩ মাস',
     'Everything in Plus, plus monthly coaching and forecasts.',
     'pro', 3, 49900, 59700, 'BDT',
     100, 0, false,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts',
           'smart_recurring','advanced_reports','data_export','goal_tracker','family_sharing','priority_support',
           'ai_quick_answer','cash_runway','budget_risk','ai_weekly_coach','goal_forecast','spending_leak',
           'category_forecast','savings_planner','ai_monthly_coach','yearly_forecast'],
     4, 0, true, false, 30),

    ('pro_6m', 'Pro — 6 Months', 'প্রো — ৬ মাস',
     'Half a year of guidance at a lower monthly rate.',
     'pro', 6, 89900, 119400, 'BDT',
     100, 100, false,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts',
           'smart_recurring','advanced_reports','data_export','goal_tracker','family_sharing','priority_support',
           'ai_quick_answer','cash_runway','budget_risk','ai_weekly_coach','goal_forecast','spending_leak',
           'category_forecast','savings_planner','ai_monthly_coach','yearly_forecast'],
     5, 0, true, false, 40),

    ('pro_12m', 'Pro — 12 Months', 'প্রো — ১২ মাস',
     'Best value. A full year of financial guidance.',
     'pro', 12, 159900, 238800, 'BDT',
     120, 300, true,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts',
           'smart_recurring','advanced_reports','data_export','goal_tracker','family_sharing','priority_support',
           'ai_quick_answer','cash_runway','budget_risk','ai_weekly_coach','goal_forecast','spending_leak',
           'category_forecast','savings_planner','ai_monthly_coach','yearly_forecast'],
     5, 0, true, true, 50),

    ('business_12m', 'Business — 12 Months', 'বিজনেস — ১২ মাস',
     'For shop owners: sales, cash flow and profit tracking.',
     'business', 12, 299900, 419900, 'BDT',
     250, 500, true,
     ARRAY['basic_analytics','safe_to_spend','custom_categories','budget_alerts',
           'smart_recurring','advanced_reports','data_export','goal_tracker','family_sharing','priority_support',
           'business_mode','ai_quick_answer','cash_runway','budget_risk','ai_weekly_coach','goal_forecast',
           'spending_leak','category_forecast','savings_planner','ai_monthly_coach','yearly_forecast',
           'business_cashflow'],
     8, 0, true, false, 60)
ON CONFLICT (code) DO NOTHING;


-- =============================================================================
-- credit_packs — the token plan
-- =============================================================================
-- Priced so that a pack always costs MORE per credit than a subscription. Pay
-- as you go should be convenient, not cheaper than committing — otherwise the
-- subscription has no reason to exist.
-- =============================================================================
INSERT INTO credit_packs (code, name, name_bn, credits, bonus_credits, price, list_price, currency, validity_days, is_active, is_popular, sort_order) VALUES
    ('tokens_20',  'Starter — 20 tokens',  'স্টার্টার — ২০ টোকেন',  20,   0,  9900,   9900, 'BDT', 0, true, false, 10),
    ('tokens_60',  'Popular — 60 tokens',  'জনপ্রিয় — ৬০ টোকেন',   50,  10, 19900,  24900, 'BDT', 0, true, true,  20),
    ('tokens_150', 'Value — 150 tokens',   'ভ্যালু — ১৫০ টোকেন',   120,  30, 39900,  59900, 'BDT', 0, true, false, 30),
    ('tokens_400', 'Bulk — 400 tokens',    'বাল্ক — ৪০০ টোকেন',    300, 100, 89900, 159900, 'BDT', 0, true, false, 40)
ON CONFLICT (code) DO NOTHING;
