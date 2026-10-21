# Decisões já tomadas

Este arquivo só aponta. O texto completo, com alternativas e consequências, está em cada ADR. Reabrir qualquer item é
com o usuário.

## ADRs

| ADR | Decisão em uma linha | Por quê, em uma linha |
|---|---|---|
| [ADR-001](../adr/ADR-001-protocolo-websocket.md) | WebSocket com `hello`, `epoch`/`version`, ring buffer de 40 lotes, delta com o ramal inteiro, hub único | replay sem perder mudança e sem patch de campo; cliente lento não trava os outros |
| [ADR-002](../adr/ADR-002-sessao-e-token-hmac.md) | sessão por cookie (hash no banco), inatividade de 12 h renovada pelo uso; token HMAC de 60 s, uso único | tela de NOC fica aberta dias; vazamento do banco não entrega sessão |
| [ADR-003](../adr/ADR-003-pacote-rpm-e-integracao-issabel.md) | RPM faz a integração (`%post`), instalador só orquestra; conta AMI com `write = system,reporting` e allowlist no código | integração descartável e reversível; as consultas PJSIP exigem a classe `system` |
| [ADR-004](../adr/ADR-004-tela-e-menu-do-issabel.md) | tela no menu do Issabel por iframe com token; chave própria do Issabel; papel no prefixo do `sub`; rota de emergência | usa o login do Issabel; o PHP não lê o segredo do daemon |
| [ADR-005](../adr/ADR-005-grupos-editaveis-manual-sobre-faixa.md) | visões editáveis pela tela, auditoria na mesma transação, mudança por delta sem epoch (em parte substituído pelo ADR-006) | editar sem recarregar as telas abertas |
| [ADR-006](../adr/ADR-006-ramal-em-n-grupos.md) | ramal em N visões: vínculo manual soma à faixa; "Sem grupo" implícito | um ramal real pertence a vários setores ao mesmo tempo |
| [ADR-007](../adr/ADR-007-relatorio-de-registro.md) | relatório de registro: só transição ao vivo conta, gravação em lote fora do estado, 30 dias, CSV no navegador | não travar o estado com escrita em disco; servidor sem gerar arquivo |
| [ADR-008](../adr/ADR-008-chamadas-e-filas-ao-vivo.md) | uma linha por chamada (`Linkedid`); ramal = quem está no inventário; filas por `QueueStatus` e eventos de classe `agent` | chamada de grupo de toque aparecia N vezes; tronco DAHDI/IAX2 virava "interno" |
| [ADR-009](../adr/ADR-009-nome-da-fila-esquema-5.md) | nome da fila no banco do painel (`queue_names`, esquema 5), só administrador | o Asterisk só tem o número da fila |
| [ADR-010](../adr/ADR-010-estado-de-registro-persistente.md) | "fora do ar" e "último registro" persistentes em `reg_state` (esquema 6), sem inventar tempo | o estado se perdia a cada reinício do daemon |
| [ADR-011](../adr/ADR-011-decisoes-operacionais-rc9.md) | pausa de 120 s só em reinício real do Asterisk; log de decisões; canais auxiliares fora; fila `default` ignorada; observabilidade de filas | reconexão do painel não pode parecer queda geral dos ramais |

## Outras fontes de decisão

- [CONTRATOS](../CONTRATOS.md), seção 0: decisões de padrão D1 a D12 (ramal = nome que casa `extension_pattern`; N visões
  por ramal; CLI nunca como root; SIGHUP recarrega visões; `/debug` e `/api/health` só localhost; envelope REST;
  histórico com lacuna; JSON em inglês `snake_case` e datas RFC 3339 UTC; instalador nunca edita arquivo do sistema;
  relatório de registro; chamadas e filas; **sem teste automatizado no repositório**).
- [DECISOES-FASE-1.md](../DECISOES-FASE-1.md): decisões do usuário da fase 1 (1 conta AMI, 2 porta 8790 por socket
  activation, 3 RPM de rollback, 4 nome do ramal, 5 login e token).
- [desvios.md](../design/desvios.md): desvios da tela em relação ao design, D1 a D52. Os mais recentes: D47 (tema claro da
  marca CAM), D48 (fontes Exo 2 e Barlow), D49 (logo e cabeçalho), D50 ("grupo" vira "visão" na tela), D51 (seleção de
  várias visões), D52 (agregados com várias visões e `meta.overlap`).
- [tools/tela/DESIGN.md](../../tools/tela/DESIGN.md): regras visuais da tela.

## Decisões de trabalho que não se reabrem sem o usuário

- Nada de teste, simulador ou carga no PC do usuário; critério de pronto é compilar e conferir por leitura; o teste é ao
  vivo no servidor (CONTRATOS D12).
- Chaves JSON e valores de enum em inglês, mesmo quando o pedido vem em português (D8).
- A aba Registros atualiza por consulta a cada 5 s com cursor, não por WebSocket (ADR-007).
- "Grupo" no código e no contrato, "Visão" na tela (D50). Renomear identificadores, rotas ou tabelas não está decidido.
