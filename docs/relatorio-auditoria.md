# Relatório de auditoria de segurança: Fase 1, versão 0.1.1

**Escopo:** daemon Go (`cmd/painel-ramais`, `internal/*`), empacotamento (`packaging/*`) e `instalar.py`.
**Base:** código no estado de 2026-10-13 às 21:12. Os arquivos alterados depois da reauditoria anterior (16:36) foram relidos às 20:55. Os que mudaram depois da primeira versão deste relatório (correções de NF1 a NF5 e do colateral) foram relidos às 21:12.
**Decisões do usuário consideradas:** `docs/DECISOES-FASE-1.md`, decisões 1 a 5 e a seção sobre captura.
**Método:** leitura do código e testes ofensivos automatizados, rodados numa cópia do módulo fora do projeto. A auditoria não alterou nenhum outro arquivo do projeto. Um item só é dado como fechado quando há teste executado ou leitura direta que comprove. O que depende de Linux está na seção 6 como auditoria incompleta.

## 1. Veredito

**SEGURO COM RESSALVAS no código, AUDITORIA INCOMPLETA nos itens de Linux.**

- Não há achado CRITICAL, HIGH ou MEDIUM em aberto. O H2 (classe `system` na conta AMI) é risco residual aceito pelo usuário na Decisão 1, e todos os controles compensatórios da seção 3 estão atendidos no código.
- NF1 a NF5 e o colateral do rollback foram corrigidos e reconferidos. O que sobrou é resíduo LOW documentado: nomes alfanuméricos de endpoint na captura e a janela do NF5 quando `httpd -t` falha.
- Nada bloqueante foi encontrado. Capturas reais podem ir para `testdata/` com `--anonimizar` e revisão manual antes do commit (seção 5, NF2).
- Produção só depois da homologação em Rocky 8 e 9, cobrindo a seção 6: rpm, systemd, SELinux, Apache e classes AMI no Asterisk real.

## 2. Evidência dos testes

Testes executados nesta rodada, numa cópia do módulo atual:

| Teste | Resultado |
|---|---|
| `go vet ./...` | sem apontamentos (saída 0) |
| `go test ./...` (todos os pacotes) | ok |
| `py -3 -B -m unittest tests.test_instalar -k rollback -k socket -k sha` | ok (inclui rollback sem SHA-256 e rollback para versão sem unit de socket) |
| `TestSecLoginLimiterRace`: 40 logins paralelos com senha errada | 4 respostas 401 e 36 respostas 429; o limite é 5 (eram 11 na primeira auditoria) |
| `TestSecLocalOnlyPathVariants`: 12 variantes de `/api/health` e `/debug` com `X-Forwarded-For` | nenhuma entrega o conteúdo (404 ou 307 para o caminho limpo) |
| `TestSecDebugNeedsSession` | `/debug` chamado de 127.0.0.1 sem cookie dá 401; com sessão dá 200 |
| `TestSecWSCrossOrigin` | upgrade do WebSocket com Origin de outro site dá 403 |
| `TestSecBcryptCostPerAttempt` | bcrypt custo 12 leva cerca de 230 ms por tentativa por núcleo |
| `TestSecMaskVariants` (captura, com `--anonimizar`) | `Password`, `Md5Cred`, `Secret`, `RemoteSecret`, `secret ` (com espaço) e o `Secret` do Login mascarados |
| `TestSecAnonymizeGaps` (captura), agora falhando se algo vazar | **passou**. Ficaram fictícios: IPv6 completo e comprimido, IPv4, `Username`, `ObjectName`, `Peer`, `AccountCode`, `Value`, `Exten`, canal `Local/...`, nomes (`ConnectedLineName`, `DestCallerIDName`, `Callerid`), host de URI e número dentro de `Message`. `Uniqueid` intacto; o mesmo número vira sempre o mesmo fictício em chaves diferentes |
| `TestSecAnonymizeResidual` (registro) | `ObjectName`/`Endpoint` alfanumérico (`maria.souza`) **não** é trocado; número curto (`190`) fica, como previsto pelo limite de 7 dígitos |
| `TestSecCleanNameHostile` (nomes vindos de fora) | CR/LF, C0, DEL, C1, bidi (U+2066/2069), zero-width (U+200B/200D/FEFF) removidos; corte em 64 caracteres, inclusive com acentos. `<script>` continua como texto, porque escapar é papel da saída: JSON e `html/template` |

