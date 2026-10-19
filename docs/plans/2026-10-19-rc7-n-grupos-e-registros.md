# Plano rc.7: ramal em N grupos e relatório de registro

**Data**: 2026-10-19
**Spec**: `docs/specs/2026-10-19-rc7-n-grupos-e-registros.md`
**Decisões**: ADR-006, ADR-007 (Aceitos). **Contrato**: `docs/CONTRATOS.md` (fonte única; backend e frontend trabalham em paralelo **sem conversar**, só pelo contrato). **Tela**: `docs/design/desvios.md` D19 a D27.

## Regras desta rodada (do usuário, valem para todos)

- **Nenhum teste no PC do usuário**: não escrever teste novo, não rodar `go test`, não executar o binário, o simulador nem a tela. Critério de pronto = **compila e confere por leitura**. O teste é ao vivo no servidor (spec, R1 a R10).
- AMI somente leitura: nenhuma action nova, nunca `Command`. Não editar `manager.conf` nem arquivos do Issabel.
- Sem travessão longo nos textos. Commit só no fim, pelo @devops.
- Cada arquivo tem um dono. Precisou mexer em arquivo de outro agente: pede ao gerente. Campo novo entra primeiro no CONTRATOS (pelo tech-lead).

## Donos de arquivo

| Agente | Arquivos | Não toca |
|---|---|---|
| backend | `internal/**` exceto `internal/web/static/**`; `cmd/**`; `*_test.go` existentes (só o mínimo para compilar, ver B13) | `internal/web/static/**`, `packaging/**`, `instalar.py`, `build.ps1`, docs |
| frontend | `internal/web/static/**`; coluna "Tela" de D19 a D27 em `docs/design/desvios.md` | qualquer `.go`, CONTRATOS |
| devops | `build.ps1`, `packaging/**`, `instalar.py`, `README.md`, `.gitleaksignore`, commit e release | `.go`, `internal/web/static/**` |

Comando Go deste repositório (Git Bash, raiz): `export GOPATH=$PWD/.tools/gopath GOCACHE=$PWD/.tools/gocache GOMODCACHE=$PWD/.tools/gopath/pkg/mod GOTOOLCHAIN=local; ./.tools/go/bin/go.exe build ./... && ./.tools/go/bin/go.exe vet ./...` (o `vet` compila os testes, não os executa). `gofmt -l cmd internal` vazio.

Ordem: backend, frontend e devops em paralelo; revisor depois dos três; devops commita por último.

---

## Backend

