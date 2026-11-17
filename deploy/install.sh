#!/bin/sh
# Jungle Monkey installer: sets up /srv/fleetpocket and starts the stack.
#
#   curl -fsSL https://raw.githubusercontent.com/AndrewSl105/fleetpocket/main/deploy/install.sh | sh
#
# Safe to re-run for updates: it refreshes the compose file and Caddyfile,
# adds missing .env keys (never changes existing ones), pulls and restarts.
#
# Non-interactive (cloud-init): pass --yes and --domain (or --sslip); agent
# keys come from the environment, never from flags (they would show in ps):
#   CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY, OPENAI_API_KEY, CURSOR_API_KEY
#
# Options:
#   --domain NAME      public host name (must resolve to this server)
#   --sslip            no domain: use <ip-with-dashes>.sslip.io
#   --agents LIST      comma-separated: claude,codex,cursor (default: from the keys given)
#   --yes              no questions; install Docker if it is missing
#   --skip-dns-check   do not check that the domain points at this server
#   --dir PATH         install directory (default /srv/fleetpocket; the
#                      workspaces and attachments bind mounts expect the default)
#   --ref REF          git ref to download deploy files from (default main)
set -eu

REPO_RAW="https://raw.githubusercontent.com/AndrewSl105/fleetpocket"
REF="${FLEETPOCKET_REF:-main}"
DIR="${FLEETPOCKET_DIR:-/srv/fleetpocket}"
DOMAIN="${FLEETPOCKET_DOMAIN:-}"
AGENTS="${FLEETPOCKET_AGENTS:-}"
YES="${FLEETPOCKET_YES:-}"
SSLIP=""
DNS_CHECK=1
# Runner and server containers run as this uid (docker-compose.deploy.yml).
APP_UID=1000

say() { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--domain) DOMAIN="${2:?--domain needs a value}"; shift ;;
	--domain=*) DOMAIN="${1#*=}" ;;
	--sslip) SSLIP=1 ;;
	--agents) AGENTS="${2:?--agents needs a value}"; shift ;;
	--agents=*) AGENTS="${1#*=}" ;;
	--yes | -y) YES=1 ;;
	--skip-dns-check) DNS_CHECK="" ;;
	--dir) DIR="${2:?--dir needs a value}"; shift ;;
	--dir=*) DIR="${1#*=}" ;;
	--ref) REF="${2:?--ref needs a value}"; shift ;;
	--ref=*) REF="${1#*=}" ;;
	-h | --help)
		sed -n '2,22p' "$0" 2>/dev/null || say "see $REPO_RAW/main/deploy/install.sh"
		exit 0
		;;
	*) die "unknown option: $1" ;;
	esac
	shift
done
BASE="$REPO_RAW/$REF/deploy"

# Questions read the terminal: with `curl … | sh`, stdin is the script.
interactive() { [ -z "$YES" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; }

ask() { # ask VAR "question" [default]
	printf '%s' "$2${3:+ [$3]}: " >/dev/tty
	IFS= read -r _a </dev/tty || _a=""
	[ -n "$_a" ] || _a="${3:-}"
	eval "$1=\$_a"
}

ask_secret() { # ask_secret VAR "question" (not echoed)
	printf '%s: ' "$2" >/dev/tty
	_old=$(stty -g </dev/tty)
	stty -echo </dev/tty
	IFS= read -r _a </dev/tty || _a=""
	stty "$_old" </dev/tty
	printf '\n' >/dev/tty
	eval "$1=\$_a"
}

confirm() { # confirm "question" → 0 for yes
	[ -n "$YES" ] && return 0
	interactive || return 1
	_c=""
	ask _c "$1 (y/N)"
	case "$_c" in y | Y | yes | YES) return 0 ;; *) return 1 ;; esac
}

