#!/usr/bin/env bash
# =============================================================================
# smoke.sh — end-to-end check of the live API
#
# Runs the real user journey against a running server and asserts on the
# response envelope. It is the fastest way to know the whole stack works after
# a change: no mocks, no fixtures, just HTTP.
#
#   ./scripts/smoke.sh                       # against localhost:8080
#   BASE=http://localhost:8085 ./scripts/smoke.sh
#
# Requires: curl. Uses only POSIX-ish shell plus grep, so it runs in Git Bash
# on Windows as well as on CI.
# =============================================================================
set -u

BASE="${BASE:-http://localhost:8080}"
API="$BASE/api/v1"

# A unique phone per run, so repeated runs do not collide on the unique index.
# 017 + 8 digits derived from the clock.
SUFFIX="$(date +%H%M%S%N 2>/dev/null | cut -c1-8)"
SUFFIX="${SUFFIX:-$RANDOM$RANDOM}"
PHONE="017${SUFFIX:0:8}"
PASSWORD="hisabji2026"

PASS=0
FAIL=0

green() { printf '\033[32m%s\033[0m\n' "$1"; }
red()   { printf '\033[31m%s\033[0m\n' "$1"; }
head2() { printf '\n\033[1;34m== %s\033[0m\n' "$1"; }

# check <label> <haystack> <needle>
check() {
  if printf '%s' "$2" | grep -q -- "$3"; then
    green "  PASS  $1"
    PASS=$((PASS + 1))
  else
    red   "  FAIL  $1"
    red   "        expected to find: $3"
    red   "        got: $(printf '%s' "$2" | head -c 400)"
    FAIL=$((FAIL + 1))
  fi
}

json() { printf '%s' "$1" | grep -oE "\"$2\":\"[^\"]*\"" | head -1 | cut -d'"' -f4; }

post() { curl -s -X POST "$1" -H "Content-Type: application/json" -d "$2"; }
auth_get()   { curl -s "$1" -H "Authorization: Bearer $TOKEN"; }
auth_post()  { curl -s -X POST "$1" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "${2:-{\}}"; }
auth_patch() { curl -s -X PATCH "$1" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d "$2"; }

printf '\033[1mHisabji API smoke test\033[0m\n'
printf 'base:  %s\n' "$BASE"
printf 'phone: %s\n' "$PHONE"

# -----------------------------------------------------------------------------
head2 "Service health"
R=$(curl -s "$BASE/health")
check "health reports ok"            "$R" '"status":"ok"'
check "database is up"               "$R" '"database":{"pool"'
R=$(curl -s "$BASE/")
check "root returns the envelope"    "$R" '"success":true'
check "root carries a request id"    "$R" '"request_id"'

# -----------------------------------------------------------------------------
head2 "Validation and error handling"
R=$(post "$API/auth/register" '{"name":"R","phone":"12345","password":"abc"}')
check "422 for invalid input"        "$R" '"code":"VALIDATION_ERROR"'
check "names the phone field"        "$R" '"field":"phone"'
check "includes a Bangla message"    "$R" '"message_bn"'
check "never echoes the password"    "$R" '"rule":"strongpass"'
if printf '%s' "$R" | grep -q '"value":"abc"'; then
  red "  FAIL  password value must never be echoed back"; FAIL=$((FAIL + 1))
else
  green "  PASS  password value is not echoed back"; PASS=$((PASS + 1))
fi

R=$(curl -s -X POST "$API/auth/register" -H "Content-Type: application/json")
check "400 for an empty body"        "$R" '"code":"BAD_REQUEST"'

R=$(curl -s "$API/nonexistent-endpoint")
check "404 for an unknown route"     "$R" '"code":"ROUTE_NOT_FOUND"'

R=$(curl -s -X DELETE "$BASE/health")
check "405 for a wrong method"       "$R" '"code":"METHOD_NOT_ALLOWED"'

R=$(auth_get "$API/users/me")
TOKEN="" R=$(curl -s "$API/users/me")
check "401 without a token"          "$R" '"code":"UNAUTHORIZED"'

R=$(curl -s "$API/users/me" -H "Authorization: Bearer not.a.real.token")
check "401 for a malformed token"    "$R" '"code":"TOKEN_INVALID"'

# -----------------------------------------------------------------------------
head2 "Registration and phone verification"
R=$(post "$API/auth/register" "{\"name\":\"Smoke Test\",\"phone\":\"$PHONE\",\"password\":\"$PASSWORD\",\"user_type\":\"job_holder\"}")
check "account created"              "$R" '"code":"CREATED"'
check "verification is required"     "$R" '"requires_verification":true'
check "next step is the OTP screen"  "$R" '"next_step":"verify_phone"'
OTP=$(printf '%s' "$R" | grep -oE '"dev_otp":"[0-9]{6}"' | grep -oE '[0-9]{6}')
check "dev OTP is exposed in dev"    "$R" '"dev_otp"'

