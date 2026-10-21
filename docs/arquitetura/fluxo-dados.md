# Fluxo de dados

Quem chama quem, em três caminhos: do evento AMI até a tela, da tela até a API e o que vai para o banco. Formatos exatos
das mensagens: [CONTRATOS](../CONTRATOS.md), seções 2, 4, 5 e 11.

## 1. Evento AMI até a tela

```
Asterisk --(AMI, só os 29 eventos do eventfilter da conta painel-ramais)--> internal/ami (leitor, fila interna 65536)
  --> supervisor (cmd/painel-ramais/supervisor.go, laço ao vivo)
        adaptador pjsip ou sip (internal/ami/pjsip, internal/ami/sip)
        internal/ami/calls (canais e chamadas)   internal/ami/queues (filas e agentes)
  --> model.Event no canal "events"
  --> internal/state (uma goroutine): aplica, marca sujo, a cada 250 ms emite Batch (só se mudou) ou Reset (epoch novo)
  --> hub (internal/api/hub.go): serializa o lote uma vez, guarda no ring buffer (40 lotes), atualiza o cache do snapshot
  --> /ws de cada tela aberta: mensagem "delta"
  --> conexao.js recebe, dados.js aplica (substitui ramal, chamada e fila inteiros por chave), app.js/chamadas.js pintam
```

Pontos que importam:

- **Conexão única e sincronização.** Ao conectar (e depois de estouro da fila interna) o supervisor faz login, detecta a
  tecnologia (`CoreSettings`, `ModuleCheck`), lê o inventário (`PJSIPShowEndpoints` ou `SIPpeers`), contatos, estados de
  dispositivo, canais em curso (`CoreShowChannels`) e filas (`QueueStatus`), e emite `SyncFinished`. Isso gera epoch novo:
  o hub manda `reload` e cada tela relê `GET /api/snapshot`.
- **AMI fora.** Todo ramal vira `unknown` (nunca "não registrado"), chamadas e filas esvaziam, epoch novo. Backoff de
  reconexão de 1 a 30 s (`internal/ami/backoff.go`).
- **Releitura do inventário** sem epoch novo: 3 s depois de `Reload`/`ModuleLoadReport`/`FullyBooted` e a cada
  `ami.inventory_refresh` (padrão 10 min). Ramal novo chega em `extensions`, apagado em `removed`. Regra: CONTRATOS 7.4.
- **Nomes dos ramais** vêm em segundo plano (`PJSIPShowEndpoint` ou `SIPshowpeer`, 20 por segundo) e do cache em banco.
- **Filas**: eventos de fila e agente mais `QueueStatus` completa (subida, sincronização, `Reload`, a cada 10 min) e por
  fila depois de saída, abandono ou fim de atendimento (`supervisor_queues.go`).
- **Visões do ramal** são calculadas no estado (`internal/state/groups.go`) a partir do catálogo lido do banco
  (`GroupsLoaded`): faixa mais vínculo manual, N visões por ramal.
- **Ordem na tela.** A tela só aplica delta com `from_version <= v < to_version`; lacuna ou epoch diferente fazem buscar o
  snapshot de novo. Códigos de fechamento e replay: CONTRATOS 5 e ADR-001.

## 2. Tela até a API

### Entrada pelo menu do Issabel

```
Issabel (menu PBX > Painel de Ramais)
  --> modules/painel_ramais/index.php: lê /etc/painel-ramais-issabel.key, papel admin ou user pelo acl do Issabel,
      token HMAC de 60 s, devolve <iframe src="/painel-ramais/#t=<token>">
  --> Apache (/painel-ramais/ -> 127.0.0.1:8790/) --> GET / (tela.go: só entrega dentro de iframe)
  --> app.js apaga o fragmento da barra e faz POST /api/token --> cookie painel_session (sessão no banco)
  --> GET /api/snapshot --> abre /ws, manda hello {epoch, version} --> deltas ao vivo
```

