# Spec rc.7: ramal em N grupos e relatório de registro

**Data**: 2026-10-19
**Release**: v0.1.2-rc.7
**Decisões**: `docs/adr/ADR-006-ramal-em-n-grupos.md`, `docs/adr/ADR-007-relatorio-de-registro.md` (Aceitos). ADR-005 substituído em parte.
**Contrato fechado**: `docs/CONTRATOS.md` seções 2.1, 4.4, 4.6, 4.7, 7.2, 9, 10 (versão 4), 11.1, 11.2, 11.4, 11.5, 12, 15.5, 15.7.
**Desvios de tela**: `docs/design/desvios.md` D19 a D27.
**Plano por agente**: `docs/plans/2026-10-19-rc7-n-grupos-e-registros.md`.

## Problema

1. Um ramal só pode estar num grupo, e faixas de grupos diferentes não podem se cruzar. A operação precisa do mesmo ramal em vários grupos (área e função).
2. O NOC não sabe quais ramais caem e voltam, quantas vezes, de onde e por quanto tempo.

## Restrições (do usuário, não reabrir)

- Rocky 8 + Issabel 5, IPv4 só. AMI **somente leitura**, nunca `Command`; nenhuma action nova. Não editar `manager.conf` nem arquivos do Issabel; o `manager_custom.conf` só muda pelo bloco do painel no `%post`.
- Nenhum teste no PC do usuário: nada de `go test`, nada de executar o binário ou a tela localmente. A verificação é por compilação e leitura; o teste é ao vivo no servidor. O `build.ps1 release` só compila e empacota.
- Mesma permissão da tela atual para ver o relatório; qualquer alteração de grupo continua exigindo administrador (conferência no `acl.db`).

## Escopo

**Entra**
- N:N ramal x grupo, faixas sobrepostas entre grupos, "Sem grupo" implícito, `assign` com `mode` (`add` vários grupos, `remove` de um grupo, só vínculo manual), CSV que só acrescenta.
- Contrato do ramal com `groups`, `range_groups`, `manual_groups` (sem `group`/`group_manual`).
- Quatro contadores de registro por ramal, só eventos ao vivo, janela de 120 s de evento do sistema após `Shutdown`, `FullyBooted` ou conexão do AMI, tempo total fora do ar, IP e porta IPv4, user agent, ramal removido marcado, nome guardado.
- Gravação em lote fora da goroutine do estado, descartes no `/api/health`, retenção de 30 dias com limpeza diária, "Dados desde".
- Esquema 4 numa migração, backup `.bak-v3`, rollback para a rc.6 que restaura e avisa.
- Tela: barra de abas (Ramais, Registros, encaixe de Chamadas sem botão), bloco "Instabilidade de registro (24 h)" na coluna esquerda, aba Registros (período, grupo, busca, tabela ordenável, gráfico, linha do tempo, exportar CSV), atualização a cada 5 s por cursor.
- `build.ps1` sem o passo `Test` no `release` (e no `all`).

**Não entra**
- Aba Chamadas (Etapa 2): só o encaixe no código.
- Persistir o intervalo fora do ar aberto entre restarts do daemon (ADR-007, alternativa E).
- Lista de exclusões de faixa.
- Mensagem nova no WebSocket.
- Configuração da janela de 120 s, da fila de 16.384 ou da retenção de 30 dias.

## Critérios de aceite (ao vivo no servidor, depois do upgrade)

| # | Critério | Como conferir |
|---|---|---|
| R1 | Banco migrou para 4 com backup | `sqlite3 /var/lib/painel-ramais/painel.db "PRAGMA user_version"` = 4; `painel.db.bak-v3` existe; log sem erro de migração |
| R2 | Vínculos antigos preservados | ramal com grupo manual antes do upgrade aparece no mesmo grupo |
| R3 | Faixas sobrepostas entre grupos | criar "Andar 2" 2000-2299 com "Suporte" 2000-2099 existente responde 200; 2050 aparece nos dois grupos e uma vez em "Todos os ramais" |
| R4 | Remover só vínculo manual | ramal pela faixa: botão "Remover deste grupo" desabilitado com "Este ramal pertence ao grupo pela faixa X-Y" |
| R5 | Contadores ao vivo | tirar o cabo de um telefone: "ficou inalcançável"; religar: "voltou a ser alcançável", com IP:porta |
| R6 | Reinício não polui | `asterisk -rx "core restart gracefully"`: ranking igual, linhas de sistema na linha do tempo, `shutdown` em `reg_system_events` |
| R7 | Sem descarte | `curl -s http://127.0.0.1:8790/api/health` mostra `registrations.dropped` 0 |
| R8 | Atualização da aba | DevTools: uma chamada `/api/registrations` a cada 5 s, `rows: []` sem ocorrência nova |
| R9 | CSV seguro | exportar com um nome de ramal começando com `=`: a célula sai com `'` na frente |
| R10 | Rollback | `./instalar.py rollback` mostra o aviso de perda; confirmado, a rc.6 sobe com o banco 3 |
