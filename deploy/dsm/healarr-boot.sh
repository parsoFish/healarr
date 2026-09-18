#!/bin/sh
# healarr NAS daemon launcher for DSM Task Scheduler (boot-up, user = agent user).
set -eu
HEALARR_DIR=/volume1/docker/healarr
export TZ=Australia/Brisbane
cd "$HEALARR_DIR"
if [ -f "$HEALARR_DIR/agent.pid" ] && kill -0 "$(cat "$HEALARR_DIR/agent.pid")" 2>/dev/null; then
  echo "healarr already running (pid $(cat "$HEALARR_DIR/agent.pid"))"; exit 0
fi
nohup "$HEALARR_DIR/healarr" agent serve --config "$HEALARR_DIR/config.toml" >> "$HEALARR_DIR/agent.log" 2>&1 &
pid=$!
# DSM's task history only records this script's exit status, so a daemon
# that died on a bad config would otherwise be reported as a clean start
# with a pid file pointing at nothing. Give it a moment to fail, then say so.
sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
  echo "healarr failed to start; see $HEALARR_DIR/agent.log"
  exit 1
fi
echo "$pid" > "$HEALARR_DIR/agent.pid"
echo "healarr started (pid $pid)"
