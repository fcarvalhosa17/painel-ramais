# Plano rc.9: estado de registro persistente, pausa por reinício real, filas sem ciclo e correções

**Data**: 2026-10-19
**Spec**: `docs/specs/2026-10-19-rc9-registros-e-filas.md`
**Decisões**: ADR-010 e ADR-011 (Aceitos); ADR-007 com a alternativa E marcada como feita. **Contrato**: `docs/CONTRATOS.md` (fonte única). Backend e frontend trabalham em paralelo **sem conversar**, só pelo contrato. **Tela**: `docs/design/desvios.md` D38 a D45.
**Ponto de partida**: tag `v0.1.2-rc.8`; o HEAD tem 3 commits só de README e docs a mais. Todas as linhas citadas abaixo foram conferidas na tag (`git show v0.1.2-rc.8:<arquivo>`). Quem for editar confere de novo antes, porque a linha anda conforme os itens entram.

## Regras desta rodada (do usuário, valem para todos)

- **Nenhum teste**: não escrever teste, não rodar `go test`, não executar o binário nem a tela no PC. Pronto = **compila e confere por leitura** (D12). O teste é ao vivo no servidor (roteiro R1 a R13 no fim).
- AMI só leitura; a allowlist (`internal/ami/actions.go:16-31`) **não muda**. `manager.conf` e o MySQL do Issabel ficam intocados; no `manager_custom.conf`, só o bloco do painel pelo `postinstall`.
- Alvo Rocky 8 com Issabel 5, só IPv4.
- Sem travessão longo nos textos (o hook bloqueia em `.html`/`.js` de tela; nos outros, é disciplina). Commit só no fim, pelo @devops, sem `Co-Authored-By`.
- Campo novo entra primeiro no CONTRATOS (tech-lead). Arquivo de outro agente: pedir ao gerente.
- **`internal/state/view.go` muda nesta rodada** (a regra da rc.8 "view.go intocado" valia só para a rc.8): só os campos de `extState`, `model()`, `removeExt` e o gancho do `onEndpointSeen` (B4).
- **Servidor do usuário na rc.8** (esquema 5, instalada em 2026-10-19): a rc.9 migra 5 para 6 e cria o `.bak-v5`; o rollback volta para a rc.8 pelo `.bak-v5` (o `.bak-v3` não é usado; CONTRATOS 15.7).
- **Item 11 decidido** (opção (b), spec "Item 11", D46): F11.

## Donos de arquivo

| Agente | Arquivos | Não toca |
|---|---|---|
| backend | `internal/**` exceto `internal/web/static/**`; `cmd/**`; `packaging/scripts/postinstall` (só B12); `instalar.py` (só B13); `.gitattributes` (só a regra de `testdata/capturas`, B14); a remoção do B14 | `internal/web/static/**`, docs, README |
| frontend | `internal/web/static/**`; coluna "Tela" de D38 a D45 em `docs/design/desvios.md` | qualquer `.go`, `instalar.py`, CONTRATOS |
| devops | `README.md`, `docs/referencia-tecnica.md`, `packaging/LEIAME-INSTALACAO.md`, `docs/publicacao.md`, conferência do `.gitleaksignore`, release e commit | `.go`, `internal/web/static/**`, `postinstall`, `instalar.py` |

Comando Go deste repositório (Git Bash, raiz):

```
export GOPATH=$PWD/.tools/gopath GOCACHE=$PWD/.tools/gocache GOMODCACHE=$PWD/.tools/gopath/pkg/mod GOTOOLCHAIN=local
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 ./.tools/go/bin/go.exe build ./... && ./.tools/go/bin/go.exe build ./... && ./.tools/go/bin/go.exe vet ./...
```

`gofmt -l cmd internal` vazio. Arquivo `.go` escrito pelo Write sai com CRLF: rodar `gofmt -w` nele e conferir `git ls-files --eol`. Editar Go por heredoc ou `node -e` corrompe literais: usar Edit/Write.

Ordem: backend, frontend e devops em paralelo (devops fecha depois dos dois); revisor depois dos três; devops commita por último.

