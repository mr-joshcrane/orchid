#!/bin/bash
# Benchmark: 4 sequential vs 4 concurrent requests (unique texts)
ENDPOINT="http://127.0.0.1:8090/v1/embeddings"

A='func NewBedrockGenerator(model string, client BedrockClient, opts ...BedrockOption) *BedrockGenerator { g := &BedrockGenerator{model: model, client: client, maxRetries: 3}; for _, opt := range opts { opt(g) }; return g }'
B='func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) { conn, err := upgrader.Upgrade(w, r, nil); if err != nil { log.Printf("upgrade: %v", err); return }; defer conn.Close() }'
C='type Pipeline struct { Steps []Step; Config PipelineConfig; Logger *slog.Logger; Metrics *MetricsCollector; mu sync.Mutex; running bool; cancel context.CancelFunc }'
D='func TestParseChunks(t *testing.T) { chunks, err := parseDir("testdata"); if err != nil { t.Fatal(err) }; if len(chunks) == 0 { t.Fatal("expected chunks") } }'

embed() { curl -s "$ENDPOINT" -H 'Content-Type: application/json' -d "$(jq -n --arg t "$1" '{"input": $t}')" > /dev/null; }

# Warmup
embed "$A"

echo "=== 4 sequential ==="
START=$(date +%s%N)
embed "$A"
embed "$B"
embed "$C"
embed "$D"
END=$(date +%s%N)
echo "  time: $(( (END - START) / 1000000 ))ms"

echo "=== 4 concurrent ==="
START=$(date +%s%N)
embed "$A" &
embed "$B" &
embed "$C" &
embed "$D" &
wait
END=$(date +%s%N)
echo "  time: $(( (END - START) / 1000000 ))ms"
