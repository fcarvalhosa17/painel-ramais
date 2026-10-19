# Spec rc.8: chamadas e filas ao vivo (Etapa 2) e correções

**Data**: 2026-10-19
**Release**: v0.1.2-rc.8 (ponto de recuperação: tag `v0.1.2-rc.7`)
**Decisões**: `docs/adr/ADR-008-chamadas-e-filas-ao-vivo.md`, `docs/adr/ADR-009-nome-da-fila-esquema-5.md` (Aceitos); `docs/adr/ADR-003` atualizado (classe `agent`, `QueueStatus`); `docs/adr/ADR-007` com nota da gravação em sublotes.
**Contrato fechado**: `docs/CONTRATOS.md` seções 0 (D11, D12), 1, 1.2, 2.2, 2.3, 2.4, 4.1, 4.4, 4.8, 5.2, 7.1, 7.2, 9, 10 (versão 5, gravação em sublotes, limpeza em blocos), 11.1, 11.3, 11.4, 11.5, 11.6, 12, 13 (S13 a S17), 15.5, 15.7.
**Desvios de tela**: `docs/design/desvios.md` D28 a D37.
**Plano por agente**: `docs/plans/2026-10-19-rc8-chamadas-e-filas.md`.

## Situação de instalação

O servidor está na **rc.6** (esquema 3). A rc.7 não foi instalada. A rc.8 é instalada direto sobre a rc.6: uma subida migra 3 para 4 para 5 com um backup só (`painel.db.bak-v3`), e o `rollback` volta para a rc.6 restaurando esse backup.

## Problema

1. O NOC não tem visão de chamadas: só o `calls[]` de cada ramal (uma linha por ramal, sem tronco, sem fila, sem as chamadas que não passam por ramal).
2. Não há visão de filas: quem aguarda, há quanto tempo, estado dos agentes, atendidas, abandonadas, nível de serviço.
3. Chamada por DAHDI, IAX2, Khomp (ou `SIP/` com a tecnologia `pjsip`) aparece como "Interna".
4. O gravador do relatório de registro pode perder um lote inteiro sem dizer quantas ocorrências nem de quando.
5. Restos: testes que o usuário não roda, diálogo "Mover" que não existe mais, "+ Novo grupo" sumiu do seletor de destino, "Todos os ramais" aceito como nome de grupo pelo daemon, foco de teclado perdido em "Chamadas ativas".

## Restrições (do usuário, não reabrir)

- Rocky 8 + Issabel 5, IPv4 só. AMI **somente leitura**, nunca `Command`; a única action nova é `QueueStatus` (`QueueReload`, `QueueReset` e as outras de fila nunca). Proibido ler ou escrever o MySQL ou arquivos do Issabel; o `manager_custom.conf` só muda pelo bloco do painel, no `%post`.
- Nenhum teste: não escrever, não rodar `go test`, não executar binário, simulador ou tela no PC. Critério de pronto = compila (`GOOS=linux go build ./...`, `go vet ./...`, `node --check`) e confere por leitura. O teste é ao vivo no servidor.
- Tudo em memória, sem histórico de chamadas. Sem teto de linhas (10 mil ramais, milhares de chamadas).
- Mesma permissão da tela atual (administrador e Operator, menu PBX); renomear fila só administrador, com a checagem do `acl.db`.
- `internal/state/view.go` não muda (nem comentário).
- Sem travessão longo nos textos.

## Escopo

**Entra**
- Projeção de chamadas (uma linha por `Linkedid`) e de filas no estado, publicada no mesmo delta (chaves opcionais) e no snapshot.
- Regra de ramal por inventário (corrige o `direction` do `calls[]` dos ramais também).
- `Linkedid` atualizado por qualquer evento de canal que o traga.
- Filas: `QueueStatus` (completa e por fila), onze eventos de classe `agent`, `read = system,call,agent` e eventfilter no bloco do painel e no simulador.
- Nome de fila (tabela `queue_names`, esquema 5), rota `POST /api/queues/rename`.
- Aba Chamadas: contadores, filtros, três listas com rolagem virtual, painel de filas e detalhe da fila, edição do nome.
- Acessibilidade de teclado nas listas novas e em "Chamadas ativas".
- Gravador do relatório: sublotes por custo, uma repetição, perda contada no health e no log; limpeza em blocos.
- Remoção de todos os testes e do alvo `test`; "Todos os ramais" reservado no daemon; "+ Novo grupo" de volta; restos do "Mover" fora; aviso de rollback ajustado.

