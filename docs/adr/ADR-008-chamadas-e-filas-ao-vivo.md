## ADR-008: Chamadas e filas ao vivo (aba Chamadas)

**Data**: 2026-10-19
**Status**: Aceito (decisões do usuário para a rc.8; o que só se confirma ao vivo está em "Suposições", com o critério de cada uma)
**Porta**: 2 vias para a projeção de chamadas e filas (chaves novas e opcionais no snapshot e no delta; nada antigo muda de formato) e para a aba Chamadas. 1 via, no sentido de postura de segurança, para a classe `agent` no `read` do bloco AMI e para a action `QueueStatus` na allowlist (ADR-003 atualizado; o bloco é regerado a cada upgrade, então voltar atrás é um upgrade).

### Contexto

A Etapa 2 pede uma tela de chamadas ao vivo e de filas para o NOC: quantas externas entrando e saindo, quantas internas, quantas aguardando em fila, quem fala com quem, por qual tronco e há quanto tempo, e o estado de cada fila e dos agentes. O painel já correlaciona canais por ramal (cada ramal leva `calls[]`, CONTRATOS 2.2), mas isso dá uma linha por **ramal**, não por **chamada**: uma chamada de URA para um grupo de toque com 30 ramais apareceria 30 vezes, e chamada sem ramal (tronco para URA, siga-me tronco com tronco) não aparece.

Há também um defeito no que existe: o "com quem fala" decide ramal ou tronco pelo `extension_pattern` e pelo nome do endpoint, e um canal que não é da tecnologia ativa (DAHDI, IAX2, Khomp, `SIP/` quando a tecnologia é `pjsip`) chega com `Endpoint` vazio e vira `internal` com outro lado desconhecido.

Restrições (do usuário, não reabrir): AMI só leitura e nunca `Command`; nada de MySQL nem arquivo do Issabel; só o bloco do painel no `manager_custom.conf`, pelo `%post`; tudo em memória, sem histórico de chamadas; 10 mil ramais e milhares de chamadas simultâneas sem teto de linhas; mesma permissão da tela atual.

### Decisão

