#!/bin/sh
# healarr NAS daemon launcher for DSM Task Scheduler (boot-up, user = agent user).
set -eu
HEALARR_DIR=/volume1/docker/healarr
export TZ=Australia/Brisbane
cd "$HEALARR_DIR"
if [ -f "$HEALARR_DIR/agent.pid" ] && kill -0 "$(cat "$HEALARR_DIR/agent.pid")" 2>/dev/null; then
  echo "healarr already running (pid $(cat "$HEALARR_DIR/agent.pid"))"; exit 0
fi
nohup setsid "$HEALARR_DIR/healarr" agent serve --config "$HEALARR_DIR/config.toml" >> "$HEALARR_DIR/agent.log" 2>&1 &
echo $! > "$HEALARR_DIR/agent.pid"
echo "healarr started (pid $(cat "$HEALARR_DIR/agent.pid"))"
