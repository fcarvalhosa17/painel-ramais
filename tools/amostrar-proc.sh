#!/bin/sh
# Amostra CPU e RSS de um processo (Linux) e grava CSV. Uso: amostrar-proc.sh <nome-exato> <saida.csv> [intervalo_s=60]
# CPU em % de 1 nucleo desde a amostra anterior (a primeira linha com dado vale desde o inicio do processo).
# Espera o processo aparecer; roda ate ser morto (kill). PROC_ROOT existe so para teste.
NOME=$1; OUT=$2; INTERVALO=${3:-60}; PROC=${PROC_ROOT:-/proc}
[ -n "$NOME" ] && [ -n "$OUT" ] || { echo "uso: $0 <nome> <saida.csv> [intervalo_s]" >&2; exit 2; }
HZ=$(getconf CLK_TCK 2>/dev/null || echo 100)
echo "timestamp_utc,minuto,pid,rss_kb,cpu_pct_1nucleo" > "$OUT"
ANT_T=; ANT_TICKS=; ANT_PID=; MIN=0
while :; do
  PID=$(pgrep -x "$NOME" | head -n 1)
  if [ -n "$PID" ] && [ -r "$PROC/$PID/stat" ]; then
    NOW=$(date +%s)
    # depois do ") " do campo comm: $12 = utime, $13 = stime (campos 14 e 15 do stat)
    TICKS=$(sed 's/.*) //' "$PROC/$PID/stat" | awk '{print $12 + $13}')
    RSS=$(awk '/^VmRSS:/ {print $2}' "$PROC/$PID/status")
    # PID novo (processo reiniciado) ou contador menor: recomeca o calculo, nunca CPU negativa
    if [ -z "$ANT_T" ] || [ "$PID" != "$ANT_PID" ] || [ "$TICKS" -lt "$ANT_TICKS" ]; then
      CPU=  # sem intervalo anterior comparavel
    else
      CPU=$(awk -v d="$((TICKS - ANT_TICKS))" -v hz="$HZ" -v s="$((NOW - ANT_T))" 'BEGIN {printf "%.1f", (s > 0) ? d / hz / s * 100 : 0}')
    fi
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ),$MIN,$PID,${RSS:-0},$CPU" >> "$OUT"
    ANT_T=$NOW; ANT_TICKS=$TICKS; ANT_PID=$PID; MIN=$((MIN + 1))
  fi
  sleep "$INTERVALO"
done
