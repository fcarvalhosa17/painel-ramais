# Spec rc.9: estado de registro persistente, pausa por reinício real, filas sem ciclo e correções

**Data**: 2026-10-19
**Decisões**: ADR-010 (estado de registro persistente, esquema 6) e ADR-011 (pausa por `Uptime`, log de decisões, releitura de filas, canais auxiliares, fila `default`, relógio, eventfilter, remoção de código sem uso). Aceitos.
**Contrato**: `docs/CONTRATOS.md` (fonte única). **Tela**: `docs/design/desvios.md` D38 a D45.
**Plano**: `docs/plans/2026-10-19-rc9-registros-e-filas.md`.
**Base**: tag `v0.1.2-rc.8` (o HEAD tem 3 commits só de README e docs a mais).

## Situação de instalação

**O servidor do usuário está na rc.8** (esquema 5, instalada em 2026-10-19). A rc.9 tem esquema 6: a primeira subida migra 5 para 6 e cria sozinha o backup `painel.db.bak-v5`. O rollback a partir da rc.9 volta para a rc.8 restaurando o `.bak-v5`.

O `.bak-v3` que existe no servidor é a cópia do banco da rc.6, feita pela rc.8 quando ela subiu pela primeira vez. Ele só servia para voltar da rc.8 para a rc.6, e **não é usado** no rollback da rc.9: o instalador guarda um nível só, e voltar a ele perderia tudo o que a rc.8 gravou.

Quem estiver na rc.6 também pode ir direto para a rc.9 (3 para 6, backup `.bak-v3`; rollback para a rc.6 com o aviso que cita o estado de registro), mas esse não é o caso do usuário.

Alvo: Rocky 8 com Issabel 5, só IPv4.

## Problema

1. A aba Registros mostra o tempo total fora do ar no período, mas não diz há quanto tempo o ramal está fora agora nem quando registrou pela última vez. O intervalo aberto se perde quando o painel reinicia.
2. Toda reconexão do AMI pausa a contagem de registros por 120 s, porque o Asterisk manda `FullyBooted` a cada login.
3. Não há como saber por que uma queda contou ou não.
4. Num PBX grande, a leitura de filas bloqueia a leitura de eventos. A fila do leitor estoura, o painel refaz o estado, lê as filas de novo e fecha um ciclo. Eventos de registro e de chamada se perdem nesse estouro.
5. Espionagem (ChanSpy), conferência e outros canais auxiliares aparecem como chamada externa.
6. O detalhe da fila redesenha as listas inteiras a cada segundo.
7. Relógio do servidor fora de sincronia estraga todos os tempos, sem aviso.
8. `cmd/loadtest`, `cmd/amisim`, `internal/amisim` e `testdata/capturas` não são usados por nada.
9. No servidor do usuário as filas não atualizam ao vivo: o bloco AMI tem 25 dos 29 `eventfilter`.
10. A fila de exemplo `default` do Asterisk aparece no painel.
11. A aba Ramais diz "X com problema" (soma não registrados e inalcançáveis); o usuário quer os dois separados (ver "Item 11").

## Restrições (do usuário, não reabrir)

- Nenhum teste no PC: não escrever teste, não rodar `go test`, não executar binário, simulador nem tela. Pronto = compila (`go build` linux e host, `go vet`, `node --check`) e confere por leitura. O teste é ao vivo no servidor (D12).
- AMI só leitura; a allowlist de actions não muda. `manager.conf` e o MySQL do Issabel ficam intocados. No `manager_custom.conf`, só o bloco do painel, pelo `%post`.
- Sem travessão longo nos textos. Commit só pelo @devops, sem `Co-Authored-By`.
- Chaves JSON em inglês (D8).

## Escopo

