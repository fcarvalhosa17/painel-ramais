# Plano rc.8: chamadas e filas ao vivo (Etapa 2) e correções

**Data**: 2026-10-19
**Spec**: `docs/specs/2026-10-19-rc8-chamadas-e-filas.md`
**Decisões**: ADR-008, ADR-009 (Aceitos); ADR-003 atualizado; ADR-007 com nota. **Contrato**: `docs/CONTRATOS.md` (fonte única; backend e frontend trabalham em paralelo **sem conversar**, só pelo contrato). **Tela**: `docs/design/desvios.md` D28 a D37.
**Ponto de recuperação**: tag `v0.1.2-rc.7` (HEAD de partida). O usuário instala a rc.8 **direto sobre a rc.6** (esquema 3).

## Regras desta rodada (do usuário, valem para todos)

- **Nenhum teste**: não escrever teste, não rodar `go test`, não executar o binário, o simulador, o `cmd/loadtest` nem a tela no PC. Critério de pronto = **compila e confere por leitura**. O teste é ao vivo no servidor (spec, C1 a C14).
- AMI somente leitura: a única action nova é `QueueStatus`; nunca `Command`, `QueueReload`, `QueueReset`. Não ler nem escrever MySQL ou arquivo do Issabel; o `manager_custom.conf` só pelo bloco do painel no `postinstall`.
- `internal/state/view.go` **não muda** (nem comentário): `git diff --stat v0.1.2-rc.7 -- internal/state/view.go` tem de sair vazio.
- Sem travessão longo nos textos. Commit só no fim, pelo @devops (sem `Co-Authored-By`).
- Campo novo entra primeiro no CONTRATOS (pelo tech-lead). Arquivo de outro agente: pedir ao gerente.

## Donos de arquivo

| Agente | Arquivos | Não toca |
|---|---|---|
| backend | `internal/**` exceto `internal/web/static/**`; `cmd/**`; **nesta rodada também** `packaging/scripts/postinstall`, `instalar.py` (só o aviso do rollback, B7), `build.ps1` (só o alvo `test`, B2) e a remoção dos testes (`*_test.go`, `internal/ami/amitest/`, `internal/ami/testdata/`, `tests/`) | `internal/web/static/**`, `internal/state/view.go`, `testdata/capturas/**`, docs |
| frontend | `internal/web/static/**`; coluna "Tela" de D28 a D37 em `docs/design/desvios.md` | qualquer `.go`, CONTRATOS |
| devops | `README.md`, `docs/publicacao.md`, `packaging/LEIAME-INSTALACAO.md` (se citar teste), conferência do `.gitleaksignore`, release e commit | `.go`, `internal/web/static/**`, `postinstall`, `instalar.py`, `build.ps1` (já são do backend nesta rodada) |

Comando Go deste repositório (Git Bash, raiz):

```
export GOPATH=$PWD/.tools/gopath GOCACHE=$PWD/.tools/gocache GOMODCACHE=$PWD/.tools/gopath/pkg/mod GOTOOLCHAIN=local
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 ./.tools/go/bin/go.exe build ./... && ./.tools/go/bin/go.exe build ./... && ./.tools/go/bin/go.exe vet ./...
```

`gofmt -l cmd internal` vazio. Arquivo `.go` escrito pelo Write sai com CRLF: rodar `gofmt -w` nele e conferir `git ls-files --eol`. Editar Go por heredoc ou `node -e` corrompe literais: usar Edit/Write.

Ordem: backend, frontend e devops em paralelo (devops só fecha depois dos dois); revisor depois dos três; devops commita por último.

---

## Backend

Itens de instalador, testes e esquema ficam **separados e numerados** (B1, B2, B4, B5, B6, B7): na rc.7 itens de instalador foram esquecidos. Cada um tem o seu "pronto quando".

