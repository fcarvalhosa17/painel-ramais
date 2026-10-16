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

## Pendencia: privilegios AMI ilegiveis marcados como OK (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Origem:** primeiro `instalar.py verificar` em Issabel 5 real (Rocky 8.8, Asterisk 18.19.0,
Python 3.6). A linha "Actions AMI" mostrou `Privilege ?` para todas as actions e mesmo assim saiu OK.

**Dependencia:** pacote `homologacao-*.tar.gz` coletado no Issabel, colocado em
`docs/homologacao/entrada/` e analisado. O `pre.txt` do pacote traz a saida bruta de
`manager show command <Action>` de cada action (`instalar.py:1156`); e dela que sai o formato real.

### Ponto a mudar

Linhas em 0188465:

- `instalar.py:333-338` `privilegio`: procura `^\s*Privilege:\s*(.*)$` e devolve `"?"` quando nao
  casa. No Asterisk 18 a saida de `manager show command` nao traz a linha `Privilege: ...` nesse
  formato (provavelmente cabecalho `[Privilege]` com o valor na linha seguinte; confirmar pela saida
  bruta do pacote).
- `instalar.py:341-360` `c_actions`: so trata `None` (action ausente); `"?"` cai no ramo OK
  (`instalar.py:359`).

### Decisao do usuario

1. Corrigir o parser para o formato de `manager show command` do Asterisk 18, usando a saida bruta
   do pacote do homologar como fixture de teste.
2. Regra: privilegio desconhecido (parser nao achou a classe) e AVISO, nunca OK. A linha mostra
   quais actions ficaram com privilegio ilegivel.

### Criterio de aceite

- Teste com a saida bruta do Asterisk 18 (copiada do `pre.txt`): cada action sai com a classe lida
  (ex.: `Privilege system,reporting`), nunca `?`.
- Teste com saida sem classe reconhecivel: a linha "Actions AMI" sai AVISO, nao OK.
- Testes atuais de `tests/test_instalar.py` e `tests/test_homologar.py` continuam passando.

## Pendencia: ContactStatus sem UserAgent no Asterisk 18 (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Origem:** primeiro `instalar.py verificar` em Issabel 5 real (Asterisk 18.19.0): o evento
`ContactStatus` nao traz o campo `UserAgent`.

**Dependencia:** pacote `homologacao-*.tar.gz` coletado no Issabel, colocado em
`docs/homologacao/entrada/` e analisado (confirma os campos reais de `ContactStatus`, `ContactList`
e `ContactStatusDetail`).

### Ponto a mudar

Linhas em 0188465:

- `internal/ami/pjsip/pjsip.go:140-153` `contactStatus`: a cada evento grava um `contact` novo
  via `parseContact`.
- `internal/ami/pjsip/pjsip.go:199-200` `parseContact`: `ua: m.Get("UserAgent")`; sem o campo no
  evento, o user agent vira vazio e apaga o valor que veio do snapshot.
- O snapshot ja le o user agent: `loadContacts` (`pjsip.go:89`, `PJSIPShowContacts` com fallback
  `PJSIPShowAors`) -> `addLocked` (`pjsip.go:155`) -> `parseContact`.
- `instalar.py:367` `c_eventos`: espera `UserAgent` em `ContactStatus`; no Asterisk 18 isso gera
  AVISO. Ajustar a lista de campos esperados ao que o evento realmente traz.

### Decisao do usuario

1. Obter o user agent do snapshot (`PJSIPShowContacts` / `ContactStatusDetail`).
2. Manter o ultimo valor conhecido: evento `ContactStatus` sem `UserAgent` (ou com ele vazio) NAO
   apaga o user agent ja conhecido daquele contato.

### Criterio de aceite

- Teste: snapshot com `UserAgent` preenchido, depois `ContactStatus` sem o campo: o
  `RegistrationChanged` emitido mantem o user agent do snapshot.
- Teste: `ContactStatus` com `UserAgent` novo atualiza o valor.
- Teste: contato `Removed` continua saindo do agregado.
- Testes atuais de `internal/ami/pjsip` continuam passando.

## Pendencia: permissao do manager_custom.conf vigiada pela verificacao de 60 s (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Origem:** primeiro `instalar.py verificar` em Issabel 5 real.

**Dependencia:** pacote `homologacao-*.tar.gz` coletado no Issabel, colocado em
`docs/homologacao/entrada/` e analisado.

### Ponto a mudar

Linhas em 0188465:

- `packaging/scripts/postinstall:99` e `:112`: o `%post` ja aplica `chmod o-rwx` so no
  `manager_custom.conf` (manter assim; conferir que continua so nesse arquivo).
- `instalar.py:402-410` `c_perm_manager_custom`: o `verificar` ja acusa o arquivo legivel para
  outros, mas so roda quando alguem chama o instalador.
- `internal/depcheck/depcheck.go:66` `Check`: hoje verifica so `checkLogin`, `checkInclude` e
  `checkApache`, a cada 60 s (`cmd/painel-ramais/serve.go:29` `depCheckEvery`, ligado em
  `serve.go:150`). Nao olha a permissao do `manager_custom.conf`.

### Decisao do usuario

