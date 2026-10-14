# Relatorio de carga: Painel de Ramais, Fase 1

Gerado por `cmd/loadtest` em 2026-10-13 20:49. **Medido no Windows** (windows/amd64, go1.27.2).

Oraculo: `cmd/amisim` embutido no mesmo processo do cliente WebSocket (mesmo relogio). O simulador foi escrito a partir do protocolo AMI do Asterisk 13/16/18 e de `docs/CONTRATOS.md`, sem importar `internal/ami` nem `internal/state`.

## Parametros desta execucao

- Daemon: `./painel-ramais-011.exe` (22120960 bytes, sha256 c59cee995f797210714651eb8652e330754a9d1a7b68f57634064fde3ca2e98d; versao 0.1.1)
- Ramais: 10000; chamadas simultaneas alvo: 400 (conversa media 20s); regime medido por 1m30s; rajada: 3000 desregistros; AMI fora por 4s
- Asterisk simulado: 18.20.0. Daemon em `technology: auto` nos dois modos.
- Daemon rodou como processo separado (binario), falando com o simulador por TCP (AMI) e com o cliente por HTTP e WebSocket; o teste nao importa nada de `internal/` do daemon.

## Resumo dos criterios de aceite

### sip

| Criterio | Resultado | Medido |
|---|---|---|
| p99 evento->delta na rajada de desregistros < 1 s | PASSA | p99=238 ms (n=3232) |
| p99 evento->delta em todas as fases < 1 s | PASSA | p99=252 ms (n=62423) |
| rajada: todos os desregistros chegaram ao cliente | PASSA | 3000/3000 em 98 ms |
| nenhuma perda de estado: snapshot final == verdade | PASSA | 0 divergentes de 10000 |
| nenhuma perda de estado: estado do cliente WS == verdade | PASSA | 0 divergentes, 0 lacunas de versao |
| nenhum estado fugaz errado visto no WS (nem de um instante, nem de outro) | **RESSALVA** | 4 fugazes errados; 6 so com dispositivo/chamadas de instantes diferentes (aceitos) |
| p99 evento->delta DURANTE o preenchimento de nomes < 1 s | PASSA | p99=250 ms (n=47213) enquanto os nomes eram preenchidos a 20/s |
| preenchimento de nomes completo e correto (callerid de cada ramal) | PASSA | 10000 corretos de 10000 ao fim; completo em 543.0 s desde o daemon pronto |
| queda do AMI: nenhum ramal 'unregistered' (snapshots com ami_connected=false) | PASSA | 16 snapshots; 0 ainda com estado antigo; max unregistered=0 |
| queda do AMI: todos 'unknown', sem chamadas | PASSA | min unknown=10000 de 10000, max chamadas=0, tudo 'unknown' apos 474 ms |
| queda do AMI: epoch novo na queda e outro na volta | PASSA | 1791933971602-9 -> 1791933971602-10 -> 1791933971602-11 |
| queda do AMI: cliente WS recebeu reload e voltou ao ar | PASSA | reloads=[epoch_changed epoch_changed] fechamentos=[4000 4000] ressincronizacoes=2 |
| queda do AMI: snapshot refeito (ramais registrados de volta) | PASSA | 9064 registrados de 10000 |
| 'com quem fala' correto em cada passo de cada cenario | PASSA | 106/106 passos |
| chamadas em andamento restauradas corretamente pelo snapshot apos reconexao (tocando, atendida, transferida) | PASSA | 48/48 cenario x rodada |
| log do simulador so com actions da allowlist | PASSA | violacoes=[] |
| modo auto detecta a tecnologia do simulador | PASSA | health.ami.technology="sip" esperado "sip" |
| versao do Asterisk lida via CoreSettings | PASSA | health.ami.asterisk_version="18.20.0" esperado "18.20.0" |
| snapshot: envelope {success,data,error,meta} e meta.count | PASSA | success=true meta.count=10000 ramais=10000 |
| snapshot: ramais ordenados por number (string) | PASSA |  |
| snapshot: Content-Encoding gzip com Accept-Encoding gzip | PASSA |  |
| snapshot: ami_connected=true e technology correta | PASSA | ami=true tech=sip |
| snapshot: 10000 ramais (sem troncos) | PASSA | 10000 ramais, esperado 10000 |
| snapshot ocioso == verdade do simulador (registro, dispositivo, grupo) | PASSA | 0 ramais divergentes de 10000 |
| memoria sem crescimento continuo | **NAO CONCLUSIVO no Windows** | 0.79 MiB/min em 580.0 s de regime (exige 30 min em Linux) |