| # | Tarefa | Contrato | Pronto quando (compilação e leitura) |
|---|---|---|---|
| [x] B1 | `internal/store/store.go`: `ddlV4` **idêntica** à seção 10 (versão 4), `migrations` com 4 itens; antes do DDL, na transação da migração, ler os `group_id IS NULL` e logar Warn (contagem e até 20 números). O `migrate` precisa receber um logger ou devolver a lista para o `cmd` logar (escolha do backend, sem importar pacote interno) | 10 | `SchemaVersion == 4`; diff da `ddlV4` contra o bloco SQL do CONTRATOS sem diferença de conteúdo |
| [x] B2 | `internal/model`: `Extension` com `Groups`, `RangeGroups`, `ManualGroups` (nunca nil) e `DownSince *time.Time json:"-"`, sem `Group`/`GroupManual`; `RegistrationChanged.Live`, `.Port`; `AsteriskSignal`; `GroupsLoaded.Manual map[string][]string`; `RegNote`, `RegCounts`, `RegStatus` | 2.1, 11.1, 11.2, 11.4, 11.5 | `git grep -n "GroupManual\|\.Group\b" internal cmd` sem uso de campo antigo |
| [x] B3 | `internal/ami/pjsip/pjsip.go` e `internal/ami/sip/sip.go`: `Live = true` só em `contactStatus` e `peerStatus`; `Port` do `ViaAddress`/`URI` e do `Address` | 11.2 | `git grep -n "Live"` em `internal/ami` mostra só essas duas funções |
| [x] B4 | `cmd/painel-ramais/supervisor.go`: no `live()`, `Shutdown` e `FullyBooted` viram `model.AsteriskSignal` (antes do `Translate`; o `FullyBooted` continua agendando releitura como hoje). `internal/amisim/proto.go`: `"Shutdown": true` no `filterSet` | 7.2, 11.1 | leitura |
| [x] B5 | `internal/store` grupos: sai `checkRangeConflicts` e a checagem entre grupos; `ParseGroupsCSV` com as regras novas (mesmo ramal em várias linhas, repetida ignorada, `Sem grupo,N` ignorado, sobreposição só no mesmo grupo, faixa idêntica ignorada); `ImportGroups` só acrescenta (`ON CONFLICT DO NOTHING`), conta `ignored`, teto de 1.000 faixas por grupo somando as existentes; `AssignGroup` vira add (vários grupos) e remove (um grupo, só manual) com `not_manual`; `LoadGroupData` com `Manual map[string][]string` | 4.6, 9 | `git grep -n "checkRangeConflicts\|em dois grupos"` vazio; leitura das mensagens contra a seção 9 |
| [x] B6 | `internal/state`: resolução N:N (busca binária **por grupo**), `setGroups` marca sujo se qualquer das três listas mudou, `counts` soma o ramal em cada grupo e uma vez no total | 2.1, 10 ("Grupos no daemon") | leitura |
| [x] B7 | `internal/state`: relatório no `onRegistration` (tabela de transições, `Live`, visão publicada), janela de 120 s (`AsteriskSignal` e `SyncFinished` com `connect`), `downSince` (abre, fecha, atravessa a sincronização, fecha no `removeExt`), `Options.OnReg`, `Stats.RegTotal`/`RegByGroup`/`SuppressedUntil` | 11.4, 11.5 | nenhum import de `store`/`database/sql` em `internal/state`; `OnReg` chamado sem nada que bloqueie |
| [x] B8 | `internal/history`: colunas `lost_registration`/`became_unreachable` pela diferença dos acumulados; `NULL` em gap; `store.InsertHistory` com as colunas | 10 (amostra) | leitura |
| [x] B9 | Novo `internal/reglog`: canal de 16.384, envio sem bloqueio no `cmd`, lote 2 s ou 500, transação da seção 10, `rev`, corte de 200, retenção diária de 30 dias, `RegStatus` | 10, 11.5 | grafo da seção 12 confere: `go list -f "{{.ImportPath}}: {{.Imports}}" ./internal/reglog ./internal/state` |
| [x] B10 | `internal/store`: consultas do relatório (linhas por período com `rev > cursor`, eventos por ramal, série por hora/dia do `history`, sinais, `since`, maior `rev`, expurgo) | 4.7, 10 | leitura |
| [x] B11 | `internal/api`: `assign` com `mode`; `import` com `ignored`; hub `/api/groups` N:N e acesso à projeção (nome, grupos, `DownSince`); as 4 rotas `GET /api/registrations*` em `server.go` com sessão de qualquer papel; `/api/health` com `registrations` | 1, 4.2, 4.4, 4.6, 4.7 | cada rota da seção 1 existe no `Handler()`; nenhuma rota nova sem sessão |
| [x] B12 | `cmd/painel-ramais`: ligar `OnReg`, `reglog`, `RegStatus`, log da migração; CLI `group list` (sem a linha `Sem grupo`) e `group import` (imprime ignoradas); `version` mostra `schema 4` | 8, 11.5 | leitura |
| [x] B13 | `*_test.go` existentes que deixarem de compilar: ajuste **mínimo** por leitura para o `go vet ./...` passar; sem teste novo e sem rodar `go test` (**confirmar com o gerente**: precedente da seção 7.4 do CONTRATOS) | | `go vet ./...` sem erro |

**Pronto do backend**: `go build ./...` e `go vet ./...` sem erro, `gofmt -l cmd internal` vazio, B1 a B13 conferidos, lista de arquivos no retorno.

