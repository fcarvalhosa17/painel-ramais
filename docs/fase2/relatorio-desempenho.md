# Relatório de desempenho da tela (fase 2)

> Medido com simulador, antes da regra de não testar neste computador. Os scripts `cmd/telalocal` e `tools/tela/medir.mjs` citados abaixo foram removidos do repositório; a validação vale na VM do Issabel. Os ajustes de tela posteriores (indicadores com "–", ranking, outro lado, layout 1250×700) não estão refletidos nestes números nem nos prints.

Medição local sem VM, plano `docs/plans/2026-10-16-fase2-menu-issabel.md` seção 6.

- Quando: 2026-10-16T16:30:23Z, depois dos ajustes da revisão (token recusado não cai no cookie, histórico volta ao entrar de novo, limite do CSV pelo corpo JSON). As execuções anteriores, às 16:10 e às 16:12, sem esses ajustes, deram mediana de 235 ms na primeira exibição e rajadas entre 171 e 249 ms.
- Ambiente: Windows 10 Pro, `cmd/telalocal` com o daemon compilado da árvore atual (`go build ./cmd/painel-ramais`, tela embutida), simulador com 10.000 ramais e carga de 400 chamadas (`-exts 10000 -calls 400`), 24 grupos de 400 ramais importados pela API de grupos (mais "Sem grupo" com 400).
- Navegadores: Chromium headless do Playwright 1.60 (viewport 1920x1080) e WebKit 2336.
- Comando: `node tools/tela/medir.mjs --url http://localhost:8091 --prints docs/fase2` (saída JSON, código 0 = metas cumpridas). Saiu 0.

## Resultado contra as metas

| Medida | Meta | Medido | Situação |
|---|---|---|---|
| Primeira exibição (`__painel.primeiraExibicaoMs`, relógio do iframe), mediana de 5 aberturas com cache frio | < 2.000 ms | mediana 302 ms; pior 327 ms (311, 327, 302, 285, 255) | cumprida |
| Primeira exibição contada desde o `goto` da página do Issabel falso (inclui buscar o token e montar o iframe) | informativo | mediana 637 ms; pior 665 ms | |
| Rajada de 3.000 desregistros: do `POST /sim/ctl/burst?n=3000` até `__painel.contagens.unregistered >= base + 3000` pintado | < 1.000 ms nas 3 | 270 ms, 235 ms, 138 ms (base 692) | cumprida |
| Tarefa longa depois da primeira exibição (rajada, restauração, 10 s de rolagem da lista, busca digitada, gráfico de 7 d, hover no mapa) | nenhuma > 200 ms | 0 tarefas longas (nenhuma acima de 50 ms) | cumprida |
| Redesenho do mapa durante as rajadas | no máximo 4 por segundo | 24 redesenhos, pico de 4 numa janela de 1 s | cumprida |
| Cards no DOM com 10.000 ramais | cerca de 200 no máximo | 48 no máximo durante a rolagem; o último ramal aparece no fim da lista | cumprida |
| WebKit | mostra dados e o `/ws` conecta | 10.000 ramais na tela, status "Ao vivo" (WebSocket aberto e `hello` enviado), primeira exibição 814 ms, sem erro de console | cumprida |

Erros de console e de página no Chromium durante a sessão medida: nenhum.

## Controle do instrumento

O observador de tarefas longas (`PerformanceObserver` "longtask", só Chromium) foi conferido com uma tarefa forçada de 300 ms dentro do frame da tela: apareceu em `__painel.tarefasLongas` com duração 300 ms. Antes da primeira exibição ele registra duas tarefas de 50 a 62 ms (leitura e aplicação do snapshot de 10.000 ramais), que a regra da medição não conta.

## O que não foi medido aqui

- Purge com o daemon avisado por SIGHUP: no Windows a CLI não acha o daemon pelo `systemctl`, então o fechamento 4001 do `/ws` em até 2 s não foi exercitado. Foi verificado o outro caminho: depois de `painel-ramais session purge --issabel medicao` ("2 sessão(ões) removida(s)"), a próxima chamada REST recebe 401 e a tela troca para "Sessão encerrada. Abra de novo pelo menu PBX do Issabel.". O caminho do 4001 fica para a homologação na VM.
- O gráfico de 7 d recebeu os 10.080 pontos do daemon, mas o daemon recém-iniciado só tinha poucos minutos gravados (o resto vem como `gap`). O desenho de 7 d com a série cheia foi exercitado contra um servidor de simulação com dados densos, sem tarefa longa; esse número não entra aqui porque não é o daemon real.
- Rede real, Apache e Issabel 5: nada disso entra na medição local.

## Prints

- `docs/fase2/tela-mapa-geral.png`: visão geral com 24 grupos, ranking e histórico de 24 h.
- `docs/fase2/tela-grupo-aberto.png`: grupo Financeiro aberto pelo ranking (cards, legenda e histórico do grupo).
- `docs/fase2/tela-ami-desconectado.png`: `POST /sim/ctl/down?d=8s`, faixa "Conexão com o Asterisk perdida." e os 10.000 ramais em desconhecido.
