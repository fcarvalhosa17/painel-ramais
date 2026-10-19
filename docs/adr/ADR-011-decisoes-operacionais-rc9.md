## ADR-011: Decisões operacionais da rc.9 (pausa, log de decisões, releitura de filas, canais auxiliares, fila "default", observabilidade)

**Data**: 2026-10-19
**Status**: Aceito (itens 2 a 10 da rc.9, aprovados pelo usuário em 2026-10-19)
**Porta**: 2 vias. Nada muda no esquema além do ADR-010. O AMI continua só leitura e a allowlist de actions não muda.

### Contexto

Linhas conferidas na tag `v0.1.2-rc.8` (o HEAD só tem commits de README e docs a mais).

**Pausa de 120 s em todo login (item 2).**
- O supervisor transforma qualquer `FullyBooted` em `AsteriskSignal` (`cmd/painel-ramais/supervisor.go:358-360`).
- O estado abre a janela de 120 s nesse sinal (`internal/state/state.go:172`) e também no `SyncFinished` de toda conexão (`state.go:283-285`).
- O Asterisk manda `FullyBooted` a cada Login, não só quando sobe. Resultado: cada reconexão do AMI pausa a contagem por 2 min.
- Fonte do Asterisk 18, lida em 2026-10-19: `main/manager.c`, `action_login`, e `main/asterisk.c`, `publish_fully_booted`. Os dois `FullyBooted` levam `Uptime` (segundos desde a subida; 0 se a hora de início não está definida) e `LastReload` (segundos desde o último reload), além de `Status: Fully Booted`.
- As capturas de `testdata/capturas` são sintéticas e trazem só `Status`.

**Log das decisões de contagem (item 3).** Hoje não existe log por transição. Quem quer saber por que uma queda não contou não tem como.

**Ciclo de releitura nas filas (item 4).** Explicação pedida pelo usuário, com as linhas:
- **O leitor AMI descarta qualquer evento quando a fila dele enche.** `ami.Client.dispatch` (`internal/ami/client.go:284-306`) põe cada evento num canal de 65.536 posições sem esperar. Cheio, ele descarta, soma `overflows` e liga a marca de ressincronização. O descarte não distingue o tipo: **registro, chamada e fila se perdem do mesmo jeito**. Sai um log Warn por episódio (`client.go:302-304`).
- **A resposta da `QueueStatus` não passa por esse canal.** Os itens com `ActionID` vão para a lista da action pendente (`client.go:286-293`, `accept` em `:310-331`). O problema não é o tamanho da resposta: é o **bloqueio**.
- **Quem esvazia o canal está ocupado.** `queuesFull` roda dentro do laço `live`, na mesma goroutine que tira eventos do canal (`supervisor.go:347-350`; `supervisor_queues.go:66-84`, prazo de 30 s). Enquanto o Asterisk monta a lista, o laço não lê eventos e o canal enche. A leitura por fila (`supervisor_queues.go:87-106`, até 10 s por fila) e a releitura do inventário (`supervisor.go:416`, até 2 min) bloqueiam do mesmo jeito.
- **O que o estouro dispara hoje.** Quando o canal esvazia, `TakeResync` (`client.go:186-191`) libera a ressincronização. `resyncIfNeeded` (`supervisor.go:457-471`) refaz o estado inteiro (`SyncStarted{overflow}`: endpoints, contatos, `DeviceStateList`, `CoreShowChannels`), também no laço `live`, e pede uma `QueueStatus` completa logo depois (`fullDue = true`, `supervisor.go:389`).
- **O ciclo.** Num PBX grande com muitos eventos, essa `QueueStatus` bloqueia o laço de novo, o canal estoura de novo, e o ciclo fecha. Basta passar de cerca de 2.200 eventos por segundo durante os 30 s de prazo.
- **O que se perde.** O estado volta certo depois da ressincronização, mas as **transições** de registro que caíram no descarte não contam (a visão em montagem não conta). Sai só o Warn do descarte, sem dizer quais ramais.

**Canal auxiliar como chamada externa (item 5).** A projeção trata como externo todo canal que não é Local e não está no inventário (`internal/state/livecalls.go:165-172`; `internal/state/calls.go:247-252`). `Snoop/`, `CBAnn/`, `CBRec/`, `Announcer/` e `Recorder/` viram "Externa" ou "Entrante" falsas. É a nota B4 do revisor da rc.8.

