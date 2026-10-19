## ADR-007: Relatório de registro dos ramais

**Data**: 2026-10-19
**Status**: Aceito (decisão do usuário para a rc.7)
**Porta**: 1 via para o esquema 4 do SQLite (as tabelas nascem na mesma migração do ADR-006) e para o `eventfilter` do `manager_custom.conf`; 2 vias para a forma de atualização da tela (consulta a cada 5 s com cursor) e para a exportação CSV montada no navegador.

### Contexto

O NOC precisa saber quais ramais caem e voltam (registro e alcance), quantas vezes, de qual IP, e por quanto tempo ficaram fora do ar, com 24 h, 7 e 30 dias de visão. Hoje o painel só mostra o estado atual e o histórico agregado por minuto (contagens), sem o "quem" nem o "quantas vezes". A leitura do AMI não pode atrasar: o estado roda numa goroutine só e a fila do AMI é a mesma que já pode estourar (CONTRATOS 11.3).

### Decisão

1. **Quatro contadores por ramal**, contados na transição de `registration`:

   | Antes | Depois | Contador |
   |---|---|---|
   | `registered` | `unregistered` | perdeu registro (`lost_registration`) |
   | `unregistered` | `registered` | registrou (`registered`) |
   | `registered` | `unreachable` | ficou inalcançável (`became_unreachable`) |
   | `unreachable` | `registered` | voltou a ser alcançável (`reachable_again`) |
   | `unreachable` | `unregistered`, ou o inverso | nenhum |
   | `unknown` | qualquer | nenhum |

2. **Só evento ao vivo conta.** `model.RegistrationChanged` ganha `Live bool`, ligado só em `pjsip.contactStatus` e `sip.peerStatus`. Snapshot, releitura periódica, releitura por recarga, reconexão e reconciliação produzem `Live=false` e nunca contam. A contagem fica em `view.onRegistration`, o único ponto que conhece o estado anterior e o novo; o refresh de REGISTER sem mudança de estado não passa do `return` que já existe. Transições aplicadas na visão em montagem (sincronização) não contam.
3. **Tempo total fora do ar.** Abre na primeira queda contada (perdeu registro ou ficou inalcançável) e só fecha quando o ramal volta a `registered` (que no contrato já quer dizer registrado e alcançável). `unreachable` para `unregistered` não conta contador e não fecha o intervalo. O intervalo aberto vive no estado (`extState.downSince`), atravessa a reconexão do AMI (é copiado para a visão nova ao fim da sincronização; se o ramal voltou registrado, fecha ali) e fecha também quando o ramal é apagado do PBX. Ao fechar, os segundos são distribuídos pelas horas do intervalo (no máximo as últimas 720 horas) na tabela agregada.
4. **Reinício do Asterisk não vira instabilidade.** `Event: Shutdown` entra no `eventfilter` do bloco do painel (só no `manager_custom.conf`, pelo `%post` do RPM, e na lista do simulador). Depois de `Shutdown`, de `FullyBooted` ou de uma conexão nova do AMI, o estado abre uma janela de supressão de 120 s: as transições ao vivo dentro dela são gravadas como **evento do sistema** (`system = 1`), fora dos contadores e do ranking, e o sinal em si vai para `reg_system_events`. Transição de sistema nunca abre intervalo de fora do ar, mas fecha.
5. **Gravação fora do caminho do AMI.** O estado chama um callback que só faz envio sem bloqueio para uma fila própria (16.384 itens); fila cheia descarta e conta. Um gravador (`internal/reglog`) junta as ocorrências e grava em lote (a cada 2 s ou 500 itens) numa transação: ocorrência bruta, agregado por hora e ramal, corte de 200 ocorrências por ramal. Descartes, tamanho da fila e erros de gravação aparecem em `/api/health` (`registrations`). A goroutine do estado nunca espera o banco.
6. **O que se guarda.** Por ocorrência: horário, ramal, tipo, IP e porta IPv4 do contato (endereço que não é IPv4 vira nulo), user agent quando houver, marca de sistema. Detalhe bruto limitado a 200 ocorrências por ramal. Agregado por hora e ramal com os quatro contadores, eventos de sistema, segundos fora do ar, última queda, último IP e porta, e o **nome** do ramal (o `pruneNames` apaga o nome do cache quando o ramal sai do PBX; o relatório não depende dele). Ramal apagado no Issabel continua no relatório até expirar, marcado `removed`. Retenção fixa de 30 dias, limpeza diária automática. A data de instalação desta versão é gravada na migração (`reg_meta.since`) e a tela mostra "Dados desde ...".
7. **Grupo no gráfico e na tabela.** O gráfico por grupo usa duas colunas novas e anuláveis em `history` (perdeu registro e ficou inalcançável por minuto, por grupo e total), alimentadas por contadores acumulados do estado: o gráfico respeita a pertença ao grupo **no minuto** em que aconteceu. A tabela e o ranking usam a pertença **atual** (filtro no navegador, a partir dos grupos de cada linha).
8. **Atualização da tela: consulta com cursor, não WebSocket.** A aba Registros chama `GET /api/registrations` a cada 5 s com o `cursor` da resposta anterior e recebe só as linhas dos ramais que mudaram (revisão do lote de gravação, coluna `rev` indexada). Virada de hora, troca de período, cursor inválido ou banco com revisão menor que a do cursor devolvem a tabela inteira (`full: true`). Nada muda no protocolo do WebSocket.
9. **Exportação CSV no navegador.** A tela monta o CSV da tabela filtrada e ordenada que está na tela, com `;`, BOM UTF-8 e CRLF, e prefixa `'` em toda célula que começa com `=`, `+`, `-`, `@`, TAB ou CR (proteção contra injeção de fórmula).
10. **Permissão.** Mesma da tela atual: qualquer sessão válida (administrador e operador do Issabel pelo menu PBX, e a sessão de emergência). Só leitura; nenhuma rota nova de escrita.