### pjsip

| Criterio | Resultado | Medido |
|---|---|---|
| p99 evento->delta na rajada de desregistros < 1 s | PASSA | p99=245 ms (n=3210) |
| p99 evento->delta em todas as fases < 1 s | PASSA | p99=250 ms (n=62085) |
| rajada: todos os desregistros chegaram ao cliente | PASSA | 3000/3000 em 257 ms |
| nenhuma perda de estado: snapshot final == verdade | PASSA | 0 divergentes de 10000 |
| nenhuma perda de estado: estado do cliente WS == verdade | PASSA | 0 divergentes, 0 lacunas de versao |
| nenhum estado fugaz errado visto no WS (nem de um instante, nem de outro) | **RESSALVA** | 3 fugazes errados; 3 so com dispositivo/chamadas de instantes diferentes (aceitos) |
| p99 evento->delta DURANTE o preenchimento de nomes < 1 s | PASSA | p99=250 ms (n=46885) enquanto os nomes eram preenchidos a 20/s |
| preenchimento de nomes completo e correto (callerid de cada ramal) | PASSA | 10000 corretos de 10000 ao fim; completo em 541.9 s desde o daemon pronto |
| queda do AMI: nenhum ramal 'unregistered' (snapshots com ami_connected=false) | PASSA | 17 snapshots; 0 ainda com estado antigo; max unregistered=0 |
| queda do AMI: todos 'unknown', sem chamadas | PASSA | min unknown=10000 de 10000, max chamadas=0, tudo 'unknown' apos 437 ms |
| queda do AMI: epoch novo na queda e outro na volta | PASSA | 1791934653515-9 -> 1791934653515-10 -> 1791934653515-11 |
| queda do AMI: cliente WS recebeu reload e voltou ao ar | PASSA | reloads=[epoch_changed epoch_changed] fechamentos=[4000 4000] ressincronizacoes=2 |
| queda do AMI: snapshot refeito (ramais registrados de volta) | PASSA | 9064 registrados de 10000 |
| 'com quem fala' correto em cada passo de cada cenario | PASSA | 106/106 passos |
| chamadas em andamento restauradas corretamente pelo snapshot apos reconexao (tocando, atendida, transferida) | PASSA | 48/48 cenario x rodada |
| log do simulador so com actions da allowlist | PASSA | violacoes=[] |
| modo auto detecta a tecnologia do simulador | PASSA | health.ami.technology="pjsip" esperado "pjsip" |
| versao do Asterisk lida via CoreSettings | PASSA | health.ami.asterisk_version="18.20.0" esperado "18.20.0" |
| snapshot: envelope {success,data,error,meta} e meta.count | PASSA | success=true meta.count=10000 ramais=10000 |
| snapshot: ramais ordenados por number (string) | PASSA |  |
| snapshot: Content-Encoding gzip com Accept-Encoding gzip | PASSA |  |
| snapshot: ami_connected=true e technology correta | PASSA | ami=true tech=pjsip |
| snapshot: 10000 ramais (sem troncos) | PASSA | 10000 ramais, esperado 10000 |
| snapshot ocioso == verdade do simulador (registro, dispositivo, grupo) | PASSA | 0 ramais divergentes de 10000 |
| memoria sem crescimento continuo | **NAO CONCLUSIVO no Windows** | 0.37 MiB/min em 582.0 s de regime (exige 30 min em Linux) |

### Capturas reproduzidas

| Criterio | Resultado | Medido |
|---|---|---|
| "com quem fala" correto em cada ponto de cada captura (sinteticas) | PASSA | 226/226 pontos em 10 capturas |
| log do simulador (reproducao) so com actions da allowlist | PASSA | violacoes=0 |

## Latencia evento -> delta (WebSocket)