R=$(post "$API/auth/register" "{\"name\":\"Someone Else\",\"phone\":\"$PHONE\",\"password\":\"$PASSWORD\"}")
check "duplicate phone is a 409"     "$R" '"code":"DUPLICATE_ENTRY"'

R=$(post "$API/auth/login" "{\"identifier\":\"$PHONE\",\"password\":\"$PASSWORD\"}")
check "login before verifying asks for the OTP" "$R" '"requires_verification":true'

R=$(post "$API/auth/verify-otp" "{\"phone\":\"$PHONE\",\"otp\":\"000000\"}")
check "a wrong OTP is rejected"      "$R" '"code":"OTP_INVALID"'
check "and reports attempts left"    "$R" '"attempts_left"'

R=$(post "$API/auth/verify-otp" "{\"phone\":\"$PHONE\",\"otp\":\"$OTP\"}")
check "the correct OTP verifies"     "$R" '"message":"Phone number verified'
check "phone_verified is now true"   "$R" '"phone_verified":true'
check "tokens are issued"            "$R" '"access_token"'
TOKEN=$(json "$R" access_token)
REFRESH=$(json "$R" refresh_token)

R=$(post "$API/auth/verify-otp" "{\"phone\":\"$PHONE\",\"otp\":\"$OTP\"}")
check "an OTP cannot be reused"      "$R" '"code":"OTP_INVALID"'

# -----------------------------------------------------------------------------
head2 "Authentication"
R=$(post "$API/auth/login" "{\"identifier\":\"$PHONE\",\"password\":\"wrong-password\"}")
check "a wrong password is a 401"    "$R" '"code":"UNAUTHORIZED"'
check "and counts down attempts"     "$R" '"attempts_remaining"'

R=$(post "$API/auth/login" "{\"identifier\":\"$PHONE\",\"password\":\"$PASSWORD\"}")
check "login succeeds"               "$R" '"message":"Signed in successfully."'
TOKEN=$(json "$R" access_token)

R=$(post "$API/auth/refresh" "{\"refresh_token\":\"$REFRESH\"}")
check "refresh returns a new pair"   "$R" '"access_token"'
NEW_REFRESH=$(json "$R" refresh_token)

R=$(post "$API/auth/refresh" "{\"refresh_token\":\"$REFRESH\"}")
check "reusing an old refresh token is refused" "$R" '"code":"TOKEN_INVALID"'
check "and signs every session out"  "$R" 'all sessions have been signed out'

R=$(post "$API/auth/login" "{\"identifier\":\"$PHONE\",\"password\":\"$PASSWORD\"}")
TOKEN=$(json "$R" access_token)
check "signing in again works"       "$R" '"access_token"'

R=$(auth_get "$API/auth/sessions")
check "sessions are listed"          "$R" '"is_current":true'

# -----------------------------------------------------------------------------
head2 "Profile and onboarding"
R=$(auth_get "$API/users/me?stats=true")
check "profile is returned"          "$R" '"onboarding_step"'
check "entitlement is included"      "$R" '"entitlement"'
check "stats are included"           "$R" '"expense_count"'

R=$(auth_patch "$API/users/me" '{"name":"Rasel Ahmed","monthly_income":30000}')
check "profile updates"              "$R" '"name":"Rasel Ahmed"'
check "money keeps two decimals"     "$R" '"monthly_income":30000.00'

R=$(auth_patch "$API/users/me" '{}')
check "an empty update is rejected"  "$R" '"code":"BAD_REQUEST"'

R=$(auth_patch "$API/users/me" '{"monthly_income":-500}')
check "negative income is rejected"  "$R" '"field":"monthly_income"'

R=$(curl -s "$API/users/username-available?username=admin")
check "reserved usernames are refused" "$R" '"available":false'

R=$(curl -s "$API/users/username-available?username=smoke_$SUFFIX")
check "a free username is available"   "$R" '"available":true'