### Alternativas consideradas

- **A (mais simples possível): calcular tudo do histórico por minuto.** Descartada: o histórico só tem contagens por grupo; não diz qual ramal caiu nem quantas vezes.
- **B: gravar cada ocorrência direto do estado, na mesma goroutine.** Descartada: um `INSERT` com `busy_timeout` de 5 s na goroutine do estado travaria a fila do AMI (rajada de 3.000 desregistros em 10.000 ramais, critério C1).
- **C: nova mensagem agregada no WebSocket.** Funciona, mas muda o protocolo do ADR-001 (cliente antigo, replay do ring buffer, reconexão) para um dado que tolera 5 s de atraso. A consulta com cursor reaproveita o REST e a sessão como estão.
- **D: CSV gerado no servidor.** Exigiria repetir no servidor a busca, o filtro e a ordenação que a tela já faz; o arquivo poderia sair diferente do que o operador vê.
- **E: persistir o intervalo aberto de fora do ar.** Adiado (ponytail): o intervalo aberto se perde quando o **daemon** reinicia (não quando o Asterisk reinicia). Teto conhecido: o tempo fora do ar de um ramal que já estava caído durante o restart do serviço fica subcontado. Entra com tabela `reg_down` se o usuário pedir.
- **F: fila externa (Redis, NATS) ou banco separado para o relatório.** Descartada pelo critério 1 (tecnologia chata): canal com buffer e o SQLite existente cobrem o volume (uma transação por 2 s).

### Suposições feitas

- O Asterisk 18 do Issabel envia `Event: Shutdown` com privilégio `system` (o usuário AMI tem `read = system,call`) e o `eventfilter` o deixa passar. A confirmar na captura (S11 do CONTRATOS).
- `ContactStatus` traz a porta no `ViaAddress` ou no `URI`, e `PeerStatus` no `Address` (`ip:porta`). Sem porta, grava nulo (S12).
- 120 s cobrem o re-registro dos telefones depois de um restart do Asterisk. É constante; vira configuração só com medição.
- O fuso do servidor é o mesmo do navegador do NOC (dias do 7d e 30d cortados à meia-noite do servidor; fuso com meia hora desalinha 30 min, aceito).

### Consequências

- Positivas: ranking de instabilidade por grupo na tela principal; aba de investigação com linha do tempo por ramal; reinício do Asterisk não polui o ranking.
- Negativas: mais escrita no SQLite (um lote a cada 2 s em rajada; quase nada em operação normal); tamanho estimado com 10.000 ramais e 30 dias: até 2 milhões de linhas brutas (200 por ramal) e algumas centenas de milhares de linhas por hora; o restart do daemon perde intervalos abertos (alternativa E).
- Fila cheia perde ocorrências (contadas em `registrations.dropped`); nunca derruba nem atrasa o painel.

### Critério de validação

Por compilação, leitura e ao vivo (sem teste automatizado, regra do usuário):
- `go build ./...` sem erro; `git grep -n "Live:"` mostra o campo ligado só em `internal/ami/pjsip/pjsip.go` e `internal/ami/sip/sip.go`; nenhuma escrita no banco dentro de `internal/state`.
- No servidor: desligar o cabo de um telefone e religar gera "ficou inalcançável" e "voltou a ser alcançável" no ramal, com IP e porta; `asterisk -rx "core restart now"` gera linhas `system = 1` e uma linha `shutdown` em `reg_system_events`, sem mudar o ranking; `curl http://127.0.0.1:8790/api/health` mostra `registrations.dropped` igual a 0 depois do restart.
- A aba Registros não faz mais que uma consulta a cada 5 s (DevTools, aba Rede) e, sem ocorrência nova, recebe `rows: []`.

### Agentes impactados

backend (model, ami/pjsip, ami/sip, state, store, novo `internal/reglog`, api, cmd), frontend (bloco de instabilidade, aba Registros, exportação), devops (`%post` com `Event: Shutdown`, simulador, README, rollback), segurança (CSV e nova rota de leitura), revisor.