**Detalhe da fila (item 6).** O tique de 1 s redesenha o detalhe inteiro: `renderDetalhe` recria todos os itens de quem aguarda e dos agentes com `replaceChildren` (`internal/web/static/assets/filas.js:126-165`, chamado em `:219-222`).

**Relógio do servidor (item 7).** O instalador não confere a sincronização do relógio. Com o relógio errado, "Fora do ar há", "Último registro" e os períodos ficam errados.

**Código de teste fora de uso (item 8).** Ver a decisão 8.

**Filas sem atualização ao vivo (item 9).** No servidor do usuário o bloco do painel em `manager_custom.conf` não tem `QueueMemberAdded`, `QueueMemberRemoved`, `AgentCalled` nem `AgentConnect`. Evidência por leitura:
- **Um gerador só.** `packaging/scripts/postinstall:46-89` (função `ami_block`) gera o bloco, com os 29 `eventfilter`. O `instalar.py` não edita o arquivo (regra D9; ele só lê, em `instalar.py:437-457`). `tools/` e `bundle.py` não geram bloco: `git grep eventfilter` só acha o `postinstall`, o simulador e um comentário do supervisor.
- **O que foi publicado está certo.** O RPM `dist/painel-ramais-0.1.2-0.1.rc.8.x86_64.rpm` e o do bundle `painel-ramais-0.1.2-rc.8.tar.gz` (SHA-256 `d5bdba84...` nos dois) trazem os 29 `eventfilter` no scriptlet, um de cada (contado no cabeçalho do RPM).
- **Nenhuma versão do repositório teve 25 linhas.** O histórico do `postinstall` tem 15, 17, 18 e 29 (`git log -- packaging/scripts/postinstall`).
- **O Asterisk copia o filtro para a sessão só no Login.** Em `main/manager.c`, `authenticate()`, os `includefilters` do usuário são ligados à sessão; um `manager reload` muda o usuário, não a sessão aberta. O `%post` grava o bloco, faz `manager reload` (`postinstall:112-126`) e só depois reinicia o serviço (`postinstall:237-239`). Se o `manager reload` falhou (aviso no `%post`), o daemon reconecta com o filtro antigo até o Asterisk recarregar.
- **O que o daemon faz com cada evento** (`internal/ami/queues/queues.go:72-89`):
  - `QueueMemberAdded` vira `QueueMemberChanged`;
  - `QueueMemberRemoved` vira `QueueMemberRemoved`;
  - `AgentConnect` marca "atendida via fila";
  - `AgentCalled`, `AgentComplete` e `AgentRingNoAnswer` não geram evento (o `AgentComplete` só agenda a `QueueStatus` da fila).
- **Conclusão.** Sem os quatro, a entrada e a saída de agente (login e logout dinâmico, `*45` no Issabel) só aparecem na reconciliação de 10 min. Os agentes "não atualizam ao vivo". O `AgentCalled` descartado não explica a falta de atualização: o estado do agente vem do `QueueMemberStatus`.
- **O delta já leva as filas.** No estado, no hub (`internal/api/hub.go:245-249`) e na tela (`internal/web/static/assets/dados.js:86`, `chamadas.aoMudarDados` em `app.js:295`), as filas chegam pelo delta, não só na carga da página.
- **Causa mais provável:** o bloco do servidor não é o que o `%post` da rc.8 publicado escreve. Ou foi editado depois, ou não foi regravado no upgrade. O `%post` não regrava quando há seção `[painel-ramais]` fora dos marcadores (`postinstall:113-114`) ou quando a gravação como usuário asterisk falha (`postinstall:127-130`); nos dois casos só sai um aviso. Não há como fechar a causa só por leitura: a conferência ao vivo está no plano (R9).

**Fila "default" (item 10).** O `queues.conf` de exemplo do Asterisk traz a fila `[default]`, que não existe no Issabel e aparece no painel. Em `apps/app_queue.c` do Asterisk 18 (lido em 2026-10-19), `queue_hash_cb` usa `ast_str_case_hash` e `queue_cmp_cb` usa `strcasecmp`. O Asterisk trata nomes de fila **sem diferenciar maiúsculas**: não pode haver `default` e `Default` ao mesmo tempo, e o nome que sai no AMI é o do arquivo.

