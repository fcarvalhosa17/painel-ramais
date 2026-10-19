# Pendencias de homologacao

Decisoes ja tomadas que ficam para depois da analise do pacote de homologacao.
A rc.2 e a versao em homologacao: nada aqui altera codigo nem gera rc nova agora.

Atualizacao 2026-10-16: o endpoint de debug do heap e as pendencias 3 a 6 abaixo (permissao do
manager_custom.conf, bit de execucao, clone confundido com o pacote, aviso de chan_sip e chan_pjsip)
passaram para a fase 2 (`docs/plans/2026-10-16-fase2-menu-issabel.md`, ADR-004). Continuam esperando
o pacote: outro lado sem ramal e privilegios AMI ilegiveis (Asterisk 18). ContactStatus sem UserAgent foi implementado na rc.5 (ver a secao).

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

**Status:** em execucao na fase 2 (plano `docs/plans/2026-10-16-fase2-menu-issabel.md`, tarefa B8:
caminho da decisao 2, build tag `debug`).

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
- A ferramenta de carga local, que lia o heap por esse endpoint, foi removida na rc.9 (recuperavel pela tag `v0.1.2-rc.8`).

### Decisao do usuario

1. Remover o endpoint do build de release: codigo (`debug.go`, `debug_test.go`, a chamada em
   `serve.go:155`) e configuracao (`PAINEL_DEBUG_ADDR`). O `net/http/pprof` sai do binario
   distribuido junto.
2. Se for preciso manter para desenvolvimento, que seja por build tag (ex.: `//go:build debug`),
   fora do binario distribuido; o build de release nao passa a tag.

### Criterio de aceite

- O binario de release nao contem o handler: `go tool nm` no binario sem `debugHandler`,
  `startDebug`, `debugListener` nem `net/http/pprof`, e `strings` sem `/debug/mem`,
  `/debug/pprof` e `PAINEL_DEBUG_ADDR`.
- `instalar.py status` continua mostrando o heap pelo `metrics.json` (linha "Heap Go: ... pos-GC").
- Testes rapidos passam: `go vet ./...`, `go test ./...` e os testes do `instalar.py`
  (`tests/test_instalar.py`).
- Docs que citarem o endpoint ajustadas.

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

**Status:** IMPLEMENTADA na rc.5 (2026-10-16), sem verificacao na VM. `internal/ami/pjsip/pjsip.go`: o
adaptador guarda o ultimo user agent conhecido por endpoint e IP (`lastUA`, preenchido pelo snapshot
`PJSIPShowContacts`/`ContactList` e por qualquer evento que traga o campo, e reiniciado a cada
releitura); `ContactStatus` sem `UserAgent` reaproveita esse valor em vez de apagar. Continua
pendente a linha do `instalar.py` (`c_eventos`) que ainda espera `UserAgent` em `ContactStatus`.
Texto original abaixo, para historico.

**Status original:** decidido, NAO implementar agora. Entra no lote de correcoes que vier depois da
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

**Status:** em execucao na fase 2 (plano `docs/plans/2026-10-16-fase2-menu-issabel.md`, tarefa B5).

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
   outro arquivo do Issabel. A regra vale so para arquivos do Issabel: arquivos do painel
   (`/etc/painel-ramais*`, `/var/lib/painel-ramais` e a chave `/etc/painel-ramais-issabel.key`)
   podem ser criados e ajustados pelo RPM (ADR-004, 2026-10-16).
2. A verificacao de dependencias a cada 60 s passa a acusar quando o `manager_custom.conf` voltar a
   ficar legivel para outros (ex.: depois de uma atualizacao do Issabel), com a dica
   `chmod o-rwx /etc/asterisk/manager_custom.conf`. O daemon so le o modo (stat); nunca altera o
   arquivo.

### Criterio de aceite

- Teste em `internal/depcheck`: arquivo com bit de leitura para outros -> item com status `fail`
  e a dica do `chmod`; arquivo `o-rwx` -> `ok`; stat falhou -> `unknown`, nunca `ok`.
- O item aparece em `/api/health` `deps[]` e a mudanca de estado sai no log (`logChanges`).
- `%post` sem nenhum `chmod`/`chown` novo em arquivo do Issabel fora do `manager_custom.conf`
  (arquivos do painel ficam de fora desta regra, ver decisao 1).

## Pendencia: instalar.py sem bit de execucao no repositorio (lote pos-analise do pacote homologacao-*.tar.gz)

**Status:** em execucao na fase 2 (plano `docs/plans/2026-10-16-fase2-menu-issabel.md`, tarefa D2).

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