need_root() {
	[ "$(id -u)" -eq 0 ] && return 0
	command -v sudo >/dev/null 2>&1 || die "run as root"
	# Piped into sh, $0 is not a file: fetch the script again for sudo.
	tmp=$(mktemp)
	curl -fsSL "$BASE/install.sh" -o "$tmp" || die "cannot download $BASE/install.sh"
	say "Jungle Monkey needs root for Docker and $DIR; re-running with sudo."
	skip_dns=""
	[ -n "$DNS_CHECK" ] || skip_dns="--skip-dns-check"
	exec sudo FLEETPOCKET_REF="$REF" FLEETPOCKET_DIR="$DIR" FLEETPOCKET_DOMAIN="$DOMAIN" \
		FLEETPOCKET_AGENTS="$AGENTS" FLEETPOCKET_YES="$YES" \
		CLAUDE_CODE_OAUTH_TOKEN="${CLAUDE_CODE_OAUTH_TOKEN:-}" ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-}" \
		OPENAI_API_KEY="${OPENAI_API_KEY:-}" CURSOR_API_KEY="${CURSOR_API_KEY:-}" \
		sh "$tmp" ${SSLIP:+--sslip} $skip_dns
}

check_os() {
	[ -r /etc/os-release ] || die "cannot detect the OS (need Ubuntu or Debian)"
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}:${ID_LIKE:-}" in
	ubuntu:* | debian:* | *:*debian* | *:*ubuntu*) ;;
	*) die "unsupported OS ${PRETTY_NAME:-unknown}: Jungle Monkey's installer supports Ubuntu and Debian" ;;
	esac
}

check_docker() {
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
		return 0
	fi
	confirm "Docker (with the compose plugin) is missing. Install it with Docker's official script (get.docker.com)?" ||
		die "install Docker first: https://docs.docker.com/engine/install/"
	curl -fsSL https://get.docker.com | sh
	docker compose version >/dev/null 2>&1 || die "docker compose is still missing"
}

public_ip() {
	curl -fsS4 --max-time 10 https://api.ipify.org 2>/dev/null || true
}

choose_domain() {
	ip=$(public_ip)
	if [ -n "$SSLIP" ]; then
		[ -n "$ip" ] || die "cannot find this server's public IPv4 address for sslip.io"
		DOMAIN="$(printf '%s' "$ip" | tr . -).sslip.io"
	fi
	if [ -z "$DOMAIN" ] && interactive; then
		say "Jungle Monkey needs a domain whose DNS A record points at this server (Caddy gets the HTTPS certificate)."
		[ -n "$ip" ] && say "No domain? Press Enter to use $(printf '%s' "$ip" | tr . -).sslip.io."
		ask DOMAIN "Domain" ""
		if [ -z "$DOMAIN" ] && [ -n "$ip" ]; then
			DOMAIN="$(printf '%s' "$ip" | tr . -).sslip.io"
		fi
	fi
	[ -n "$DOMAIN" ] || die "no domain: pass --domain NAME or --sslip"
	DOMAIN=$(printf '%s' "$DOMAIN" | tr '[:upper:]' '[:lower:]')
	printf '%s' "$DOMAIN" | grep -Eq '^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$' || die "invalid domain: $DOMAIN"
	case "$DOMAIN" in
	*.sslip.io) warn "sslip.io is shared by many servers; Let's Encrypt rate limits can delay the certificate. Use your own domain when you can." ;;
	esac
	[ -n "$DNS_CHECK" ] || return 0
	[ -n "$ip" ] || {
		warn "cannot find this server's public IP; skipping the DNS check"
		return 0
	}
	resolved=$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')
	case " $resolved " in
	*" $ip "*) say "DNS ok: $DOMAIN → $ip" ;;
	*)
		warn "$DOMAIN resolves to '${resolved% }', not to this server ($ip); HTTPS will fail until DNS points here."
		confirm "Continue anyway?" || die "fix DNS (or use --sslip) and run the installer again"
		;;
	esac
}

