#!/bin/bash
# Runs ON the training Mac, in ~/zshai-train. Thermal-guarded MLX LoRA -> fuse -> GGUF (f16 -> Q4_K_M, Q8_0).
#   ./run-mac.sh <hf-base> <name> <iters> [lora.yaml]
# Needs: ~/psai/mlx (mlx-lm), conv/ (python with llama.cpp's convert deps), llama.cpp/ (built llama-quantize),
#        ~/psai/thermal.sh (prints the macOS thermal state 0..3).
set -uo pipefail
BASE=$1; NAME=$2; ITERS=$3; CFG=${4:-lora.yaml}
cd ~/zshai-train
MLX=~/psai/mlx/bin; T=~/psai/thermal.sh; TL=logs/$NAME.thermal.log; LOG=logs/$NAME.log
mkdir -p logs adapters fused gguf
: > "$TL"; rm -f "logs/$NAME.done" "logs/$NAME.failed"
log() { echo "$(date '+%F %T') $*" >> "$TL"; }
while s=$($T); [ "$s" -gt 1 ]; do log "state=$s waiting to start"; sleep 60; done
sed "s/arguments: \[\(.*\), [0-9]*, \(.*\)\]/arguments: [\1, $ITERS, \2]/" "$CFG" > "logs/$NAME.yaml"
PYTHONUNBUFFERED=1 $MLX/mlx_lm.lora -c "logs/$NAME.yaml" --model "$BASE" --iters "$ITERS" --adapter-path "adapters/$NAME" > "$LOG" 2>&1 &
PID=$!; paused=0
while kill -0 $PID 2>/dev/null; do
  s=$($T)
  if [ "$s" -ge 2 ] && [ $paused -eq 0 ]; then kill -STOP $PID; paused=1; log "state=$s PAUSED"
  elif [ "$s" -le 1 ] && [ $paused -eq 1 ]; then kill -CONT $PID; paused=0; log "state=$s resumed"; fi
  sleep 30
done
wait $PID; rc=$?
log "training exited rc=$rc"
[ $rc -ne 0 ] && { touch "logs/$NAME.failed"; exit $rc; }
rm -rf "fused/$NAME"
$MLX/mlx_lm.fuse --model "$BASE" --adapter-path "adapters/$NAME" --save-path "fused/$NAME" --dequantize >> "$LOG" 2>&1 \
 && conv/bin/python -c "
import json,sys; p=sys.argv[1]; c=json.load(open(p))
# newer mlx/transformers write a list here; the converter's transformers wants a dict
if isinstance(c.get('extra_special_tokens'), list): del c['extra_special_tokens']
json.dump(c, open(p,'w'), indent=2)" "fused/$NAME/tokenizer_config.json" \
 && conv/bin/python llama.cpp/convert_hf_to_gguf.py "fused/$NAME" --outfile "gguf/$NAME-f16.gguf" --outtype f16 >> "$LOG" 2>&1 \
 && llama.cpp/build-static/bin/llama-quantize "gguf/$NAME-f16.gguf" "gguf/$NAME-Q4_K_M.gguf" Q4_K_M >> "$LOG" 2>&1 \
 && llama.cpp/build-static/bin/llama-quantize "gguf/$NAME-f16.gguf" "gguf/$NAME-Q8_0.gguf" Q8_0 >> "$LOG" 2>&1 \
 && ls -la gguf/$NAME-* >> "$LOG" && log "fused + gguf" && touch "logs/$NAME.done" || { touch "logs/$NAME.failed"; log "fuse/convert failed"; }