**Não entra**
- Histórico de chamadas, gravação, escuta, transferência ou qualquer ação sobre chamada.
- `QueueSummary`, actions de escrita de fila, pausa de agente pela tela.
- Ação de clique nas linhas de chamada.
- Auditoria do nome de fila em `group_audit`.
- Persistir o intervalo fora do ar aberto (ADR-007, alternativa E).
- Qualquer mudança em `internal/state/view.go`.

## Critérios de aceite (ao vivo no servidor, depois de instalar a rc.8 sobre a rc.6)

| # | Critério | Como conferir |
|---|---|---|
| C1 | Migração 3 para 5 com um backup | `sqlite3 /var/lib/painel-ramais/painel.db "PRAGMA user_version"` = 5; existe `painel.db.bak-v3`; **não** existe `painel.db.bak-v4`; `journalctl -u painel-ramais` sem erro de migração |
| C2 | Bloco AMI novo | `grep -A3 "BEGIN painel-ramais" /etc/asterisk/manager_custom.conf` mostra `read = system,call,agent`; `grep -c "eventfilter = Event: Queue\|eventfilter = Event: Agent" /etc/asterisk/manager_custom.conf` = 11; `asterisk -rx "manager show user painel-ramais"` lista a classe `agent` em leitura |
| C3 | Entrante para fila | ligar de fora para a fila: uma linha "Em fila 600" em "Externas entrantes", "Aguardando em fila" = 1, a fila mostra o chamador com o tempo correndo |
| C4 | Atendida via fila | o agente atende: a linha vira "Em conversa via fila 600" com o ramal do agente; na fila, o agente fica "em atendimento"; em até 30 s "atendidas" sobe ao fim da chamada |
| C5 | Abandono | desligar antes de atender: a linha some; em até 30 s "abandonadas" sobe |
| C6 | Saída por tronco não PJSIP | ligação de saída por DAHDI, IAX2 ou Khomp (se o servidor tiver): aparece em "Externas saintes" com o tronco `DAHDI/...` e o número discado; nunca em "Internas". O `calls[]` do ramal mostra "Saída" |
| C7 | Interna | ramal para ramal: uma linha em "Internas" com origem e destino; ramal para a fila: "Internas" com "Em fila 600" |
| C8 | Transferência | entrante atendida por A, transferida para B: a linha mantém o mesmo `id` (DevTools, delta), passa a mostrar B; a linha interna temporária some (S14) |
| C9 | Nome da fila | administrador renomeia a fila 600: outra tela aberta mostra o nome em até 1 s; operador não vê o botão; `curl` sem papel de administrador na rota dá 403 |
| C10 | Volume | em pico real (ou `cmd/loadtest` na VM, nunca no PC), `curl -s http://127.0.0.1:8790/api/health` mostra `lag_ms` abaixo de 1000 e `live_calls` igual ao total da aba |
| C11 | Filas reconciliadas | `queues.last_full_at` muda a cada 10 min; `queues.refresh_errors` = 0; fila sem agente aparece com zeros |
| C12 | Relatório de registro sem perda silenciosa | `registrations.lost_events` = 0 e, se algum dia for maior que 0, o journal tem o Warn `relatorio de registro: ocorrencias perdidas` com quantidade e período |
| C13 | Correções de tela | "+ Novo grupo" aparece em "Adicionar aos grupos" (administrador) e o grupo criado volta marcado; criar grupo "Todos os ramais" pela API dá 400; em "Chamadas ativas", setas, Home e End movem o foco e o foco não se perde quando a lista atualiza |
| C14 | Rollback | `./instalar.py rollback` mostra "voltando para a rc.6 (esquema 3)" e a lista de perdas com "nomes de fila"; confirmado, a rc.6 sobe com o banco 3 e a tela abre |