1. **Uma linha por chamada, agrupada por `Linkedid`.** O `Linkedid` do Asterisk é o `Uniqueid` do canal que começou a chamada e é herdado por todo canal criado a partir dela (Dial, Local, fila). A chamada é o conjunto de canais vivos com o mesmo `Linkedid`; o `id` da linha é o próprio `Linkedid` (o Uniqueid do canal âncora, o que começou a chamada), estável enquanto a chamada existe, mesmo depois que o canal âncora desliga numa transferência. Grupo só com canais Local não vira linha.
2. **`Linkedid` atualizado por qualquer evento que o traga.** O Asterisk 12 em diante troca o `Linkedid` dos canais que entram numa bridge pelo mais antigo (transferência assistida junta duas chamadas numa). Todo evento de canal traduzido passa a levar o `Linkedid` do evento, e o estado move o canal de grupo quando ele muda. Duas linhas que viram uma: a mais nova sai em `calls_removed`, a mais antiga continua.
3. **Ramal é quem está no inventário.** Canal que não é Local e cujo endpoint **não** está no inventário de ramais (`PJSIP`/`SIP` de tronco, DAHDI, IAX2, Khomp, qualquer outra tecnologia) é externo, nunca ramal. A mesma regra corrige o `direction` do `calls[]` de cada ramal (CONTRATOS 2.2). O tronco aparece como o nome do canal sem o sufixo de sequência: `PJSIP/operadora_x`, `SIP/trunk-acme`, `DAHDI/i1`, `IAX2/operadora` (regra exata no CONTRATOS 2.3).
4. **Direção pelo canal que começou a chamada**, guardada na primeira vez que a linha é montada e mantida até a linha sumir (sobrevive a transferência): começou fora do inventário = `inbound` (para ramal, fila, URA ou outro tronco: siga-me tronco com tronco é uma linha de entrada); começou num ramal = `outbound` se houver canal externo vivo na chamada, senão `internal`. Âncora que é Local ou que já tinha desligado no snapshot: vale o canal não Local mais antigo da chamada.
5. **Número externo**: entrante = CallerID do canal âncora (tronco); sainte = número do `DialString` do `DialBegin` que criou o canal do tronco (`011987654321@operadora_x`, `operadora_x/011987654321`), com o argumento do Dial (`DialData`) no snapshot e o `Exten` do canal do ramal como reserva.
6. **Estados**, em ordem de precedência: `queued` (algum canal da chamada está aguardando numa fila), `hold`, `talking` (alguma bridge tem dois ou mais canais da chamada, ou ninguém mais está tocando), `ringing`. "Em conversa via fila X" é `talking` com `queue` preenchido (a fila que atendeu, pelo `AgentConnect`; no snapshot, pelo canal que ainda está na aplicação `Queue`).
7. **Chamada em fila é uma linha só**, na lista da direção dela: externa vai para "Externas entrantes" com "Em fila X" e conta no contador de entrantes; ramal que liga para a fila fica em "Internas" com o mesmo estado. "Aguardando em fila" no topo conta as linhas em `queued`, de qualquer direção.
8. **Filas: QueueStatus e eventos, sem QueueSummary.** Fonte só leitura: a action `QueueStatus` (única action nova na allowlist; `QueueReload` e `QueueReset` nunca) e os eventos `QueueCallerJoin`, `QueueCallerLeave`, `QueueCallerAbandon`, `QueueMemberStatus`, `QueueMemberPause`, `QueueMemberAdded`, `QueueMemberRemoved`, `AgentCalled`, `AgentConnect`, `AgentComplete`, `AgentRingNoAnswer` (classe `agent`). Quem espera e o estado dos agentes vêm dos eventos; atendidas, abandonadas e nível de serviço (`Completed`, `Abandoned`, `ServicelevelPerf` do `QueueParams`) vêm só da `QueueStatus`. Reconciliação completa ao subir, a cada sincronização (conexão e overflow), depois de um `Reload` e a cada 10 min; e, depois de `QueueCallerLeave`, `QueueCallerAbandon` ou `AgentComplete`, uma `QueueStatus` só da fila afetada, no máximo uma vez a cada 30 s por fila. A tela diz que os contadores valem "desde o último reinício do Asterisk". `AgentCalled` e `AgentRingNoAnswer` entram no filtro (decisão aprovada) mas não mudam o estado na rc.8: o estado do agente vem do `QueueMemberStatus`.
9. **Nome da fila no banco do painel**, editável pela tela só por administrador (checagem do `acl.db` de sempre); sem nome, a tela mostra "Fila 600". É a única escrita nova e a única mudança de esquema (ADR-009).
10. **Mesmo fluxo de dados da tela de ramais.** O estado projeta chamadas e filas e as publica no **mesmo** lote de 250 ms e no **mesmo** WebSocket, com chaves novas e opcionais: `calls`/`calls_removed` e `queues`/`queues_removed` no delta, `calls` e `queues` completos no snapshot e no `Reset`. Cada objeto vai inteiro (substituição por `id` ou por `queue`), como os ramais. A versão avança uma unidade por objeto mudado ou removido. Sem teto de linhas; a tela usa rolagem virtual em cada lista.
11. **Custo por evento limitado.** A projeção de chamadas mora no `State`, fora da `view` (o `internal/state/view.go` não muda nesta release). Cada evento de canal só marca como sujo o `Linkedid` do canal antes e depois do evento (e, em `BridgeEnter`/`BridgeLeave`, os `Linkedid` dos canais daquela bridge). No `Flush`, só as chamadas sujas são remontadas, cada uma em O(k) nos k canais dela. Nada de varrer todas as chamadas por evento. A condição de saída do lote passa a considerar chamadas e filas sujas.
12. **"Local sem par"**: chamada que tem canal Local e continua sem o outro lado (entrante sem ramal e fora de fila, ou interna com um ramal só e fora de fila) por 1 s seguido mostra "outro lado desconhecido" e gera **um** log Info por chamada, `chamada com Local sem par`, com `uniqueid`, `linkedid` e `canal`. Sem canal Local (URA pura) ou com tronco do outro lado: mostra "externo" ou "desconhecido" sem log.