### Decisão

1. **Pausa só num reinício real do Asterisk.**
   - `Shutdown`: pausa de 120 s a partir do evento, como hoje.
   - `FullyBooted` com `Uptime` inteiro e maior ou igual a 0 **e menor que 120**: pausa até `At + (120 - Uptime) s`, isto é, 120 s depois da subida.
   - `FullyBooted` com `Uptime` de 120 ou mais: **não pausa**, não grava sinal `fully_booted` e sai um log Info.
   - `FullyBooted` sem `Uptime` ou com valor inválido: comportamento da rc.8 (pausa de 120 s), com log Info uma vez por sessão AMI dizendo por quê.
   - `LastReload` é lido e vai só para o log: reload não derruba registro.
   - O `SyncFinished` de conexão **deixa de abrir a pausa**. Continua gravando o sinal `ami_connected`, que é informativo no gráfico.
   - Motivo da pausa: `asterisk_restart`.
   - A tela recebe `paused_until` e `pause_reason` no cabeçalho de toda resposta da 4.7 e mostra "Contagem de registros pausada até HH:MM (Asterisk reiniciado)" na aba Registros e no bloco de instabilidade da aba Ramais.

2. **Log de cada decisão de contagem, sem bloquear.**
   - O estado entrega `model.RegDecision` por um `OnDecision`, envio sem bloqueio num canal de 4.096.
   - O `internal/reglog` (arquivo novo `decisions.go`, goroutine própria) escreve uma linha Info por transição até **20 por segundo**, com rajada de 100 (balde de fichas).
   - O que passa do limite, ou não cabe no canal, só soma um contador por motivo. A cada **10 s**, sai uma linha de resumo por motivo com contagem acima de zero.
   - Motivos: `contada`, `pausa`, `primeiro_estado`, `releitura`, `reconciliacao`, `fora_da_tabela`.
   - Nada de I/O na goroutine do estado. **Sem esquema novo.**

3. **Releitura de filas sem fechar o ciclo.**
   - (a) A `QueueStatus`, completa ou por fila, roda numa goroutine própria. No máximo uma de cada vez. O resultado volta ao laço `live` por canal.
   - (b) O laço `live` **continua lendo eventos** durante a leitura. Eventos de registro e de chamada seguem na hora. Eventos de fila que chegam durante a leitura ficam guardados, no máximo 50.000, e são emitidos logo depois da lista, mantendo a regra de hoje: "o que chega durante a leitura é aplicado depois da lista".
   - (c) Se passar de 50.000 guardados, os guardados são descartados, um Warn diz quantos, e a próxima completa é agendada.
   - (d) **Intervalo mínimo entre releituras completas, com espera crescente.**
     - `QueueStatus` completa: 30 s entre o fim de uma e o início da próxima. Dobra cada vez que um estouro do AMI acontece até 10 min depois de uma completa, com teto de 10 min. Volta a 30 s depois de 30 min sem estouro.
     - Ressincronização por estouro: 30 s, dobrando até 5 min com a mesma regra.
     - O pedido de completa depois de uma ressincronização respeita esse mínimo, não roda na hora.
   - (e) Log Info de cada releitura completa (filas e ressincronização) com motivo, duração, itens e o próximo mínimo.
   - (f) O descarte do leitor continua contado (`queue.overflows`). As transições perdidas aparecem no log de decisões como `reconciliacao` quando a ressincronização acha o estado novo: nunca em silêncio.
   - A releitura do inventário de ramais (7.4) continua síncrona nesta versão: o ciclo vem da fila, e o inventário já tem debounce e nova tentativa.

4. **Canais auxiliares nunca são chamada.** Em `internal/ami/calls`, a função `IsAuxChannel(name string) bool` testa estes prefixos, com comparação exata de maiúsculas, como o Asterisk escreve: `Snoop/`, `CBAnn/`, `CBRec/`, `Announcer/`, `Recorder/`, `Bridge/`, `Message/`, `MulticastRTP/`, `UnicastRTP/`. `Translate` e `FromCoreShowChannel` descartam todo evento cujo `Channel` (no `DialBegin`, também o `DestChannel`) seja auxiliar. O canal nunca entra no estado: não aparece nas listas da aba Chamadas, nos contadores, no `calls[]` dos ramais nem como externo. `MixMonitor` e `ChanSpy` usam audiohook e não criam canal: nada a filtrar. Numa interna com espionagem, a chamada principal continua; o canal de quem espiona é chamada própria dele, sem outro lado.

