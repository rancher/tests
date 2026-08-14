#!/usr/bin/env bash
#
# jenkins-run.sh — trigger Jenkins' go-automation-freeform-job from the terminal.
#
# Launches the freeform job with the right parameters, prints the browsable
# build URL, streams the gotestsum console to stdout, and exits 0/1.
#
# Dependencies: bash 4+, curl, jq.  No Go, no Jenkinsfile edits, no installs.
# Auth: a Jenkins API token via env vars (JENKINS_URL / JENKINS_USER / JENKINS_API_TOKEN).
#
# -----------------------------------------------------------------------------
# Shell-style note (deliberate deviation — do NOT "fix" back to `set -e`):
#
# Other repo scripts (e.g. validation/pipeline/scripts/setup_environment.sh) use
# `set -ex`.  This script uses `set -uo pipefail` and deliberately omits `-e`,
# because the CSRF-crumb fetch (§3), the queue/console polling curls (§5/§6), and
# the non-SUCCESS result handling (§7) all rely on inspecting non-zero exit
# codes from individual commands.  With `-e` the script would abort on the first
# transient curl failure or on the expected non-2xx crumb probe, instead of
# looping or branching on the result.
# -----------------------------------------------------------------------------

set -uo pipefail

readonly PROG_NAME="jenkins-run.sh"

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
BRANCH=""
TAGS="validation"
TEST_PACKAGE="./validation/..."
GOTEST_TESTCASE=""
CONFIG_FILE="cattle-config.yaml"
TIMEOUT="60m"
REPO="https://github.com/rancher/tests"
QASE_TEST_RUN_ID=""
QASE_SCHEMA_PREFIX=""
ENV_VARIABLES=()
DETACH=false
DRY_RUN=false

# Optional build display-name prefix: tags the triggered build "<prefix> <branch>"
# via Jenkins' configSubmit so your runs are findable in the shared job history.
BUILD_PREFIX="${JENKINS_BUILD_PREFIX:-}"

# JENKINS_URL / JENKINS_USER / JENKINS_API_TOKEN / JENKINS_JOB come from the
# environment (or --job); they are NOT defaulted here, so the startup check
# below sees the values the caller actually provided.

# Temp files tracked for cleanup
resp_file="" curl_err="" hdr_file="" chunk_file=""