### Alternativas consideradas

- **A (mais simples possível): montar a aba Chamadas no navegador a partir do `calls[]` de cada ramal.** Descartada: não mostra chamada sem ramal (URA, siga-me tronco com tronco, fila sem agente), duplica a chamada de grupo de toque e não tem tronco nem fila. Os dados para isso não estão no ramal.
- **B: uma linha por canal (estilo `core show channels`).** Descartada: o NOC quer chamada, não perna; com Local channels e grupos de toque, a lista viraria ruído.
- **C: identidade pela âncora "viva" (trocando o id quando ela desliga).** Descartada: a transferência faria a linha sumir e voltar com outro id e outra direção. O `Linkedid` é o id que o próprio Asterisk dá à chamada.
- **D: mensagem nova no WebSocket ou rota REST com consulta periódica (como a aba Registros).** Descartada: chamada ao vivo precisa da mesma latência dos ramais (250 ms), e o delta já tem replay, epoch e coalescência. As chaves novas são aditivas; o `dados.js` já ignora chave desconhecida.
- **E: `QueueSummary` periódica para os contadores.** Descartada pelo usuário: `QueueStatus` traz os mesmos números e mais (membros e quem espera); uma action a menos na allowlist.
- **F: `QueueStatus` periódica a cada poucos segundos em vez de eventos.** Descartada: atraso visível e carga proporcional a membros x filas o tempo todo; os eventos dão o "ao vivo" e a `QueueStatus` por fila com teto de 30 s mantém os contadores perto do real.
- **G: guardar histórico de chamadas (CDR próprio).** Fora do escopo (decisão do usuário: tudo em memória). O CDR do Issabel continua sendo a fonte histórica.
- **H: datastore novo (Redis) para a projeção.** Descartada pelo critério 1: a projeção cabe na memória do processo que já tem os canais.

### Suposições feitas (só se confirmam ao vivo; nenhuma tem teste local, regra do usuário)

