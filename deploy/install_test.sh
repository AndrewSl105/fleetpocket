#!/bin/sh
# Tests the parts of install.sh that need no root, Docker or network:
#   sh deploy/install_test.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
FLEETPOCKET_DIR=$(mktemp -d)
trap 'rm -rf "$FLEETPOCKET_DIR"' EXIT
FLEETPOCKET_INSTALL_LIB=1
# shellcheck source=deploy/install.sh
. "$here/install.sh" # sets DIR=$FLEETPOCKET_DIR
cp "$here/.env.example" "$DIR/.env.example"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

DOMAIN=fp.example.com DEFAULT_AGENT=codex OPENAI_API_KEY="it's \$secret"
write_env >/dev/null
[ "$(stat -c %a "$DIR/.env")" = 600 ] || fail ".env mode $(stat -c %a "$DIR/.env")"
[ "$(env_value PUBLIC_BASE_URL)" = "https://fp.example.com" ] || fail "PUBLIC_BASE_URL"
[ "$(env_value DEFAULT_AGENT)" = codex ] || fail "DEFAULT_AGENT"
grep -q "^OPENAI_API_KEY='it'\\\\''s \$secret'\$" "$DIR/.env" || fail "quoting: $(grep OPENAI_API_KEY "$DIR/.env")"
pg=$(env_value PG_PASSWORD)
[ "${#pg}" -eq 48 ] || fail "PG_PASSWORD not generated"
[ "$(env_value SESSION_SECRET | wc -c)" -eq 65 ] || fail "SESSION_SECRET not generated"
sk=$(env_value SECRETS_KEY)
[ "${#sk}" -eq 64 ] || fail "SECRETS_KEY not generated"
[ -z "$(env_value ADMIN_PASSWORD_HASH)" ] || fail "owner must be left to the setup wizard"

# Re-run: nothing changes.
before=$(cat "$DIR/.env")
DOMAIN=other.example.com
write_env >/dev/null
[ "$before" = "$(cat "$DIR/.env")" ] || fail "re-run changed .env"

# A key the user removed (or a new release added) is appended; others stay.
sed -i '/^SIDECAR_MEMORY=/d; s/^MAX_CONCURRENT_TASKS=.*/MAX_CONCURRENT_TASKS=7/' "$DIR/.env"
out=$(write_env)
case "$out" in *SIDECAR_MEMORY*) ;; *) fail "missing key not reported: $out" ;; esac
[ "$(env_value SIDECAR_MEMORY)" = 512m ] || fail "missing key not added"
[ "$(env_value MAX_CONCURRENT_TASKS)" = 7 ] || fail "existing value changed"
[ "$(env_value PG_PASSWORD)" = "$pg" ] || fail "PG_PASSWORD regenerated"
[ "$(env_value SECRETS_KEY)" = "$sk" ] || fail "SECRETS_KEY regenerated"

# Upgrade of an install from before SECRETS_KEY: the key is generated once.
sed -i '/^SECRETS_KEY=/d' "$DIR/.env"
out=$(write_env)
case "$out" in *SECRETS_KEY*) ;; *) fail "SECRETS_KEY not added on upgrade: $out" ;; esac
sk2=$(env_value SECRETS_KEY)
[ "${#sk2}" -eq 64 ] || fail "SECRETS_KEY on upgrade: $sk2"
[ "$(grep -c '^SECRETS_KEY=' "$DIR/.env")" -eq 1 ] || fail "SECRETS_KEY added twice"
write_env >/dev/null
[ "$(env_value SECRETS_KEY)" = "$sk2" ] || fail "SECRETS_KEY changed on a second upgrade"
[ "$(stat -c %a "$DIR/.env")" = 600 ] || fail ".env mode after update"

echo "install.sh: ok"
