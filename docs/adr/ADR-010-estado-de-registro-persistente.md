## ADR-010: Estado de registro persistente por ramal (esquema 6)

**Data**: 2026-10-19
**Status**: Aceito (decisões aprovadas pelo usuário em 2026-10-19, rc.9, item 1)
**Porta**: 2 vias. A migração 5 para 6 é só aditiva (tabela nova e uma coluna nova em `reg_hourly`); voltar exige o backup (`painel.db.bak-v3` para a rc.6, `painel.db.bak-v5` para a rc.8), pelo `instalar.py rollback` que já existe.

### Contexto

A aba Registros mostra hoje o "tempo total fora do ar no período". O usuário quer saber há quanto tempo cada ramal está fora agora e quando ele registrou pela última vez. Esses dados precisam sobreviver ao reinício do painel e não podem depender da retenção de 30 dias nem do limite de 200 ocorrências por ramal.

Situação na tag `v0.1.2-rc.8` (linhas conferidas na tag):
- O intervalo fora do ar aberto existe só na memória, em `extState.downSince` (`internal/state/view.go:25-26`). Não há tabela para ele (CONTRATOS 10, notas da v4; ADR-007, alternativa E).
- O intervalo abre só em queda contada, fora da janela de 120 s (`internal/state/regreport.go:142-149`), e fecha em `registered`/`reachable_again`, na remoção do ramal ou no `carryDown` do `SyncFinished` (`regreport.go:150-184`).
- O fechamento vira `down_s` por hora em `reg_hourly` (`internal/store/regreport.go:157-174`).
- Num reinício do daemon, todo intervalo aberto se perde: não sai nota de fechamento, e o fato de o ramal estar caído some.
- Ramal que já está fora quando o painel sobe nunca abre intervalo. A primeira sincronização usa a visão em montagem, que não conta (`regreport.go:113-118`), e o `onEndpointSeen` passa de desconhecido para não registrado sem passar pela regra (`view.go:208-209`).
- Ramal que cai dentro da janela de 120 s e não volta também fica sem intervalo.
- Com o AMI fora, `markUnknown` (`view.go:260-272`) apaga o estado anterior. Na volta, ninguém sabe quem estava registrado antes da queda.

### Decisão

1. **Um conceito só de "fora do ar".** O ramal está fora quando `registration` é `unregistered` ou `unreachable` (inalcançável conta). Fica fora até voltar a `registered`. Passar de não registrado para inalcançável, ou o contrário, não reinicia o relógio. A janela de 120 s vale **só para os contadores** (`lost_registration`, `became_unreachable`), nunca para o estado. O `down_s` (tempo total no período) sai deste mesmo intervalo.

2. **Tabela nova `reg_state`** (DDL exata no CONTRATOS 10, versão 6), uma linha por número já visto:
   - `down_since`: início do "fora" atual, ou NULL quando ok;
   - `down_kind`: `observed`, `before_install` ou `unknown_start`;
   - `down_seg`: início do trecho que está sendo contado em `down_s`;
   - `last_reg`: última vez que ficou registrado;
   - `last_reg_exact`: 1 quando a hora foi vista, 0 quando é limite ("antes de");
   - `rev`: revisão do lote, usada pelo cursor.

   Fica fora da retenção e do limite de 200. Teto conhecido: números apagados do PBX continuam com a linha (dezenas ou centenas de linhas; sem expurgo, YAGNI).

3. **Coluna nova `reg_hourly.down_gap`** (0 ou 1): a hora recebeu segundos de um trecho cortado porque o painel ou o AMI estavam parados. A API devolve `down_gap` por ramal no período. É a "lacuna" que o detalhe e o CSV mostram.

4. **Quem grava: a fila do reglog, em lote.** O estado emite uma nota de estado sempre que muda `down_since`, `down_kind`, `down_seg`, `last_reg` ou `last_reg_exact`. Pelo mesmo `OnReg` sem bloqueio, o `internal/reglog` junta as notas por número (a última vence) e faz um upsert em `reg_state` na mesma transação do sublote, com custo 1. O caminho do AMI não ganha nenhuma escrita.

5. **Carga na subida.** Segue o padrão do `loadNames` (`cmd/painel-ramais/serve.go:255`): antes de `d.start()`, o `cmd` lê `reg_state` e o último minuto de `history` com `group_name = ''` **e `gap = 0`**, que é o batimento do painel com o AMI conectado. O resultado vai ao estado como `RegStateLoaded{Rows, Fresh, LastAlive}`. `Fresh` = tabela vazia. `LastAlive` = esse minuto, **sem somar 60 s**: a linha do minuto `M` é gravada no instante `M` (`internal/history/history.go:53-57`), e `gap = 0` prova AMI conectado até `M`. Sem linha ou com erro de leitura, `LastAlive` fica zero (desconhecido).