| # | Tarefa | Contrato | Pronto quando (compilação e leitura) |
|---|---|---|---|
| B1 | **Remover todos os testes**: `git rm` dos 40 `*_test.go` (`git ls-files '*_test.go'`), de `internal/ami/amitest/` (2 arquivos), de `internal/ami/testdata/` (13 arquivos) e de `tests/` (4 arquivos, inclusive `__init__.py`). **`testdata/capturas/` fica** (o `cmd/loadtest` usa) | D12 | `git ls-files '*_test.go' internal/ami/amitest internal/ami/testdata tests` vazio; `git ls-files testdata/capturas \| wc -l` = 21 (igual à rc.7); build linux e vet sem erro |
| [x] B2 | **`build.ps1`**: tirar o alvo `test` (do `ValidateSet`, da função `Test`, do `switch`, da linha de uso 1 e do comentário da linha 4, que cita `go test`). `release` e `all` já não rodam `go test` (rc.7) | D12 | `git grep -n "'test'\|function Test {\|go test\|Test }" build.ps1` vazio (o `Test-Path` fica) |
| [x] B3 | Menções a teste no código Go: comentário de `internal/ami/actions.go:9-13` ("Um teste garante...") passa a citar a conferência por leitura do CONTRATOS 7.1 e a lista de proibidas da 7.1 (inclui as de fila) | 7.1 | `git grep -n -i "um teste\|amitest\|_test.go" -- '*.go'` vazio |
| [x] B4 | **Migração v5** em `internal/store/store.go`: `ddlV5` **idêntica** ao bloco SQL da seção 10 (versão 5); `migrations` com 5 itens (`SchemaVersion` = 5); o `backup` continua **fora** do laço (uma chamada por `migrate`, só com a versão inicial). Novo `internal/store/queues.go`: `LoadQueueNames(ctx) (map[string]string, error)`, `SetQueueName(ctx, subject, queue, name string, now time.Time) (changed bool, err error)` (`name == ""` apaga; mesmo valor não grava), `ValidQueueName(name string) error` (regras de caractere do nome de grupo, sem nome reservado) | 4.8, 10 (versão 5), ADR-009 | diff da `ddlV5` contra o CONTRATOS sem diferença de conteúdo; `git show v0.1.2-rc.7:internal/store/store.go` e o atual com o mesmo `migrate` (só a lista cresce); `painel-ramais version` imprime `schema 5` por leitura de `SchemaVersion` |
| [x] B5 | **`packaging/scripts/postinstall`** (bloco AMI): `read = system,call,agent` e **um** `eventfilter = Event: X` para cada um dos onze (`QueueCallerJoin`, `QueueCallerLeave`, `QueueCallerAbandon`, `QueueMemberStatus`, `QueueMemberPause`, `QueueMemberAdded`, `QueueMemberRemoved`, `AgentCalled`, `AgentConnect`, `AgentComplete`, `AgentRingNoAnswer`), depois do `Shutdown`, na ordem do CONTRATOS 7.2; atualizar a NOTA de eventos do topo (classe `agent` e motivo). Só dentro do bloco do painel; nada fora dos marcadores | 7.2, ADR-003 | `bash -n packaging/scripts/postinstall` sem erro (só sintaxe); `grep -c "^eventfilter = Event: " packaging/scripts/postinstall` = 29; `grep -n "^read = " packaging/scripts/postinstall` = `system,call,agent`; LF conferido (`tools/check-lf.ps1`) |
| [x] B6 | **Simulador**: `internal/amisim/proto.go` `filterSet` com **exatamente** os 29 nomes do bloco (hoje faltam `ModuleLoadReport` e `FullyBooted`; entram os onze de fila); o usuário do simulador com leitura `agent` onde o `read` for configurado. Sem implementar `QueueStatus` no simulador (o daemon trata a resposta de action desconhecida como "sem módulo de filas", 11.6) | 7.2 | leitura: lista do `filterSet` igual à do `postinstall` (mesma contagem, mesmos nomes) |
| [x] B7 | **`instalar.py` rollback** (`cmd_rollback`, hoje `instalar.py:1267-1271`): condição `sch_ant == 3 and sch_db >= 4`; texto exato da seção 15.7 ("voltando para a rc.6 (esquema 3); o banco esta no esquema N", lista de perdas com "os nomes de fila"); o aviso genérico seguinte fica | 15.7, ADR-009 | `python -c "import ast,sys;ast.parse(open(sys.argv[1],encoding='utf-8').read())" instalar.py` sem erro (não usar `py_compile`: cria `__pycache__`); `vermin --target=3.6- instalar.py` sem violação; `git grep -n "voltando da rc.7" instalar.py` vazio |
| [x] B8 | `internal/model`: `LiveCall` (2.3) e `Queue`, `QueueEntryView`, `QueueMemberView` (2.4) com as tags JSON exatas (listas nunca `nil`; `*string` para os anuláveis); eventos da 11.6 (`QueueCallerJoined`, `QueueCallerLeft`, `QueueMemberChanged`, `QueueMemberRemoved`, `QueueAgentConnected`, `QueuesListed`, `QueueMember`, `QueueSnapshot`, `QueueEntry`) e `QueueNamesLoaded` (11.1); campos novos da 11.3 (`Linkedid`, `SrcLinkedid`, `DstLinkedid`, `DialString`, `ChannelCreated.Queue`); `Batch` e `Reset` com os campos da 11.4; `RegStatus` com `LostEvents`, `LostSince`, `LostUntil` | 2.3, 2.4, 11.1, 11.3, 11.4, 11.5, 11.6 | `json.Marshal` de cada tipo novo confere, por leitura das tags, com os exemplos JSON do CONTRATOS (nomes e anuláveis) |
| [x] B9 | `internal/ami/calls/calls.go`: `Linkedid` em `Newstate`, `NewCallerid`, `NewConnectedLine`, `BridgeEnter`, `BridgeLeave`, `Hold`, `Unhold`; no `DialBegin`, `Linkedid`, `DestLinkedid` e `DialString`; no `FromCoreShowChannel`, `Queue` = primeiro argumento do `ApplicationData` quando `Application` = `Queue` | 11.3 | leitura |
| [x] B10 | Novo `internal/ami/queues` (importa só `internal/ami` e `internal/model`): `Translate(ami.Message) []model.Event` (onze eventos; `AgentCalled`, `AgentComplete`, `AgentRingNoAnswer` devolvem nada) e `Status(ctx, *ami.Client, queue string) (model.QueuesListed, error)` (lista da `QueueStatus`, `Unsupported` para "Invalid/unknown command"). `internal/ami/actions.go`: `QueueStatus` na allowlist (única entrada nova) | 7.1, 11.6, 12 | `go list -f "{{.ImportPath}}: {{.Imports}}" ./internal/ami/queues` confere com o grafo da seção 12; `git diff v0.1.2-rc.7 -- internal/ami/actions.go` mostra só `QueueStatus` e o comentário |
| [x] B11 | `internal/state/channels.go`: mover o canal de grupo quando o `Linkedid` do evento muda (dentro do `mutate`, antes do resto); `internal/state/calls.go` (`describe`): ramal = endpoint presente em `v.exts`; canal não Local sem endpoint ou fora do inventário = externo (`dialed` = `outbound`, senão `inbound`), nunca `internal` | 2.2, 11.3 | `git diff --stat v0.1.2-rc.7 -- internal/state/view.go` vazio; leitura do `describe` contra a 2.2 |
| [x] B12 | `internal/state`, arquivos **novos** (por exemplo `livecalls.go` e `queues.go`): projeção de chamadas por `Linkedid` (origem guardada, `started_at`, direção, estado com precedência, `extensions`, `external`, `trunk`, `queue`, regra do tronco e do número discado) e de filas (entradas, membros, nomes, `QueuesListed` completo e por fila, `Unsupported`); marcação de sujos no `Apply` (só visão publicada; bridge marca os canais da bridge; fila marca o chamador); `Flush` remonta só os sujos, monta `Calls`/`CallsRemoved`/`Queues`/`QueuesRemoved`, soma a versão e sai se qualquer lista tiver item; `Reset` com tudo; `AMIDisconnected` esvazia; `SyncFinished` refaz a projeção; `InventoryListed` marca todas; `Options.OnLocalNoPeer func(uniqueid, linkedid, channel string)` chamado uma vez por `id` depois de 1 s; `Stats` com a quantidade de linhas e de filas | 2.3, 2.4, 11.4, 11.6, ADR-008 itens 10 a 12 | leitura: nenhum laço sobre todas as chamadas dentro do `Apply`; nenhuma escrita em banco; `view.go` intacto |
| [x] B13 | `cmd/painel-ramais/supervisor.go`: no `live()` (e no ramo que esvazia a fila na queda), `queues.Translate` ao lado do `ad.Translate`; `QueueCallerLeave`, `QueueCallerAbandon`, `AgentComplete` marcam a fila pendente; reconciliação completa depois de cada `sync` (conexão e overflow), depois de releitura com motivo `reload` e a cada 10 min (`queueFullEvery`); por fila no `poll` com `queueMinGap` de 30 s; prazos de 30 s e 10 s; erro = Warn e contador, nada apagado; `unsupported` com Info uma vez por sessão; contadores para o health (`last_full_at`, `refresh_errors`, `unsupported`) expostos sem import novo entre pacotes (escolha do backend). Se o arquivo passar de 800 linhas, seguir a regra da seção 12 (`internal/collector`) | 7.2, 11.6 | leitura: a `QueueStatus` roda na mesma goroutine do `live` (como o `reread`) |
| [x] B14 | `internal/api`: hub guarda `calls` e `queues` (mapas por `id` e `queue`) a partir de `Reset`/`Batch`; snapshot com `calls` e `queues` sempre presentes e ordenados; `deltaFrame` escreve as quatro chaves só quando a lista tem item; rota `POST /api/queues/rename` (administrador antes do corpo, 4 KiB, campo desconhecido 400, `acl.db` M1, `queue` existente na projeção do hub, `ValidNumber` e `ValidQueueName`, upsert ou apagar, log Info, recarga `QueueNamesLoaded`, `500` se a recarga falhar); `/api/health` com `live_calls`, `queues` e `registrations.lost_events`, `lost_since`, `lost_until` | 1, 4.1, 4.4, 4.8, 5.2 | a rota existe no `Handler()` com sessão e checagem de administrador; leitura do frame contra o exemplo da 5.2 |
| [x] B15 | `cmd/painel-ramais` (serve): `QueueNamesLoaded` na subida e depois de cada rename; `OnLocalNoPeer` vira log Info `chamada com Local sem par` (`uniqueid`, `linkedid`, `canal`); Warn uma vez na subida se o catálogo tiver o grupo `Todos os ramais` | 2.3, 9, 11.1 | leitura |
| [x] B16 | `internal/store/groups_edit.go` `ValidGroupName`: recusar `Todos os ramais` (sem diferenciar maiúsculas) com a mensagem da seção 9; conferir que `update` (nome atual) e `delete` não chamam `ValidGroupName` no nome atual e que `validAssignGroup` não muda; `internal/api/groups.go` sem regra paralela | 4.6, 9 | `git grep -n "Todos os ramais" -- internal/store` mostra a regra num ponto só; leitura de `UpdateGroup`/`DeleteGroup` |
| [x] B17 | `internal/reglog` e `internal/store/regreport.go`: sublotes por custo (teto `SubBatchCost` = 5000, nota inteira, `rev` por sublote), repetição única, `lost_events`/`lost_since`/`lost_until` e Warn `relatorio de registro: ocorrencias perdidas` (quantidade, `de`, `ate`, `erro`), Info na repetição que deu certo; `PurgeReg` em blocos de 5.000 (SQL da seção 10), parando no primeiro erro; nenhum `sql.Open` de escrita novo | 4.4, 10, 11.5, ADR-007 nota | leitura: custo calculado como na seção 10; nenhum caminho de descarte sem log e contador |
| B18 | `gofmt -w` nos arquivos tocados; build linux, build do host e vet | | comandos do topo sem erro; lista de arquivos no retorno |

