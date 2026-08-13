#!/usr/bin/env bash
#
# update_all_demos.sh: rebuild mar, then redeploy every demo app in parallel.
#
# What it does:
#   1. `make`  - builds ./mar (embedded runtime stubs + iOS template). This is
#      the FULL build, so the deploy stubs carry the current runtime.js.
#   2. `./mar deploy examples/<app> --no-open` for each app below, all AT ONCE.
#
# Each app's output is written to its own file under deploy-logs/<timestamp>/,
# and a ✓/✗ summary prints at the end (failed logs are tailed). The script's
# exit code is non-zero if the build or any deploy failed.
#
#   Usage:  ./update_all_demos.sh
#
# Deploys are outward-facing (Fly / Cloudflare Pages) and use whatever Fly /
# wrangler auth is already on this machine. It always runs the freshly built
# ./mar, never a `mar` from PATH.

set -uo pipefail

# Run from the repo root (this script's own directory) so `examples/<app>`
# resolves and we pick up the ./mar we just built, not one on PATH.
cd "$(dirname "${BASH_SOURCE[0]}")" || exit 1
ROOT="$(pwd)"

# The demo apps to redeploy.
# Every app with a card on /demos. Keep this in step with Frontend/Demos.mar:
# an app missing here still has a card on the page, and that card goes stale
# the moment the runtime moves under it.
APPS=(lendas myrkheim mini-soccer seasons-gp quiz-duel daily-checklist pulse-runner star-condor iron-meridian pocket-synth vortex roll-call mar-trix)

# Colors only when stdout is a real terminal.
if [ -t 1 ]; then
  BOLD=$'\033[1m'; RED=$'\033[31m'; GREEN=$'\033[32m'; CYAN=$'\033[36m'; DIM=$'\033[2m'; RESET=$'\033[0m'
else
  BOLD=''; RED=''; GREEN=''; CYAN=''; DIM=''; RESET=''
fi
say() { printf '%s\n' "$*"; }

# === 1. Build ===============================================================
say "${BOLD}==> Building mar (make)${RESET}"
if ! make; then
  say "${RED}✗ make failed: aborting, nothing deployed.${RESET}"
  exit 1
fi
if [ ! -x ./mar ]; then
  say "${RED}✗ ./mar missing after make: aborting.${RESET}"
  exit 1
fi
say "${GREEN}✓ build ok${RESET}"

# === 2. Kick off every deploy in parallel ===================================
STAMP="$(date +%Y%m%d-%H%M%S)"
LOGDIR="$ROOT/deploy-logs/$STAMP"
mkdir -p "$LOGDIR"

say ""
say "${BOLD}==> Deploying ${#APPS[@]} apps in parallel${RESET} ${DIM}(logs: deploy-logs/$STAMP/)${RESET}"

pids=()
for app in "${APPS[@]}"; do
  # CI=1 forces non-interactive (skips the Fly confirm prompts, no browser);
  # --no-open belt-and-suspenders; </dev/null so nothing can block on stdin.
  ( CI=1 ./mar deploy "examples/$app" --no-open ) </dev/null >"$LOGDIR/$app.log" 2>&1 &
  pids+=("$!")
  say "  ${CYAN}→${RESET} $app ${DIM}(pid $!)${RESET}"
done

# === 3. Wait for each, record pass/fail =====================================
say ""
say "${BOLD}==> Waiting for deploys...${RESET}"
status=()
fail=0
for i in "${!APPS[@]}"; do
  app="${APPS[$i]}"
  if wait "${pids[$i]}"; then
    status[$i]=0
    say "  ${GREEN}✓${RESET} $app"
  else
    status[$i]=1
    fail=$((fail + 1))
    say "  ${RED}✗${RESET} $app ${DIM}(deploy-logs/$STAMP/$app.log)${RESET}"
  fi
done

# === 4. Summary =============================================================
say ""
if [ "$fail" -eq 0 ]; then
  say "${GREEN}${BOLD}All ${#APPS[@]} deploys succeeded.${RESET}"
  say "${DIM}Logs: deploy-logs/$STAMP/${RESET}"
  exit 0
fi

say "${RED}${BOLD}$fail of ${#APPS[@]} deploy(s) failed.${RESET} Tail of each failed log:"
for i in "${!APPS[@]}"; do
  if [ "${status[$i]}" -ne 0 ]; then
    app="${APPS[$i]}"
    say ""
    say "${RED}--- $app (last 20 lines) ---${RESET}"
    tail -n 20 "$LOGDIR/$app.log"
  fi
done
exit 1