5. **Detalhe da fila com rolagem virtual.** As listas "Quem aguarda" e "Agentes" usam o `ListaLinhas` (`assets/linhas.js`), como a lista de filas. O tique de 1 s só atualiza o texto do tempo de espera das linhas visíveis. A lista é remontada só quando os dados da fila mudam (delta). Nada muda no servidor.

6. **Relógio no instalador.** `verificar` e `status` dão AVISO, nunca ERRO, quando o relógio não está sincronizado. Leitura no Rocky 8 (systemd 239): `timedatectl show -p NTPSynchronized --value`. Se esse comando falhar, `timedatectl` sem argumento e a linha `System clock synchronized:` ou `NTP synchronized:`. Correção sugerida: `systemctl enable --now chronyd && chronyc makestep`. Sem `timedatectl`: AVISO "não consegui conferir".

7. **Eventos de fila conferíveis (item 9).**
   - `/api/health` ganha `queues.events_received`: contagem por nome de evento de fila e agente desde a subida, contada no laço `live` **antes** de qualquer descarte, sem contar a fila `default`.
   - O `verificar` e o `status` conferem o bloco do painel em `manager_custom.conf` contra a lista dos 29 e dão AVISO com os nomes que faltam e a correção (reinstalar pelo `instalar.py instalar`, que regrava o bloco pelo `%post`, e reiniciar o serviço para a sessão AMI pegar o filtro novo).
   - O `%post` confere, depois de gravar, se o bloco tem os 29 e avisa se não tiver.
   - O contrato de filas fica aberto ao complemento que o usuário mandar depois do teste dele.

8. **Remover o código de carga e simulação que nada usa (item 8).** Verificado por leitura: `instalar.py`, `build.ps1`, `tools/` e `packaging/` não usam `cmd/loadtest`, `cmd/amisim`, `internal/amisim` nem `testdata/capturas`. O `build.ps1:51` e o `tools/ci-build.sh:9` compilam só `./cmd/painel-ramais`. O `homologar` usa `painel-ramais capturar` e `filtrar`, que importam só `internal/capture`. Saem:
   - `cmd/loadtest/` (9 arquivos);
   - `cmd/amisim/main.go`;
   - `internal/amisim/` (6 arquivos);
   - `testdata/capturas/` (21 arquivos);
   - a regra `testdata/capturas/*.txt binary` (e o comentário dela) do `.gitattributes`.

   **Fica** `internal/capture` (usado pelo `capturar`, pelo `filtrar` e pelo `homologar`). Se a proteção de permissão negar a remoção, o backend **não contorna**: devolve a lista exata.

9. **Fila "default" ignorada.**
   - Função única `model.IgnoredQueue(name string) bool`, comparação exata e com diferença de maiúsculas: `name == "default"`.
   - Aplicada na tradução, antes do estado: `queues.Translate` não devolve evento da fila; `queues.parse` pula `QueueParams`, `QueueMember` e `QueueEntry` dela; `calls.FromCoreShowChannel` deixa `Queue` vazio quando o `ApplicationData` aponta para ela; a fila não é marcada como pendente; não conta em `events_received`.
   - Como a fila nunca entra no estado, ela não aparece no painel de filas, em `queues.count` nem no "Aguardando em fila" do topo. A chamada que estiver nela é tratada **como sem fila**: aparece com o estado de canal (tocando ou em conversa), nunca "Em fila default".
   - Log Info **uma vez por processo do daemon**, na primeira vez que a fila aparece: `fila "default" ignorada (exemplo do Asterisk, nao existe no Issabel)`.
   - Qualquer outro nome continua aparecendo, inclusive `Default` com maiúscula: o Asterisk não deixa os dois existirem juntos, e um nome escrito de outro jeito foi escolhido por alguém.

### Alternativas consideradas