**Pronto do backend**: B1 a B18 conferidos, build e vet sem erro, `gofmt -l cmd internal` vazio, `view.go` intacto, lista de arquivos e as linhas de cada `ddl`/regra no retorno.

## Frontend

| # | Tarefa | Contrato / desvio | Pronto quando (sintaxe e leitura) |
|---|---|---|---|
| F1 [x] | Aba "Chamadas": botão na `#abas`, painel `#aba-chamadas`, `ABAS` em `app.js` com `"chamadas"`, Densidade escondida, pintura e relógio só com a aba visível | D28 | leitura do `trocarAba` (três painéis) |
| F2 [x] | `dados.js`: snapshot com `calls` e `queues`; delta aplica `calls`, `calls_removed`, `queues`, `queues_removed` quando presentes (substitui por `id`/`queue`, apaga ignorando desconhecido); `reload` relê tudo. Tirar o comentário "testado com node --test" de `dados.js` e `estado.js` | 4.1, 5.2, D12 | leitura contra a tabela da 5.2; `git grep -n "node --test" internal/web/static` vazio |
| F3 [x] | Regras puras (em `estado.js` ou arquivo novo sem DOM): lista de cada chamada pela `direction`; filtros (grupo = algum ramal de `extensions` no grupo, pelo `groups` da lista de ramais; busca sem maiúsculas e acentos; tronco; fila; tipo); contadores (D29); textos de estado e de outro lado (2.3 e D31); derivados da fila (aguardando, maior espera, agentes por `state`) | 2.3, 2.4, D29 a D31 | leitura |
| F4 [x] | Três listas com rolagem virtual (técnica do D18, sem teto), mais longas primeiro, tempo ao vivo pelo `started_at` com o desvio de relógio do `server_time` | D31 | leitura: só as linhas visíveis no DOM |
| F5 [x] | Painel de filas e detalhe da fila (quem aguarda e agentes; nome do ramal pela lista de ramais) | 2.4, D32 | leitura |
| F6 [x] | Renomear fila (só administrador): `conexao.js` `renomearFila(queue, name)` para `POST api/queues/rename`; diálogo; erros 400/403/404 | 4.8, D33 | leitura |
| F7 [x] | Acessibilidade das listas novas: `role="list"`, `aria-label` com total, `role="listitem"`, roving tabindex, setas/Home/End rolando a janela, foco devolvido por `data-id`/`data-fila` com `preventScroll` | D34 | leitura |
| F8 [x] | "Chamadas ativas" da aba Ramais (`app.js`, hoje cerca de 556-660, `renderChamadas`/`pintarChamadas`): guardar o `dataset.numero` da linha focada antes de pintar e refocar a linha do mesmo número (`preventScroll: true`); roving tabindex; setas/Home/End rolando a janela; caixa com `role="list"` e `aria-label` | D34, item 6 da rc.8 | leitura |
| F9 [x] | "+ Novo grupo" dentro do seletor de "Adicionar aos grupos" e de "Adicionar ao grupo" (só administrador), grupo criado volta marcado; `dialogo-mover`/`dm-*` renomeados para `dialogo-vinculo`/`dv-*`; CSS sem uso removido | D35, D36 | `git grep -n "dialogo-mover\|\"dm-\|\.dm-" internal/web/static` vazio |
| F10 [x] | "Todos os ramais": manter o bloqueio da tela e mostrar a mensagem do `400` do daemon | D37 | leitura |
| F11 [x] | CSP: nada inline, nada externo, texto do Asterisk só por `textContent` | 1.3 | `git grep -n "innerHTML\|on[a-z]*=\"" internal/web/static` sem caso novo |
| F12 [x] | Coluna "Tela" de D28 a D37 em `docs/design/desvios.md` com arquivo:linha; ajuste de medida que a montagem pedir vira linha nova (D38 em diante) | | todas preenchidas |