Da gravacao do evento AMI no socket do simulador ate o ramal aparecer no estado entregue pelo WebSocket com o estado esperado (ou um posterior, se coalescido). Lote de 250 ms do daemon incluido.

| Fase | sip | pjsip |
|---|---|---|
| isolada p50 | 129 ms | 145 ms |
| isolada p99 | 236 ms | 245 ms |
| isolada max | 238 ms | 251 ms |
| isolada amostras | 124 | 124 |
| carga p50 | 128 ms | 126 ms |
| carga p99 | 250 ms | 250 ms |
| carga max | 453 ms | 453 ms |
| carga amostras | 47213 | 46885 |
| carga-pos p50 | 126 ms | 127 ms |
| carga-pos p99 | 250 ms | 251 ms |
| carga-pos max | 253 ms | 253 ms |
| carga-pos amostras | 8592 | 8684 |
| rajada p50 | 78 ms | 240 ms |
| rajada p99 | 238 ms | 245 ms |
| rajada max | 259 ms | 263 ms |
| rajada amostras | 3232 | 3210 |
| restauracao p50 | 19 ms | 181 ms |
| restauracao p99 | 256 ms | 204 ms |
| restauracao max | 256 ms | 242 ms |
| restauracao amostras | 3262 | 3182 |
| queda p50 | 131 ms | 125 ms |
| queda p99 | 253 ms | 280 ms |
| queda max | 428 ms | 427 ms |
| queda amostras | 327 | 344 |
| **todas (exceto queda) p50** | 116 ms | 140 ms |
| **todas (exceto queda) p99** | 252 ms | 250 ms |

Fases: `isolada` = um evento por vez (cenarios passo a passo); `carga` = regime com 400 chamadas; `rajada` = 3000 desregistros de uma vez; `restauracao` = os mesmos reregistrando; `queda` = o que chegou na janela depois da volta do AMI (inclui o tempo de reconexao e de ressincronizacao; fora dos criterios).

## Snapshot (GET /api/snapshot)

| Medida | sip | pjsip |
|---|---|---|
| ocioso: ramais / chamadas | 10000 / 0 | 10000 / 0 |
| ocioso: tamanho cru | 1829.2 KiB | 2018.6 KiB |
| ocioso: tamanho com gzip | 70.0 KiB | 86.3 KiB |
| ocioso: tempo da requisicao (sem gzip) | 2 ms | 3 ms |
| sob carga: ramais / chamadas | 10000 / 592 | 10000 / 595 |
| sob carga: tamanho cru | 1952.8 KiB | 2143.1 KiB |
| sob carga: tamanho com gzip | 94.2 KiB | 111.4 KiB |
| sob carga: tempo da requisicao (sem gzip) | 8 ms | 7 ms |
| ramais com `name` preenchido (ocioso) | 2 de 10000 | 1 de 10000 |
| ramais com `user_agent` preenchido (ocioso) | 0 de 10000 | 9249 de 10000 |
| ramais com `name` preenchido (no fim do teste) | 884 de 10000 | 893 de 10000 |
| ramais com `user_agent` preenchido (no fim do teste) | 0 de 10000 | 9249 de 10000 |

## Deltas no WebSocket

| Fase / medida | sip | pjsip |
|---|---|---|
| carga: deltas recebidos | 1967 | 1967 |
| carga: tamanho medio | 9156 B | 9669 B |
| carga: p50 / p99 / max | 9007 / 15741 / 21067 B | 9569 / 16814 / 19993 B |
| carga: ramais por delta (media) | 27.7 | 27.6 |
| carga: bytes por ramal | 330 B | 351 B |
| carga: vazao no WS | 35.8 KiB/s | 37.8 KiB/s |
| carga-pos: deltas recebidos | 361 | 361 |
| carga-pos: tamanho medio | 8395 B | 9047 B |
| carga-pos: p50 / p99 / max | 8223 / 15473 / 17472 B | 8719 / 17093 / 18820 B |
| carga-pos: ramais por delta (media) | 24.0 | 24.4 |
| carga-pos: bytes por ramal | 349 B | 371 B |
| carga-pos: vazao no WS | 32.8 KiB/s | 35.3 KiB/s |
| rajada: deltas recebidos | 10 | 11 |
| rajada: tamanho medio | 70200 B | 64256 B |
| rajada: p50 / p99 / max | 9486 / 11329 / 623257 B | 8037 / 9829 / 628998 B |
| rajada: ramais por delta (media) | 323.6 | 293.8 |
| rajada: bytes por ramal | 217 B | 219 B |
| rajada: vazao no WS | 287.2 KiB/s | 272.8 KiB/s |
| restauracao: deltas recebidos | 10 | 9 |
| restauracao: tamanho medio | 71643 B | 83666 B |
| restauracao: p50 / p99 / max | 9499 / 102757 / 536977 B | 6790 / 14201 / 688159 B |
| restauracao: ramais por delta (media) | 326.6 | 354.0 |
| restauracao: bytes por ramal | 219 B | 236 B |
| restauracao: vazao no WS | 282.0 KiB/s | 326.2 KiB/s |