usage() {
  cat <<'EOF'
Usage: jenkins-run.sh [--flag value | --flag=value]...

Trigger Jenkins' go-automation-freeform-job and stream its console.

Required environment:
  JENKINS_URL          Base URL of the Jenkins controller (e.g. https://jenkins.example.com)
  JENKINS_USER         Jenkins user id for the API token
  JENKINS_API_TOKEN    Jenkins API token (not your login password)

Optional environment:
  JENKINS_JOB          Job name; may be foldered (e.g. qa/go-automation-freeform-job).
                       Default: go-automation-freeform-job
  JENKINS_BUILD_PREFIX  Optional prefix for the triggered build's display name
                       (tags builds "<prefix> <testcase>", or "<prefix> <branch>" when no testcase).

Flags (flag → Jenkins build parameter):
  --branch <git-branch>        BRANCH              (default: current git branch, else main)
  --tags <csv>                 TAGS                (default: validation)
  --package <path>             TEST_PACKAGE        (default: ./validation/...)
  --testcase <regex>           GOTEST_TESTCASE     (sent as '-run <regex>'; values starting with '-' pass through raw)
  --config <file>              CONFIG (file text)  (default: cattle-config.yaml)
  --timeout <duration>         TIMEOUT             (default: 60m)
  --repo <url>                 REPO                (default: https://github.com/rancher/tests)
  --qase-run-id <id>           QASE_TEST_RUN_ID    (default: empty)
  --qase-schema-prefix <p>     QASE_SCHEMA_PREFIX  (default: empty)
  --env <KEY=VALUE>            ENV_VARIABLE        (repeatable; joined with newlines)
  --job <name|folder/name>     selects the job     (default: $JENKINS_JOB)

Control flags:
  --build-prefix <text>        Tag build display name "<text> <testcase>" (or "<text> <branch>"); default: $JENKINS_BUILD_PREFIX, off when unset
  --detach                     POST the build, print the queue URL, and exit 0 (do not watch)
  --dry-run                    Print the exact curl (token masked) and exit 0; send nothing
  --help, -h                   Show this help and exit

Exit codes:
  0   build triggered (detached) or build finished SUCCESS
  1   build failed / was cancelled, or the trigger was rejected
  2   misuse: missing env, missing tool, or bad CLI args

Fork / PR usage:
  The job clones --repo and checks out --branch from it (GitSCM "*/<BRANCH>",
  single remote). For a branch that lives on your fork, point --repo at the fork
  and --branch at the branch name you pushed there:
    jenkins-run.sh --repo https://github.com/<you>/tests --branch <your-branch>
  Leave --repo at its default only when the branch exists on rancher/tests itself.
EOF
}

cleanup() {
  for f in "$resp_file" "$curl_err" "$hdr_file" "$chunk_file"; do
    [[ -n "$f" ]] && rm -f "$f"
  done
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Startup contract (§1) — fail fast, exit 2 on misuse
# ---------------------------------------------------------------------------
# Parse --help early so help works even before env checks.
for arg in "$@"; do
  case "$arg" in
    --help|-h) usage; exit 0 ;;
  esac
done

# Require env (help already handled above).
missing=()
[[ -n "${JENKINS_URL:-}" ]]       || missing+=("JENKINS_URL")
[[ -n "${JENKINS_USER:-}" ]]      || missing+=("JENKINS_USER")
[[ -n "${JENKINS_API_TOKEN:-}" ]] || missing+=("JENKINS_API_TOKEN")
if ((${#missing[@]})); then
  echo "${PROG_NAME}: missing required environment: ${missing[*]}" >&2
  echo "set JENKINS_URL / JENKINS_USER / JENKINS_API_TOKEN and retry." >&2
  exit 2
fi

# Require tools.
for tool in curl jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "${PROG_NAME}: '${tool}' is required but not on PATH." >&2
    echo "install it (e.g. 'sudo apt install ${tool}' / 'brew install ${tool}') and retry." >&2
    exit 2
  fi
done

# Resolve env-derived globals.
JENKINS_URL="${JENKINS_URL%/}"
# (JENKINS_USER / JENKINS_API_TOKEN used as-is from the environment.)
JENKINS_JOB="${JENKINS_JOB:-go-automation-freeform-job}"

# ---------------------------------------------------------------------------
# CLI flags → Jenkins parameters (§2)
# ---------------------------------------------------------------------------
# BRANCH default: current git branch, else 'main'.
BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo main)"
[[ -n "$BRANCH" ]] || BRANCH="main"

while (($#)); do
  case "$1" in
    --branch|--tags|--package|--testcase|--config|--timeout|--repo|--qase-run-id|--qase-schema-prefix|--job|--build-prefix)
      if [[ -z "${2+SET}" ]]; then
        echo "${PROG_NAME}: $1 requires a value" >&2
        exit 2
      fi
      case "$1" in
        --branch)             BRANCH="$2" ;;
        --tags)               TAGS="$2" ;;
        --package)            TEST_PACKAGE="$2" ;;
        --testcase)           GOTEST_TESTCASE="$2" ;;
        --config)             CONFIG_FILE="$2" ;;
        --timeout)            TIMEOUT="$2" ;;
        --repo)               REPO="$2" ;;
        --qase-run-id)        QASE_TEST_RUN_ID="$2" ;;
        --qase-schema-prefix) QASE_SCHEMA_PREFIX="$2" ;;
        --job)                JENKINS_JOB="$2" ;;
        --build-prefix)       BUILD_PREFIX="$2" ;;
      esac
      shift 2
      ;;
    --branch=*)             BRANCH="${1#*=}"; shift ;;
    --tags=*)               TAGS="${1#*=}"; shift ;;
    --package=*)            TEST_PACKAGE="${1#*=}"; shift ;;
    --testcase=*)           GOTEST_TESTCASE="${1#*=}"; shift ;;
    --config=*)             CONFIG_FILE="${1#*=}"; shift ;;
    --timeout=*)            TIMEOUT="${1#*=}"; shift ;;
    --repo=*)               REPO="${1#*=}"; shift ;;
    --qase-run-id=*)        QASE_TEST_RUN_ID="${1#*=}"; shift ;;
    --qase-schema-prefix=*) QASE_SCHEMA_PREFIX="${1#*=}"; shift ;;
    --build-prefix=*)          BUILD_PREFIX="${1#*=}"; shift ;;
    --job=*)                JENKINS_JOB="${1#*=}"; shift ;;
    --env)
      if [[ -z "${2+SET}" ]]; then
        echo "${PROG_NAME}: --env requires a value" >&2
        exit 2
      fi
      ENV_VARIABLES+=("$2"); shift 2
      ;;
    --env=*) ENV_VARIABLES+=("${1#*=}"); shift ;;
    --detach)  DETACH=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    --help|-h) usage; exit 0 ;;
    --) shift; break ;;
    -*) echo "${PROG_NAME}: unknown flag: $1" >&2; echo "run '${PROG_NAME} --help' for usage." >&2; exit 2 ;;
    *)  echo "${PROG_NAME}: unexpected argument: $1" >&2; exit 2 ;;
  esac