6. **Reconciliação no `SyncFinished`** (substitui o `carryDown`). Detalhada no CONTRATOS 11.5:
   - fora e continua fora: mantém `down_since` e `down_kind`;
   - fora e voltou: o trecho fecha no último instante provado em que o painel viu o ramal fora (`LastAlive` depois de reiniciar o daemon, `lastDown` depois de queda do AMI), marcado com lacuna, e `last_reg` vira "antes de <hora da sincronização>". Se esse instante é desconhecido (`LastAlive` zero) ou anterior ao início do trecho, o trecho fecha com **tamanho zero** e marcado com lacuna: nenhum segundo é somado;
   - estava ok e agora está fora: `unknown_start`, "fora desde antes de <hora da sincronização>", sem relógio;
   - nunca visto, com `reg_state` vazia: `before_install`;
   - nunca visto, com a tabela já em uso: `unknown_start`.

   Num ramal que continua fora depois de o painel ficar parado, o trecho contado fecha no `LastAlive` com lacuna (ou com tamanho zero, se o `LastAlive` é desconhecido) e um trecho novo começa na sincronização (`down_seg`). O `down_since` exibido não muda. Assim nunca se inventa tempo.

7. **Ramal removido do Issabel**: o trecho fecha na hora da remoção, `down_since` vira NULL e `last_reg` fica guardado. A linha aparece como `removed` enquanto tiver dado no período.

8. **AMI fora**: nada é gravado. A API devolve `state_unknown_since`, e a tela congela e acinzenta o relógio.

### Alternativas consideradas

- **A (mais simples possível): manter em memória e aceitar a perda no reinício (ADR-007, alternativa E).** Descartada pelo usuário: "isso precisa acabar".
- **B: colunas em `reg_hourly`.** Descartada: a retenção de 30 dias apaga as linhas.
- **C: reconstruir na subida a partir de `reg_events`.** Descartada: depende do limite de 200 e da retenção, que o usuário excluiu, e não cobre o ramal que nunca caiu desde a instalação.
- **D: gravar direto da goroutine do estado.** Descartada: poria escrita de banco no caminho do AMI (regra do ADR-007).
- **E: backfill na migração a partir de `reg_events` para quem vem da rc.8.** Descartada (ponytail): cobre só quem caiu dentro da retenção, e a data do upgrade já é um limite honesto ("fora desde antes de").
- **F: tabela de intervalos (uma linha por intervalo).** Descartada: o tempo por período já sai de `reg_hourly.down_s`; uma linha de estado por ramal basta.

### Suposições feitas

- O último minuto de `history` com `group_name = ''` e `gap = 0` marca o último instante provado com o daemon vivo e o AMI conectado. O amostrador grava uma linha por minuto no instante da virada (`internal/history/history.go:53-57`), inclusive com `gap = 1`; minuto com `gap = 1` não serve, porque o AMI estava fora (a 4.7 proíbe contar esse tempo).
- Uma nota de estado pode se perder (fila cheia ou sublote perdido duas vezes). A memória continua certa enquanto o daemon roda; o erro aparece só no próximo reinício, e a perda já sai no `/api/health` (`dropped`, `lost_events`). Correção, se a medição mostrar problema: regravar o estado inteiro em cada `SyncFinished`.
- Data de "fora desde antes da instalação do painel (<data>)" (decisão do usuário, D11): o "Dados desde" já gravado (`reg_meta.since`, criado pela migração v4 da rc.7/rc.8, ou na própria atualização rc.6 para rc.9), lido pela tela no `since` do cabeçalho da 4.7. Só sem `since` a tela usa o `down_since` do ramal (hora da primeira sincronização da rc.9). Para o usuário, que vem da rc.8 instalada em 2026-10-19, a data é a da rc.8. Limite aceito: o ramal que caiu entre a instalação da rc.8 e a subida da rc.9 também aparece "antes da instalação", porque a rc.8 não guardava esse estado.

### Consequências

- Positivas: "Fora do ar há" e "Último registro" sobrevivem a reinícios. O ramal caído antes da instalação aparece, sem tempo inventado. O tempo total por período passa a contar quedas de reinício do Asterisk, que antes sumiam.
- Negativas e trade-offs:
  - Mais um ponto de escrita no lote (custo 1 por nota).
  - Os números de `down_s` mudam de semântica em relação à rc.8: passam a incluir trechos de sistema.
  - A tabela de ramais pode encher o topo com ramais nunca registrados. Mitigação: o filtro "Ocultar fora desde antes da instalação" e a faixa própria na ordenação (CONTRATOS 4.7.2).

### Critério de validação

Por leitura e compilação no PC (D12). Ao vivo no servidor:
1. Desligar um telefone, esperar 5 min, `systemctl restart painel-ramais`. "Fora do ar há" continua contando da hora original.
2. Ramal desligado antes da primeira subida da rc.9 mostra "fora desde antes da instalação do painel (<data>)", sem relógio.
3. Religar o telefone: a coluna esvazia e "Último registro" mostra a hora.
4. Parar o painel, religar um telefone que estava fora e subir o painel de novo: "Último registro" mostra "antes de <hora>", e o detalhe mostra o tempo do período "com lacuna".
5. Sem `sqlite3` de linha de comando: o do Rocky 8 (3.26) não abre tabela `STRICT`. A conferência é pela API: depois de `systemctl restart painel-ramais`, a aba Registros (ou `GET /api/registrations` com sessão) mantém `down_since` e `last_registered` de antes do restart, e `curl -s http://127.0.0.1:8790/api/health` mostra `registrations.dropped` e `registrations.lost_events` em 0.

### Agentes impactados

backend (estado, reglog, store, api, cmd), frontend (aba Registros, detalhe, CSV), revisor, devops (README: atualização e rollback).