## Preenchimento de nomes em segundo plano (20 consultas/s, `ami.name_fill_per_second`)

O daemon pergunta o callerid de cada ramal (`PJSIPShowEndpoint` ou `SIPshowpeer`) depois do snapshot, sem rajada. Aqui o daemon comecou com o banco vazio (sem cache de nomes), e a carga de chamadas e as demais medidas rodaram enquanto os nomes eram preenchidos.

| Medida | sip | pjsip |
|---|---|---|
| nomes preenchidos no inicio do regime sob carga | 603 de 10000 | 607 de 10000 |
| nomes preenchidos e corretos ao fim do teste | 10000 de 10000 | 10000 de 10000 |
| preenchimento completo | true | true |
| tempo do daemon pronto ate o ultimo nome | 543.0 s | 541.9 s |
| taxa medida (nomes/s, entre as amostras) | 19.1 | 19.1 |
| duracao do regime sob carga (preenchimento + regime com nomes completos) | 582.1 s | 582.2 s |
| latencia evento->delta durante o preenchimento: p50 / p99 / max | 128 ms / 250 ms / 453 ms (n=47213) | 126 ms / 250 ms / 453 ms (n=46885) |
| latencia evento->delta depois do preenchimento: p50 / p99 / max | 126 ms / 250 ms / 253 ms (n=8592) | 127 ms / 251 ms / 253 ms (n=8684) |
| CPU do daemon durante / depois do preenchimento (% de 1 nucleo, Windows) | 0.7% / 0.6% | 1.8% / 2.7% |
| RSS ao fim do preenchimento / no fim do regime (MiB, Windows) | 61.5 / 64.6 | 75.2 / 75.4 |

Progresso (sip), nomes preenchidos por tempo desde o daemon pronto: 51.3 s=603; 101.4 s=2130; 151.6 s=3477; 201.8 s=4664; 252.0 s=5775; 302.1 s=6733; 352.3 s=7578; 402.5 s=8321; 452.7 s=9020; 502.9 s=9652; 553.0 s=10000; 603.2 s=10000; 633.3 s=10000.

Progresso (pjsip), nomes preenchidos por tempo desde o daemon pronto: 50.0 s=607; 100.2 s=2137; 150.4 s=3497; 200.6 s=4690; 250.8 s=5792; 300.9 s=6748; 351.1 s=7603; 401.3 s=8358; 451.5 s=9037; 501.7 s=9644; 551.9 s=10000; 602.1 s=10000; 632.2 s=10000.

## "Com quem fala" sobre capturas reproduzidas (`amisim -replay`)

Cada captura de `testdata/capturas` e reproduzida pelo simulador (respostas na ordem de fila, eventos no tempo gravado dividido por 20) para o daemon real, que roda em `technology: auto`. Em cada ponto do arquivo o estado do cliente WebSocket tem de igualar o oraculo (`.esperado.json`): registro, dispositivo, nome e, por chamada, id, direcao, estado e com quem fala. **Todas as capturas atuais sao sinteticas** (ver `testdata/capturas/README.md`); capturas reais virao da homologacao.