---

## Backend

Itens de esquema, instalador, `postinstall` e remoção ficam **separados e numerados**, cada um com o seu "pronto quando".

| # | Tarefa | Contrato | Pronto quando (compilação e leitura) |
|---|---|---|---|
| [x] B1 | **Migração v6** em `internal/store/store.go`: `ddlV6` **idêntica** ao bloco SQL da seção 10 (versão 6); `migrations` com 6 itens (`SchemaVersion` = 6, hoje `store.go:70`); o `backup` continua fora do laço (`store.go:86-90`) | 10 (v6) | diff da `ddlV6` contra o CONTRATOS sem diferença de conteúdo; `migrate` igual ao da tag (só a lista cresce); `painel-ramais version` imprime `schema 6` por leitura de `SchemaVersion` |
| [x] B2 | **Store** (`internal/store/regreport.go`): `RegBatch.States []RegStateRow` e `RegDown.Gap bool`; em `WriteRegBatch` (hoje `:113-186`) o passo (6) upsert em `reg_state` e o `down_gap` no `upsertDownSQL` (hoje `:78-82`); `MaxRegRev` (`:189-193`) com o maior `rev` das duas tabelas; `RegRow` ganha `DownGap`, `LastReg`, `LastRegExact` (consulta de `:266-310`); funções novas `LoadRegState`, `LastAliveMinute` (`max(minute)` com `group_name = ''` **e `gap = 0`**, sem somar 60 s) e `RegStateTouched`/`RegStateOne` (as duas devolvem `[]RegRow` com `Number`, `LastReg`, `LastRegExact` de `reg_state`; seção 10, notas da v6); "lacuna sem tempo": `RegDown` com `Gap` e `To <= From` grava só `down_gap = 1` na hora de `From`, com `down_s` 0 (seção 10) | 10 (gravação e notas da v6), 4.7.2 | leitura do SQL contra a seção 10; nenhum `sql.Open` novo |
| [x] B3 | **Model** (`internal/model`): `RegState{DownSince, DownSeg, LastReg time.Time; DownKind string; LastRegExact bool}`; `RegNote` com `DownGap bool` e `State *RegState`; `RegDecision`; `AsteriskSignal` com `Uptime`, `HasUptime`, `LastReload`, `HasLastReload`; evento `RegStateLoaded`; `QueuesListed.IgnoredDefault`; `IgnoredQueue(name string) bool` (`name == "default"`); `Extension` com `DownKind`, `DownSeg`, `LastReg`, `LastRegExact` (`json:"-"`); `RegStatus` com `DecisionLines`, `DecisionSummarized` | 11.1, 11.4, 11.5, 2.4 | leitura; `json.Marshal` de `Extension` sem chave nova (tags `-`) |
| [x] B4 | **Estado: intervalo único e reconciliação** (`internal/state/regreport.go`, `view.go`, `state.go`): campos novos em `extState` (`view.go:25-26`) e cópia em `model()` (`view.go:287-303`); regra da tabela "visão publicada" da 11.5 item 3 em `noteRegistration`/`noteTransition` (`regreport.go:113-156`), com `downSince` aberto em toda ida de ok para fora (fora da pausa ou não); gancho em `onEndpointSeen` (`view.go:208-210`) para ramal criado agora; `closeDown` (`regreport.go:159-168`) zera `down*` e mantém `lastReg`; `carryDown` (`regreport.go:172-184`, chamado em `state.go:282`) substituído pela reconciliação da 11.5 item 3 (`cap`, lacuna, `Fresh`, `LastAlive`; `cap` desconhecido ou anterior a `downSeg` fecha o trecho com tamanho zero e lacuna, sem inventar tempo); `RegStateLoaded` guardado até a primeira `SyncFinished`; toda mudança chama `markDirty` e emite a nota de estado | 11.5 item 3, ADR-010 | leitura linha a linha contra as duas tabelas da 11.5; `markUnknown` (`view.go:260-272`) não toca nos campos novos |
| [x] B5 | **Estado: pausa** (`internal/state/regreport.go`, `state.go:172` e `:283-285`): regra da 11.5 item 2 (`Shutdown`; `FullyBooted` por `Uptime`; sem `Uptime` = rc.8 com Info uma vez por sessão); `SyncFinished` de `connect` só grava o sinal `ami_connected`, sem abrir pausa; `Stats.PauseReason` | 11.5 item 2, 4.4 | leitura: `openWindow` só é chamado pelos dois sinais; logs com os textos da 11.5 |
| [x] B6 | **Estado: decisões** (`OnDecision` em `state.Options`, `internal/state/state.go:21-33`): uma `RegDecision` por transição com `x.reg != e.State` na visão publicada e por mudança ok/fora da reconciliação, com o `Reason` da 11.5 item 4 | 11.5 item 4 | leitura: nenhum I/O na goroutine do estado; motivos exatamente os seis do contrato |
| [x] B7 | **Reglog** (`internal/reglog/reglog.go`): `toBatch` (`:260-275`) com notas de estado (a última por número vence) e `DownGap`; `noteCost` (`:197-210`) com custo 1 para a nota de estado. **Novo** `internal/reglog/decisions.go`: canal de 4.096, balde de 20/s com rajada de 100, resumo a cada 10 s por motivo, contadores para o health (constantes da 11.5) | 11.5 (gravador e log de decisões), 4.4 | leitura; `Enqueue` com `select` e `default` |
| [x] B8 | **Supervisor: `FullyBooted`** (`cmd/painel-ramais/supervisor.go:358-360` e o ramo de queda `:398-401`): ler `Uptime` e `LastReload` como inteiros (inválido ou ausente = sem campo) e passar no `AsteriskSignal` | 7.2, 11.1 | leitura; nenhuma action nova |
| [x] B9 | **Supervisor: filas fora do laço** (`cmd/painel-ramais/supervisor.go:326-427`, `supervisor_queues.go`): a `QueueStatus` (completa e por fila) roda numa goroutine, uma de cada vez, com resultado por canal; eventos de fila guardados durante a leitura (teto de 50.000, `held_dropped`, Warn) e emitidos depois do `QueuesListed`; `queueFullMinGap` de 30 s a 10 min e intervalo mínimo da ressincronização por estouro de 30 s a 5 min (`resyncIfNeeded`, `:457-471`), com as regras de dobra e volta da 11.3 e 11.6; log Info de cada completa e de cada ressincronização com os textos do contrato; `queues.events_received` contado antes do `queues.Translate`, sem a `default`; campos novos do health em `QueueInfo` e `Status` | 11.3, 11.6, 4.4, ADR-011 d3 | leitura: nenhum `c.Do` de `QueueStatus` na goroutine do `live`; evento de registro e de chamada nunca fica guardado; a ordem do inventário (7.4) não muda |
| [x] B10 | **Canais auxiliares e fila `default` na tradução**: `internal/ami/calls/calls.go` ganha `IsAuxChannel` (lista exata da 2.3) aplicada em `Translate` (`:73-103`, `Channel` e, no `DialBegin`, `DestChannel`) e `FromCoreShowChannel` (`:118-150`); `cc.Queue` vazio quando `model.IgnoredQueue` (`:127-128`). `internal/ami/queues/queues.go`: `Translate` (`:72-89`) devolve `nil` para a `default`; `parse` (`:138-167`) pula os itens dela e marca `IgnoredDefault`. Supervisor: `qs.mark` não marca a `default`; log Info uma vez por processo (campo no `supervisor`) | 2.3, 2.4, 11.6, ADR-011 d4 e d9 | leitura; `git grep -n '"default"' internal cmd` acha só a função do `model` |
| [x] B11 | **API** (`internal/api`): `regHead` (`registrations.go:89-103`) com `paused_until`, `pause_reason`, `state_unknown_since`; `regRow` (`:105-121`) sem `down_open_since` e com `down_gap`, `down_since`, `down_kind`, `down_count_from`, `last_registered`, `last_registered_exact`; `buildRegRow`/`listed` (`:123-156`) com a regra de lista da 4.7; em `registrations` (`:177-243`), com `full: false`, juntar `RegStateTouched` e devolver as linhas tocadas sem filtro; em `/extension` (`:337-403`), sem linha de `RegRows` no período, usar `RegStateOne` para o `last_registered`; `ExtInfo`/`infoOf` (`hub.go:425-439`) com os quatro campos; health (`data.go:183-202`) com os campos novos da 4.4 | 4.4, 4.7, 4.7.2, 11.4 | leitura do JSON contra os exemplos (os blocos do CONTRATOS fazem parse) |
| [x] B12 | **`packaging/scripts/postinstall`**: depois de gravar o bloco ou de achar o bloco igual (`:110-135`), contar as linhas `eventfilter = Event:` entre os marcadores (lendo como asterisk) e avisar se não forem 29, com o texto da 7.2. Nada mais muda: mesma lista, mesma ordem, `manager reload` antes do `try-restart` (`:237-239`) | 7.2, 15.5 | `sh -n packaging/scripts/postinstall` sem erro; leitura |
| [x] B13 | **`instalar.py`**: (a) `c_eventfilter()` com a constante `EVENTOS_AMI` (os 29, cópia literal do `postinstall:58-86`), no `executar_checagens` (`:704-720`) e no `cmd_status` (`:1178-1210`); (b) `c_relogio()` (`timedatectl show -p NTPSynchronized --value`, com a reserva da 15.4), no mesmo lugar, sempre AVISO e nunca ERRO; (c) `status` imprime `queues.events_received`; (d) aviso de rollback (`:1267-1271`) com o texto exato da 15.7 da rc.9 (condição `sch_ant == 3 and sch_db >= 4` igual). Python 3.6, stdlib, sem `subprocess.run(capture_output)` | 7.2, 15.4, 15.6, 15.7 | `ast.parse` e leitura; `vermin --target=3.6-` se o venv existir |
| [x] B14 | **Remoção do item 8**, com `git rm`: `cmd/loadtest/` (9 arquivos), `cmd/amisim/main.go`, `internal/amisim/` (6 arquivos), `testdata/capturas/` (21 arquivos) e as linhas 19-20 do `.gitattributes` (comentário e `testdata/capturas/*.txt binary`). **Fica** `internal/capture`. **Se a proteção de permissão negar o `git rm` ou a remoção, NÃO contornar** (nada de `rm` alternativo, script ou outro caminho): parar este item e devolver ao gerente a lista exata de caminhos acima | 0 (D12), 12, ADR-011 d8 | `git ls-files cmd/loadtest cmd/amisim internal/amisim testdata` vazio; `git grep -n "amisim\|loadtest\|testdata/capturas" -- '*.go' build.ps1 tools packaging instalar.py` vazio; build linux e vet passam |
| [x] B15 | **`cmd/painel-ramais/serve.go`**: `loadRegState` (lê `reg_state` e `LastAliveMinute` e entrega `RegStateLoaded`) entre `loadQueueNames` e `loadNames` (`:251-255`), antes de `d.start()`; falha de leitura do `reg_state` = log Warn e segue com `Fresh = false` e nenhuma linha (nunca `before_install` por erro); falha do `LastAliveMinute` = log Warn e `LastAlive` zero (o estado fecha os trechos com tamanho zero e lacuna, 11.5 item 3); ligar `OnDecision` ao novo gravador de decisões e `d.run("decisoes", ...)` em `start()` (`:192-211`) | 11.1, 11.5 | leitura: a ordem garante o evento antes do primeiro `SyncStarted` |
| [x] B16 | `gofmt -w` nos arquivos tocados; build linux, build do host e vet | | comandos do topo sem erro; lista de arquivos no retorno |

