#!/usr/bin/env bash
# Downloadable entry point: safe to invoke using `curl ... | sudo bash -s -- ...`.
set -Eeuo pipefail
umask 077

bootstrap() {
 local repo='https://github.com/moeendehqan/rahpanel.git'
 local ref='main' directory='/opt/rahpanel' domain='' agent_host='' no_docker=0
 while (($#)); do
  case "$1" in
   --repo|--ref|--dir|--domain|--agent-host)
    (($# >= 2)) || { echo "Missing value for $1" >&2; return 1; }
    case "$1" in
     --repo) repo="$2" ;; --ref) ref="$2" ;; --dir) directory="$2" ;;
     --domain) domain="$2" ;; --agent-host) agent_host="$2" ;;
    esac
    shift 2 ;;
   --no-install-docker) no_docker=1; shift ;;
   -h|--help)
    cat <<'HELP'
Usage: bootstrap.sh [--domain panel.example.com] [--agent-host agents.example.com]
                    [--dir /opt/rahpanel] [--ref main] [--repo URL] [--no-install-docker]

Downloads Rah Panel from GitHub and runs its automatic Docker installer.
Without --domain, the installer asks using the terminal, even when piped from curl.
Reruns resume the existing checkout without resetting files or deployment secrets.
Use --ref with a branch/tag for a fresh installation of a particular version.
HELP
    return 0 ;;
   *) echo "Unknown option: $1" >&2; return 1 ;;
  esac
 done
 # Authentication must come from an SSH agent or Git credential helper, never URL tokens.
 if [[ ! "$repo" =~ ^https://[a-zA-Z0-9.-]+(:[0-9]+)?/[a-zA-Z0-9._/-]+$ &&
       ! "$repo" =~ ^git@[a-zA-Z0-9.-]+:[a-zA-Z0-9._/-]+$ ]]; then
  echo 'Repository must be an HTTPS or git@host:path URL without embedded credentials, query or fragment.' >&2
  return 1
 fi
 [[ "$directory" == /* && "$directory" != / ]] || { echo '--dir must be an absolute directory other than /.' >&2; return 1; }
 directory="$(realpath -m -- "$directory")"
 [[ "$directory" != / ]] || return 1
 if ! command -v git >/dev/null 2>&1; then
  [[ "$EUID" == 0 && -r /etc/os-release ]] || { echo 'Git is missing; run with sudo on Ubuntu/Debian.' >&2; return 1; }
  local ID=''
  . /etc/os-release
  [[ "$ID" == ubuntu || "$ID" == debian ]] || { echo 'Install Git first on this operating system.' >&2; return 1; }
  export DEBIAN_FRONTEND=noninteractive
  apt-get update </dev/null
  apt-get install -y ca-certificates git </dev/null
 fi
 git check-ref-format "refs/heads/$ref" >/dev/null || { echo 'Invalid source branch/tag.' >&2; return 1; }
 local parent lock staging=''
 parent="$(dirname -- "$directory")"
 mkdir -p -- "$parent"
 lock="$directory.bootstrap.lock"
 mkdir -- "$lock" 2>/dev/null || { echo "Bootstrap already running, or interrupted lock remains: $lock" >&2; return 1; }
 # Only the unique mktemp directory created by this process is eligible for recursive cleanup.
 trap 'if [[ -n "${staging:-}" && "$staging" == "$parent/.rahpanel-source."* ]]; then rm -rf -- "$staging"; fi; rmdir -- "$lock" 2>/dev/null || true' EXIT
 if [[ -e "$directory" ]]; then
  [[ -d "$directory/.git" && -f "$directory/install.sh" ]] || { echo 'Destination exists and is not a Rah checkout. Choose another --dir.' >&2; exit 1; }
  local origin head requested
  origin="$(git -C "$directory" remote get-url origin)"
  [[ "${origin%.git}" == "${repo%.git}" ]] || { echo 'Existing checkout belongs to another repository; refusing to replace it.' >&2; exit 1; }
  head="$(git -C "$directory" rev-parse --verify HEAD)"
  requested="$(git -C "$directory" rev-parse --verify "$ref^{commit}")"
  [[ "$head" == "$requested" ]] || { echo 'Existing checkout uses a different ref. Fetch/check out the intended version explicitly before rerunning.' >&2; exit 1; }
  printf 'Resuming existing checkout; source and secrets are preserved.\n'
 else
  staging="$(mktemp -d "$parent/.rahpanel-source.XXXXXX")"
  git clone --depth 1 --single-branch --branch "$ref" -- "$repo" "$staging/source" </dev/null
  [[ -f "$staging/source/install.sh" && -f "$staging/source/compose.yaml" ]] || { echo 'Downloaded repository is missing Rah installer files.' >&2; exit 1; }
  mv -T -- "$staging/source" "$directory"
  rmdir -- "$staging"
  staging=''
 fi
 local -a args=()
 [[ -z "$domain" ]] || args+=(--domain "$domain")
 [[ -z "$agent_host" ]] || args+=(--agent-host "$agent_host")
 [[ "$no_docker" == 0 ]] || args+=(--no-install-docker)
 bash "$directory/install.sh" "${args[@]}"
 rmdir -- "$lock"
 trap - EXIT
}

# Define the complete program before running it; apt/git cannot consume source from stdin.
bootstrap "$@"
