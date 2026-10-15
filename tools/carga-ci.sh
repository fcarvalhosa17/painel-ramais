#!/bin/sh
# Carga do CI (um modo): daemon + simulador com 10.000 ramais, CPU/RSS por minuto, heap Go
# pos-GC por minuto, perfis pprof de inicio/fim e relatorio com veredito.
# Uso: carga-ci.sh <pjsip|sip> <duracao_de_medicao> <aquecimento>   (ex.: pjsip 5h 10m)
# Espera ./painel-ramais e ./loadtest (linux/amd64) no diretorio atual; grava tudo em saida/.
# Sai com o codigo do loadtest: 0 = APROVA e demais criterios ok, 1 = REPROVA/falha, 3 = daemon ausente.
MODO=$1; DURACAO=$2; WARMUP=$3
[ -n "$MODO" ] && [ -n "$DURACAO" ] && [ -n "$WARMUP" ] || { echo "uso: $0 <modo> <duracao> <aquecimento>" >&2; exit 2; }
mkdir -p saida
sh tools/amostrar-proc.sh painel-ramais "saida/cpu-rss-$MODO.csv" 60 &
amostrador=$!
rc=0
./loadtest -daemon ./painel-ramais -modes "$MODO" -exts 10000 -duration "$DURACAO" -warmup "$WARMUP" -heap \
  -captures "" -out "saida/relatorio-carga-$MODO.md" || rc=$?
kill "$amostrador" 2>/dev/null || true
echo "loadtest saiu com $rc"
# Diff de heap vivo (inicio -> fim): a estrutura que cresce aparece no topo.
if [ -s "saida/inicio-$MODO.pb.gz" ] && [ -s "saida/fim-$MODO.pb.gz" ]; then
  go tool pprof -top -inuse_space -nodecount=40 -base "saida/inicio-$MODO.pb.gz" "saida/fim-$MODO.pb.gz" > "saida/diff-pprof-$MODO.txt" 2>&1 \
    || echo "go tool pprof falhou; veja saida/diff-pprof-$MODO.txt"
else
  echo "perfis inicio/fim ausentes: sem diff pprof" > "saida/diff-pprof-$MODO.txt"
fi
# Resumo na pagina do run (inclinacao, R2, n, veredito)
if [ -n "$GITHUB_STEP_SUMMARY" ]; then
  { echo "### Heap pos-GC ($MODO)"; sed -n '/^### Heap Go pos-GC/,/^## /p' "saida/relatorio-carga-$MODO.md" | grep '^|'; } >> "$GITHUB_STEP_SUMMARY"
fi
exit "$rc"