Testes do próprio projeto que cobrem os controles:
- `TestRefusedActionsNeverReachTheSocket` (`internal/ami/client_test.go:407`): 33 actions proibidas e os cabeçalhos `Action`/`ActionID` passados como parâmetro são recusados e nunca chegam ao socket.
- `TestSocketActivation*` (`cmd/painel-ramais/listen_test.go`).
- `TestWSKeepsSessionAliveWhileConnected` e `TestWSWarnsBeforeClosingExpiredSession` (`internal/api/sec_test.go`).
- `TestCapturar*` (`cmd/painel-ramais/capturar_test.go`).
- NF1: teste do supervisor que exige `level=ERROR` e `action=Command` no log (`cmd/painel-ramais/supervisor_test.go:97-120`).
- NF2: `TestAnonymizedCaptureLeavesNoOriginalValue`, `TestLoginUsernameIsKeptButSecretIsMasked` e `TestAnonymizeDoesNotMangleTimesAndDurations` (`internal/capture/anonymize_test.go`).
- NF3: `TestCleanName` (`internal/model/model_test.go`), `TestNamesFromOutsideAreNormalized` (`internal/state/names_test.go`) e `TestPruneNames` (`internal/store/store_test.go:365`).

## 3. Risco residual aceito: Decisão 1 (`write = system,reporting`)

**Aceite:** o usuário, dono do produto, decidiu manter `write = system,reporting` em `docs/DECISOES-FASE-1.md`, Decisão 1. O motivo registrado é que, sem a classe `system`, as actions `PJSIPShow*` e `ModuleCheck` não funcionam, e a maioria dos clientes usa PJSIP. O documento não registra data nem nome; a data do aceite será a do commit que versionar esse arquivo.

**Risco residual aceito:** se o próprio daemon for comprometido, ou se o segredo AMI vazar, a classe `system` dá acesso a actions que o painel nunca usa:
- `GetConfig`, que lê toda a configuração, incluindo senhas SIP e AMI;
- `ModuleUnload`/`ModuleLoad` e `Reload`, que podem parar o PBX;
- `Hangup`, que derruba chamadas;
- `DBPut`, que permite gravar desvio de chamada;
- `PJSIPNotify`/`SIPnotify`, que podem reiniciar os telefones em massa;
- `MixMonitor` com o parâmetro `Command`, que executa comando no sistema como o usuário asterisk (a correção AST-2014-006 passou a exigir justamente a classe `system`).

Nota da versão 0.1.1: o daemon agora chama `PJSIPShowEndpoint` a cada ciclo de nomes. Essa resposta traz o evento `AuthDetail` com `Password` e `Md5Cred` dos ramais (ver `internal/ami/testdata/pjsip/endpoint_detail.txt:14-18`). O código só usa o `EndpointDetail` (`internal/ami/pjsip/enrich.go:49-58`) e não grava nem registra em log o restante, mas as credenciais SIP passam pela memória do processo. Isso não muda o risco aceito: com a classe `system`, o `GetConfig` já dava o mesmo acesso.

**Controles compensatórios obrigatórios (Decisão 1) e status:**

| Controle | Status | Evidência |
|---|---|---|
| Tabela action → classe no README, marcada "a confirmar com `manager show command <Action>`" | **Atendido** | `README.md:308-326` |
| Lista fixa de actions no código; action fora dela recusada **antes do socket** | **Atendido** | `internal/ami/actions.go:14-57` e `internal/ami/client.go:192-195`; teste `TestRefusedActionsNeverReachTheSocket` |
| ...**com log de erro** | **Atendido** (NF1 fechado) | recusa chama `OnRefused` (`internal/ami/client.go:196-201`), que o supervisor registra em nível Error com o nome da action (`cmd/painel-ramais/supervisor.go:54-56`); visível com `log_level: info`; teste exige `level=ERROR` |
| `permit` somente 127.0.0.1 | **Atendido no código; validar em Linux** | `deny = 0.0.0.0/0.0.0.0` e `permit = 127.0.0.1/255.255.255.255` no bloco do `packaging/scripts/postinstall` |
| Segredo 0600 do usuário painel-ramais | **Atendido** | `postinstall:29-30`; o daemon recusa subir se o modo ou o dono forem outros (`internal/config/perm_unix.go`). A cópia dentro de `manager_custom.conf` fica `asterisk` 0640, com `o-rwx` (M1) |
| Hardening da unit (NoNewPrivileges, ProtectSystem=strict, ProtectHome, PrivateTmp, ReadWritePaths só `/var/lib/painel-ramais`, CapabilityBoundingSet vazio) | **Atendido no arquivo; validar em Linux** | `packaging/painel-ramais.service:116-132`; comando `systemd-analyze security painel-ramais` documentado em `README.md:339`. `ProtectKernelLogs` exige systemd 244; no Rocky 8 (systemd 239) é ignorado com aviso |

