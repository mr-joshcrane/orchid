#!/usr/bin/env bash
# Run the full experiment: control then treatment for all questions
# Usage: ./run-experiment.sh [codebase-name]
# Example: ./run-experiment.sh kubernetes
#          ./run-experiment.sh traefik    (default)

set -e

CODEBASE="${1:-traefik}"
ORCHID_DIR="$(cd "$(dirname "$0")" && pwd)"

case "$CODEBASE" in
  traefik)
    export CODEBASE_ROOT="/tmp/traefik"
    export CODEBASE_NAME="traefik"
    # Use default hardcoded questions (no QUESTIONS_FILE)
    unset QUESTIONS_FILE
    ;;
  templ)
    export CODEBASE_ROOT="/tmp/templ"
    export CODEBASE_NAME="templ"
    export QUESTIONS_FILE="/tmp/templ/questions.json"
    ;;
  prometheus)
    export CODEBASE_ROOT="/tmp/prometheus"
    export CODEBASE_NAME="prometheus"
    export QUESTIONS_FILE="/tmp/prometheus/questions.json"
    ;;
  kubernetes)
    export CODEBASE_ROOT="/tmp/kubernetes"
    export CODEBASE_NAME="kubernetes"
    export QUESTIONS_FILE="/tmp/kubernetes/questions.json"
    ;;
  *)
    echo "Unknown codebase: $CODEBASE"
    echo "Supported: traefik, templ, prometheus, kubernetes"
    echo "Or set CODEBASE_ROOT, CODEBASE_NAME, QUESTIONS_FILE manually"
    exit 1
    ;;
esac

RESULTS_DIR="/tmp/experiment-results-${CODEBASE}"
mkdir -p "$RESULTS_DIR"

export EMBED_ENDPOINT="${EMBED_ENDPOINT:-https://nixcariot.tail60ffe.ts.net/embeddings}"
export GH_TOKEN="${GH_TOKEN:-$(gh auth token)}"

cd "$ORCHID_DIR"

echo "Building experiment binary..."
go build -tags sqlite -o experiment ./cmd/experiment || { echo "Build failed"; exit 1; }
go build -o semantic ./cmd/semantic || { echo "Build failed"; exit 1; }

echo "Building semantic index for $CODEBASE..."
export SEMANTIC_INDEX="/tmp/${CODEBASE_NAME}-index.json"
(cd "$CODEBASE_ROOT" && "$ORCHID_DIR/semantic" index .) || { echo "Index build failed"; exit 1; }
mv "$CODEBASE_ROOT/index.json" "$SEMANTIC_INDEX"

NUM_QUESTIONS=$(./experiment count 2>&1)
echo "Codebase: $CODEBASE ($CODEBASE_ROOT)"
echo "Found $NUM_QUESTIONS questions"
echo "Results: $RESULTS_DIR"
echo ""

echo "════════════════════════════════════════"
echo "  CONTROL (no semantic search)"
echo "════════════════════════════════════════"
./experiment control 2>"$RESULTS_DIR/control.stderr" | tee "$RESULTS_DIR/control.txt"

echo ""
echo "════════════════════════════════════════"
echo "  TREATMENT (with semantic search)"
echo "════════════════════════════════════════"
./experiment treatment 2>"$RESULTS_DIR/treatment.stderr" | tee "$RESULTS_DIR/treatment.txt"

echo ""
echo "Done. Results in $RESULTS_DIR"
