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