---

## Frontend

| # | Tarefa | Contrato / desvio | Pronto quando (sintaxe e leitura) |
|---|---|---|---|
| F1 [x] | **Colunas novas da aba Registros**: `index.html:316-324` (sai o cabeçalho `data-chave="fora"`, entram `foraha` e `ultreg`); `app.css:517` com 10 colunas; `registros.js`: `criarLinha`/`preencherLinha` (hoje `:330-371`), `renderTabela` (`:273-292`), texto das células da 4.7.2, `title`/`aria-label` da frase completa de `before_install`; `formatarDuracao` (`:49-54`) com dias | 4.7.2, D38, D45 | `node --check`; leitura |
| F2 [x] | **Ordem em 3 faixas** (`ORDENS`, `:125-141`) como padrão (`:195`, era `instab`), segundo clique inverte só a faixa (a); **filtro** "Ocultar fora desde antes da instalação" (caixa nova na faixa de controles, desligada ao abrir, aplicada em `renderTabela`, na contagem e no CSV) | 4.7.2, D39, D40 | leitura da ordem contra as 3 faixas |
| F3 [x] | **Relógio ao vivo**: `registros.js` exporta `tique()`, que só chama `pintar()` com a aba visível (sem reordenar); `app.js:251-257` chama `registros.tique()`. Com `state_unknown_since`: parar o relógio nessa hora, cor de desconhecido e aviso; aviso de pausa por `paused_until`/`pause_reason` | 4.7 (cabeçalho), D41, D42 | leitura: nenhuma chamada de rede no tique |
| F4 [x] | **Delta** (`aplicar`, `:247-264`): no `full: false`, remover do `mapa` a linha que não passa na regra de lista da 4.7 | 4.7.2 | leitura |
| F5 [x] | **CSV** (`CABECALHO_CSV` e `gerarCsv`, `:69-102`): colunas e formatos da 4.7.5 (sai `Tempo fora do ar (s)`); a tabela filtrada e ordenada da tela | 4.7.5 | leitura do cabeçalho contra a 4.7.5, letra a letra |
| F6 [x] | **Detalhe do ramal** (`renderLinhaTempo`, `:610-626`; `index.html:355`): linhas "Fora do ar há", "Último registro" e "Tempo fora do ar no período" com os sufixos de lacuna; `tempoFora` (`:39-47`) passa a usar `down_count_from` e congela em `state_unknown_since` | 4.7, 4.7.3, D43 | leitura |
| F7 [x] | **Aviso de pausa no bloco da aba Ramais** (`instabilidade.js`, leitura do `summary` a cada 30 s, `:9` e `:117`): linha de nota com `role="status"` quando `paused_until` está no futuro | 4.7 (cabeçalho), D42 | leitura |
| F8 [x] | **Detalhe da fila com rolagem virtual** (`filas.js:126-165`, tique em `:219-222`; `index.html:456-460`; CSS): "Quem aguarda" e "Agentes" com `ListaLinhas` (`assets/linhas.js`); remontar só quando a fila muda (`render`), e no tique atualizar só o tempo das linhas visíveis | D44, ADR-011 d5 | leitura: nenhum `replaceChildren` da lista inteira no tique |
| F9 [x] | Preencher a coluna "Tela" de D38 a D45 com arquivo:linha | desvios | leitura |
| F10 [x] | (substituído pelo F11: o item 11 foi decidido) | | |
| F11 | **Item 11, coluna Grupos da aba Ramais** (`app.js:384-410`, `renderGrupos`): trocar "N com problema" por "X não registrados" (singular "1 não registrado", sempre presente) e, só quando houver, " · Y inalcançáveis" (singular "1 inalcançável"), no mesmo `span`; o nome acessível do botão leva o mesmo texto. Não mudam: `ruins`/`problema` na ordem "Mais problemas" e no destaque acima de 25%, e o "pior grupo" | spec "Item 11", D46 | leitura; `git grep -n "com problema" internal/web/static` vazio |
| F12 | **Data de "antes da instalação do painel"** em toda frase de `before_install` (`title`/`aria-label` da célula, detalhe do ramal, CSV): o `since` do cabeçalho (o mesmo do "Dados desde"); `down_since` da linha só quando `since` vem vazio ou inválido | 4.7 ("Data da instalação"), 4.7.2, 4.7.5, D38 | leitura das três saídas |