## 4. Status dos achados anteriores

| Achado | Gravidade original | Status | Evidência e observação |
|---|---|---|---|
| H1: instalar.py, rodando como root, operava em `/var/lib/painel-ramais`, que pertence ao usuário do serviço | HIGH | **Fechado** | Os RPMs ficam em `/var/lib/painel-ramais-instalador`, que precisa ser do root, 0700 e não pode ser symlink (`instalar.py:599`). Antes do rollback, o instalador confere o nome do pacote com `rpm -qp` e o SHA-256 (`instalar.py:637-651, 916-917`). O CSV vai pelo stdin da CLI. Restauração do banco roda via `runuser -u painel-ramais`. `--purge` apaga também o diretório do instalador (`instalar.py:956`). NF4 fechado: sem SHA registrado, o rollback é recusado |
| H2: conta AMI com `write = system` | HIGH | **Risco residual aceito** (Decisão 1) | Seção 3 |
| M1: segredo AMI legível por outros em `manager_custom.conf` | MEDIUM | **Fechado no código; validar em Linux** | Arquivo novo nasce `asterisk` 0640; o existente recebe `chmod o-rwx` (`postinstall:93-108`); o `verificar` avisa (`instalar.py:374-376`). Pendente: confirmar se alguma rotina do Issabel volta o modo para 0664 |
| M2: edição como root dentro de `/etc/asterisk` | MEDIUM | **Fechado** | Montagem num `mktemp -d` em `/run`; leitura e escrita como o usuário asterisk (`postinstall:69-113`; `postremove:11-30`) |
| M3: corrida no limite de login e bcrypt sem teto | MEDIUM | **Fechado** | Reserva atômica antes do bcrypt (`internal/auth/auth.go:101-108`) e teto de 4 comparações (`internal/api/server.go:41`); `TestSecLoginLimiterRace`: 4 de 40 |
| M4: outro processo ocupando a porta 8790 com o daemon parado | MEDIUM | **Fechado** (Decisão 2); validar em Linux | Socket activation: `packaging/painel-ramais.socket` segura 127.0.0.1:8790. O daemon usa o descritor herdado (`cmd/painel-ramais/listen.go:29-52`) e recusa socket que não seja TCP em loopback (`listen.go:79-85`). Se `listen:` mudar, um drop-in ajusta o socket (`postinstall:150-157`). NF5 fechado, com resíduo pequeno (ver seção 5) |
| L1: `/debug` sem autenticação para processos locais | LOW | **Fechado** | localOnly + sessão (`internal/api/server.go:92`); `TestSecDebugNeedsSession` |
| L2: sessão de token sem revogação | LOW | **Fechado** | `painel-ramais session purge [--all]` (`cmd/painel-ramais/cli_session.go`) |
| L3: hardening da unit | LOW | **Fechado no arquivo; validar em Linux** | `painel-ramais.service:116-132` |
| L4: sem ReadTimeout e sem nosniff | LOW | **Fechado** | `ReadTimeout` 15 s (`serve.go:122`); o WebSocket limpa o prazo; `nosniff` (`server.go:181`) |
| L5: usuário digitado gravado no log | LOW | **Fechado** | Log com tamanho e 8 hex do SHA-256 (`internal/api/authn.go:89, 121`) |
| L6: bundle sem checksum | LOW | **Fechado, com resíduo** | Instalador confere `SHA256SUMS`. Isso só detecta corrupção; autenticidade exigiria assinatura GPG do RPM (resíduo aceitável para ferramenta interna) |
| L7: permissões do log do instalador | LOW | **Fechado** | Log aberto com 0600 e `chmod` a cada escrita |
| N1: limitador negava qualquer nome novo quando cheio | LOW | **Fechado** | Descarta a chave mais antiga em vez de negar (`auth.go:110-138`). Resíduo documentado no código: com uns 10 mil nomes falsos, que custam um bcrypt cada (cerca de 10 min com o teto de 4), o atacante zera a contagem de uma vítima e ganha mais 5 tentativas |
| N2: falha na gravação truncava `manager_custom.conf` e apagava a cópia | LOW | **Fechado** | Em falha, o `$TMPD` é mantido e a mensagem mostra o caminho para restaurar (`postinstall:102-106`; `postremove:22-26`). Resíduo: `/run` é tmpfs; a mensagem avisa para copiar antes de reiniciar |

## 5. Achados novos (versão 0.1.1)