Sem fragmento ou com token recusado a tela tenta `GET /api/session`; 401 mostra "Sessão encerrada. Abra de novo pelo menu
PBX do Issabel.". Rota de emergência `/painel-ramais/emergencia/`: formulário de senha do usuário `emergencia`
(`POST /api/login`).

### Leituras periódicas da tela

| O quê | Rota | Quando | Código |
|---|---|---|---|
| "Últimas 24 h" (aba Ramais) | `GET /api/history` com `group` repetido para as visões escolhidas | ao abrir, ao trocar visão, a cada minuto | `app.js` (`carregarHistorico`) |
| "Instabilidade de registro (24 h)" | `GET /api/registrations/summary`, uma chamada por visão, juntadas por número | ao abrir, ao trocar visão, a cada 30 s | `instabilidade.js` |
| Tabela da aba Registros | `GET /api/registrations?period&cursor` | a cada 5 s com a aba visível | `registros.js` |
| Gráfico da aba Registros | `GET /api/registrations/series` com `group` repetido | ao abrir, ao trocar período ou visão, a cada 60 s | `registros.js` |
| Detalhe do ramal (Registros) | `GET /api/registrations/extension` | ao abrir o ramal, ao trocar período | `registros.js` |

Toda chamada REST passa por `conexao.js` (envelope `{success, data, error, meta}`, prefixo `/painel-ramais/`).

### Escritas (só administrador do Issabel)

```
edicao.js / grupos.js / filas.js --> POST /api/groups/{create,update,assign,import,delete} ou /api/queues/rename
  --> session -> adminOnly (sessão issabel:admin:) -> StillAdmin (acl.db do Issabel, só leitura, falha fechada)
  --> store: uma transação (com linha em group_audit nas visões)
  --> recarga: loadGroups (GroupsLoaded) ou loadQueueNames (QueueNamesLoaded) entregue ao estado
  --> o estado publica a diferença no próximo delta, sem epoch novo; as telas abertas atualizam sozinhas
```

A CLI (`painel-ramais group ...`, `user ...`) grava no mesmo banco e manda SIGHUP; o daemon relê as visões e revalida as
sessões dos WebSockets abertos (`onSIGHUP` em `cmd/painel-ramais/serve.go`).

## 3. Gravação no banco

Um único `*store.Store` com uma conexão de escrita (WAL, `busy_timeout` 5 s, `BEGIN IMMEDIATE`). Quem grava:

| Dado | Quem grava | Quando | Tabela |
|---|---|---|---|
| Sessões | `internal/api/authn.go` | login, token, renovação por uso e pelo WebSocket | `sessions` |
| Visões, faixas, vínculos manuais | rotas `/api/groups/*` e CLI `group` | a cada escrita, com auditoria na mesma transação | `groups`, `group_ranges`, `extension_groups`, `group_audit` |
| Nome de fila | `/api/queues/rename` | a cada escrita | `queue_names` |
| Histórico por minuto | `internal/history` | virada de cada minuto: total (`''`) e uma linha por visão; `gap=1` se o AMI caiu no minuto | `history` |
| Relatório de registro | `internal/state` enfileira (`OnReg`), `internal/reglog` grava | lote a cada 2 s ou 500 notas, em sublotes por custo, uma repetição; perda contada no health | `reg_events`, `reg_hourly`, `reg_system_events`, `reg_state` |
| Cache de nomes | `runNameCache` em `serve.go` | lote a cada 5 s e no desligamento; expurgo depois de cada sincronização | `extension_names` |
| Usuário de emergência | CLI `user add` chamada pelo `instalar.py` | instalação | `users` |

Manutenção (`housekeep` em `serve.go` e `internal/reglog`): a cada hora apaga histórico além de `history.retention_days`,
sessões vencidas e auditoria antiga; uma vez por dia apaga o relatório de registro com mais de 30 dias, em blocos.
`reg_state` não expira. Esquema completo: [banco-migracoes.md](banco-migracoes.md).