choose_agents() {
	if [ -z "$AGENTS" ]; then
		[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}${ANTHROPIC_API_KEY:-}" ] && AGENTS="claude"
		[ -n "${OPENAI_API_KEY:-}" ] && AGENTS="${AGENTS:+$AGENTS,}codex"
		[ -n "${CURSOR_API_KEY:-}" ] && AGENTS="${AGENTS:+$AGENTS,}cursor"
	fi
	if [ -z "$AGENTS" ] && interactive; then
		ask AGENTS "Coding agents to enable (claude, codex, cursor; comma-separated)" "claude"
	fi
	[ -n "$AGENTS" ] || die "no agent: set --agents and the agent's key in the environment"
	for a in $(printf '%s' "$AGENTS" | tr ',' ' '); do
		case "$a" in
		claude)
			if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}${ANTHROPIC_API_KEY:-}" ] && interactive; then
				ask_secret CLAUDE_CODE_OAUTH_TOKEN "Claude token from \`claude setup-token\` (Enter to use an API key instead)"
				[ -n "$CLAUDE_CODE_OAUTH_TOKEN" ] || ask_secret ANTHROPIC_API_KEY "Anthropic API key"
			fi
			[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}${ANTHROPIC_API_KEY:-}" ] || die "claude needs CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY"
			;;
		codex)
			[ -n "${OPENAI_API_KEY:-}" ] || ! interactive || ask_secret OPENAI_API_KEY "OpenAI API key (Codex)"
			[ -n "${OPENAI_API_KEY:-}" ] || die "codex needs OPENAI_API_KEY"
			;;
		cursor)
			[ -n "${CURSOR_API_KEY:-}" ] || ! interactive || ask_secret CURSOR_API_KEY "Cursor API key"
			[ -n "${CURSOR_API_KEY:-}" ] || die "cursor needs CURSOR_API_KEY"
			;;
		*) die "unknown agent: $a (claude, codex or cursor)" ;;
		esac
	done
	DEFAULT_AGENT=${AGENTS%%,*}
}

make_dirs() {
	mkdir -p "$DIR/workspaces" "$DIR/attachments" "$DIR/secrets" "$DIR/backups"
	chmod 755 "$DIR"
	chown -R "$APP_UID:$APP_UID" "$DIR/workspaces" "$DIR/attachments" "$DIR/secrets"
	chmod 700 "$DIR/secrets"
	# A file bind mount: if it were missing, Docker would create a directory.
	[ -e "$DIR/secrets/github-app.pem" ] || : >"$DIR/secrets/github-app.pem"
	chown "$APP_UID:$APP_UID" "$DIR/secrets/github-app.pem"
	chmod 600 "$DIR/secrets/github-app.pem"
}

download() {
	for f in docker-compose.deploy.yml Caddyfile .env.example; do
		curl -fsSL "$BASE/$f" -o "$DIR/$f.tmp" || die "cannot download $BASE/$f"
		mv "$DIR/$f.tmp" "$DIR/$f"
	done
}

# env_line KEY VALUE: a .env line; single quotes keep `$` literal for compose.
env_line() { printf "%s='%s'\n" "$1" "$(printf '%s' "$2" | sed "s/'/'\\\\''/g")"; }

# render_env: .env.example with this install's values filled in.
render_env() {
	gen_pg=$(openssl rand -hex 24)
	gen_session=$(openssl rand -hex 32)
	gen_secrets=$(openssl rand -hex 32)
	while IFS= read -r line; do
		key=${line%%=*}
		case "$line" in
		FLEETPOCKET_DOMAIN=*) env_line "$key" "$DOMAIN" ;;
		PUBLIC_BASE_URL=* | WEB_APP_URL=*) env_line "$key" "https://$DOMAIN" ;;
		PG_PASSWORD=*) env_line "$key" "$gen_pg" ;;
		SESSION_SECRET=*) env_line "$key" "$gen_session" ;;
		SECRETS_KEY=*) env_line "$key" "$gen_secrets" ;;
		DEFAULT_AGENT=*) env_line "$key" "$DEFAULT_AGENT" ;;
		CLAUDE_CODE_OAUTH_TOKEN=*) env_line "$key" "${CLAUDE_CODE_OAUTH_TOKEN:-}" ;;
		ANTHROPIC_API_KEY=*) env_line "$key" "${ANTHROPIC_API_KEY:-}" ;;
		OPENAI_API_KEY=*) env_line "$key" "${OPENAI_API_KEY:-}" ;;
		CURSOR_API_KEY=*) env_line "$key" "${CURSOR_API_KEY:-}" ;;
		*) printf '%s\n' "$line" ;;
		esac
	done <"$DIR/.env.example"
}