1. O `%post` do RPM aplica `chmod o-rwx` somente no `manager_custom.conf`. Nao mexer em nenhum
   outro arquivo do Issabel.
2. A verificacao de dependencias a cada 60 s passa a acusar quando o `manager_custom.conf` voltar a
   ficar legivel para outros (ex.: depois de uma atualizacao do Issabel), com a dica
   `chmod o-rwx /etc/asterisk/manager_custom.conf`. O daemon so le o modo (stat); nunca altera o
   arquivo.

### Criterio de aceite

- Teste em `internal/depcheck`: arquivo com bit de leitura para outros -> item com status `fail`
  e a dica do `chmod`; arquivo `o-rwx` -> `ok`; stat falhou -> `unknown`, nunca `ok`.
- O item aparece em `/api/health` `deps[]` e a mudanca de estado sai no log (`logChanges`).
- `%post` sem nenhum `chmod`/`chown` novo fora do `manager_custom.conf`.

## Pendencia: instalar.py sem bit de execucao no repositorio (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora (so registro). Entra no lote de correcoes que vier
depois da analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em
homologacao.

**Origem:** primeiro uso em Issabel 5 real: `./instalar.py` a partir de um clone do repositorio
nao executa (sem bit de execucao).

### Ponto a mudar

- `git ls-files -s instalar.py` em 0188465: modo `100644`.

### Decisao do usuario

1. Gravar o modo executavel no git: `git update-index --chmod=+x instalar.py`, para que um clone
   tambem funcione.
2. O pacote `tar.gz` da Release continua sendo o caminho oficial de instalacao.

### Criterio de aceite

- `git ls-files -s instalar.py` mostra `100755`.
- Clone novo: `./instalar.py --help` executa sem `python3` na frente.

## Pendencia: clone do repositorio confundido com o pacote (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Origem:** primeiro uso em Issabel 5 real: `instalar.py instalar` rodado na pasta do clone, sem o
RPM ao lado.

### Ponto a mudar

Linhas em 0188465:

- `instalar.py:203-208` `achar_rpm`: sem RPM, falha com "RPM painel-ramais-*.rpm nao encontrado ao
  lado do instalar.py.", sem dizer que a pasta e o codigo-fonte nem como obter o pacote.
- Chamado por `cmd_instalar` (`instalar.py:800`). `bundle_rpm_ou_none` (`instalar.py:522-526`)
  tambem usa `achar_rpm`, mas engole a `Falha`; nao muda.
- Comando de download ja documentado: `docs/homologacao/ROTEIRO-MANUAL.md:70` e `README.md:18`.

### Decisao do usuario

1. Se `instalar.py instalar` rodar sem o RPM na mesma pasta, falhar com mensagem clara: "esta pasta
   e o codigo-fonte; baixe o pacote da Release", seguida do comando de download, por exemplo
   `gh release download "$TAG" -R fcarvalhosa17/painel-ramais -p 'painel-ramais-*.tar.gz' -p SHA256SUMS`
   (ou `curl -fLO` a partir de `https://github.com/fcarvalhosa17/painel-ramais/releases/download/<tag>/`).
2. Nada e alterado no sistema antes dessa falha.

### Criterio de aceite

- Teste em `tests/test_instalar.py`: pasta sem RPM -> saida com "esta pasta e o codigo-fonte; baixe
  o pacote da Release" e o comando de download; codigo de saida de erro; nenhum `rpm`/`dnf` chamado.
- Pasta com RPM: fluxo atual sem alteracao.

## Pendencia: aviso de chan_sip e chan_pjsip juntos (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
analise do pacote `homologacao-*.tar.gz`, sem rc nova agora; a rc.2 e a versao em homologacao.

**Origem:** primeiro `instalar.py verificar` em Issabel 5 real: `chan_sip` e `chan_pjsip`
carregados juntos, que e o padrao do Issabel 5. O aviso atual sugere que isso e anormal.

### Ponto a mudar

Linhas em 0188465:

- `instalar.py:322-324` `c_tecnologia`: texto "chan_sip e chan_pjsip carregados juntos; o painel
  usara a que tiver ramais (o padrao e usar so uma)." e devolve `"pjsip"` fixo, sem contar ramais.
- Regra real do modo auto no daemon: `internal/ami/detect.go:73-87` conta ramais das duas
  tecnologias e escolhe `sip` so se `sip > pjsip` (empate = `pjsip`).

### Decisao do usuario

1. Ajustar o texto do aviso para dizer que `chan_sip` e `chan_pjsip` carregados juntos e o padrao
   do Issabel 5.
2. Mostrar qual tecnologia o modo auto vai escolher e quantos ramais ela tem, com a mesma regra do
   `detect.go` (contagem das duas; empate = PJSIP).

### Criterio de aceite

- Teste com os dois modulos carregados e mais ramais PJSIP: o aviso cita o padrao do Issabel 5, que
  o modo auto usara PJSIP e a quantidade de ramais PJSIP; tecnologia devolvida `pjsip`.
- Teste com mais ramais SIP: o aviso diz SIP com a quantidade; tecnologia devolvida `sip`.
- Teste de empate: PJSIP, como no `detect.go`.
