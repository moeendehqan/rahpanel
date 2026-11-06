#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$project_dir"
domain=''
agent_host=''
install_docker=1
usage() {
 cat <<'HELP'
Usage: sudo bash install.sh --domain panel.example.com [--agent-host agents.example.com]
       bash install.sh --no-install-docker

Build and start Rah Panel, PostgreSQL and automatic HTTPS using Docker Compose.
On a fresh Ubuntu/Debian server, missing Docker is installed from its official apt repository.
Existing .env, administrator credentials and database volumes are preserved on rerun.
  --domain HOST          Public panel hostname (prompted on first interactive run)
  --agent-host HOST      DNS-only node endpoint; defaults to panel hostname
  --no-install-docker    Require Docker and Compose to be installed already
  -h, --help             Show this help
HELP
}
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
while (($#)); do
 case "$1" in
  --domain|--agent-host)
   (($# >= 2)) || die "Missing value for $1"
   if [[ "$1" == --domain ]]; then domain="$2"; else agent_host="$2"; fi
   shift 2 ;;
  --no-install-docker) install_docker=0; shift ;;
  -h|--help) usage; exit 0 ;;
  *) die "Unknown option: $1 (see --help)" ;;
 esac
done
valid_host() {
 local label
 [[ "$1" =~ ^[a-z0-9.-]+\.[a-z]{2,}$ && ${#1} -le 253 ]] || return 1
 local -a labels
 IFS=. read -r -a labels <<< "$1"
 for label in "${labels[@]}"; do
  [[ ${#label} -ge 1 && ${#label} -le 63 && "$label" != -* && "$label" != *- ]] || return 1
 done
}
[[ -f compose.yaml && -f Dockerfile && -f deploy/Caddyfile ]] || die 'Run from a complete Rah Panel source checkout.'
if [[ -f .env ]]; then
 existing_domain="$(sed -n 's/^PANEL_HOST=//p' .env | tr -d '\r')"
 valid_host "$existing_domain" || die 'Existing .env has an invalid or missing PANEL_HOST; repair it without replacing its secrets.'
 [[ -z "$domain" || "$domain" == "$existing_domain" ]] || die 'Domain differs from existing .env. Edit deployment URLs deliberately before changing domains.'
 domain="$existing_domain"
 if [[ -n "$agent_host" ]]; then
  existing_agent="$(sed -n 's/^AGENT_URL=https:\/\/\([^:/]*\):9443$/\1/p' .env | tr -d '\r')"
  [[ "$agent_host" == "$existing_agent" ]] || die 'Agent hostname differs from existing .env.'
 fi
else
 if [[ -z "$domain" ]]; then
  if [[ -t 0 ]]; then
   read -r -p 'Panel hostname (e.g. panel.example.com): ' domain
  elif [[ -r /dev/tty ]]; then
   # A curl | bash installation reads its program from stdin; prompt on the terminal instead.
   read -r -p 'Panel hostname (e.g. panel.example.com): ' domain </dev/tty || true
  fi
 fi
fi
valid_host "$domain" || die 'Supply a valid lowercase hostname with --domain panel.example.com.'
[[ -z "$agent_host" ]] || valid_host "$agent_host" || die 'Invalid --agent-host.'
mkdir .install.lock 2>/dev/null || die 'Another installation is running, or .install.lock remains after an interrupted run. Check before removing the lock.'
trap 'rmdir "$project_dir/.install.lock" 2>/dev/null || true' EXIT
trap 'printf "Installation stopped. Settings and database volumes are preserved. Fix the error above, then run install.sh again.\n" >&2' ERR

setup_docker() {
 [[ "$install_docker" == 1 ]] || die 'Docker is missing. Install Docker Engine and the Compose plugin, or rerun without --no-install-docker.'
 [[ "$EUID" == 0 ]] || die 'Docker is missing. Rerun with sudo to install it automatically.'
 [[ -r /etc/os-release ]] || die 'Automatic Docker installation requires Ubuntu or Debian.'
 # Distribution metadata is supplied by the operating system.
 . /etc/os-release
 [[ "$ID" == ubuntu || "$ID" == debian ]] || die 'Automatic Docker installation supports Ubuntu/Debian only; install Docker manually on this OS.'
 local package arch codename
 for package in docker.io docker-compose docker-compose-v2 docker-doc podman-docker containerd runc; do
  if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -qx 'install ok installed'; then
   die "Conflicting package $package is installed. Migrate it using Docker documentation; the installer will not remove existing container software."
  fi
 done
 arch="$(dpkg --print-architecture)"
 [[ "$arch" == amd64 || "$arch" == arm64 ]] || die 'Rah Panel supports amd64 and arm64 hosts.'
 codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
 [[ -n "$codename" ]] || die 'Cannot determine distribution codename.'
 export DEBIAN_FRONTEND=noninteractive
 apt-get update
 apt-get install -y ca-certificates curl
 install -m 0755 -d /etc/apt/keyrings
 curl -fsSL --retry 3 "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/rah-docker.asc
 chmod 0644 /etc/apt/keyrings/rah-docker.asc
 cat > /etc/apt/sources.list.d/rah-docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/$ID
Suites: $codename
Components: stable
Architectures: $arch
Signed-By: /etc/apt/keyrings/rah-docker.asc
EOF
 chmod 0644 /etc/apt/sources.list.d/rah-docker.sources
 apt-get update
 apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
 systemctl enable --now docker
}
if ! command -v docker >/dev/null 2>&1; then setup_docker; fi
docker compose version >/dev/null 2>&1 || die 'Docker Compose plugin is missing. Install docker-compose-plugin, then retry.'
docker info >/dev/null 2>&1 || die 'Docker daemon is unavailable or access is denied. Start Docker and rerun with sudo if necessary.'
printf 'Building Rah Panel (the first build downloads dependencies and verified Xray releases)...\n'
docker build --pull --tag rahpanel:local .
if [[ ! -f .env ]]; then
 init_args=(init --domain "$domain" --dir /output)
 [[ -z "$agent_host" ]] || init_args+=(--agent-host "$agent_host")
 docker run --rm --entrypoint /app/rahctl --user "$(id -u):$(id -g)" \
  --volume "$project_dir:/output" rahpanel:local "${init_args[@]}"
else
 printf 'Keeping existing deployment secrets and administrator credentials.\n'
fi
docker compose config --quiet
docker compose up -d --no-build --wait --wait-timeout 180
docker compose ps
printf '\nServices are healthy. Open https://%s\n' "$domain"
printf 'Point public DNS at this server and allow TCP 80, 443 and 9443. The agent endpoint must be DNS-only.\n'
printf 'Caddy obtains public HTTPS once DNS resolves and ports are reachable. Service health does not verify public DNS/TLS.\n'
printf 'Administrator password and authenticator seed: %s/bootstrap-admin.txt\n' "$project_dir"
printf 'Read that file privately; then enter Cloudflare settings in the panel.\n'
printf 'Manage: cd %q && sudo bash rah.sh status (or logs / backup)\n' "$project_dir"