| Captura | Tecnologia | Pontos ok / total | Actions fora da allowlist | Erro |
|---|---|---|---|---|
| sintetica-entrada-pjsip | pjsip | 5 / 5 | [] | nenhum |
| sintetica-entrada-sip | sip | 5 / 5 | [] | nenhum |
| sintetica-interna-pjsip | pjsip | 16 / 16 | [] | nenhum |
| sintetica-interna-sip | sip | 16 / 16 | [] | nenhum |
| sintetica-local-pjsip | pjsip | 11 / 11 | [] | nenhum |
| sintetica-local-sip | sip | 11 / 11 | [] | nenhum |
| sintetica-saida-pjsip | pjsip | 5 / 5 | [] | nenhum |
| sintetica-saida-sip | sip | 5 / 5 | [] | nenhum |
| sintetica-transferencia-pjsip | pjsip | 76 / 76 | [] | nenhum |
| sintetica-transferencia-sip | sip | 76 / 76 | [] | nenhum |
| **total** | | **226 / 226** | | |


## Rajada de desregistros e queda do AMI

| Medida | sip | pjsip |
|---|---|---|
| rajada: ramais desregistrados | 3000 | 3000 |
| rajada: tempo para o simulador emitir tudo | 9 ms | 11 ms |
| rajada: tempo ate o ultimo ramal aparecer 'unregistered' no cliente | 98 ms | 257 ms |
| restauracao: tempo ate todos 'registered' no cliente | 273 ms | 204 ms |
| queda: daemon acusou ami.connected=false em | 0.53 ms | 0.86 ms |
| queda: snapshots lidos com AMI fora | 16 | 17 |
| queda: ramais 'unregistered' (maximo visto) | 0 | 0 |
| queda: ramais 'unknown' (minimo visto) | 10000 | 10000 |
| queda: chamadas visiveis (maximo) | 0 | 0 |
| queda: daemon reconectou apos a volta do AMI em | 127 ms | 304 ms |
| queda: reloads recebidos / codigos de fechamento | [epoch_changed epoch_changed] / [4000 4000] | [epoch_changed epoch_changed] / [4000 4000] |
| queda: snapshots com ami_connected=false e ramais ainda com estado antigo | 0 | 0 |
| queda: epoch novo publicado no /api/health apos o AMI cair (resolucao 50 ms) | 291 ms | 307 ms |
| queda: 1o snapshot todo 'unknown' (limite superior: cada leitura demora ~2 requisicoes) | 474 ms | 437 ms |
| queda: snapshot passou a ami_connected=false apos o health acusar (snapshots velhos antes disso) | 474 ms (2 snapshots antes) | 437 ms (2 snapshots antes) |
| queda: erro | nenhum | nenhum |

Snapshots lidos durante a queda (sip), a cada ~200 ms desde a primeira deteccao:

| t | registered | unregistered | unknown | chamadas | epoch |
|---|---|---|---|---|---|
| 474 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 710 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 943 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 1177 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 1423 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 1657 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 1888 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 2123 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 2357 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 2602 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 2838 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 3074 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 3308 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 3543 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 3787 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |
| 4023 ms | 0 | 0 | 10000 | 0 | 1791933971602-10 |

Snapshots lidos durante a queda (pjsip), a cada ~200 ms desde a primeira deteccao:

| t | registered | unregistered | unknown | chamadas | epoch |
|---|---|---|---|---|---|
| 437 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 673 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 910 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 1146 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 1394 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 1630 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 1868 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 2104 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 2341 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 2588 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 2823 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 3061 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 3296 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 3534 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 3781 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 4017 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |
| 4253 ms | 0 | 0 | 10000 | 0 | 1791934653515-10 |

## Cenarios de chamada: "com quem fala"

Cada passo do cenario muda a verdade no simulador, e o teste espera o estado do cliente WebSocket igualar a verdade (registro, dispositivo, e por chamada: id, direcao, estado, outro lado). Passos: 16 cenarios.