- **S13 (campo do número discado).** O `DialBegin` do Asterisk 18 traz `DialString` com o destino do Dial (`011987654321@operadora_x` no PJSIP, `operadora_x/011987654321` no SIP, `g0/011987654321` no DAHDI). Confirmar com uma chamada de saída: a linha mostra o número discado. Se o campo não vier, a reserva (`Exten` do ramal) mostra o que o usuário discou antes das regras de saída.
- **S14 (Linkedid depois de transferência).** Na transferência assistida e na cega, os canais que ficam passam a ter o `Linkedid` da chamada mais antiga e o evento seguinte (`BridgeEnter`, `NewConnectedLine` ou `Newstate`) o traz. Confirmar: transferir uma entrante de A para B; a linha de entrada continua com o mesmo `id`, agora com B, e a linha interna A para B some. Se o Asterisk não propagar, a tela mostra duas linhas até o fim (erro visível, sem perda de dado).
- **S15 (eventos de fila no Asterisk 18).** Nomes e campos: `QueueCallerJoin`/`Leave`/`Abandon` com `Queue` e o canal do chamador (`Uniqueid`, `Linkedid`, `CallerIDNum`); `QueueMemberStatus`/`Pause`/`Added`/`Removed` com `Queue`, `Interface`, `StateInterface`, `MemberName`, `Status`, `Paused`, `PausedReason`, `InCall`, `CallsTaken`; `AgentConnect` com `Queue` e o canal do chamador; todos com privilégio `agent`. `QueueStatus` responde em lista (`QueueParams`, `QueueMember` com `Location`, `QueueEntry` com `Wait`) terminada por `QueueStatusComplete`. Confirmar no `instalar.py homologar` (`manager show event QueueCallerJoin` e os demais) e com uma ligação para uma fila.
- **S16 (formato da interface do membro).** No Issabel os membros são `Local/2001@from-queue/n` com `StateInterface` `hint:2001@ext-local` ou `PJSIP/2001`; o ramal sai do texto entre a primeira `/` ou `:` e o primeiro `@` ou `/`. Membro que não casar com um ramal do inventário aparece com o nome do membro e sem ramal.
- **S17 (nome do canal fora de PJSIP/SIP).** DAHDI `DAHDI/i1/1199-1a` ou `DAHDI/1-1`, IAX2 `IAX2/peer-1234`, Khomp com formato a confirmar; a regra do tronco (CONTRATOS 2.3) dá `DAHDI/i1`, `DAHDI/1`, `IAX2/peer`; um formato inesperado aparece inteiro, sem quebrar nada.
- O canal do chamador que espera em fila não está numa bridge com outro canal da chamada (a música de espera é tocada no próprio canal); de qualquer forma `queued` tem precedência.
- Fila com prioridade: a posição mostrada é a ordem de chegada (`since`); a `QueueStatus` seguinte corrige a lista, não a ordem. Aceito.

### Consequências

- Positivas: a operação vê todas as chamadas, inclusive as sem ramal; filas e agentes ao vivo sem consulta periódica pesada; o `direction` dos ramais deixa de chamar DAHDI/IAX2/Khomp de interna.
- Negativas / trade-offs:
  - Mais bytes no delta em hora de pico (cada chamada mudada vai inteira; um grupo de toque de 30 ramais é uma linha só, com os 30 números).
  - Uma action a mais (`QueueStatus`, privilégio de escrita nenhum) e a classe de leitura `agent`, que só dá eventos de fila e agente.
  - Contadores de fila podem atrasar até 30 s (por fila) ou 10 min (sem evento), e zeram quando o Asterisk reinicia (aviso na tela).
  - Chamadas restauradas por snapshot (reconexão do AMI) perdem a fila "via" se o canal não estiver mais na aplicação `Queue`, e a espera (`hold`) como já acontece hoje (CONTRATOS 11.3).

### Critério de validação

Por compilação e leitura no PC (`go build` com `GOOS=linux ./...`, `go vet ./...`, `node --check` nos `.js`), e ao vivo no servidor (spec rc.8, critérios C1 a C14):
- Entrante para fila: uma linha "Em fila 600" em "Externas entrantes", "Aguardando em fila" = 1, a fila 600 mostra o chamador; ao atender, "Em conversa via fila 600" com o ramal do agente.
- Saída por tronco DAHDI ou IAX2: aparece em "Externas saintes" com o tronco `DAHDI/...`, nunca em "Internas".
- Transferência assistida: o `id` da linha de entrada se mantém e a linha interna temporária some (S14).
- `curl -s http://127.0.0.1:8790/api/health`: `live_calls` igual ao total da tela; `queues.refresh_errors` 0.
- Com 2.000 chamadas no ar (pico real ou `cmd/loadtest` na VM, sem rodar no PC), `lag_ms` continua abaixo de 1 s.

### Agentes impactados

backend (`internal/model`, `internal/ami/calls`, novo `internal/ami/queues`, `internal/ami/actions.go`, `internal/state`, `internal/api`, `internal/store`, `cmd/painel-ramais`, `internal/amisim`, `packaging/scripts/postinstall`), frontend (aba Chamadas, `dados.js`), devops (release, README), segurança (classe `agent`, action `QueueStatus`, rota de renomear fila), revisor.
