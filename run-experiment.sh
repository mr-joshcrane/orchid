#!/usr/bin/env bash
# Run the full experiment: control then treatment for all questions
# Usage: ./run-experiment.sh [codebase-name]
# Example: ./run-experiment.sh kubernetes
#          ./run-experiment.sh traefik    (default)

set -e

CODEBASE="${1:-traefik}"
ORCHID_DIR="/home/joshc/code/orchid"

case "$CODEBASE" in
  traefik)
    export CODEBASE_ROOT="/tmp/traefik"
    export CODEBASE_NAME="traefik"
    # Use default hardcoded questions (no QUESTIONS_FILE)
    unset QUESTIONS_FILE
    ;;
  kubernetes)
    export CODEBASE_ROOT="/tmp/kubernetes"
    export CODEBASE_NAME="kubernetes"
    export QUESTIONS_FILE="/tmp/kubernetes/questions.json"
    ;;
  *)
    echo "Unknown codebase: $CODEBASE"
    echo "Supported: traefik, kubernetes"
    echo "Or set CODEBASE_ROOT, CODEBASE_NAME, QUESTIONS_FILE manually"
    exit 1
    ;;
esac

RESULTS_DIR="/tmp/experiment-results-${CODEBASE}"
mkdir -p "$RESULTS_DIR"

export LLAMA_EMBEDDING=/home/joshc/code/civclone/scripts/llama-cpp/build/bin/llama-embedding
export EMBED_MODEL="$HOME/models/qwen3-embedding-4b/Qwen3-Embedding-4B-Q6_K.gguf"

cd "$ORCHID_DIR"

echo "Building experiment binary..."
go build -o experiment ./cmd/experiment || { echo "Build failed"; exit 1; }

NUM_QUESTIONS=$(./experiment count 2>&1)
echo "Codebase: $CODEBASE ($CODEBASE_ROOT)"
echo "Found $NUM_QUESTIONS questions"
echo "Results: $RESULTS_DIR"
echo ""

for q in $(seq 0 $((NUM_QUESTIONS - 1))); do
  echo "════════════════════════════════════════"
  echo "  Question $q — CONTROL (no semantic search)"
  echo "════════════════════════════════════════"
  ./experiment control "$q" 2>"$RESULTS_DIR/q${q}-control.stderr" | tee "$RESULTS_DIR/q${q}-control.txt"

  echo ""
  echo "════════════════════════════════════════"
  echo "  Question $q — TREATMENT (with semantic search)"
  echo "════════════════════════════════════════"
  ./experiment treatment "$q" 2>"$RESULTS_DIR/q${q}-treatment.stderr" | tee "$RESULTS_DIR/q${q}-treatment.txt"

  echo ""
  echo ""
done

echo "Done. Results in $RESULTS_DIR"