## Frontend

| # | Tarefa | Contrato / desvio | Pronto quando (compilação e leitura) |
|---|---|---|---|
| F1 [x] | Troca `group`/`group_manual` por `groups`/`range_groups`/`manual_groups` em `estado.js`, `app.js`, `edicao.js`, `grupos.js`: filtro por grupo (`groups` contém), contagens N:N, "Todos os ramais" uma vez | 2.1, D25 | `git grep -n "group_manual\|\.group\b" internal/web/static` vazio |
| F2 [x] | Detalhe: "Grupos" com origem por grupo (faixa achada no catálogo com `BigInt`); "Adicionar ao grupo" (vários) e "Remover deste grupo" (desabilitado com a frase quando pela faixa) | 2.1, 4.6, D22, D23 | leitura contra a regra de habilitação da seção 2.1 |
| F3 [x] | Seleção múltipla com `mode: add` e `mode: remove`, contagem à parte dos ramais pela faixa (`not_manual`) | 4.6, D24 | leitura |
| F4 [x] | Gestão de grupos: textos novos, sem tratamento de 409 por faixa, `ignored` na mensagem do import | 4.6, 9, D26 | leitura |
| F5 [x] | Barra de abas na `.topo-dir`, `role="tablist"`, encaixe de "Chamadas" só no código (comentário e lista de abas), Densidade escondida em Registros | D19 | nenhum botão "Chamadas" no HTML |
| F6 [x] | Bloco de instabilidade na `.col-esq`: `GET /api/registrations/summary?group=` ao abrir, ao trocar grupo e a cada 30 s (aba Ramais visível); clique abre Registros com o ramal | 4.7.1, D20 | caixa da lista de ramais continua com 600 px e rolagem virtual intacta (leitura de `lista.js`/`app.css`) |
| F7 [x] | Aba Registros: período, grupo, busca (no navegador), tabela ordenável com rolagem virtual, gráfico (`/series`, a cada 60 s), linha do tempo (`/extension`), "Dados desde", eventos do sistema | 4.7.2 a 4.7.4, D21 | uma chamada por vez, intervalo de 5 s **depois** da resposta anterior, parada com aba ou documento escondido |
| F8 [x] | Exportar CSV no navegador: `;`, BOM, CRLF, aspas, `'` antes de `=`, `+`, `-`, `@`, TAB, CR; colunas e nome do arquivo da 4.7.5 | 4.7.5, D27 | leitura da função de célula |
| F9 [x] | CSP: nada inline, nada externo, texto só por `textContent`, sem `innerHTML` novo | 1.3 | `git grep -n "innerHTML\|on[a-z]*=\"" internal/web/static` sem caso novo |
| F10 [x] | Preencher a coluna "Tela" de D19 a D27 em `docs/design/desvios.md` | | todas com arquivo:linha |

**Pronto do frontend**: `node --check` em cada `internal/web/static/assets/*.js` sem erro (só sintaxe, não executa a tela), F1 a F10 conferidos.

## Devops

| # | Tarefa | Pronto quando |
|---|---|---|
| D1 | `build.ps1`: tirar **definitivamente** o `Test` do `release` (linha `Test; Rpm; Bundle`) e do `all`; atualizar o comentário do topo ("release: gera RPM + tar.gz ..."). O alvo `test` pode ficar, sem uso automático | `git grep -n "Test;" build.ps1` vazio |
| D2 | `packaging/scripts/postinstall`: `eventfilter = Event: Shutdown` no `ami_block` (só no bloco do painel); atualizar a NOTA de eventos | `bash -n packaging/scripts/postinstall` sem erro (só sintaxe); LF conferido por `tools/check-lf.ps1` |
| D3 | `instalar.py` `cmd_rollback`: quando o banco é 4 e a versão anterior 3, mensagem da seção 15.7 (vínculos em vários grupos, relatório de registros e tudo depois da atualização serão PERDIDOS) antes da confirmação | `vermin --target=3.6- instalar.py` sem violação; `python -m py_compile instalar.py` |
| D4 | `README.md`: CSV N:N (seção 9), "Sem grupo" implícito, relatório de registro, `registrations` do health, rollback rc.7 para rc.6 | leitura contra o CONTRATOS |
| D5 | `.gitleaksignore`: depois de todas as edições de docs, rodar `.tools/gitleaks/gitleaks.exe detect --no-git --source . --no-banner` (só confere fingerprints, não é teste) e acertar as linhas | 0 vazamentos com o `.gitleaksignore` do repositório |
| D6 | Release `v0.1.2-rc.7` (`.\build.ps1 release -Version 0.1.2 -Pre rc.7`, sem `go test`), commit único no fim com a lista de arquivos dos três agentes | tag e Release publicadas; `painel-ramais version` dentro do RPM diz `schema 4` (lido do `versao.txt` do bundle) |

