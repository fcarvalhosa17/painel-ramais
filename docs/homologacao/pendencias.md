# Pendencias de homologacao

Decisoes ja tomadas que ficam para depois da analise do pacote de homologacao.
A rc.2 e a versao em homologacao: nada aqui altera codigo nem gera rc nova agora.

## Pendencia: outro lado sem ramal em conversa (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`.

**Dependencia:** pacote `homologacao-*.tar.gz` coletado no Issabel e colocado em
`docs/homologacao/entrada/`. A captura real e que confirma como o Asterisk do Issabel entrega os
canais Local e os de tronco (se o tronco chega com `Endpoint` preenchido ou vazio, e como o par
Local `;1`/`;2` aparece nos eventos).

### Ponto a mudar

`describe` em `internal/state/calls.go` (linhas 249-250 em 4f43a68):

```go
case peer.endpoint == "":
	return model.DirInternal, model.Peer{Kind: model.PeerUnknown, Number: model.StrPtr(first(peer.callerNum, dialedNumber(c)))}
```

Com `peer != nil` e `peer.endpoint == ""` o resultado e `PeerUnknown` sem passar por `logNoPeer`
(nenhum log). Hoje esse ramo junta dois casos diferentes: o outro lado externo cujo canal chegou sem
`Endpoint` e o canal Local que nao foi resolvido ate um ramal ou ate a origem.

### Decisao do usuario

1. **Tronco ou numero externo:** o outro lado e conhecido, so que externo. Exibir como externo
   (`PeerExternal`) com o numero, nunca como desconhecido. Sem log.
2. **Canal Local sem resolucao (fila, ring group):** e falha de correlacao. Seguir o par Local
   `;1`/`;2` ate o ramal real ou ate a origem. Se nao resolver, exibir desconhecido e gravar log
   UMA vez por canal, com Uniqueid e Linkedid, no mesmo formato da protecao de 1 s: mensagem
   "chamada em conversa sem outro lado resolvido" com `uniqueid=` e `linkedid=`, pelo mesmo caminho
   `logNoPeer` (`internal/state/calls.go`) -> `onNoPeer` (`cmd/painel-ramais/serve.go`).

### Criterio de aceite

- Teste com o outro lado em tronco (inclusive canal de tronco sem `Endpoint`, se a captura mostrar
  esse caso): a chamada sai como externo com o numero; nenhum log "sem outro lado resolvido".
- Teste com Local irresolvido (fila ou ring group sem ramal atendendo): a chamada sai como
  desconhecido e o log aparece exatamente 1 vez por canal, com `uniqueid` e `linkedid`, mesmo com
  varias reavaliacoes do mesmo canal.
- Teste com Local que resolve ate um ramal: exibe o ramal, sem log.
- Os testes atuais da protecao de 1 s continuam passando sem alteracao.

## Pendencia: endpoint de debug do heap fora da release final (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Dependencia:** pacote `homologacao-*.tar.gz` coletado no Issabel, colocado em
`docs/homologacao/entrada/` e analisado.

### Ponto a mudar

O endpoint de debug do heap so servia aos testes de carga (workflows removidos em c93b7dc), e o
`instalar.py status` ja le o heap pelo `metrics.json` (`instalar.py:868` `METRICAS`,
`instalar.py:884-910` `memoria_do_daemon`). O que existe hoje (linhas em ed871a6):

- `cmd/painel-ramais/debug.go` (arquivo inteiro): `envDebugAddr = "PAINEL_DEBUG_ADDR"` (linha 20),
  `debugHandler` com `/debug/mem` (linha 26) e `/debug/pprof/heap` via `net/http/pprof`
  (linha 36), `debugListener` (linha 41) e `startDebug` (linha 53).
- `cmd/painel-ramais/debug_test.go` (arquivo inteiro): `TestDebugListenerSoLoopback` (linha 10) e
  `TestDebugHandler` (linha 24; rotas nas linhas 27, 32 e 37).
- `cmd/painel-ramais/serve.go:155`: `startDebug(ctx, d.log)`, a linha que liga o endpoint.
- Variavel `PAINEL_DEBUG_ADDR`: so lida em `debug.go`; nenhuma unit systemd, README, LEIAME, doc ou
  script a define ou cita (conferido com busca no repositorio inteiro).
- `cmd/loadtest` (`loadtest -heap`), que perde a fonte do heap:
  - `cmd/loadtest/main.go:39` flag `-heap`; `cmd/loadtest/run.go:36` campo `heap` (comentario cita
    `PAINEL_DEBUG_ADDR`);
  - `cmd/loadtest/client.go:90` campo `dbg`; `cmd/loadtest/client.go:153-157` define
    `PAINEL_DEBUG_ADDR` no ambiente do daemon;
  - `cmd/loadtest/heap.go:16` comentario; `cmd/loadtest/heap.go:109` e `:115` `GET /debug/mem`
    (`HeapStats`); `cmd/loadtest/heap.go:129` e `:135` `GET /debug/pprof/heap?gc=1` (`HeapProfile`);
  - `cmd/loadtest/regime_test.go:23` e `:30` daemon falso com `/debug/mem` e `/debug/pprof/heap`.

### Decisao do usuario

1. Remover o endpoint do build de release: codigo (`debug.go`, `debug_test.go`, a chamada em
   `serve.go:155`) e configuracao (`PAINEL_DEBUG_ADDR`). O `net/http/pprof` sai do binario
   distribuido junto.
2. Se for preciso manter para desenvolvimento, que seja por build tag (ex.: `//go:build debug`),
   fora do binario distribuido; o build de release nao passa a tag.
3. `loadtest -heap`: se o loadtest local continuar, passa a ler o heap por outra via (ex.: o
   `heap_live`/`heap_inuse` do `metrics.json`, sem GC forcado e sem perfil pprof) ou contra um
   daemon compilado com a tag de desenvolvimento. Ajustar `regime_test.go` ao caminho escolhido.

### Criterio de aceite

- O binario de release nao contem o handler: `go tool nm` no binario sem `debugHandler`,
  `startDebug`, `debugListener` nem `net/http/pprof`, e `strings` sem `/debug/mem`,
  `/debug/pprof` e `PAINEL_DEBUG_ADDR`.
- `instalar.py status` continua mostrando o heap pelo `metrics.json` (linha "Heap Go: ... pos-GC").
- Testes rapidos passam: `go vet ./...`, `go test ./...` e os testes do `instalar.py`
  (`tests/test_instalar.py`).
- Docs que citarem o endpoint ajustadas (hoje so os comentarios de `cmd/loadtest` o citam).