---

## Devops

| # | Tarefa | Pronto quando |
|---|---|---|
| D1 | `README.md` (texto do técnico de campo, sem endereço interno): atualização para a rc.9 **partindo da rc.8** (caso do usuário: o painel cria sozinho o `painel.db.bak-v5` na primeira subida), com uma nota curta para quem ainda está na rc.6 (`.bak-v3`); rollback da rc.9 volta para a rc.8 restaurando o `.bak-v5`, e o texto explica em 3 linhas o `.bak-v3` (cópia do banco da rc.6 feita pela rc.8, só servia para voltar da rc.8 para a rc.6, não é usado a partir da rc.9; CONTRATOS 15.7); "Fora desde antes da instalação do painel" usa a data do "Dados desde"; **aviso de relógio**: "O painel mostra tempos (fora do ar há, último registro, períodos). Se o relógio do servidor não estiver sincronizado, esses tempos ficam errados. O `./instalar.py verificar` e o `status` avisam. Para corrigir: `systemctl enable --now chronyd && chronyc makestep`."; "Filas não atualizam": rodar `./instalar.py status` (filtro AMI e eventos de fila recebidos) e, se faltar evento, `./instalar.py instalar` e `systemctl restart painel-ramais`; tirar menções a simulador e teste de carga | leitura contra CONTRATOS 15.5 a 15.7 e 17 |
| D2 | `docs/referencia-tecnica.md` e `packaging/LEIAME-INSTALACAO.md`: as mesmas mudanças onde citam esquema, rollback, simulador ou carga | `git grep -n "amisim\|loadtest\|schema 5" README.md docs/referencia-tecnica.md packaging/LEIAME-INSTALACAO.md` sem resto indevido |
| D3 | `.gitleaksignore`: conferir que o token do CONTRATOS 3.3 continua na linha 328 (`.tools/gitleaks/gitleaks.exe detect --no-git --source docs --gitleaks-ignore-path .gitleaksignore --no-banner` sem achado) | 0 achados |
| D4 | Release rc.9 (`build.ps1 release`, versão `0.1.2-rc.9`), notas de pré-release, `SHA256SUMS`, tag `v0.1.2-rc.9`; commit único sem `Co-Authored-By`, depois do revisor | tag e assets publicados |