**Status:** SUBSTITUIDA pelo inicializador do `instalar.py`: fora de um pacote (sem o RPM ao lado,
mesma deteccao), `instalar` nao falha mais com o aviso de "codigo-fonte"; baixa a Release do GitHub,
confere o SHA256, extrai em `/root/painel-ramais-<versao>/` e executa o instalar.py de la
(`--pre`, `--versao`). Roteiro: `docs/homologacao/ROTEIRO-MANUAL.md`, etapas 18 a 20. O texto abaixo
fica como historico; o aviso so continua no codigo como rede de seguranca.

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

**Status:** em execucao na fase 2 (plano `docs/plans/2026-10-16-fase2-menu-issabel.md`: lado Go na
tarefa B9, lado do `instalar.py` na tarefa D4).

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

## Pendencia: instalar.py sem perguntas (lote pos-homologacao da rc.3)

**Status:** implementada no lote rc.4 (aguardando homologacao). `instalar.py instalar` nao pergunta nada nem le o stdin; flags `--tecnologia` e `--grupos`; `--respostas` inalterado (nao combina com as flags).

**Origem:** homologacao da rc.3: ao colar um bloco de comandos no terminal, a linha seguinte
(`./instalar.py status`) foi lida como resposta da pergunta de tecnologia e o instalador falhou com
`tecnologia invalida './instalar.py statusauto'`.

### Ponto a mudar

Linhas em d7893de:

- `instalar.py:727-732` `pedir`: le o stdin (`input`) para cada pergunta.
- `instalar.py:754` `coletar_respostas`: pergunta "Tecnologia (auto/sip/pjsip)" (padrao `auto`).
- `instalar.py:755`: pergunta "Caminho do CSV de grupos (opcional, Enter para pular)".
- `instalar.py:734-753` (ramo `--respostas`, chaves `tecnologia` e `grupos_csv`, `CHAVES_RESPOSTAS` na
  linha 66): comportamento a manter.
- `instalar.py:1853-1860` `montar_parser`: nao existem `--tecnologia` nem `--grupos`.
- Texto que fala em perguntas interativas: `README.md:34` e `README.md:409`,
  `packaging/LEIAME-INSTALACAO.md:38`, `docs/homologacao/ROTEIRO-MANUAL.md:105`.

### Decisao do usuario

1. `instalar.py instalar` nao faz nenhuma pergunta.
2. Tecnologia: sempre `auto` (deteccao pelo daemon). So muda com a flag `--tecnologia sip|pjsip`,
   para casos especiais.
3. Grupos: nao perguntar CSV na instalacao. Os grupos sao criados depois, pela tela "Grupos" do painel
   ou pela CLI. A flag `--grupos arquivo.csv` continua opcional.
4. Com `--respostas` (modo nao interativo) o comportamento atual fica como esta.

### Criterio de aceite

- `instalar.py instalar` sem flags nao le o stdin (colar comandos em seguida nao e consumido),
  usa tecnologia `auto` e nao pergunta grupos.
- `--tecnologia sip|pjsip` e `--grupos arquivo.csv` continuam funcionando; valor invalido continua
  saindo com codigo 3.
- `--respostas` inalterado.
- `README.md`, `packaging/LEIAME-INSTALACAO.md` e `docs/homologacao/ROTEIRO-MANUAL.md` sem referencia
  a perguntas interativas.

## Pendencia: servidor sem ramais nao e erro (lote rc.4)

**Status:** implementada no lote rc.4 (aguardando homologacao), nos dois lados. No `instalar.py`, `verificar` e `instalar` tratam lista vazia como `0 ramais` e a instalacao nao espera ramais. No daemon (backend), a resposta Error "no ... found" tambem foi implementada como lista vazia.

**Origem:** homologacao da rc.3 numa VM Issabel sem ramais: "No AORs found"/"No endpoints found" e lista vazia sao zero ramais, mas o instalador esperava ramais para
considerar o painel pronto.

### Ponto a mudar

- `instalar.py` `health_pronto`/`esperar_health`: exigiam `extensions.total > 0`.
- `instalar.py` `c_tecnologia`: a contagem de ramais precisa tratar lista vazia como `0 ramais`, sem ERRO nem AVISO.

### Decisao do usuario

Servidor sem ramais e valido: `verificar` e `instalar` informam `0 ramais`, sem erro nem aviso, e a instalacao so espera o AMI conectado.

### Criterio de aceite

- `verificar` num servidor sem ramais: linha da tecnologia com `0 ramais`, sem AVISO/ERRO por isso.
- `instalar` termina em ate 60 s com o painel mostrando 0.

## Pendencia: contagem de ramais do verificar igual a do daemon (lote rc.4)

**Status:** implementada no lote rc.4 (aguardando homologacao).