- **Pausa (A, mais simples): tirar a pausa do login e manter só a do `Shutdown`.** Descartada: o Asterisk que cai sem `Shutdown` (kill, queda de energia) volta sem pausa, e a rajada de registros conta como queda. O `Uptime` resolve os dois casos.
- **Pausa (B): ler `CoreStatus` para saber a hora de início.** Descartada: exige action nova na allowlist; o `Uptime` já vem no evento.
- **Log (A): uma linha por transição, sem limite.** Descartada: 10.000 ramais num reinício são 10.000 linhas no journald em segundos.
- **Log (B): logar direto da goroutine do estado com `slog`.** Descartada: I/O síncrono no caminho quente.
- **Filas (A): só aumentar o canal do leitor.** Descartada: adia o estouro, não tira o bloqueio, e custa memória fixa.
- **Filas (B): leitor de eventos em goroutine separada do `live`.** Descartada por ora: muda a ordem entre releitura de inventário e eventos, que hoje é garantida por desenho (7.4). A goroutine só para a `QueueStatus` resolve o ciclo, que é o que o usuário viu.
- **Auxiliares (A): filtrar no estado.** Descartada: o canal entraria em `chans`, bridges e grupos de `Linkedid`, e cada leitor teria que pular. Filtrar na tradução é um ponto só.
- **"default" (A): filtrar no estado de filas.** Descartada: a contagem de `events_received`, a `QueueStatus` e o `ApplicationData` do snapshot passariam com a fila; a tradução é o ponto comum dos três.
- **Remoção (A): manter o código de carga.** Descartada pelo usuário: o código sem uso cobra manutenção e confunde o técnico.

### Suposições feitas

- `Uptime` e `LastReload` vêm no `FullyBooted` do Asterisk 18 do Issabel. Confirmado no fonte do Asterisk 18; **só se confirma ao vivo** com `asterisk -rx "manager show event FullyBooted"` e com o log Info da decisão na próxima reconexão. Sem o campo, vale o comportamento da rc.8, com o log explicando.
- `Uptime` medido pelo Asterisk e `At` medido pelo daemon estão no mesmo servidor e no mesmo relógio.
- Os prefixos de canal auxiliar são os nomes de tecnologia do Asterisk 18: `Snoop` (`app_chanspy`/ARI snoop), `CBAnn`/`CBRec` (`app_confbridge`), `Announcer`/`Recorder` (mídia de bridge), `Message`, `MulticastRTP`, `UnicastRTP`. Outro nome que aparecer no servidor entra na lista.
- A causa do item 9 se fecha ao vivo (plano, R9).

### Consequências

- Positivas:
  - reconexão do AMI deixa de pausar a contagem;
  - toda decisão fica explicada no journald sem inundar;
  - o ciclo de releitura deixa de existir: a fila não bloqueia o laço e as completas têm piso crescente;
  - a aba Chamadas para de mostrar espionagem e conferência como externa;
  - o detalhe da fila deixa de recriar o DOM a cada segundo;
  - a fila de exemplo some.
- Negativas e trade-offs:
  - a goroutine da `QueueStatus` traz um buffer de eventos de fila guardados (teto de 50.000);
  - a lista de prefixos auxiliares é fixa no código;
  - remover `cmd/loadtest` tira a ferramenta de carga (critério C1 da seção 16 fica sem ferramenta no repositório).

### Critério de validação

Compilação e leitura no PC (D12). Ao vivo, roteiro do plano:
1. `systemctl restart asterisk`: pausa com aviso.
2. `systemctl restart painel-ramais` com o Asterisk no ar: sem pausa, com log "nao pausada".
3. `journalctl -u painel-ramais | grep "registro: transicao"`: linhas e resumos.
4. Fila com agentes: `/api/health` `queues.events_received` subindo, sem `overflows` novos.
5. ChanSpy numa interna: a aba Chamadas mostra só a interna.
6. A fila `default` sumiu e as filas reais continuam.
7. Relógio dessincronizado: `verificar` com AVISO.

### Agentes impactados

backend (supervisor, `ami/calls`, `ami/queues`, `model`, `state`, `reglog`, `api`, `postinstall`, `instalar.py`, remoção), frontend (aviso de pausa, detalhe da fila), revisor, devops (README: relógio).