write_env() {
	umask 077
	new="$DIR/.env.new"
	render_env >"$new"
	if [ ! -e "$DIR/.env" ]; then
		mv "$new" "$DIR/.env"
		say "Wrote $DIR/.env"
	else
		# Existing install: never change a value, only append missing keys.
		added=""
		grep -E '^[A-Z_][A-Z0-9_]*=' "$new" | while IFS= read -r line; do
			grep -q "^${line%%=*}=" "$DIR/.env" || printf '%s\n' "$line"
		done >"$new.missing"
		if [ -s "$new.missing" ]; then
			{
				printf '\n# Added by install.sh on %s\n' "$(date -u +%Y-%m-%d)"
				cat "$new.missing"
			} >>"$DIR/.env"
			added=$(sed 's/=.*//' "$new.missing" | tr '\n' ' ')
			say "Added missing .env keys: $added"
		fi
		rm -f "$new" "$new.missing"
	fi
	chmod 600 "$DIR/.env"
}

env_value() { # env_value KEY: the value in .env without quotes
	sed -n "s/^$1=//p" "$DIR/.env" | tail -n 1 | sed "s/^'\(.*\)'\$/\1/; s/^\"\(.*\)\"\$/\1/"
}

compose() { docker compose -f "$DIR/docker-compose.deploy.yml" --project-directory "$DIR" "$@"; }

start_stack() {
	say "Pulling images…"
	compose pull --quiet
	docker pull --quiet "$(env_value RUNNER_IMAGE)" >/dev/null # runners are never pulled by the server
	compose up -d --remove-orphans
	say "Waiting for the server…"
	i=0
	until compose exec -T caddy wget -qO- http://server:8080/healthz >/dev/null 2>&1; do
		i=$((i + 1))
		[ "$i" -lt 90 ] || die "the server did not become healthy; see: docker compose -f $DIR/docker-compose.deploy.yml logs server"
		sleep 2
	done
}

print_next() {
	url=$(compose logs --no-log-prefix server 2>/dev/null | grep '"setup required' | tail -n 1 |
		sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
	say ""
	if [ -n "$url" ]; then
		say "Jungle Monkey is running. Finish the setup in your browser:"
		say ""
		say "    $url"
		say ""
		say "(The first HTTPS request can take a minute while Caddy gets the certificate.)"
	else
		say "Jungle Monkey is running: https://$(env_value FLEETPOCKET_DOMAIN)"
	fi
}

main() {
	need_root
	check_os
	command -v curl >/dev/null 2>&1 || die "curl is required"
	command -v openssl >/dev/null 2>&1 || die "openssl is required (apt-get install openssl)"
	check_docker
	if [ -e "$DIR/.env" ]; then
		say "Updating the existing install in $DIR (.env values stay as they are)."
	else
		choose_domain
		choose_agents
	fi
	make_dirs
	download
	if [ -e "$DIR/.env" ]; then
		# Values only appended when keys are missing; reuse what is there.
		DOMAIN=$(env_value FLEETPOCKET_DOMAIN)
		DEFAULT_AGENT=$(env_value DEFAULT_AGENT)
	fi
	write_env
	start_stack
	print_next
}

# FLEETPOCKET_INSTALL_LIB=1 sources the functions without running (tests).
[ -n "${FLEETPOCKET_INSTALL_LIB:-}" ] || main