R=$(curl -s -X PUT "$API/users/me/username" -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" -d "{\"username\":\"smoke_$SUFFIX\"}")
check "username is claimed"          "$R" "\"username\":\"smoke_$SUFFIX\""

R=$(auth_post "$API/users/me/onboarding" '{"step":"income","monthly_income":30000,"month_start_day":7}')
check "onboarding advances"          "$R" '"onboarding_step":"income"'

R=$(auth_post "$API/users/me/onboarding" '{"step":"done"}')
check "onboarding completes"         "$R" '"onboarding_complete":true'

# -----------------------------------------------------------------------------
head2 "Billing: catalogue"
R=$(curl -s "$API/billing/plans")
check "plans are public"             "$R" '"code":"pro_12m"'
check "monthly price is computed"    "$R" '"monthly_price"'
check "savings percent is computed"  "$R" '"savings_percent"'

R=$(curl -s "$API/billing/credit-packs")
check "credit packs are listed"      "$R" '"tokens_60"'
check "price per credit is computed" "$R" '"price_per_credit"'

R=$(curl -s "$API/billing/features")
check "features are listed"          "$R" '"ai_monthly_coach"'
check "grouped by category"          "$R" '"by_category"'

# -----------------------------------------------------------------------------
head2 "Billing: the free tier"
R=$(auth_get "$API/billing/me")
check "billing summary loads"        "$R" '"plan_code":"free"'
check "free signup credits granted"  "$R" '"total_credits":3'

R=$(auth_get "$API/billing/wallet")
check "wallet reports the balance"   "$R" '"purchased_credits":3'

R=$(auth_get "$API/billing/ledger")
check "ledger records the bonus"     "$R" '"reason":"signup_bonus"'

R=$(auth_get "$API/billing/features/ai_quick_answer/access")
check "a 1-credit feature is allowed" "$R" '"allowed":true'

R=$(auth_get "$API/billing/features/ai_monthly_coach/access")
check "a 5-credit feature is refused on 3 credits" "$R" '"allowed":false'
check "and says credits are needed"  "$R" '"needs_credits":true'

R=$(auth_get "$API/billing/features/business_cashflow/access")
check "a business-only feature is priced for pay-as-you-go" "$R" '"credit_cost":5'

# -----------------------------------------------------------------------------
head2 "Billing: buying credits (sandbox)"
R=$(auth_post "$API/billing/credits/buy" '{"pack_code":"tokens_60"}')
check "checkout starts"              "$R" '"code":"CREATED"'
PAYMENT_ID=$(printf '%s' "$R" | grep -oE '"id":"[0-9a-f-]{36}"' | head -1 | cut -d'"' -f4)

R=$(auth_post "$API/billing/credits/buy" '{"pack_code":"does_not_exist"}')
check "an unknown pack is a 404"     "$R" '"code":"NOT_FOUND"'

R=$(auth_post "$API/billing/payments/confirm" "{\"payment_id\":\"$PAYMENT_ID\"}")
check "payment confirms"             "$R" '"credits_granted":60'

R=$(auth_post "$API/billing/payments/confirm" "{\"payment_id\":\"$PAYMENT_ID\"}")
check "confirming twice is idempotent" "$R" '"already_confirmed":true'

R=$(auth_get "$API/billing/wallet")
check "credits landed in the wallet" "$R" '"total_credits":63'

R=$(auth_get "$API/billing/features/ai_monthly_coach/access")
check "the 5-credit feature is now allowed" "$R" '"allowed":true'

# -----------------------------------------------------------------------------
head2 "Billing: subscribing"
R=$(auth_post "$API/billing/subscribe" '{"plan_code":"pro_3m"}')
check "subscription checkout starts" "$R" '"kind":"subscription"'
SUB_PAYMENT=$(printf '%s' "$R" | grep -oE '"id":"[0-9a-f-]{36}"' | head -1 | cut -d'"' -f4)

R=$(auth_post "$API/billing/subscribe" '{"plan_code":"free"}')
check "the free plan cannot be purchased" "$R" '"code":"BAD_REQUEST"'

R=$(auth_post "$API/billing/payments/confirm" "{\"payment_id\":\"$SUB_PAYMENT\"}")
check "subscription activates"       "$R" '"plan_code":"pro_3m"'
check "monthly credits are granted"  "$R" '"credits_granted":100'

R=$(auth_get "$API/billing/me")
check "the plan is now pro_3m"       "$R" '"plan_code":"pro_3m"'
check "status is active"             "$R" '"subscription_status":"active"'

R=$(auth_post "$API/billing/subscribe" '{"plan_code":"pro_6m"}')
check "a second live subscription is refused" "$R" '"code":"CONFLICT"'

R=$(auth_post "$API/billing/subscription/cancel" '{"reason":"testing the smoke suite"}')
check "cancellation works"           "$R" '"auto_renew":false'
check "access continues until expiry" "$R" '"days_remaining"'

# -----------------------------------------------------------------------------
head2 "Search, filter and pagination contract"
R=$(curl -s "$API/billing/ledger?page=1&limit=2" -H "Authorization: Bearer $TOKEN")
check "pagination meta is present"   "$R" '"total_pages"'
check "has_next is reported"         "$R" '"has_next"'

# -----------------------------------------------------------------------------
head2 "Signing out"
R=$(auth_post "$API/auth/logout" '{"all_devices":true}')
check "logout succeeds"              "$R" '"all_devices":true'

R=$(auth_get "$API/users/me")
check "the token is rejected after logout" "$R" '"code":"TOKEN_INVALID"'

# -----------------------------------------------------------------------------
printf '\n\033[1m--------------------------------------------------\033[0m\n'
if [ "$FAIL" -eq 0 ]; then
  green "ALL $PASS CHECKS PASSED"
else
  red "$FAIL of $((PASS + FAIL)) checks FAILED ($PASS passed)"
fi
printf '\033[1m--------------------------------------------------\033[0m\n\n'

[ "$FAIL" -eq 0 ]