**Pronto do frontend**: `node --check` em cada `internal/web/static/assets/*.js` sem erro (só sintaxe, não executa a tela), F1 a F12 conferidos.

## Devops

| # | Tarefa | Pronto quando |
|---|---|---|
| D1 | `README.md`: aba Chamadas e filas, rota de renomear fila, `live_calls`/`queues`/`registrations.lost_*` do health, classe `agent` e os onze eventos, esquema 5, upgrade rc.6 para rc.8 e rollback para a rc.6 (CONTRATOS 15.5 e 15.7); tirar a seção de testes (linhas que citam `python -m unittest`, `go test`, `go test -race`) | leitura contra o CONTRATOS; `git grep -n "unittest\|go test" README.md` vazio |
| D2 | `docs/publicacao.md`: tirar as citações de arquivos de teste e de `internal/ami/testdata` (linhas de achado e listas), deixando uma linha "rc.8: os testes foram removidos do repositório (CONTRATOS D12)" | `git grep -n "_test.go\|ami/testdata\|unittest" docs/publicacao.md` vazio |
| D3 | `.gitleaksignore` (o tech-lead já trocou para `docs/CONTRATOS.md:generic-api-key:328` e tirou as linhas de `_test.go`): depois do B1 e de toda edição de docs, `.tools/gitleaks/gitleaks.exe detect --no-git --source . --gitleaks-ignore-path .gitleaksignore --no-banner` (só confere fingerprint) | `no leaks found`; se a linha do CONTRATOS mudou, achar a nova com ignore vazio e acertar |
| D4 | Release `v0.1.2-rc.8` (`.\build.ps1 release -Version 0.1.2 -Pre rc.8`, sem `go test`); notas da pré-release com a situação "instalar sobre a rc.6" e o rollback | tag e Release publicadas; `versao.txt` do bundle diz `schema 5` |
| D5 | Commit único no fim com os arquivos dos três agentes e os docs do tech-lead; conferir `git diff --cached --name-status` (remoções do B1 incluídas) | commit sem `Co-Authored-By`; `git status` limpo |