| Cenario (passos ok / total) | sip | pjsip |
|---|---|---|
| internal | 4 / 4 | 4 / 4 |
| internal-hold | 6 / 6 | 6 / 6 |
| internal-noanswer | 3 / 3 | 3 / 3 |
| local-internal | 5 / 5 | 5 / 5 |
| inbound | 4 / 4 | 4 / 4 |
| local-inbound | 5 / 5 | 5 / 5 |
| outbound | 4 / 4 | 4 / 4 |
| blind-internal | 7 / 7 | 7 / 7 |
| blind-inbound | 7 / 7 | 7 / 7 |
| blind-outbound | 7 / 7 | 7 / 7 |
| attended-merge-internal | 9 / 9 | 9 / 9 |
| attended-merge-inbound | 9 / 9 | 9 / 9 |
| attended-merge-outbound | 9 / 9 | 9 / 9 |
| attended-link-internal | 9 / 9 | 9 / 9 |
| attended-link-inbound | 9 / 9 | 9 / 9 |
| attended-link-outbound | 9 / 9 | 9 / 9 |


| Convergencia por passo (isolado) | sip | pjsip |
|---|---|---|
| p50 | 95 ms | 127 ms |
| p99 | 244 ms | 254 ms |

## Snapshot com chamadas em andamento (reconexao do AMI)

Todos os cenarios parados no mesmo ponto, conexao AMI derrubada, o daemon reconecta e refaz o snapshot (`CoreShowChannels` + `DeviceStateList`); o estado restaurado e comparado com a verdade. `hold` nao e comparado como `hold`: o `CoreShowChannels` nao informa espera (limite do protocolo AMI).

| Cenario / rodada | sip | pjsip |
|---|---|---|
| internal / tocando | ok | ok |
| internal / atendida | ok | ok |
| internal / apos transferencia/hold/otimizacao | ok | ok |
| internal-hold / tocando | ok | ok |
| internal-hold / atendida | ok | ok |
| internal-hold / apos transferencia/hold/otimizacao | ok | ok |
| internal-noanswer / tocando | ok | ok |
| internal-noanswer / atendida | ok | ok |
| internal-noanswer / apos transferencia/hold/otimizacao | ok | ok |
| local-internal / tocando | ok | ok |
| local-internal / atendida | ok | ok |
| local-internal / apos transferencia/hold/otimizacao | ok | ok |
| inbound / tocando | ok | ok |
| inbound / atendida | ok | ok |
| inbound / apos transferencia/hold/otimizacao | ok | ok |
| local-inbound / tocando | ok | ok |
| local-inbound / atendida | ok | ok |
| local-inbound / apos transferencia/hold/otimizacao | ok | ok |
| outbound / tocando | ok | ok |
| outbound / atendida | ok | ok |
| outbound / apos transferencia/hold/otimizacao | ok | ok |
| blind-internal / tocando | ok | ok |
| blind-internal / atendida | ok | ok |
| blind-internal / apos transferencia/hold/otimizacao | ok | ok |
| blind-inbound / tocando | ok | ok |
| blind-inbound / atendida | ok | ok |
| blind-inbound / apos transferencia/hold/otimizacao | ok | ok |
| blind-outbound / tocando | ok | ok |
| blind-outbound / atendida | ok | ok |
| blind-outbound / apos transferencia/hold/otimizacao | ok | ok |
| attended-merge-internal / tocando | ok | ok |
| attended-merge-internal / atendida | ok | ok |
| attended-merge-internal / apos transferencia/hold/otimizacao | ok | ok |
| attended-merge-inbound / tocando | ok | ok |
| attended-merge-inbound / atendida | ok | ok |
| attended-merge-inbound / apos transferencia/hold/otimizacao | ok | ok |
| attended-merge-outbound / tocando | ok | ok |
| attended-merge-outbound / atendida | ok | ok |
| attended-merge-outbound / apos transferencia/hold/otimizacao | ok | ok |
| attended-link-internal / tocando | ok | ok |
| attended-link-internal / atendida | ok | ok |
| attended-link-internal / apos transferencia/hold/otimizacao | ok | ok |
| attended-link-inbound / tocando | ok | ok |
| attended-link-inbound / atendida | ok | ok |
| attended-link-inbound / apos transferencia/hold/otimizacao | ok | ok |
| attended-link-outbound / tocando | ok | ok |
| attended-link-outbound / atendida | ok | ok |
| attended-link-outbound / apos transferencia/hold/otimizacao | ok | ok |

## Estado final x verdade do simulador

