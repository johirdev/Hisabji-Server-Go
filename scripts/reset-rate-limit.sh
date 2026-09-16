#!/usr/bin/env bash
# =============================================================================
# reset-rate-limit.sh — clear rate-limit buckets during development
#
# The limiter is a sliding window: each rule keeps TWO Redis keys (the current
# time slot and the previous one, weighted). Deleting only the current slot
# would leave the previous one still counting against you, so this script
# always deletes a rule's whole key family.
#
#   ./scripts/reset-rate-limit.sh              # clear every bucket
#   ./scripts/reset-rate-limit.sh register     # clear one rule
#   ./scripts/reset-rate-limit.sh auth         # clear every auth rule
#   ./scripts/reset-rate-limit.sh --list       # show what is limited right now
#   ./scripts/reset-rate-limit.sh --help
#
# Requires: docker compose (the redis service). Override the container with
# REDIS_CONTAINER=my-redis, or the key prefix with REDIS_KEY_PREFIX=hisabji.
# =============================================================================
set -u

PREFIX="${REDIS_KEY_PREFIX:-hisabji}:rl"
SERVICE="${REDIS_CONTAINER:-redis}"

red()   { printf '\033[31m%s\033[0m\n' "$1"; }
green() { printf '\033[32m%s\033[0m\n' "$1"; }
dim()   { printf '\033[2m%s\033[0m\n'  "$1"; }
bold()  { printf '\033[1m%s\033[0m\n'  "$1"; }

# redis <args...> — run redis-cli inside the compose service.
#
# stdin is closed deliberately: `docker compose exec` reads stdin even with -T,
# so calling this inside a `while read` loop would swallow the loop's input and
# the loop would run exactly once.
redis() { docker compose exec -T "$SERVICE" redis-cli "$@" < /dev/null; }

# Friendly name -> the rule name the Go middleware uses.
rule_for() {
  case "$1" in
    register)          echo "auth:register" ;;
    login)             echo "auth:login" ;;
    verify|verify-otp) echo "auth:verify" ;;
    resend|resend-otp) echo "auth:resend" ;;
    refresh)           echo "auth:refresh" ;;
    forgot)            echo "auth:forgot" ;;
    reset-password)    echo "auth:reset" ;;
    global)            echo "global" ;;
    profile)           echo "write:profile" ;;
    preferences)       echo "write:preferences" ;;
    onboarding)        echo "write:onboarding" ;;
    username)          echo "strict:username" ;;
    subscribe)         echo "strict:subscribe" ;;
    buy|credits)       echo "strict:buy_credits" ;;
    confirm)           echo "strict:confirm_payment" ;;
    cancel)            echo "strict:cancel" ;;
    password)          echo "strict:change_password" ;;
    delete-account)    echo "strict:delete_account" ;;
    # Whole families, and anything else passed through verbatim.
    auth)              echo "auth:*" ;;
    write)             echo "write:*" ;;
    strict)            echo "strict:*" ;;
    *)                 echo "$1" ;;
  esac
}

usage() {
  bold "reset-rate-limit.sh — clear rate-limit buckets"
  echo
  echo "  ./scripts/reset-rate-limit.sh              clear everything"
  echo "  ./scripts/reset-rate-limit.sh <rule>       clear one rule or family"
  echo "  ./scripts/reset-rate-limit.sh --list       show current buckets"
  echo
  bold "Rule names"
  echo "  register  login  verify  resend  refresh  forgot  reset-password"
  echo "  global    profile  preferences  onboarding"
  echo "  username  subscribe  buy  confirm  cancel  password  delete-account"
  echo "  auth      write     strict          (whole families)"
  echo
  dim "Tip: to stop hitting limits at all while developing, start the server"
  dim "with RATE_LIMIT_ENABLED=false instead of resetting repeatedly."
}

# ---------------------------------------------------------------------------

if ! docker compose ps --status running --services 2>/dev/null | grep -qx "$SERVICE"; then
  red "The '$SERVICE' service is not running."
  echo "Start it with:  docker compose up -d redis"
  exit 1
fi

case "${1:-}" in
  -h|--help) usage; exit 0 ;;

  -l|--list)
    bold "Active rate-limit buckets"
    # Each key is <rule>:<scope>:<id>:<slot>. One MGET fetches every counter in
    # a single round trip rather than one docker exec per key.
    keys="$(redis --scan --pattern "$PREFIX:*" | tr -d '\r' | sort)"
    if [ -z "$keys" ]; then
      dim "  (none — nothing is rate limited right now)"
      exit 0
    fi
    # shellcheck disable=SC2086  # keys never contain whitespace
    counts="$(redis MGET $keys | tr -d '\r')"
    paste -d'\t' <(printf '%s\n' "$keys") <(printf '%s\n' "$counts") |
      while IFS=$'\t' read -r key count; do
        printf '  %-58s %s\n' "${key#"$PREFIX":}" "$count"
      done
    echo
    dim "  Format: <rule>:<scope>:<id>:<time-slot> = requests counted"
    exit 0
    ;;
esac

if [ $# -eq 0 ]; then
  PATTERN="$PREFIX:*"
  LABEL="every rule"
else
  RULE="$(rule_for "$1")"
  PATTERN="$PREFIX:$RULE:*"
  LABEL="$RULE"
fi

# Count first, so the script can report honestly instead of claiming success
# when the pattern matched nothing (usually a typo in the rule name).
KEYS="$(redis --scan --pattern "$PATTERN" | tr -d '\r')"
COUNT="$(printf '%s' "$KEYS" | grep -c . || true)"

if [ "$COUNT" -eq 0 ]; then
  dim "Nothing to clear for $LABEL."
  dim "Run with --list to see what is actually limited."
  exit 0
fi

# One DEL with every key, rather than one docker exec per key.
# shellcheck disable=SC2086  # keys never contain whitespace
redis DEL $KEYS > /dev/null

green "Cleared $COUNT bucket(s) for $LABEL."
dim "Retry the request — the limit is gone."