---

## Revisor (depois de backend, frontend e devops; antes do commit)

Ler o diff contra o contrato e devolver o veredito (aprovado, aprovado com ajustes ou reprovado):
1. Esquema: `ddlV6` igual à seção 10; backup único fora do laço; `SchemaVersion` 6; aviso de rollback com o texto da 15.7 da rc.9.
2. Estado fora do ar: as duas tabelas da 11.5 item 3, linha por linha; nenhum caminho inventa tempo (fechamento no `cap` com lacuna); `markDirty` em toda mudança; nada de I/O na goroutine do estado.
3. Pausa: só `Shutdown` e `FullyBooted` com `Uptime` abaixo de 120 (ou sem o campo); `SyncFinished` não abre pausa; logs.
4. Decisões: 20/s, rajada de 100, resumo de 10 s, seis motivos, canal sem bloqueio.
5. Filas: nenhuma `QueueStatus` na goroutine do `live`; eventos guardados emitidos depois da lista; teto de 50.000 contado; intervalos mínimos e logs; `events_received` antes do tradutor e sem a `default`.
6. Auxiliares e `default`: um ponto só (`calls.IsAuxChannel`, `model.IgnoredQueue`), lista exata.
7. Instalador: `c_eventfilter` com os 29 iguais ao `postinstall`; `c_relogio` nunca ERRO; `postinstall` com a contagem e sem outra mudança; allowlist intacta; nenhuma escrita em `manager_custom.conf` pelo `instalar.py`.
8. Remoção: os quatro caminhos fora, `internal/capture` dentro, `.gitattributes` sem a regra, build linux e vet passam.
9. Tela: colunas, 3 faixas, filtro, relógio parado e acinzentado, avisos, CSV na ordem da 4.7.5, detalhe do ramal, detalhe da fila virtual; D38 a D46 com a coluna "Tela"; texto da coluna Grupos com "não registrados"/"inalcançáveis" e singular certo (F11); data de `before_install` pelo `since` (F12).
10. Caminhos de erro: falha de leitura do `reg_state` na subida não vira `before_install`; perda de nota de estado aparece em `dropped`/`lost_events`; `QueueStatus` com erro emite os guardados e não apaga estado.