| Medida | sip | pjsip |
|---|---|---|
| ramais comparados | 10000 | 10000 |
| divergentes no snapshot | 0 | 0 |
| divergentes no estado do cliente WS | 0 | 0 |
| chamadas restantes no fim (deve ser 0) | 0 | 0 |
| lacunas de versao no WS | 0 | 0 |
| estados fugazes so com dispositivo/chamadas de instantes diferentes (aceitos) | 6 | 3 |
| estados fugazes errados (fora da janela de ressincronizacao) | 4 | 3 |
| estados inesperados dentro da janela de 3 s apos ressincronizar | 8 | 6 |
| estados inesperados herdados de chamadas restauradas por snapshot (ver secao anterior) | 0 | 0 |
| mutacoes sem estado correspondente visto ate o fim | 0 | 0 |

- (sip) inesperado: [fase carga] ramal 10353: daemon mostrou reg=registered dev=inuse calls=[1791933971.12245 internal talking unknown:]; pendentes=1; verdade esperada=base[1791933971.12245 inbound talking external:11909461356] | not_inuse
- (sip) inesperado: [fase carga] ramal 3456: daemon mostrou reg=registered dev=inuse calls=[1791933971.32296 internal talking unknown:8515]; pendentes=1; verdade esperada=base[1791933971.32296 internal talking extension:8515] | inuse[1791933971.32296 internal talking extension:8108]
- (sip) inesperado: [fase carga] ramal 8515: daemon mostrou reg=registered dev=inuse calls=[1791933971.32899 internal talking extension:8108]; pendentes=2; verdade esperada=base[1791933971.32298 internal hold extension:3456][1791933971.32899 internal ringing extension:8108] | inuse[1791933971.32298 internal hold extension:3456][1791933971.32899 internal talking extension:8108] | not_inuse
- (sip) inesperado: [fase carga-pos] ramal 9551: daemon mostrou reg=registered dev=not_inuse calls=[1791933971.45766 internal ringing unknown:2926]; pendentes=1; verdade esperada=base | inuse[1791933971.45766 internal ringing extension:2926]
- (pjsip) inesperado: [fase carga] ramal 11337: daemon mostrou reg=registered dev=inuse calls=[1791934653.22568 internal talking unknown:]; pendentes=1; verdade esperada=base[1791934653.22568 inbound talking external:11940649906] | not_inuse
- (pjsip) inesperado: [fase carga] ramal 5578: daemon mostrou reg=registered dev=inuse calls=[1791934653.28047 internal talking unknown:]; pendentes=2; verdade esperada=base[1791934653.28047 internal ringing extension:10837] | inuse[1791934653.28047 internal talking extension:10837] | inuse[1791934653.28047 internal talking extension:7973]
- (pjsip) inesperado: [fase carga] ramal 7973: daemon mostrou reg=registered dev=inuse calls=[1791934653.26711 internal talking unknown:10837]; pendentes=1; verdade esperada=base[1791934653.26711 internal talking extension:10837] | inuse[1791934653.26711 internal talking extension:5578]

## Actions recebidas pelo simulador (log do endpoint HTTP exclusivo `/actions`)

| Action | sip | pjsip |
|---|---|---|
| CoreSettings | 5 | 5 |
| CoreShowChannels | 5 | 5 |
| DeviceStateList | 5 | 5 |
| Login | 5 | 5 |
| ModuleCheck | 10 | 10 |
| PJSIPShowContacts | 0 | 5 |
| PJSIPShowEndpoint | 0 | 11314 |
| PJSIPShowEndpoints | 0 | 5 |
| Ping | 21 | 21 |
| SIPpeers | 5 | 0 |
| SIPshowpeer | 11342 | 0 |
| **fora da allowlist** | [] | [] |

## CPU e memoria do daemon (Windows)

> **Medido no Windows, nao em Linux.** Working set e tempo de CPU lidos do processo do daemon (build para windows). Valem como ordem de grandeza e para deteccao de vazamento grosseiro; o criterio de memoria da especificacao (sem crescimento continuo em 30 min) e a medicao de CPU so valem em Linux (WSL2, container ou Rocky), com `-duration 30m`.