done

# CONFIG file must exist (the job errors on an empty CONFIG — Jenkinsfile.individual.e2e:143-144).
if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "${PROG_NAME}: config file not found: $CONFIG_FILE (--config <file>)" >&2
  exit 2
fi

# Best-effort: surface the target Rancher host from the config so the operator
# always sees which instance a run hits (CONFIG is a snapshot sent to Jenkins,
# not the file on the checked-out branch). Tolerant — empty if not found.
config_host="$(awk '
  /^[^[:space:]#]/ { in_rancher = ($0 ~ /^rancher:/); next }
  in_rancher && /^[[:space:]]+host:/ {
    v=$0; sub(/^[[:space:]]+host:[[:space:]]*/,"",v); gsub(/#.*/,"",v)
    gsub(/[[:space:]]*$/,"",v); gsub(/^["'\'']|["'\'']$/,"",v); print v; exit
  }
' "$CONFIG_FILE")"

# ---------------------------------------------------------------------------
# Build the non-CONFIG Jenkins parameters (§2)
# ---------------------------------------------------------------------------
params=()
add_param() { [[ -n "${2:-}" ]] && params+=("$1=$2"); }
add_param "BRANCH"             "$BRANCH"
add_param "TAGS"               "$TAGS"
add_param "TEST_PACKAGE"       "$TEST_PACKAGE"
# GOTEST_TESTCASE is appended verbatim to `go test` args by the job (see
# Jenkinsfile.ha.deploy:20, Jenkinsfile.rc:268), so the repo convention is to
# include the `-run` flag. Emit `-run <regex>`; pass through raw when the value
# already starts with `-` (advanced go-test args like "-run X -p 1").
if [[ -n "$GOTEST_TESTCASE" ]]; then
  if [[ "$GOTEST_TESTCASE" == -* ]]; then
    params+=("GOTEST_TESTCASE=$GOTEST_TESTCASE")
  else
    params+=("GOTEST_TESTCASE=-run $GOTEST_TESTCASE")
  fi
fi
add_param "TIMEOUT"            "$TIMEOUT"
add_param "REPO"               "$REPO"
add_param "QASE_TEST_RUN_ID"   "$QASE_TEST_RUN_ID"
add_param "QASE_SCHEMA_PREFIX" "$QASE_SCHEMA_PREFIX"
# ENV_VARIABLE: pack repeated --env flags into one multiline value.
if ((${#ENV_VARIABLES[@]})); then
  params+=("ENV_VARIABLE=$(printf '%s\n' "${ENV_VARIABLES[@]}")")
fi

# Job URL path: flat → /job/<name>; foldered → /job/<f>/job/<name> (§4).
jobPath="/job/$(printf '%s' "$JENKINS_JOB" | sed 's#/#/job/#g')"
triggerUrl="${JENKINS_URL}${jobPath}/buildWithParameters"

# ---------------------------------------------------------------------------
# CSRF crumb (§3) — tolerate CSRF being off
# ---------------------------------------------------------------------------
crumb_header=""
fetch_crumb() {
  local body field crumb
  # Tolerate non-200: 404 means CSRF protection is disabled (no crumb needed).
  # -L follows a host/scheme redirect to the canonical controller before parsing.
  body="$(curl -sS -L -u "$JENKINS_USER:$JENKINS_API_TOKEN" \
            "${JENKINS_URL}/crumbIssuer/api/json" 2>/dev/null)" || true
  if [[ -n "$body" ]] && printf '%s' "$body" | jq -e '.crumb // empty' >/dev/null 2>&1; then
    field="$(printf '%s' "$body" | jq -r '.crumbRequestField')"
    crumb="$(printf '%s' "$body"  | jq -r '.crumb')"
    crumb_header="${field}: ${crumb}"
  fi
}

# (Re)build the curl argument list from current params + crumb (§4).
build_trigger_args() {
  trigger_args=(-sS -i -X POST -u "${JENKINS_USER}:${JENKINS_API_TOKEN}")
  [[ -n "$crumb_header" ]] && trigger_args+=(-H "$crumb_header")
  local p
  for p in "${params[@]}"; do
    trigger_args+=(--data-urlencode "$p")
  done
  # CONFIG carries the file CONTENTS (not a path) — the job writes these into
  # validation/cattle-config.yaml in the build workspace (Jenkinsfile.individual.e2e:146).
  trigger_args+=(--data-urlencode "CONFIG=$(<"$CONFIG_FILE")")
}

# Execute the trigger POST and capture status + Location (§4).
do_trigger() {
  resp_file="$(mktemp)"
  if ! curl "${trigger_args[@]}" "$post_url" >"$resp_file" 2>"$curl_err"; then
    echo "${PROG_NAME}: failed to reach Jenkins at ${JENKINS_URL}" >&2
    cat "$curl_err" >&2
    exit 1
  fi
  # Each call is a single (non-followed) response. On 3xx, `location` is the
  # redirect target; on 2xx success it is the queue-item URL.
  http_code="$(awk 'NR==1{if(match($0,/[0-9][0-9][0-9]/))print substr($0,RSTART,3);exit}' "$resp_file")"
  location="$(awk 'tolower($0)~/^location:/{sub(/^[Ll]ocation:[[:space:]]*/,"");gsub(/[[:space:][:cntrl:]]*$/,"");print;exit}' "$resp_file")"
}

# Set the triggered build's display name to "<prefix> <testcase>" (or "<prefix>
# <branch>" when no testcase is given) so runs are findable in the shared job
# history. Best-effort: a failure here (e.g. no Run/Update permission) must NOT
# fail the run — the build still proceeds with Jenkins' default name.
set_build_display_name() {
  [[ -z "$BUILD_PREFIX" ]] && return 0
  local url="$1" label display desc json code
  # Lead with the test name when given (most identifying); fall back to the branch.
  if [[ -n "$GOTEST_TESTCASE" ]]; then
    label="$GOTEST_TESTCASE"
  else
    label="$BRANCH"
  fi
  display="${BUILD_PREFIX} ${label}"
  desc="triggered by jenkins-run.sh | branch=${BRANCH} | tags=${TAGS}"
  [[ -n "$GOTEST_TESTCASE" ]] && desc="${desc} | testcase=${GOTEST_TESTCASE}"
  # jq builds the JSON so branch/testcase values are escaped safely.
  json="$(jq -n --arg d "$display" --arg c "$desc" '{displayName:$d,description:$c}')"
  local -a args=(-sS -o /dev/null -w '%{http_code}' -X POST -u "${JENKINS_USER}:${JENKINS_API_TOKEN}")
  [[ -n "$crumb_header" ]] && args+=(-H "$crumb_header")
  args+=(--data-urlencode "json=${json}")
  args+=("${url}/configSubmit")
  code="$(curl "${args[@]}" 2>/dev/null)" || code="000"
  if [[ "$code" == 2* || "$code" == 3* ]]; then
    echo "${PROG_NAME}: build display name set to '${display}'" >&2
  else
    echo "${PROG_NAME}: warning: could not set build display name (HTTP ${code}; need build-update permission?)" >&2
  fi
}

# ---------------------------------------------------------------------------
# Dry-run (§4): print the exact curl (JENKINS_API_TOKEN masked) and exit 0.
# CONFIG is summarised to its path/size to avoid spraying its adminToken.
# ---------------------------------------------------------------------------
if $DRY_RUN; then
  [[ -n "$config_host" ]] && echo "# target host: ${config_host}"
  fetch_crumb
  build_trigger_args   # same array the real POST uses — single source of truth
  echo "# dry-run — nothing is sent"
  printf 'curl'
  i=0
  while (( i < ${#trigger_args[@]} )); do
    a="${trigger_args[$i]}"
    case "$a" in
      -u)
        # value is "<user>:<token>" — mask the token
        cred="${trigger_args[$((i+1))]}"
        printf ' \\\n  -u %s:***' "${cred%%:*}"
        i=$((i+2)) ;;
      -H)
        printf ' \\\n  -H %q' "${trigger_args[$((i+1))]}"
        i=$((i+2)) ;;
      --data-urlencode)
        v="${trigger_args[$((i+1))]}"
        if [[ "$v" == CONFIG=* ]]; then
          printf ' \\\n  --data-urlencode CONFIG=<%s, %s bytes>' "$CONFIG_FILE" "$(wc -c <"$CONFIG_FILE" | tr -d ' ')"
        else
          printf ' \\\n  --data-urlencode %q' "$v"
        fi
        i=$((i+2)) ;;
      *)
        printf ' \\\n  %q' "$a"
        i=$((i+1)) ;;
    esac
  done
  printf ' \\\n  %q\n' "$triggerUrl"
  if [[ -n "$BUILD_PREFIX" ]]; then
    if [[ -n "$GOTEST_TESTCASE" ]]; then
      echo "# build display name would be: ${BUILD_PREFIX} ${GOTEST_TESTCASE}"
    else
      echo "# build display name would be: ${BUILD_PREFIX} ${BRANCH}"
    fi
  fi
  exit 0
fi

# ---------------------------------------------------------------------------
# Trigger the build (§4)
# ---------------------------------------------------------------------------
[[ -n "$config_host" ]] && echo "target host: ${config_host}" >&2
curl_err="$(mktemp)"
fetch_crumb
build_trigger_args
post_url="$triggerUrl"

# Follow server redirects (host alias → canonical HTTPS controller) by re-POSTing
# to each Location. curl -L is NOT used: it converts POST→GET on a 301, which
# would drop the buildWithParameters body. Bounded to 5 hops.
redirects=0
while :; do
  do_trigger
  if [[ "${http_code:-}" == 3?? ]] && [[ -n "${location:-}" ]]; then
    redirects=$((redirects + 1))
    if (( redirects > 5 )); then
      echo "${PROG_NAME}: too many redirects while triggering the build" >&2
      exit 1
    fi
    echo "${PROG_NAME}: redirect (${http_code}) → ${location}" >&2
    post_url="$location"
    continue
  fi
  break
done

# CSRF contingency: a 403 mentioning the crumb means the fetch silently failed
# or the crumb expired — refresh once and resend the identical POST (§4, assumptions).
if [[ "${http_code:-}" == "403" ]] && grep -qi 'crumb' "$resp_file"; then
  echo "${PROG_NAME}: CSRF crumb rejected (HTTP 403); refreshing crumb and retrying once..." >&2
  fetch_crumb
  build_trigger_args
  do_trigger
fi

if [[ "${http_code:-}" != 2* ]]; then
  echo "${PROG_NAME}: Jenkins rejected the trigger (HTTP ${http_code:-unknown})" >&2
  cat "$resp_file" >&2
  exit 1
fi

if [[ -z "${location:-}" ]]; then
  echo "${PROG_NAME}: trigger accepted but no Location header returned; cannot resolve the queue item." >&2
  cat "$resp_file" >&2
  exit 1
fi

queueUrl="${location%/}"

# --detach: return promptly after the POST. If a build display-name prefix is
# requested we still resolve the queue → build URL to tag it, then exit.
if $DETACH && [[ -z "$BUILD_PREFIX" ]]; then
  echo "Build queued: ${queueUrl}"
  exit 0
fi

# ---------------------------------------------------------------------------
# Resolve queue item → build URL (§5)
# ---------------------------------------------------------------------------
echo "Queued: ${queueUrl}"
buildUrl=""
buildNumber=""
while true; do
  qbody="$(curl -sS -u "$JENKINS_USER:$JENKINS_API_TOKEN" "${queueUrl}/api/json" 2>/dev/null)" || { sleep 5; continue; }

  if [[ "$(printf '%s' "$qbody" | jq -r '.cancelled // false')" == "true" ]]; then
    echo "${PROG_NAME}: build was cancelled in the Jenkins queue." >&2
    exit 1
  fi

  buildUrl="$(printf '%s' "$qbody" | jq -r '.executable.url // empty')"
  if [[ -n "$buildUrl" ]]; then
    buildNumber="$(printf '%s' "$qbody" | jq -r '.executable.number // empty')"
    break
  fi

  echo "queue: $(printf '%s' "$qbody" | jq -r '.why // "waiting in queue"')"
  sleep 5
done
buildUrl="${buildUrl%/}"

# Best-effort early tag (opt-in). The job sets its OWN displayName early in the
# run and may overwrite this, so we re-assert after the build finishes (below).
# In --detach mode this is the last step before exiting.
if [[ -n "$BUILD_PREFIX" ]]; then
  set_build_display_name "$buildUrl"
  if $DETACH; then
    echo "Build #${buildNumber}: ${buildUrl}"
    exit 0
  fi
fi

# ---------------------------------------------------------------------------
# Stream console via progressive-text (§6)
# ---------------------------------------------------------------------------
echo "Build #${buildNumber} started: ${buildUrl}"
echo "Streaming console output (gotestsum):"
echo "-----------------------------------------------------------------------"
startByte=0
more=true
hdr_file="$(mktemp)"
chunk_file="$(mktemp)"
while $more; do
  if ! curl -sS -u "$JENKINS_USER:$JENKINS_API_TOKEN" \
        -D "$hdr_file" -o "$chunk_file" \
        "${buildUrl}/logText/progressiveText?start=${startByte}" 2>/dev/null; then
    sleep 3
    continue
  fi
  cat "$chunk_file"
  startByte="$(awk '{l=tolower($0)} l~/^x-text-size:/{sub(/^x-text-size:[[:space:]]*/,"",l);gsub(/[[:space:][:cntrl:]]*$/,"",l);print l;exit}' "$hdr_file")"
  startByte="${startByte:-0}"
  more="$(awk '{l=tolower($0)} l~/^x-more-data:/{sub(/^x-more-data:[[:space:]]*/,"",l);gsub(/[[:space:][:cntrl:]]*$/,"",l);print l;exit}' "$hdr_file")"
  [[ "$more" == "true" ]] || more=false
  $more && sleep 3
done

# ---------------------------------------------------------------------------
# Final result + exit code (§7)
# ---------------------------------------------------------------------------
result=""
while true; do
  rbody="$(curl -sS -u "$JENKINS_USER:$JENKINS_API_TOKEN" "${buildUrl}/api/json" 2>/dev/null)" || { sleep 3; continue; }
  result="$(printf '%s' "$rbody" | jq -r '.result // empty')"
  [[ -n "$result" ]] && break
  sleep 3
done

# Authoritative re-tag: the build is finished, so the job will no longer
# overwrite the display name — this set sticks.
if [[ -n "$BUILD_PREFIX" ]]; then
  set_build_display_name "$buildUrl"
fi

echo "-----------------------------------------------------------------------"
echo "result: ${result}"
echo "${buildUrl}"   # browsable build URL printed last

if [[ "$result" == "SUCCESS" ]]; then
  exit 0
fi
exit 1