| Item | O que entra | Onde está a regra |
|---|---|---|
| 1 | Colunas "Fora do ar há" e "Último registro"; tempo total no detalhe e no CSV; `reg_state` (esquema 6); reconciliação; ordem em 3 faixas; filtro "Ocultar fora desde antes da instalação"; relógio ao vivo, parado e acinzentado com AMI fora; lacuna | ADR-010; CONTRATOS 4.7, 4.7.2, 4.7.3, 4.7.5, 10 (v6), 11.1, 11.4, 11.5; D38 a D43, D45 |
| 2 | Pausa só em reinício real (`Uptime` do `FullyBooted` abaixo de 120 s, ou `Shutdown`); aviso na tela | ADR-011 d1; CONTRATOS 4.7 (cabeçalho), 7.2, 11.5 item 2; D42 |
| 3 | Log Info por decisão de contagem, 20/s com rajada de 100, resumo a cada 10 s | ADR-011 d2; CONTRATOS 11.5 "Log de decisões", 4.4 |
| 4 | `QueueStatus` fora do laço ao vivo, eventos de fila guardados, intervalo mínimo crescente, log de cada releitura completa | ADR-011 d3; CONTRATOS 11.3, 11.6, 4.4 |
| 5 | Canais auxiliares descartados na tradução | ADR-011 d4; CONTRATOS 2.3 |
| 6 | Detalhe da fila com rolagem virtual e atualização por mudança | ADR-011 d5; D44 |
| 7 | Aviso de relógio no `verificar` e no `status`; texto no README | ADR-011 d6; CONTRATOS 15.4, 15.6, 17 |
| 8 | Remoção de `cmd/loadtest`, `cmd/amisim`, `internal/amisim`, `testdata/capturas` e da regra no `.gitattributes` | ADR-011 d8; CONTRATOS 0 (D12), 12 |
| 9 | Conferência do bloco AMI (`%post`, `verificar`, `status`); `queues.events_received` no health | ADR-011 d7; CONTRATOS 7.2, 4.4, 15.4, 15.6 |
| 10 | Fila `default` ignorada (nome exato) | ADR-011 d9; CONTRATOS 2.4, 11.6 |
| 11 | Coluna Grupos da aba Ramais: "X não registrados" e, quando houver, "Y inalcançáveis", no lugar de "N com problema" (opção (b) do usuário) | D46; seção "Item 11" abaixo |

## Item 11 (decidido pelo usuário: opção (b))

Na tag `v0.1.2-rc.8`, "N com problema" (`internal/web/static/assets/app.js:409`) soma **não registrado + inalcançável** (`app.js:384`). Passa a ser, em cada linha de grupo e em "Todos os ramais", "X não registrados" (singular "1 não registrado") e, só quando houver, "Y inalcançáveis" (singular "1 inalcançável"), também no nome acessível do botão (D46). Continuam iguais a ordem "Mais problemas" (`app.js:385`, `:392`, pela soma), o destaque acima de 25% (`app.js:410`) e o "pior grupo" (`app.js:345-347`, só não registrados). Nenhum campo novo no servidor: os números já vêm da contagem por estado da tela.

## Critérios de aceite (ao vivo no servidor, roteiro R1 a R13 do plano)

Conferência pela tela, pela API (`/api/registrations`, `/api/health`) e pelo `journalctl`, nunca pelo `sqlite3` de linha de comando (o do Rocky 8, 3.26, não abre tabela `STRICT`).

C1. Desligar um telefone: "Fora do ar há" conta ao vivo. Reiniciar o painel: continua da hora original.
C2. Ramal fora antes da primeira subida da rc.9: "fora desde antes da instalação do painel (<data>)", sem relógio, na segunda faixa da ordem; a data é a do "Dados desde" da aba (instalação da rc.8), não a do upgrade.
C3. Religar o telefone: a coluna esvazia e "Último registro" mostra a hora. O CSV traz as colunas novas na ordem da 4.7.5.
C4. Painel parado com um telefone voltando nesse meio: "Último registro" mostra "antes de <hora>", e o detalhe mostra o tempo "com lacuna"; o tempo somado vai só até o último minuto em que o painel estava vivo com o AMI conectado (nunca o tempo parado).
C5. AMI fora (parar o Asterisk): relógio parado e acinzentado, aviso de estado desconhecido.
C6. `systemctl restart painel-ramais` com o Asterisk no ar: **sem** aviso de pausa. Log "contagem nao pausada".
C7. `systemctl restart asterisk`: aviso "Contagem de registros pausada até HH:MM (Asterisk reiniciado)" na aba Registros e no bloco da aba Ramais.
C8. `journalctl -u painel-ramais | grep "registro: "`: linhas por transição e resumo por motivo, sem inundar.
C9. Fila com movimento: `/api/health` mostra `queues.events_received` subindo e `queue.overflows` parado. Log de cada leitura completa com o próximo mínimo.
C10. ChanSpy numa interna: a aba Chamadas mostra só a interna, sem externa falsa.
C11. A fila `default` sumiu; as filas reais continuam. Log "fila default ignorada" uma vez.
C12. `./instalar.py verificar` e `status` mostram o relógio e o filtro AMI (AVISO quando cabe, nunca ERRO).
C13. Rollback a partir da rc.9 volta para a rc.8 e restaura o `.bak-v5` (o `.bak-v3` não é usado).
C14. Coluna Grupos da aba Ramais: "X não registrados" e, quando houver, "Y inalcançáveis", com o singular certo.