| # | Achado | Gravidade | Arquivo:linha | Correção sugerida | Agente |
|---|---|---|---|---|---|
| NF1 | Recusa de action fora da lista era registrada no nível Debug, e o controle compensatório da Decisão 1 pede log de erro | LOW | antes: `cmd/painel-ramais/supervisor.go:88` | **Fechado.** `OnRefused` em `internal/ami/client.go:196-201`, registrado em Error no `supervisor.go:54-56`; o teste exige `level=ERROR` | backend |
| NF2 | A anonimização da captura deixava passar IPv6, `Username`, `ObjectName`, `Peer`, `AccountCode` e `Value` | MEDIUM (LGPD) | `internal/capture/sanitize.go` | **Fechado, com resíduo LOW.** A anonimização passou a ser por exclusão: todo valor é limpo, exceto os de chaves estruturais seguras (`sanitize.go:52-55`). Há tratamento próprio para nomes, identificadores, hosts e IPv4/IPv6 (`sanitize.go:57-70, 110-138`); `TestSecAnonymizeGaps` passou. Resíduo: endpoint alfanumérico (`maria.souza` em `ObjectName`, `Endpoint` e `Channel`) e número externo com menos de 7 dígitos não são trocados. No Issabel os ramais são numéricos (`extension_pattern ^[0-9]+$`), então um endpoint alfanumérico costuma ser tronco. Controle de processo: revisar a captura antes do commit (o CLI avisa "revise o arquivo antes de compartilhar") | backend |
| NF3 | O nome vindo do display name do telefone entrava sem limpeza no cache e na tela, e o cache nunca era expurgado | LOW agora; requisito para a fase 2 | `internal/state/channels.go`, `view.go`, `state.go`; `internal/store/names.go` | **Fechado.** `model.CleanName` (`internal/model/model.go:166`) remove caracteres de controle Unicode (C0, C1 e formatação: bidi, zero-width e BOM) e corta em 64 caracteres. É aplicado nas três entradas: `channels.go:64`, `view.go:112` e no cache antigo em `state.go:224`. `PruneNames` (`internal/store/names.go:55`, chamado em `serve.go:350`) tira do cache os ramais removidos do PBX. `TestSecCleanNameHostile` passou. **Fase 2, ainda pendente:** a tela divide a origem com o admin do Issabel; renderizar nomes só como texto, nunca com `innerHTML`, e com CSP estrita | backend (fechado), frontend (fase 2) |
| NF4 | Rollback seguia só com a checagem de nome quando o arquivo `SHA256` faltava | LOW | `instalar.py` | **Fechado.** Sem SHA-256 registrado, o rollback é abortado sem alterar nada (`instalar.py:919-922`) | devops |
| NF5 | No `rpm -e`, o socket parava antes de a conf do Apache sair, deixando a 8790 livre enquanto o proxy ainda apontava para ela | LOW | `packaging/scripts/preremove` | **Fechado, com resíduo.** A conf do Apache sai e o httpd é recarregado antes de parar o socket (`preremove:4-12`). Resíduo: se `httpd -t` falhar por outra configuração quebrada, o httpd não é recarregado e continua repassando para a 8790 até o próximo reload; o script avisa no stderr | devops |
| NF6 | Sessão por inatividade sem teto absoluto: com WebSocket ativo, a sessão nunca expira | LOW, **aceito pela Decisão 5** | `internal/api/ws.go` (`renewSession`); `internal/api/authn.go` (`slide`); `internal/store/users.go` (`RenewSession`) | Nenhuma correção. Controles existentes: revogação por `user del`, `user passwd` e `session purge --all`; cookie `HttpOnly`, `Secure` e `SameSite=Strict`; aviso `session_expired` antes do fechamento 4001 | — |

Colateral funcional do rollback: **corrigido no código**. Quando a versão anterior não tem a unit de socket, o rollback para e desabilita o `painel-ramais.socket` e remove o symlink de enable que sobraria (`instalar.py:939-947`). Fica uma janela de milissegundos com a 8790 livre até o daemon antigo abrir a porta; é o comportamento normal da versão antiga. Validar na homologação (seção 6, item 7).

Verificado sem achado:
- `listen.go` aceita só um descritor e só TCP em loopback, e limpa `LISTEN_*`.
- O SQL do cache de nomes é parametrizado.
- Nomes vão para o JSON com `json.Marshal`, que escapa HTML e caracteres de controle C0, e para o `/debug` com `html/template`.
- `capturar`:
  - cria o arquivo com `O_CREAT|O_EXCL` e modo 0600, então recusa sobrescrever e recusa symlink;
  - roda como painel-ramais, porque a CLI recusa root;
  - mascara `Secret`, `Password` e `Md5Cred` também no frame de Login enviado;
  - passa pela mesma lista fixa de actions.