## Revisor (depois de backend, frontend e devops; antes do commit)

Ler o diff contra o contrato e devolver veredito (aprovado, aprovado com ajustes, reprovado):
1. `ddlV4` igual à seção 10; migração numa transação; backup antes; log dos "Sem grupo" manuais.
2. N:N: resolução com busca por grupo; `groups` nunca vazio; ordem igual ao catálogo; `/api/groups` e contagens N:N; total uma vez.
3. `Live` só em `contactStatus`/`peerStatus`; tabela de transições exata; `unknown` nunca conta; `unreachable` para `unregistered` não fecha intervalo; janela de 120 s nas três origens.
4. Estado nunca bloqueia: `OnReg` com `select`/`default`; nenhum acesso a banco em `internal/state`; descarte contado.
5. Lote: transação única; corte de 200; `down_s` com teto; retenção de 30 dias diária.
6. Segurança: rotas novas com sessão; `assign` com checagem de administrador e `acl.db` (M1); validação de `number`, `period`, `group`, `cursor`; CSV com proteção de fórmula; sem `innerHTML`; nenhuma action AMI nova (`internal/ami/actions.go` inalterado).
7. Erros: nada engolido em silêncio (falha de lote vira `write_errors` e log Warn).
8. `build.ps1` sem `Test` no `release`; `eventfilter = Event: Shutdown` só no bloco do painel.

## Pendências conhecidas (não bloqueiam a rc.7)

- Intervalo fora do ar aberto se perde no restart do daemon (ADR-007, alternativa E).
- Nome "Todos os ramais" ainda aceito como nome de grupo pelo daemon e pelo CSV (dívida do D17).
- S11 e S12 (CONTRATOS 13) só se confirmam ao vivo.
- Testes antigos compilam mas vão falhar com o novo comportamento: `internal/store/store_test.go` (`TestParseCSV`, casos "sobreposição", "sobreposição igual" e linhas 1 e 2; `TestImportGroups`), `internal/store/groups_limits_test.go` (`TestCheckExistingDoesNotAccumulateMessages`), eventos ao vivo de `pjsip_test` e `sip_test` (campos `Live` e `Port`) e possivelmente o health em `api_test` e `cli_test`. Corrigir quando o usuário liberar testes. Até lá `build.ps1 test` fica vermelho (o `release` e o `all` não rodam `go test`).
- A tabela incremental de registros pode ficar até 1 h defasada numa mudança de grupo (`internal/api/registrations.go:209-236`).
- O fechamento de intervalo longo pode estourar o `writeTimeout` do lote (`internal/store/regreport.go:157-174`). Saída: somar por hora em memória.
- A limpeza diária ocupa a única conexão do banco por alguns segundos (`internal/reglog/reglog.go:129-138`).
- Linha redundante em `internal/state/view.go:625-627`.
- O `assign` aceita `"group": null` no modo add (`internal/api/groups.go:197`).
- Faixa acima de 2^53 perde precisão no JavaScript (`internal/web/static/assets/estado.js:180-190`).
- 11 arquivos Go com CRLF na cópia de trabalho (fora desta entrega).
