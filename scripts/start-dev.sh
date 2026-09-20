#!/usr/bin/env bash
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_DIR="$REPO_ROOT/src/server"
DATA_DIR="$REPO_ROOT/downloads"
FRONTEND_PORT="${FRONTEND_PORT:-5173}"
BACKEND_PORT="${BACKEND_PORT:-8080}"

# Get IP addresses
get_ips() {
    if command -v hostname >/dev/null 2>&1 && hostname -I >/dev/null 2>&1; then
        hostname -I
    elif command -v ip >/dev/null 2>&1; then
        ip route get 1.2.3.4 2>/dev/null | awk '{print $7}'
    else
        echo "127.0.0.1"
    fi
}

DETECTED_IPS=$(get_ips)

echo ""
echo "=========================================================================="
echo "                   YouTube Downloader - Dev Environment                   "
echo "=========================================================================="
echo ""
echo "  [Frontend - Vite Development Server]"
echo "    Local:   http://localhost:${FRONTEND_PORT}/"
for ip in $DETECTED_IPS; do
    echo "    Network: http://${ip}:${FRONTEND_PORT}/"
done
echo ""
echo "  [Backend - Go API Service]"
echo "    Local:   http://localhost:${BACKEND_PORT}/"
echo "    Health:  http://127.0.0.1:${BACKEND_PORT}/api/health"
for ip in $DETECTED_IPS; do
    echo "    Network: http://${ip}:${BACKEND_PORT}/"
done
echo ""
echo "  [Host IP Addresses]"
echo "    Loopback: 127.0.0.1"
for ip in $DETECTED_IPS; do
    echo "    Active:   ${ip}"
done
echo ""
echo "=========================================================================="
echo "  Press [Ctrl+C] to stop all development services."
echo "=========================================================================="
echo ""

cleanup() {
    echo ""
    echo "Stopping dev instances..."
    kill $(jobs -p) 2>/dev/null || true
    echo "Dev instances stopped."
}

trap cleanup EXIT INT TERM

# Start Backend
export CGO_ENABLED=0
export ADDR="127.0.0.1:${BACKEND_PORT}"
export DATA_DIR="${DATA_DIR}"
export NO_BROWSER=1
(cd "$SERVER_DIR" && go run .) &

# Start Frontend
(cd "$REPO_ROOT" && node ./node_modules/vite/bin/vite.js --port "$FRONTEND_PORT" --host) &

wait