- O eventfilter `Event: Reload` usa a classe `system`, que já está em `read`.

## 6. Depende de Linux (auditoria incompleta)

Nada abaixo foi executado. Validar na homologação em Rocky 8 e Rocky 9:

1. `asterisk -rx 'manager show command <Action>'` para cada action da tabela do README, e também para `MixMonitor`, `GetConfig` e `PJSIPNotify`, confirmando o impacto do risco aceito. Confirmar se `PJSIPShowEndpoint` expõe `Password` no `AuthDetail` da versão do Issabel 5.
2. `manager show user painel-ramais`: `read`/`write`, `permit` e eventfilter (inclusive `Reload`).
3. `stat -c '%a %U:%G' /etc/asterisk /etc/asterisk/manager_custom.conf /etc/painel-ramais/*` após `rpm -i`, após `rpm -U` e depois de uma "atualização do Issabel" simulada (M1).
4. Socket activation: `systemctl stop painel-ramais.service` e, como outro usuário, `nc -l 127.0.0.1 8790` precisa falhar com "address in use" (M4). Drop-in com `listen:` diferente. Restart em crash.
5. `systemd-analyze security painel-ramais` e o serviço subindo com `MemoryDenyWriteExecute` e `CapabilityBoundingSet` vazio (L3).
6. Apache (httpd 2.4.37): `/painel-ramais/debug`, `/painel-ramais/api/health` e `/painel-ramais//debug` não podem retornar JSON nem HTML do daemon; upgrade do WebSocket chega ao daemon; Origin cruzado dá 403.
7. Rollback com symlinks e um RPM falso plantados por painel-ramais (H1), e rollback para uma versão sem a unit de socket (colateral).
8. `%post` e `%postun` com `/etc/asterisk/manager_custom.conf` como symlink plantado pelo asterisk (M2), e com o disco cheio (N2).

## 7. Suíte ofensiva para o QA

Estado atual de cada teste:

| # | Ataque | Esperado | Estado |
|---|---|---|---|
| 1 | 40 logins paralelos com senha errada | no máximo 5 respostas 401 | verde |
| 2 | Variantes de caminho em `/api/health` e `/debug` com `X-Forwarded-For` | nunca 200 | verde (daemon) |
| 3 | `/debug` local sem cookie | 401 | verde |
| 4 | WebSocket com Origin de outro site | 403 | verde |
| 5 | Token adulterado, vencido, com `exp` longo, com campo extra ou reutilizado | 401 | verde (testes do projeto) |
| 6 | Action fora da lista, ou `Action`/`ActionID` como parâmetro | recusada, sem chegar ao socket | verde |
| 6b | ... com log no nível Error | log de erro visível com `log_level: info` | verde (teste do supervisor exige `level=ERROR`) |
| 7 | Captura sem `--anonimizar` | segredos mascarados | verde |
| 8 | Captura com `--anonimizar`: IPv6, `Username`, `ObjectName`, `Peer`, `AccountCode`, `Value`, nomes, hosts | anonimizados | verde (`TestSecAnonymizeGaps`, `TestAnonymizedCaptureLeavesNoOriginalValue`); resíduo LOW: endpoint alfanumérico |
| 9 | Nome com ESC, CR/LF, bidi, zero-width ou com mais de 64 caracteres vindo do telefone | normalizado | verde (`TestSecCleanNameHostile`, `TestCleanName`, `TestNamesFromOutsideAreNormalized`) |
| 10 | Rollback sem `SHA256` registrado | recusado | verde (`test_rollback_sem_sha256_registrado_aborta_sem_tocar_em_nada`, rodado nesta rodada) |
| 10b | Rollback para versão sem unit de socket | socket parado e desabilitado | verde no teste unitário (`test_rollback_para_versao_sem_unit_de_socket...`); validar em Linux |
| 11-17 | Itens 1 a 8 da seção 6 | ver seção 6 | pendente (Linux) |

## 8. Próximo passo

Nada bloqueante. NF1 a NF5 e o colateral estão fechados no código.

- **qa:** executar a seção 6 na homologação Rocky 8 e 9 e anexar a saída. Os itens 1 a 10b da seção 7 já estão automatizados.
- **operador da captura:** rodar sempre com `--anonimizar` e revisar o arquivo antes do commit em `testdata/`, buscando principalmente nome de endpoint alfanumérico (resíduo do NF2).
- **frontend (fase 2):** renderizar nomes e números só como texto (nunca `innerHTML`), com CSP estrita, porque o painel divide a origem com o admin do Issabel (NF3).
- **gerente:** a data do aceite da Decisão 1 fica registrada pelo commit de `docs/DECISOES-FASE-1.md`.