| Medida | sip | pjsip |
|---|---|---|
| amostras de RSS (a cada 2 s) | 340 | 340 |
| RSS logo apos subir (MiB) | 49.6 | 76.1 |
| RSS apos snapshot/cenarios ociosos (MiB) | 46.9 | 76.3 |
| RSS no inicio do regime (MiB) | 54.6 | 67.9 |
| RSS no fim do regime (MiB) | 64.6 | 75.4 |
| pico de RSS (MiB) | 89.1 | 110.7 |
| inclinacao do RSS no regime (MiB/min, sem o primeiro terco) | 0.79 em 580.0 s | 0.37 em 582.0 s |
| CPU do daemon no regime inteiro (% de 1 nucleo) | 0.7% | 1.9% |
| CPU durante o preenchimento de nomes (% de 1 nucleo) | 0.7% | 1.8% |
| CPU depois do preenchimento, nomes completos (% de 1 nucleo) | 0.6% | 2.7% |
| RSS quando o preenchimento terminou (MiB) | 61.5 | 75.2 |
| erro de leitura de processo | nenhum | nenhum |

## /api/health durante o teste

| Medida | sip | pjsip |
|---|---|---|
| amostras | 170 | 170 |
| eventos/s (maximo da media de 60 s) | 2183.8 | 2189.3 |
| fila interna (maximo) | 12 | 13 |
| lag de processamento (maximo) | 1 ms | 0 ms |
| estouros de fila | 0 | 0 |
| reconexoes AMI | 4 | 4 |
| status / dependencias na ultima amostra | degraded / ami_user=ok, manager_custom_include=fail, apache_proxy_conf=fail | degraded / ami_user=ok, manager_custom_include=fail, apache_proxy_conf=fail |
| Asterisk lido pelo daemon | 18.20.0 | 18.20.0 |
| tecnologia detectada em `auto` | sip | pjsip |
| tempo ate o daemon ficar pronto (10.000 ramais) | 648 ms | 647 ms |

## O que so vale em Linux

- Memoria: o criterio "sem crescimento continuo" pede 30 min de carga em Linux; aqui foi uma execucao curta no Windows e a inclinacao acima nao e conclusiva.
- CPU do daemon: medida pelo tempo de CPU do processo; o agendador e o custo de syscalls do Windows diferem do Linux. Refazer em Linux.
- `rpm -i/-U/-e`, systemd, SELinux, Apache e `instalar.py`: fora do alcance deste teste (criterios C5, C6, A1 a A9).
- Reproduzir em Linux (WSL2/container/Rocky), com o simulador e o cliente no mesmo processo:

```
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o painel-ramais ./cmd/painel-ramais
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o loadtest ./cmd/loadtest
./loadtest -daemon ./painel-ramais -duration 30m -out docs/relatorio-carga-linux.md
```

## Notas do oraculo

- (sip) `name` vem de `PJSIPShowEndpoint` (pjsip) ou `SIPshowpeer` (sip), em segundo plano e com taxa limitada (20/s configurados): 11342 consultas no teste inteiro. O snapshot ocioso foi lido logo depois de o daemon subir e por isso mostra preenchimento parcial; veja a secao do preenchimento de nomes.
- (pjsip) `name` vem de `PJSIPShowEndpoint` (pjsip) ou `SIPshowpeer` (sip), em segundo plano e com taxa limitada (20/s configurados): 11314 consultas no teste inteiro. O snapshot ocioso foi lido logo depois de o daemon subir e por isso mostra preenchimento parcial; veja a secao do preenchimento de nomes.
- (sip) `/api/health` ficou `degraded` por `deps` em `fail` (ami_user=ok, manager_custom_include=fail, apache_proxy_conf=fail): esperado fora de um Issabel (nao existem manager.conf nem conf do Apache nesta maquina). Nao afeta o resultado do teste.
- (pjsip) `/api/health` ficou `degraded` por `deps` em `fail` (ami_user=ok, manager_custom_include=fail, apache_proxy_conf=fail): esperado fora de um Issabel (nao existem manager.conf nem conf do Apache nesta maquina). Nao afeta o resultado do teste.
- Duracao total por modo: sip 681.9 s; pjsip 680.9 s; 