## Revisor (depois de backend, frontend e devops; antes do commit)

Ler o diff contra o contrato e devolver veredito (aprovado, aprovado com ajustes, reprovado):
1. Esquema: `ddlV5` igual à seção 10; backup único antes do laço; `SchemaVersion` 5; aviso de rollback com a condição `>= 4` e o texto da 15.7.
2. Bloco AMI: `read = system,call,agent`, 29 `eventfilter`, só dentro dos marcadores; `filterSet` do simulador igual; allowlist com **só** `QueueStatus` a mais.
3. Projeção: `id` = `Linkedid`; regra de ramal por inventário (inclusive no `describe`); direção guardada; precedência de estado; regra do tronco e do número discado; `Linkedid` atualizado só em `channels.go`; `view.go` intacto.
4. Custo: o `Apply` só marca `Linkedid` (e a bridge); o `Flush` remonta só os sujos; lote sai com qualquer lista com item; versão soma os itens.
5. Filas: idempotência de `Join`/`Leave`/`Member*`; completa substitui tudo; por fila respeita 30 s; erro nunca apaga estado.
6. Segurança: rota de renomear com administrador antes do corpo, `acl.db`, 4 KiB, validação, sem `innerHTML`; nenhuma action nova além de `QueueStatus`.
7. Erros: perda do relatório nunca silenciosa (contador, período, Warn); `QueueStatus` com erro vira contador e Warn; "Local sem par" uma vez por chamada.
8. Remoções: nenhum `*_test.go`, `amitest`, `ami/testdata`, `tests/`; `testdata/capturas` intacto; `build.ps1` sem alvo `test`; build linux e vet passam.
9. Tela: D28 a D37 com coluna "Tela"; roving tabindex e foco preservado nas quatro listas; "+ Novo grupo" só para administrador.

## Pendências conhecidas (não bloqueiam a rc.8)

- S13 a S17 (CONTRATOS 13) só se confirmam ao vivo: campo `DialString`, `Linkedid` depois de transferência, eventos de fila do Asterisk 18, interface dos membros, nome de canal DAHDI/IAX2/Khomp.
- Conferência (ConfBridge) com vários externos pode virar uma linha só se o Asterisk unificar o `Linkedid` na bridge (efeito do mesmo mecanismo do S14); aceito até ver ao vivo.
- Chamada restaurada por snapshot perde a espera (`hold`) e, se já saiu da aplicação `Queue`, a fila "via" (limite do `CoreShowChannels`).
- Intervalo fora do ar aberto se perde no restart do daemon (ADR-007, alternativa E).
- A tabela incremental de registros pode ficar até 1 h defasada numa mudança de grupo (`internal/api/registrations.go`, conferir a linha na tag antes de citar).
- Faixa acima de 2^53 perde precisão no JavaScript (`estado.js`).
- Sem teste automatizado no repositório (D12): regressão só aparece ao vivo; `cmd/loadtest` e `testdata/capturas` continuam disponíveis para medição na VM.
