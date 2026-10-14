## ADR-001: Protocolo WebSocket (hello, epoch/version, ring buffer, cliente lento)

**Data**: 2026-10-13
**Status**: Aceito
**Porta**: 2 vias (reversível). O único consumidor é a tela da fase 2, no mesmo repositório; mudar o protocolo custa alterar cliente e servidor juntos, sem cliente externo.

### Contexto

A especificação (`docs/PROMPT-FASE-1.md`, seção API) pede `GET /ws` com deltas agrupados a cada 250 ms, coalescidos por ramal, e que o cliente informe a versão que tem: havendo lacuna ou epoch novo, o servidor manda recarregar o snapshot. Cenário de referência: 10.000 ramais, rajada de 3.000 desregistros de uma vez, p99 evento até delta abaixo de 1 s, queda do AMI no meio da carga, acesso via Apache com mod_proxy_wstunnel. Clientes esperados: dezenas de navegadores (operadores e TVs de NOC), não milhares.

O problema real é manter todos os navegadores coerentes com o estado do daemon sem que (a) um cliente lento atrase os outros, (b) um piscar de rede force todo mundo a baixar o snapshot inteiro, (c) exista janela em que o cliente aplica delta sobre base errada.

### Decisão

1. **Hub único no pacote `api`.** Uma goroutine é dona de: a projeção pública do estado (mapa `number -> Extension`), o ring buffer, a lista de clientes e o cache do snapshot (JSON e gzip da versão atual). Tudo que mexe nessas estruturas passa por ela, então "registrar cliente + calcular replay" e "chegou lote novo" nunca se cruzam.
2. **O `state` produz, o hub distribui.** O `state` emite `Batch` a cada 250 ms quando houve mudança (ramal coalescido, estado final completo) e `Reset` a cada epoch novo (contrato em `docs/CONTRATOS.md`, seção 11.4). O hub serializa cada lote uma vez e envia os mesmos bytes a todos.
3. **Snapshot servido pela projeção do hub**, não pelo `state`. Assim `version` do snapshot é sempre a fronteira do último lote distribuído e nunca fica à frente do ring buffer. O JSON e o gzip ficam em cache até o próximo lote: 30 clientes recarregando juntos custam uma serialização.
4. **Epoch** é string opaca `<unix ms do início do processo>-<contador>`, única entre reinícios. Muda em toda mudança em massa: subida do daemon, queda do AMI (tudo vira `unknown`), fim de sincronização (reconexão ou estouro da fila interna) e recarga de grupos por SIGHUP. Mudança de um ramal nunca muda o epoch. **Version** é inteiro monotônico na vida do processo.
5. **Delta = estado completo do ramal**, não patch de campo. Aplicar um lote cujo intervalo se sobrepõe à versão local é idempotente. Regra do cliente: aplica se `from_version <= v < to_version`; ignora se `to_version <= v`; lacuna se `from_version > v`.
6. **hello** é a primeira e única mensagem do cliente, em até 5 s (`{"type":"hello","epoch":...,"version":...}`). Decisão do hub, com `E` = epoch atual, `C` = versão atual, `R` = ring:

   | Condição | Resposta |
   |---|---|
   | epoch do hello diferente de `E` | `reload` com `epoch_changed`, fecha 4000 |
   | `version == C` | nada a repor, segue ao vivo |
   | `version > C` | `reload` com `version_ahead`, fecha 4000 |
   | `version < C` e `R[0].from_version <= version` | envia os lotes com `to_version > version`, segue ao vivo |
   | `version < C` e ring vazio ou mais novo que a versão | `reload` com `version_gap`, fecha 4000 |

7. **Ring buffer de 40 lotes** (cerca de 10 s com tráfego contínuo a 250 ms; em período calmo cobre mais tempo, o que só ajuda). Guarda os bytes já serializados. É esvaziado em todo `Reset`.
8. **Epoch novo**: o hub troca a projeção, esvazia o ring, invalida o cache do snapshot e manda `reload` com `epoch_changed` a todos os clientes, fechando com 4000. O cliente busca o snapshot e reconecta com hello.
9. **Broadcast nunca bloqueia.** Cada cliente tem fila de envio com 64 mensagens e uma goroutine escritora com timeout de 10 s por escrita. O hub envia com `select { case fila <- msg: default: desconecta }`. Fila cheia ou escrita estourada: o cliente é desconectado com 4002. Ele volta com hello e se recupera por replay ou reload, sem atrasar ninguém.
10. **Sessão**: validada no upgrade (sem sessão, HTTP 401 antes do upgrade). Um timer fecha a conexão com 4001 no `expires_at` da sessão. No SIGHUP (mudança de usuário pela CLI), o hub revalida a sessão de cada conexão e fecha com 4001 as que sumiram.
11. **Origem**: `github.com/coder/websocket` com a checagem padrão de Origin (host do Origin igual ao Host). O Apache usa `ProxyPreserveHost On` só dentro do `<Location "/painel-ramais/">` (ADR-003), então o Host que chega ao daemon é o público. Proibido `InsecureSkipVerify`.
12. **Leitura do cliente**: limite de 1 KiB; depois do hello, `CloseRead` (descarta e trata frames de controle). Mensagem de dados extra do cliente fecha com 4003.
13. **Heartbeat**: ping de controle a cada 25 s, abaixo do `Timeout` do Apache.
14. **Compressão do WebSocket desligada** na fase 1. O tamanho médio de delta é medido no teste de carga; ligar `permessage-deflate` só se o número justificar.