---

## Roteiro de conferência ao vivo (o usuário, no servidor, depois de instalar)

Conferência pela tela, pela API e pelo `journalctl`. **Não usar o `sqlite3` de linha de comando**: o do Rocky 8 (3.26) não abre tabela `STRICT` (`reg_state` e as demais).

| # | Passo | Esperado |
|---|---|---|
| R1 | Antes de atualizar (rc.8): anotar o "Dados desde" da aba Registros. Depois `./instalar.py instalar` com o bundle da rc.9 | instalação sem erro; `ls -l /var/lib/painel-ramais/painel.db.bak-v5` existe (o painel cria na primeira subida) |
| R2 | `./instalar.py status` | linhas "Relogio" e "Filtro AMI: OK"; "Filas: eventos recebidos" |
| R3 | `grep -c '^eventfilter' /etc/asterisk/manager_custom.conf` e `curl -s http://127.0.0.1:8790/api/health` | 29; o health traz `queues.events_received` |
| R4 | Aba Registros, ramal que já estava fora antes da rc.9 | célula "antes da instalação"; no detalhe e no `title`, a data é a do "Dados desde" anotado no R1 (a da rc.8), **não** a do upgrade; segunda faixa da ordem; some com o filtro ligado |
| R5 | Desligar um telefone, esperar 2 min, `systemctl restart painel-ramais` | "Fora do ar há" conta e continua da hora original depois do restart; **sem** aviso de pausa |
| R6 | Religar o telefone; exportar o CSV | coluna vazia, "Último registro" com a hora; CSV com "Fora do ar há (s)", "Último registro", "Tempo total fora no período (s)" e "Lacuna no período" |
| R7 | `systemctl restart asterisk` | durante a queda, relógio parado e cinza com o aviso de estado desconhecido; depois, "Contagem de registros pausada até HH:MM (Asterisk reiniciado)" na aba Registros e no bloco da aba Ramais, que some na hora indicada |
| R8 | ChanSpy numa ligação interna | a aba Chamadas mostra só a interna, sem externa falsa |
| R9 | Agente entra e sai da fila pelo `*45` | o painel de filas muda sem F5; `queues.events_received` ganha `QueueMemberAdded` e `QueueMemberRemoved` |
| R10 | Painel de filas | a fila `default` sumiu; as filas reais continuam |
| R11 | Aba Ramais, coluna Grupos (e "Todos os ramais") | "X não registrados" e, só quando houver, "Y inalcançáveis", com singular certo ("1 não registrado", "1 inalcançável") |
| R12 | `journalctl -u painel-ramais --since -15min \| grep "registro: "` | linhas por transição e resumos por motivo, sem inundar |
| R13 | Só se precisar voltar: `./instalar.py rollback` | volta para a rc.8 e restaura o `painel.db.bak-v5`; o `.bak-v3` não é usado |

## Pendências conhecidas (não bloqueiam a rc.9)

- S18 a S21 (CONTRATOS 13) só se confirmam ao vivo: `Uptime` no `FullyBooted` do Issabel, nomes dos canais auxiliares, causa do bloco com 25 filtros, batimento pelo `history`.
- Nota de estado perdida (fila cheia ou sublote perdido) deixa o `reg_state` velho até o próximo reinício (ADR-010, suposições); aparece em `dropped`/`lost_events`.
- A releitura do inventário de ramais continua bloqueando o laço ao vivo (7.4); só a de filas saiu dele.
- Sem simulador nem ferramenta de carga no repositório (critério C1 da seção 16 sem ferramenta).