**Origem:** `instalar.py verificar` contou `sip=1` numa VM sem ramais: a linha de resumo `N sip peers [...]` do `sip show peers` era lida como um peer chamado `1`, que casa com o
`extension_pattern` padrao.

### Ponto a mudar

- `instalar.py` `nomes_endpoints` (resumo `N sip peers` e cabecalho `<Endpoint/CID...>`) e `c_tecnologia` (nova `ramais_do_asterisk`, mesma regra do `internal/ami/detect.go`: nome casa com
  `extension_pattern`; `sip` so se `sip > pjsip`, empate = `pjsip`).

### Decisao do usuario

O verificar conta ramais como o daemon.

### Criterio de aceite

Numa VM sem ramais e com `chan_sip` e `chan_pjsip` carregados, o verificar mostra `pjsip=0, sip=0`.

## Pendencia: endpoint interno dummy_endpoint nao e ramal (lote rc.4)

**Status:** implementada no lote rc.4 (aguardando homologacao): constante `ENDPOINTS_INTERNOS` no `instalar.py` (comparacao sem diferenciar maiusculas), que espelha a lista do daemon
(`internal/model/model.go`, `model.InternalEndpoints`, do backend); manter as duas listas iguais.

**Origem:** o Issabel cria `dummy_endpoint`, que nao e ramal e nao deve entrar na contagem.

### Criterio de aceite

Com `extension_pattern` que casaria o nome, `dummy_endpoint` (qualquer caixa) fica fora da contagem do verificar e do daemon.

## Pendencia: ProtectKernelLogs na unit e o systemd 239 do Rocky 8 (lote rc.4)

**Status:** implementada no lote rc.4 (aguardando homologacao).

**Origem:** `ProtectKernelLogs=` so existe a partir do systemd 244; o Rocky 8 traz o 239 (opcao desconhecida gera aviso no journal).

### Ponto a mudar

- `packaging/painel-ramais.service`: removida `ProtectKernelLogs=yes`. As demais opcoes de seguranca existem no systemd 239 (NoNewPrivileges 187, ProtectSystem=strict 214, PrivateDevices,
  ProtectKernelTunables 211, ProtectKernelModules e ProtectControlGroups 232, RestrictNamespaces 233, LockPersonality 235, MemoryDenyWriteExecute 231, RestrictAddressFamilies 211,
  SystemCallFilter=~@debug, SystemCallArchitectures 209, RuntimeDirectory 211) e ficaram. O teste de seguranca em `tests/test_instalar.py` deixou de exigir `ProtectKernelLogs`.

### Criterio de aceite

`systemctl daemon-reload` e `journalctl -u painel-ramais` na VM sem `Unknown key name`/`Unknown lvalue` da unit.

## Pendencia: inventario de ramais dinamico (lote rc.4)

**Status:** implementada no lote rc.4 (aguardando homologacao), toda no daemon (backend); o `instalar.py` nao tem mudanca nesta pendencia.

**Origem:** ramal criado, apagado ou renomeado no Issabel (Apply Config) so aparecia depois de reiniciar o painel.

### Decisao do usuario

O painel acompanha o inventario do Asterisk: ramal novo aparece, ramal apagado some e nome novo aparece, em ate 10 s.

### Criterio de aceite

Etapa 16c do `docs/homologacao/ROTEIRO-MANUAL.md` (criar, apagar e renomear um ramal).

### A conferir na homologacao da rc.4 (inventario dinamico e eventfilter)

- Nome exato dos eventos de recarga no Asterisk 18 do Issabel e se `ModuleLoadReport` e `FullyBooted` passam pelo `eventfilter` (o bloco gerenciado da rc.4 inclui `Reload`,
  `ModuleLoadReport` e `FullyBooted`; o `%post` reescreve o bloco de forma idempotente no upgrade e roda `manager reload`).
- Quantos `Reload` um Apply Config gera.
- Texto exato das respostas de lista vazia ("no ... found") e se o `EndpointList` traz `Callerid`.
- Outros endpoints internos alem de `dummy_endpoint` (resumo da S10 do `docs/CONTRATOS.md`; a lista unica e `model.InternalEndpoints`, espelhada em `ENDPOINTS_INTERNOS` do `instalar.py`).

### Sem testes automatizados para o inventario (por ordem do usuario)

Por ordem do usuario (sem suites novas neste computador) **nao foram criados testes automatizados** para a logica nova: no daemon, `IsEmptyList`/`DoList`, `IsExtension`,
`reconcile`/`tomb`/`gone`, `removed` no delta, debounce e `Reread`; no `instalar.py`, `nomes_endpoints` e `ramais_do_asterisk`. A validacao e na homologacao (Etapas 16c e do upgrade rc.3 -> rc.4).