Códigos de fechamento e exemplos JSON: `docs/CONTRATOS.md`, seção 5.

### Alternativas consideradas

- **A (mais simples possível): sem ring buffer, toda reconexão recarrega o snapshot.** Descartada. Um restart do Apache ou um piscar de Wi-Fi faria todos os clientes baixarem o snapshot ao mesmo tempo (estimativa: 2 a 3 MB cru, 200 a 400 KB com gzip para 10.000 ramais), e a especificação já descreve o hello com versão para evitar isso. O ring custa 40 ponteiros para bytes já serializados.
- **B: Server-Sent Events.** Teria retomada nativa por `Last-Event-ID`. Descartada porque a especificação fixa `/ws` e WebSocket; a diferença de complexidade aqui é pequena.
- **C: delta como patch de campo (JSON Patch ou similar).** Descartada. O ramal completo tem cerca de 300 bytes; o patch economiza pouco e torna a aplicação sensível à ordem, quebrando a idempotência que permite a sobreposição de intervalos.
- **D: broadcast bloqueante com timeout por cliente.** Descartada. Um cliente travado seguraria o lote de todos pelo tempo do timeout e estouraria o p99 de 1 s.
- **E: snapshot servido direto pelo `state`.** Descartada. O snapshot poderia sair numa versão cujo lote ainda não chegou ao hub; o hello seguinte cairia em `version_ahead` e geraria reload falso.
- **F: queda do AMI como delta (10.000 ramais para `unknown`) em vez de epoch novo.** Descartada. Seria um lote de 2 a 3 MB, e o cliente teria o mesmo trabalho de um reload. Tratar como mudança em massa mantém uma regra só.
- **G: versão por epoch reiniciando em 0.** Descartada. Versão monotônica na vida do processo deixa `version_ahead` detectável e não custa nada.

### Suposições feitas

- Dezenas de clientes simultâneos. Com centenas, o custo dominante continua sendo uma serialização por lote, mas a fila por cliente (64 mensagens) e o reload em massa no epoch novo precisam ser remedidos.
- `Timeout` do Apache do Issabel maior que 25 s (padrão do httpd é 60 s). Confirmar no relatório do `instalar.py verificar`.
- O navegador responde ping de controle sozinho (comportamento padrão dos navegadores).

### Consequências

- Positivas: cliente lento nunca atrasa os outros; reconexão curta não baixa snapshot; uma regra única para mudança em massa (epoch) e para mudança pontual (delta); cliente simples (substitui por `number`, compara epoch, compara intervalo).
- Negativas / trade-offs: a projeção no hub duplica o estado público (estimativa de poucos MB para 10.000 ramais); toda recarga de grupos e toda queda do AMI fazem todos os clientes baixarem o snapshot (aceitável: são eventos raros); 64 mensagens de fila podem ser pouco se o teste de carga mostrar rajadas longas, e aí o ajuste é de constante, não de desenho.

### Critério de validação

Medido pelo `cmd/loadtest` contra o `cmd/amisim`, nos modos sip e pjsip, com 10.000 ramais e a rajada de 3.000 desregistros:

1. p99 de evento AMI até delta no cliente abaixo de 1 s.
2. Cliente que pausa a leitura por 5 s e reconecta com o hello recebe replay (nenhum `reload`) e termina com estado igual ao snapshot.
3. Cliente que para de ler por 30 s é fechado com 4002, e o p99 dos demais clientes no mesmo intervalo continua abaixo de 1 s.
4. AMI derrubado no meio da carga: todos os clientes recebem `reload` com epoch novo; no snapshot seguinte nenhum ramal está `unregistered` por causa da queda (todos `unknown`); na reconexão, novo `reload` e estado completo.
5. Estado final do cliente (snapshot inicial mais todos os deltas aplicados) igual ao snapshot pedido no fim do teste, ramal por ramal.
6. Memória do daemon sem crescimento contínuo durante 30 min de carga (medida em Linux).

### Agentes impactados

backend (hub, state, api), frontend (fase 2: cliente do protocolo), qa (teste de carga e casos 1 a 6), seguranca (checagem de Origin e sessão no upgrade).
