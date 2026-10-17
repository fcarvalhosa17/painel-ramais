# Roteiro manual de homologacao (Issabel 5 / Rocky)

Este roteiro e para quem instala na VM e acompanha as chamadas. Ninguem do time acessa a VM: tudo o que voce ve
deve ser anotado (o `homologar` guarda as suas anotacoes com a hora) e o pacote gerado volta para analise.
Texto sem acentos de proposito: e o mesmo que o `./instalar.py homologar` mostra no terminal.

Cada etapa tem tres campos: **Comando**, **Resultado esperado**, **Se for diferente**. Os **Passo N** da etapa 9 sao os
mesmos passos (titulo e texto) que o `homologar` mostra, um por vez.

## 0. Antes de comecar

Voce precisa de:

- a VM Rocky Linux 8 com Issabel 5, com acesso root e dois checkpoints planejados: **limpo** (agora, antes de instalar) e **instalado** (depois da etapa 7);
- tres ramais de teste registrados (A, B e C), um tronco (T) com um numero de entrada, um celular seu (X), uma fila (F) com um ramal agente e um ring group (R);
- um segundo terminal ssh na VM (o primeiro fica preso no `homologar`);
- o computador onde o pacote sera analisado, com este repositorio e Python 3.

Legenda usada em todo o roteiro: **A, B, C** = os tres ramais; **T** = tronco; **X** = seu celular; **F** = fila; **R** = ring group.

Variaveis da versao (troque pelos valores da release que vai testar):

```
export TAG=v0.1.2-rc.4
export VERSAO_RPM=0.1.2-0.1.rc.4    # versao-release do RPM: rc = release 0.1.rc.N; a final = 0.1.2-1
```

Ver o painel por dentro (segundo terminal). Cole uma vez; `painel_ver` mostra o que o painel acha agora:

```
PY=$(command -v python3 || echo /usr/libexec/platform-python)
painel_login() {
  local tok
  tok=$(runuser -u painel-ramais -- /usr/bin/painel-ramais token --sub roteiro --ttl 60s) || return 1
  PAINEL_COOKIE=$(curl -si -H 'Content-Type: application/json' -d "{\"token\":\"$tok\"}" http://127.0.0.1:8790/api/token \
    | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(painel_session=[^;]*\).*/\1/p')
  [ -n "$PAINEL_COOKIE" ] && echo "sessao ok" || echo "sem cookie: token recusado?"
}
painel_ver() {
  curl -s -H "Cookie: $PAINEL_COOKIE" http://127.0.0.1:8790/api/snapshot | $PY -c '
import sys, json
d = json.load(sys.stdin)["data"]
ex = d["extensions"]
cont = {}
for e in ex: cont[e["registration"]] = cont.get(e["registration"], 0) + 1
print("AMI conectado:", d["ami_connected"], "| ramais:", len(ex), cont)
for e in ex:
    for c in e["calls"]:
        print(e["number"], c["direction"], c["state"], c["peer"]["kind"], c["peer"]["number"])
'
}
```

O lado do Asterisk, no mesmo terminal: `asterisk -rx 'core show channels concise'` e `asterisk -rx 'pjsip show contacts'`
(`sip show peers` se a tecnologia for chan_sip).

## Parte 1: instalar

### Etapa 1. Checkpoint "limpo"

- **Comando:** no hipervisor, crie o checkpoint (snapshot) da VM com o nome `limpo`, com a VM ja ligada e sem o painel instalado. Na VM: `rpm -q painel-ramais`.
- **Resultado esperado:** o checkpoint existe; o `rpm -q` responde `package painel-ramais is not installed`.
- **Se for diferente:** se o painel ja esta instalado, volte a um estado sem ele (ou `./instalar.py remover --purge --sim` de uma versao anterior) antes de criar o checkpoint. Nao siga sem um checkpoint "limpo": a etapa 17 depende dele.

### Etapa 2. Baixar

- **Comando** (numa pasta de trabalho, por exemplo `mkdir -p ~/homologacao && cd ~/homologacao`):

```
gh release download "$TAG" -R fcarvalhosa17/painel-ramais -p 'painel-ramais-*.tar.gz' -p SHA256SUMS
# alternativa sem o gh:
BASE=https://github.com/fcarvalhosa17/painel-ramais/releases/download/$TAG
curl -fLO "$BASE/painel-ramais-${TAG#v}.tar.gz"
curl -fLO "$BASE/SHA256SUMS"
# para a rc.4 os enderecos completos sao:
#   https://github.com/fcarvalhosa17/painel-ramais/releases/download/v0.1.2-rc.4/painel-ramais-0.1.2-rc.4.tar.gz
#   https://github.com/fcarvalhosa17/painel-ramais/releases/download/v0.1.2-rc.4/SHA256SUMS
#   https://github.com/fcarvalhosa17/painel-ramais/releases/download/v0.1.2-rc.4/painel-ramais-0.1.2-0.1.rc.4.x86_64.rpm   (so se quiser o RPM avulso)
```

  O bundle (`painel-ramais-0.1.2-rc.4.tar.gz`) ja traz o RPM dentro e o `instalar.py` o usa; o RPM avulso (`painel-ramais-${VERSAO_RPM}.x86_64.rpm`) so e preciso para conferir o pacote sozinho (`rpm -qp`) ou instalar sem o instalador. Nesse caso baixe tambem: `gh release download "$TAG" -R fcarvalhosa17/painel-ramais -p 'painel-ramais-*.rpm'` ou `curl -fLO "$BASE/painel-ramais-${VERSAO_RPM}.x86_64.rpm"`.
- **Resultado esperado:** na pasta atual ficam o tar.gz do bundle (e o RPM, se pedido) e o `SHA256SUMS`, sem erro de rede.
- **Se for diferente:** `404` = a TAG nao existe ou o arquivo ainda nao foi publicado; confirme `echo $TAG` e a lista de arquivos da release. Sem internet na VM: baixe no seu computador e copie com `scp` (veja o LEIAME-INSTALACAO, passo 1c).

### Etapa 3. Conferir o SHA256

- **Comando:** `sha256sum -c --ignore-missing SHA256SUMS` (o `SHA256SUMS` lista o RPM e o tar.gz; `--ignore-missing` pula o que voce nao baixou)
- **Resultado esperado:** uma linha `OK` para cada arquivo baixado, por exemplo `painel-ramais-0.1.2-rc.4.tar.gz: OK` (e `painel-ramais-0.1.2-0.1.rc.4.x86_64.rpm: OK` se baixou o RPM), e nenhuma linha `FAILED`.
- **Se for diferente:** `FAILED` ou `No such file`: pare, apague o arquivo e baixe de novo. Nao rode o instalador com SHA diferente. Se repetir, anote os dois hashes (`sha256sum arquivo` e a linha do `SHA256SUMS`).

### Etapa 4. Extrair

- **Comando:** `tar xzf painel-ramais-*.tar.gz && cd painel-ramais-*/ && ls`
- **Resultado esperado:** `instalar.py`, o RPM, `SHA256SUMS` e `LEIAME-INSTALACAO.md` na pasta; `instalar.py` executavel.
- **Se for diferente:** mais de um tar.gz na pasta faz o glob falhar: use o nome completo. `instalar.py` sem permissao de execucao: `chmod +x instalar.py` e anote (o bundle deveria ter o modo 0755).

### Etapa 5. Verificar o ambiente

- **Comando:** `./instalar.py verificar`
- **Resultado esperado:** linhas `[OK   ] ...` (alguns `[AVISO]` sao normais), no fim `Resultado: 0 erro(s), N aviso(s).`, `Relatorio salvo em /var/log/painel-ramais/verificacao-AAAAMMDD-HHMMSS.txt` e codigo de saida 0 (`echo $?`). Nada e alterado no sistema.
- **Se for diferente:** cada `[ERRO]` traz `Como corrigir:`; corrija e rode de novo. Se a correcao proposta nao funcionar, ou a mensagem nao for clara, anote (texto exato e o que voce fez). Guarde o arquivo de verificacao.

### Etapa 6. Instalar

- **Comando:** `./instalar.py instalar` (nao faz perguntas: tecnologia `auto`, sem senha; grupos pela tela ou pela CLI; `--tecnologia sip|pjsip` e `--grupos arquivo.csv` sao opcionais)
- **Resultado esperado:** `== Instalacao ==`, a verificacao sem erro, `Aguardando o painel (ate 60s)...` e o `== Resumo ==` com `Versao`, `Tecnologia`, `Ramais` (total maior que 0; com 0 e aceito quando o servidor nao tem ramais), `Grupos`, `Acesso: https://<ip>/painel-ramais/` e `Log`. Codigo de saida 0.
- **Menu do Issabel:** depois do teste do Apache o instalador imprime `Item do menu do Issabel:` com `[OK   ] Menu do Issabel: item painel_ramais - pai=pbxconfig tipo=module ordem=10`, as permissoes (grupos 1 e 2) e a chave (`painel-ramais:asterisk 0640`). Qualquer `[AVISO]` ali: rode `./instalar.py menu` (ou `./instalar.py menu --recriar` se faltar grupo; isso apaga permissoes extras dadas a outros grupos).
- **Se for diferente:** a mensagem de `FALHA` traz o comando de diagnostico (`journalctl -u painel-ramais -n 50 --no-pager`, `httpd -t`...). Nada e desfeito: corrija e rode `instalar` de novo (e idempotente). Guarde `/var/log/painel-ramais/instalacao.log`.

### Etapa 7. Conferir a instalacao e checkpoint "instalado"

- **Comando:** `./instalar.py status; rpm -q painel-ramais; systemctl is-active painel-ramais.socket painel-ramais.service; painel_login; painel_ver`
- **Resultado esperado:** `Socket ... ativo`, `Servico: ativo`, `AMI: conectado=True tecnologia=<a sua>`, `rpm -q` igual a `painel-ramais-${VERSAO_RPM}.x86_64`, duas linhas `active`, `sessao ok` e `AMI conectado: True | ramais: N {...}` com N igual ao numero de ramais do Issabel. Depois, crie o checkpoint `instalado` no hipervisor.
- **Se for diferente:** `PARADO` em socket ou servico: `journalctl -u painel-ramais -n 50 --no-pager`. `AMI conectado: False`: veja `/etc/asterisk/manager_custom.conf` (bloco `painel-ramais`) e `asterisk -rx 'manager show users'`. `rpm -q` com versao diferente: voce instalou o bundle errado; volte ao checkpoint `limpo`.

## Parte 2: homologar

### Etapa 8. Preparar a execucao

- **Comando:** no terminal 1, dentro da pasta do bundle (a mesma da etapa 4, onde esta o `instalar.py`): `df -h /var/tmp /root`; avise quem usa o PBX que havera chamadas de teste e um restart do Asterisk. Quando for iniciar, tenha A, B, C, X, F e R a mao.
- **Resultado esperado:** pelo menos 500 MB livres em `/var/tmp` e em `/root`.
- **Se for diferente:** pouco espaco: libere antes; o pacote leva alguns MB, mas os dados brutos temporarios ficam em `/var/tmp/painel-homologacao-*` durante a execucao.

### Etapa 9. Rodar o homologar e seguir os passos

- **Comando:** na pasta do bundle, `sudo ./instalar.py homologar --saida /root` (45 minutos; para outra duracao: `--duracao 60`). O pacote fica em `/root`, nao na pasta do bundle.
- **Resultado esperado:** o terminal mostra o resultado do `verificar`, a legenda (A, B, C, T, X, F, R), `>>> PASSO 1/11: Chamada interna` e uma linha de relogio (`[hora] restam MM:SS | passo N/11: ...`). **Enter** passa ao proximo passo; **qualquer texto + Enter** grava uma anotacao com a hora (sem avancar). Ao fim: `Pacote: /root/homologacao-AAAAMMDD-HHMMSS.tar.gz`. Ctrl-C encerra antes, avisa que aguarda o capturar gravar o snapshot final (ate 3 min; Ctrl-C de novo abandona a captura) e ainda gera o pacote. Se o ssh cair (SIGHUP) ou o processo receber SIGTERM, nada e gerado e os dados brutos sao apagados: rode de novo.
- **Se for diferente:** `Painel nao instalado`: volte a etapa 6. `O filtro de mascara falhou`: nenhum pacote e criado e os dados brutos ficam em `/var/tmp/painel-homologacao-*` (NAO envie; avise o time e guarde a mensagem). Faltou tempo: rode de novo com `--duracao` maior.

Faca os passos abaixo na ordem, um por vez, com 20 a 30 s entre eles. Em cada um, aperte Enter no terminal 1 ao terminar. No segundo terminal use `painel_ver` durante a chamada. Se o que voce ve for diferente do esperado, **digite uma anotacao** no terminal 1 (o que viu, a hora e o ramal) e siga: a analise cruza as horas depois.

**Checagens da correcao V1 (processo do capturar)**

Faca durante o Passo 1, com o `homologar` rodando: o `capturar` so existe enquanto ele roda. A correcao V1 manda o `capturar` rodar em sessao propria (`setsid`), sem terminal e sem stdin, e sem permitir que outro processo se anexe a ele. Duas conferencias, no segundo terminal:

- **Comando (i):**

```
PID=$(pgrep -u painel-ramais -f 'painel-ramais capturar' | head -n 1); echo "PID=$PID"
ps -o pid,sid,tty,user,cmd -p "$PID"
```

- **Resultado esperado (i):** `PID` preenchido; na linha do `ps`, a coluna `TTY` e `?` (sem terminal), `SID` igual ao `PID` (sessao propria) e `USER` `painel-ramais`.
- **Se for diferente (i):** `TTY` com `pts/N`, ou `SID` diferente do `PID`, ou `PID` vazio (o `capturar` nao esta rodando: veja a mensagem do terminal 1): copie a linha inteira do `ps` para a anotacao.
- **Comando (ii):** primeiro `command -v strace`. Se existir: `sudo -u painel-ramais strace -p "$PID"` (se algum dia ele anexar, Ctrl-C na hora e anote). Se `command -v strace` nao imprimir nada, **nao instale o strace**: registre "teste nao feito".
- **Resultado esperado (ii):** permissao negada (`Operation not permitted` ou `attach: ptrace ... Permission denied`) e o `strace` encerra sozinho, sem mostrar chamadas de sistema do `capturar`.
- **Se for diferente (ii):** se aparecerem chamadas de sistema (`read(`, `write(`, `epoll_wait(`...), o V1 falhou: pare o `strace`, anote a hora e o texto exato da saida.

**Onde registrar (i) e (ii):** no terminal 1, o do `homologar`, digite uma linha de anotacao por checagem e aperte Enter (texto + Enter grava a anotacao com a hora, sem avancar de passo). Elas vao para o `roteiro.log` dentro do `homologacao-AAAAMMDD-HHMMSS.tar.gz`, o unico arquivo de texto livre do pacote; nao existe pasta onde soltar outro arquivo. Escreva sempre uma linha so (uma linha vazia avanca o passo, entao nao cole varias linhas de uma vez) e comece por `V1-i` ou `V1-ii`:

```
V1-i tty=? sid=<SID> pid=<PID> user=painel-ramais
V1-ii strace: permissao negada (Operation not permitted)
V1-ii teste nao feito: strace nao instalado na VM
```

Se o `ps` mostrar outra coisa, escreva o que viu (por exemplo `V1-i tty=pts/0 sid=1234 pid=5678`). O pacote passa pelo filtro de anonimizacao, entao hosts e usuarios podem aparecer trocados por valores ficticios.

**Checagem M1 (memoria do daemon no Rocky 8, systemd 239)**

O daemon grava `/run/painel-ramais/metrics.json` a cada 30 s (o systemd 239 nao exporta `RUNTIME_DIRECTORY`; o daemon usa a pasta fixa). Confira no segundo terminal, depois de instalar ou fazer o upgrade (o `restart` do servico recria a pasta):

- **Comando:** `sudo systemctl restart painel-ramais; sleep 35; sudo ls -l /run/painel-ramais/metrics.json; sudo ./instalar.py status | grep -E 'Memoria|Heap|Uptime'`
- **Resultado esperado:** o arquivo existe (`-rw-------`, dono `painel-ramais`) e o `status` mostra `Memoria RSS:`, `Heap Go: ... pos-GC` e `Uptime do daemon:` com valores, sem `indisponivel`.
- **Se for diferente:** `No such file`: o pacote e anterior a esta versao ou a unit nao foi recarregada (`systemctl daemon-reload`, restart de novo). `Permission denied` no `status`: faltou `sudo`.

Registre uma linha no terminal 1 (a do `homologar`), comecando por `M1`; ela vai para o `roteiro.log` como as checagens V1:

```
M1 metrics.json ok em 35s; RSS=<valor> heap=<valor> uptime=<valor>
M1 metrics.json ausente apos 35s (ls: No such file or directory)
M1 status: heap indisponivel (<texto exato>)
```

**Passo 1: Chamada interna**

A liga para B, B atende, conversam 20 s, A desliga.

- **Comando:** nenhum; faca a chamada. No terminal 2, durante a conversa: `painel_ver`.
- **Resultado esperado:** A e B mostram uma chamada `internal talking extension` cada, com o numero do outro. Ao desligar, as duas somem em ate 2 s.
- **Se for diferente:** chamada ausente, atrasada mais de 5 s, ou com `unknown` no lugar de `extension`: anote o `painel_ver` e `asterisk -rx 'core show channels concise'` no mesmo instante.

**Passo 2: Entrada por tronco**

De X, ligue para o numero do tronco T; A atende, 20 s, desligue.

- **Comando:** nenhum; faca a chamada. Durante a conversa: `painel_ver`.
- **Resultado esperado:** enquanto toca, A mostra `inbound ringing external <numero de X>`; depois de atender, `inbound talking external <numero de X>`. Ao desligar, a chamada some.
- **Se for diferente:** direcao `outbound` ou `internal`, ou numero externo errado/vazio: anote o numero que apareceu e o que X realmente discou.

**Passo 3: Saida por tronco**

A liga para X (com o prefixo de saida), atenda, 20 s, desligue.

- **Comando:** nenhum; faca a chamada. Durante a conversa: `painel_ver`.
- **Resultado esperado:** A mostra `outbound` (`ringing` enquanto toca, `talking` depois de atendida) com `external` e o numero de X (pode aparecer com ou sem o prefixo de saida).
- **Se for diferente:** direcao `inbound`/`internal`, ou nenhum numero: anote o numero discado exato (com prefixo) e o que o painel mostrou.

**Passo 4: Transferencia cega**

X liga para A; A atende e transfere (cega) para B; B atende, 10 s, desliga. Repita com chamada interna: B liga para A e A transfere para C.

- **Comando:** nenhum; faca as duas chamadas, uma de cada vez. Depois de transferir, `painel_ver`.
- **Resultado esperado:** externa: apos a transferencia A fica sem chamada e B mostra `inbound talking external <numero de X>` (a direcao de uma chamada que nasceu fora continua `inbound`). Interna: A fica sem chamada e B e C mostram `internal talking extension` um do outro.
- **Se for diferente:** A ainda com chamada, B sem chamada, ou direcao trocada: anote a hora exata da transferencia e o que cada ramal mostrou.

**Passo 5: Transferencia assistida**

X liga para A; A consulta C (assistida), C atende, A completa a transferencia; 10 s, desliga. Repita cancelando a consulta (A volta para X).

- **Comando:** nenhum; faca as duas chamadas. Durante a consulta e depois: `painel_ver`.
- **Resultado esperado:** durante a consulta A mostra duas chamadas (a de X em `hold` e a de C em `talking`); ao completar, C mostra `inbound talking external <numero de X>` e A fica sem chamada. Ao cancelar a consulta, A volta a ter so a chamada com X em `talking`.
- **Se for diferente:** sem `hold` na consulta, chamadas duplicadas depois de completar, ou chamada que nao some: anote a sequencia e a hora de cada acao (consultou, C atendeu, completou, cancelou).

**Passo 6: Chamada em espera**

A em chamada com B coloca B em espera, 15 s, retoma, desliga.

- **Comando:** nenhum; faca a chamada. Durante a espera: `painel_ver`.
- **Resultado esperado:** durante a espera uma das pontas mostra `hold` (anote qual: A ou B; isto e para confirmar); ao retomar volta a `talking`; ao desligar somem.
- **Se for diferente:** nenhuma ponta em `hold`, ou `hold` que nao sai ao retomar: anote qual ponta e quanto tempo demorou.

**Passo 7: Fila**

De X, ligue para F; um agente atende; 15 s; desligue. Faca outra deixando tocar ate o transbordo.

- **Comando:** nenhum; faca as duas chamadas. Durante a chamada: `painel_ver`.
- **Resultado esperado:** o agente que atende mostra `inbound talking external <numero de X>`; agentes que estavam tocando mostram `ringing` e depois ficam sem chamada. Na segunda chamada, ao transbordar nenhum ramal fica com chamada pendurada.
- **Se for diferente:** ramal de agente com chamada que nao some, ou numero externo errado: anote a hora e o `core show channels concise` do momento.

**Passo 8: Ring group**

Ligue para R; um dos ramais atende; 15 s; desligue. Faca outra em que ninguem atende.

- **Comando:** nenhum; faca as duas chamadas. Durante a chamada: `painel_ver`.
- **Resultado esperado:** todos os ramais do grupo mostram `ringing` ao mesmo tempo; quem atende passa a `talking` e os demais ficam sem chamada. Na segunda, ao encerrar o toque todos ficam sem chamada.
- **Se for diferente:** ramal do grupo ainda `ringing` depois que outro atendeu: anote quais e por quanto tempo.

**Passo 9: Chamada nao atendida**

A liga para B e B nao atende (deixe cair no timeout ou no correio de voz). Repita desligando do lado de A antes de tocar muito.

- **Comando:** nenhum; faca as duas chamadas. Enquanto toca: `painel_ver`.
- **Resultado esperado:** A e B mostram `ringing` (A, que discou, tambem fica `ringing` ate o destino atender). No timeout/correio de voz ou ao A desligar, as chamadas somem. Anote o que o painel mostrou durante o correio de voz.
- **Se for diferente:** A em `talking` enquanto B ainda toca, ou chamada que fica depois do fim: anote a hora.

**Passo 10: Ramal desregistrando e voltando**

Tire o telefone de C da rede por 40 s, religue e espere registrar. Repita com C em chamada.

- **Comando:** desligue a rede do telefone/softphone de C; acompanhe com `painel_ver` e `asterisk -rx 'pjsip show contacts'` (ou `sip show peers`).
- **Resultado esperado:** C sai de `registered` (para `unreachable` quando o qualify falha, ou `unregistered` se o contato for removido) e volta a `registered`, com o mesmo IP, depois de religar. Anote quanto tempo levou cada mudanca.
- **Se for diferente:** C continua `registered` muito depois do Asterisk mostrar `Unavail`, ou volta com IP diferente do que o Asterisk lista: anote o IP dos dois lados e a hora.

**Passo 11: Restart do Asterisk no meio de uma chamada**

Com A falando com B e X ligando para F (tocando), rode em OUTRO terminal: systemctl restart asterisk. Espere o painel reconectar.

- **Comando:** `systemctl restart asterisk` (terminal 2). Depois: `./instalar.py status` e `painel_ver` a cada alguns segundos ate estabilizar.
- **Resultado esperado:** o painel passa a `AMI conectado: False` e os ramais ficam `unknown` (nao `unregistered`); as chamadas somem; em ate 60 s reconecta sozinho, o snapshot e refeito e os ramais voltam a `registered`. O servico `painel-ramais` nao reinicia.
- **Se for diferente:** ramais em `unregistered` durante a queda, chamadas fantasma depois do restart, ou o painel que nao reconecta em 2 minutos: anote as horas e guarde `journalctl -u painel-ramais --since "-10min"`.

Fim do roteiro: deixe o relogio zerar (ou Ctrl-C). O pacote fica em `homologacao-AAAAMMDD-HHMMSS.tar.gz` (modo 0600).

### Etapa 10. Analisar o pacote

- **Comando:** copie o pacote para o seu computador (`scp root@SERVIDOR:/root/homologacao-*.tar.gz docs/homologacao/entrada/`; a pasta `entrada/` nao vai para o git) e rode: `python3 tools/analisar_homologacao.py docs/homologacao/entrada/homologacao-AAAAMMDD-HHMMSS.tar.gz` (ramais = so digitos; se o `extension_pattern` do `config.yaml` for outro, acrescente `--pattern '<regex>'`: o padrao gravado no pacote so e exibido, nunca usado; o analisador recusa pacote com mais de 64 arquivos, 64 MiB por arquivo ou 256 MiB no total)
- **Resultado esperado:** lista cada divergencia painel x Asterisk com a hora, o ramal, o campo (registro, ip, chamadas, estado_chamada, direcao, outro_lado), o passo do roteiro e as linhas do Asterisk como prova; e lista as linhas em que a anonimizacao pode ter falhado. Codigo de saida 0 = nada a revisar.
- **Se for diferente:** divergencia de uma amostra so (`transitoria`) pode ser o atraso entre as duas leituras; as que duram mais de uma amostra sao as que importam. Cada `[SUSPEITA]` deve ser olhada por voce: se for dado real, o arquivo NAO vai para o repositorio nem para ninguem. So depois de revisado algo vai para `testdata/`. Para o README, copie de `pre.txt` a classe (`Privilege`) de cada action para a coluna "Classe confirmada" da secao 6.

## Parte 3: atualizar, voltar, remover, reinstalar

### Etapa 11. Upgrade

- **Comando:** com `TAG` e `VERSAO_RPM` da versao mais nova, repita as etapas 2 a 4 (baixar, `sha256sum -c --ignore-missing SHA256SUMS`, extrair numa pasta nova; o RPM vem dentro do bundle, nao baixe separado) e rode, na pasta nova: `./instalar.py instalar` (interativo ou `--respostas`; o usuario de emergencia e o arquivo da senha ja existem e sao mantidos).
- **Resultado esperado:** `Upgrade <versao antiga> -> <versao nova>.`, o `== Resumo ==` com a versao nova, `AVISO: usuario 'emergencia' ja existe; senha mantida (arquivo /etc/painel-ramais/admin-emergencia)`, o arquivo da senha com o mesmo conteudo de antes (as sessoes de senha antigas sao encerradas pelo instalador), dados e grupos preservados, painel de volta em ate 60 s.
- **Se for diferente:** do rc para a versao final (`0.1.2-0.1.rc.1` para `0.1.2-1`) e entre rcs (`rc.1` para `rc.2`, `rc.2` para `rc.10`) o instalador trata como upgrade. `Versao instalada maior que a do bundle` so deve aparecer ao tentar o contrario (final para rc). Qualquer outra mensagem: anote o texto exato e `rpm -q painel-ramais`.

#### Upgrade da rc.3 para a rc.4 (a rc.3 ja esta instalada, com o painel funcionando)

Use o proprio instalador da rc.4. Nunca troque arquivo do painel a mao nem rode `rpm -U` solto. Esta e a homologacao do upgrade com o `%post` do menu
(que a rc.3 nao cobriu: ela foi instalacao nova).

- **Antes** (na rc.3 instalada): anote `rpm -q painel-ramais` (esperado `painel-ramais-0.1.2-0.1.rc.3.x86_64`), `./instalar.py status`, o conteudo e o modo da
  chave (`sudo sha256sum /etc/painel-ramais-issabel.key; ls -l /etc/painel-ramais-issabel.key` = `painel-ramais asterisk -rw-r-----`), os grupos
  (`sudo -u painel-ramais painel-ramais group list`) e se o item aparece no menu. Se possivel, crie um checkpoint.
- **Comando:** `export TAG=v0.1.2-rc.4`, repita as etapas 2 a 4 numa pasta nova (baixar, `sha256sum -c --ignore-missing SHA256SUMS`, extrair) e, dentro da pasta
  extraida da rc.4: `sudo ./instalar.py instalar` (sem perguntas).
- **Resultado esperado:** `Upgrade 0.1.2-0.1.rc.3 -> 0.1.2-0.1.rc.4.`, `rpm -q` responde `painel-ramais-0.1.2-0.1.rc.4.x86_64`, `AVISO: usuario 'emergencia' ja existe;
  senha mantida`, `Item do menu do Issabel:` com os tres itens `[OK   ]`, servico ativo e AMI conectado.
- **Sobrevivem ao upgrade:** (1) a chave do Issabel: mesmo `sha256sum` de antes e mesmo dono e modo; (2) o item do menu, no mesmo lugar (depois de Operator Panel), abrindo a tela
  sem senha como `administrator` e como `Operator`; (3) os grupos: a mesma lista de antes e a tela "Grupos" intacta; (4) a senha de emergencia
  (`/etc/painel-ramais/admin-emergencia` com o mesmo conteudo).
- **Se for diferente:** chave diferente ou item sumido: anote o texto do instalador, `sudo ./instalar.py menu` e `rpm -q`. Para voltar: etapa 12.

### Etapa 12. Rollback

- **Comando:** `./instalar.py rollback` (responda `s`) e depois `./instalar.py status`
- **Resultado esperado:** `Plano: reinstalar painel-ramais-<versao anterior>...rpm (rpm -U --oldpackage), preservando dados.`, `Rollback concluido.` e o `status` com a versao anterior, servico ativo, AMI conectado. Se o esquema do banco mudou entre as versoes, o instalador avisa que o historico recente sera perdido e pede confirmacao.
- **Menu:** se a versao anterior nao tem o modulo PHP, o rollback tambem remove o item `painel_ramais` do menu do Issabel e imprime `Item painel_ramais removido do menu do Issabel`.
- **Se for diferente:** `Nao ha versao anterior guardada`: o upgrade foi feito com RPM instalado a mao; anote. Qualquer `Rollback abortado sem alterar nada`: copie a mensagem; nada foi alterado.

### Etapa 13. Remover (mantendo os dados) e remover tudo

- **Comando:** `./instalar.py remover` (responda `s`); conferir `rpm -q painel-ramais; ls /var/lib/painel-ramais /etc/painel-ramais`; depois `./instalar.py remover --purge` (digite o nome do servidor quando pedir) e conferir de novo.
- **Resultado esperado:** o primeiro deixa `Dados mantidos em /var/lib/painel-ramais`; o `--purge` imprime `Dados apagados (...)`; `systemctl status painel-ramais.socket` mostra a unit inexistente; `ls /etc/httpd/conf.d/painel-ramais.conf` nao encontra; o Apache e o Asterisk continuam de pe (`systemctl is-active httpd asterisk`).
- **Se for diferente:** sobrou arquivo do painel, ou o Apache ficou fora do ar: anote `httpd -t` e `systemctl status httpd`.

### Etapa 14. Reinstalar

- **Comando:** `./instalar.py instalar`, depois `./instalar.py status`
- **Resultado esperado:** instalacao nova como na etapa 6 (apos o `--purge` pede usuario e senha de novo); se voce removeu sem purge, `AVISO: usuario ... ja existe` e os dados antigos voltam.
- **Se for diferente:** anote a mensagem e o passo em que falhou (o instalador diz `Falhou no passo: ...`).

## Parte 4: Issabel e navegador

### Etapa 15. Simular a atualizacao do Issabel

- **Comando:** com o painel instalado e o `homologar` fora de execucao (ou dentro de um novo `homologar --duracao 15`, aproveitando os snapshots), faca em ordem e espere 1 minuto entre cada:

```
./instalar.py status; painel_login; painel_ver; date
# 1) no IssabelPBX (web): botao "Apply Config" (vermelho) e confirme
# 2) systemctl restart httpd
# 3) systemctl restart asterisk
# 4) conferir (abaixo)
./instalar.py verificar
./instalar.py status; painel_login; painel_ver
httpd -t; ls -l /etc/httpd/conf.d/painel-ramais.conf /etc/asterisk/manager_custom.conf
grep -n 'manager_custom' /etc/asterisk/manager.conf
journalctl -u painel-ramais --since "-15min" --no-pager | tail -40
```

- **Resultado esperado:** o Apply Config recarrega o Asterisk sem derrubar o painel (AMI segue conectado; nomes dos ramais refeitos); o restart do `httpd` nao reinicia o painel e `curl -sk https://127.0.0.1/painel-ramais/` ainda devolve JSON do daemon; o restart do Asterisk derruba a conexao e o painel reconecta sozinho (`unknown` durante a queda e depois `registered`). No fim: `verificar` com 0 erros, `status` com AMI conectado e a mesma contagem de ramais de antes, `manager.conf` ainda inclui `manager_custom.conf`, o arquivo do Apache continua la, o servico `painel-ramais` nao foi reiniciado (`systemctl show painel-ramais -p ActiveEnterTimestamp` igual ao de antes).
- **Se for diferente:** painel que nao reconecta depois do restart do Asterisk: `journalctl -u painel-ramais` e `asterisk -rx 'manager show users'` (o usuario `painel-ramais` sumiu?). `manager.conf` sem o include, ou `painel-ramais.conf` sumido: anote qual passo (Apply Config, httpd ou Asterisk) removeu. Opcional, so na VM de laboratorio com checkpoint: `yum -y update` e repetir as conferencias.

### Etapa 16. Acessar pelo navegador (IP e nome, certificado autoassinado)

- **Comando:** no navegador de outro computador da rede: `https://<IP-DO-SERVIDOR>/painel-ramais/`, depois `https://<NOME-DO-SERVIDOR>/painel-ramais/` e `https://<IP-DO-SERVIDOR>/painel-ramais/api/health`. O nome precisa resolver (DNS ou arquivo hosts do seu computador).
- **Resultado esperado:** o navegador avisa que o certificado nao e confiavel (autoassinado; por IP ou por nome o aviso e esperado, inclusive "nome nao confere"); depois de aceitar, `/painel-ramais/` aberto direto no navegador (fora do iframe do Issabel) redireciona (303) para a pagina de aviso "Acesse pelo menu PBX do Issabel." (do painel, nao uma pagina de erro do Apache), `/painel-ramais/api/session` mostra um JSON `{"success":false,...}` com `unauthorized` (401) e `/painel-ramais/api/health` mostra a pagina 404 do Apache, sem JSON (a saude so existe em 127.0.0.1).
- **Se for diferente:** pagina de erro do Apache em `/painel-ramais/`: `httpd -t`, `getsebool httpd_can_network_connect` (deve estar `on`) e `curl -s http://127.0.0.1:8790/api/health` na VM. Nome que nao abre e IP que abre: e DNS/hosts, nao o painel. JSON em `/api/health`: anote (o proxy estaria expondo a rota interna).

### Etapa 16b. Conferir a tela pelo menu do Issabel (fase 2)

Itens marcados "quando o modulo estiver instalado" dependem do modulo PHP e do menu, que ainda nao existem (esperam a leitura da VM). Os demais valem ja pelo endereco direto e pela rota de emergencia descrita no LEIAME.

- **Menu:** quando o modulo estiver instalado, em PBX aparece "Painel de Ramais" logo depois de "Operator Panel". Entre como usuario `administrator` e depois como usuario do grupo `Operator`: nos dois a tela abre sem pedir senha.
- **Bloqueio de acesso direto:** abra `https://<IP>/painel-ramais/` direto no navegador (fora do menu). Esperado: aviso "Acesse pelo menu PBX do Issabel.", sem dados de ramais.
- **Mapa geral:** a tela abre com indicadores (total, registrados, nao registrados, inalcancaveis, em chamada) e o mapa de grupos. Anote o tempo percebido da abertura (do clique no menu ate ver os ramais).
- **Grupo aberto e busca:** clique num grupo; confira os cards e busque por numero e por nome (com e sem acento).
- **Cronometro:** faca uma chamada entre dois ramais; o card mostra o outro lado, a direcao e o cronometro andando; ao desligar, volta a "livre".
- **AMI desconectado:** com `systemctl stop asterisk` por alguns segundos, a tela mostra a faixa "Conexao com o Asterisk perdida" e os ramais em "desconhecido"; ao voltar o Asterisk, a faixa some. Suba o Asterisk de novo.
- **Tela cheia:** o botao de tela cheia entra e sai (dentro do menu depende do modulo; quando o modulo estiver instalado, confira tambem pelo iframe).
- **Administrador x operador:** quando o modulo estiver instalado, a area "Grupos" (importar CSV e remover) aparece so para `administrator`.
- **Encerrar sessao:** `sudo -u painel-ramais painel-ramais session purge --issabel <usuario>`; a tela aberta desse usuario mostra "Sessao encerrada" em ate alguns segundos.
- **Indicadores com AMI desconectado:** com o Asterisk fora, registrados, nao registrados, inalcancaveis e em chamada mostram "—" (nunca 0), com o texto "sem leitura com o Asterisk fora".
- **Ranking "Mais nao registrados":** todos os itens, inclusive o ultimo, aparecem com barra e numero.
- **Card do ramal:** o outro lado da chamada aparece uma vez so ("11468 · Ramal 11468"; chamada externa: "externo" e o numero).
- **Layout dentro do Issabel:** na area de conteudo (cerca de 1250 x 700 px, menu lateral visivel) nao ha rolagem horizontal, o historico fica visivel ou com indicacao de que existe, e a tela cheia usa a tela inteira.
- **Sessao do Issabel de 24 min (gc_maxlifetime = 1440 s):** abra o painel pelo menu e deixe a tela aberta (pode ser na TV) por mais de 24 minutos sem usar o Issabel; depois recarregue a pagina inteira (F5). Cada carga de `index.php?menu=painel_ramais` executa o modulo e gera um token novo; a sessao do painel e propria (12 h sem uso, renovada pelo WebSocket) e nao depende da sessao do Issabel depois de criada.
  - Esperado: durante os 24 minutos a tela continua atualizando. No F5, se a sessao do Issabel expirou, aparece o login do Issabel e, depois de entrar, abrir o menu mostra a tela sem pedir outra senha. Se a sessao do Issabel ainda existir (o PHP limpa sessoes por amostragem), a tela abre direto. Nunca deve aparecer "Sessao encerrada" com o Issabel logado.
  - Anote tambem se o proprio Issabel troca a pagina inteira pelo login sem F5 (algum aviso periodico do tema): a TV perderia a tela. Anote o tempo e o que apareceu.
- **open_basedir real:** `grep -rn open_basedir /etc/php.ini /etc/php.d /etc/php-fpm.d /etc/httpd/conf.d 2>/dev/null`. Esperado: nada que impeca o PHP de ler `/etc/painel-ramais-issabel.key`. Se o modulo mostrar "integracao com o Issabel desligada (chave ausente ou ilegivel)" com a chave existente (`ls -l /etc/painel-ramais-issabel.key` deve mostrar `painel-ramais asterisk` e `-rw-r-----`), anote o valor de `open_basedir`.
- **Altura do iframe no tema:** com o menu lateral visivel, a altura do iframe preenche a area de conteudo sem barra de rolagem dupla; anote a resolucao e o tema do Issabel. Se sobrar ou faltar espaco, anote quantos pixels.
- **Permissao de /etc/asterisk (manager_custom_perm):** `ls -ld /etc/asterisk /etc/asterisk/manager_custom.conf` e `curl -fsS http://127.0.0.1:8790/api/health`. Esperado: o item `manager_custom_perm` em `deps` nao fica `unknown`. Se ficar `unknown` por falta de `x` do usuario `painel-ramais` na pasta `/etc/asterisk`, anote o modo da pasta e o detalhe do item.
- **Token do modulo contra o daemon:** com o painel aberto pelo menu, a tela carrega os ramais sem pedir senha (o token do PHP foi aceito) e `journalctl -u painel-ramais -n 20 --no-pager` nao mostra `invalid_token`. Como `administrator` a area "Grupos" aparece; como `Operator` nao. Isto confere o emissor do PHP e o `instalar.py menu` na VM, porque nao ha teste automatizado deles.
- **Se for diferente:** anote o texto exato da tela, o navegador e `journalctl -u painel-ramais -n 50 --no-pager`.

## Parte 5: ramais dinamicos e servidor sem ramais (rc.4)

### Etapa 16c. Inventario de ramais e servidor sem ramais (rc.4)

- **Ramal criado:** no Issabel crie um ramal novo (PBX > Extensions) e de Apply Config. Esperado: em ate 10 s ele aparece no painel como "nao registrado" (ainda sem telefone registrado), sem reiniciar nada.
- **Ramal apagado:** apague esse ramal e de Apply Config. Esperado: em ate 10 s ele some do painel e dos totais.
- **Ramal renomeado:** mude o nome (display name) de um ramal e de Apply Config. Esperado: o nome novo aparece no painel (card e busca) ate o fim da passada de nomes (numero de ramais / `name_fill_per_second`, em segundos; ex.: ~40 s com ~800 ramais e o padrao 20 por segundo). Anote se o `EndpointList` traz `Callerid` (item da S10 do `docs/CONTRATOS.md`): o `EndpointList` do Asterisk 18 e o `SIPpeers` nao trazem, entao o nome novo so chega pela passada de nomes.
- **Servidor sem ramais:** numa VM Issabel sem nenhum ramal (checkpoint limpo, ou apague todos), `./instalar.py verificar` e `./instalar.py instalar` nao dao ERRO nem AVISO por isso; a
  linha da tecnologia diz `0 ramais`; o instalador nao espera ramais (termina em ate 60 s); a tela abre mostrando 0 ramais. Confira que o verificar nao conta o endpoint
  interno `dummy_endpoint` nem uma linha de resumo como ramal (antes dava `sip=1` sem ramais).
- **Eventos de recarga (conferir e anotar):** o nome exato dos eventos que o Asterisk 18 do Issabel emite ao recarregar e se `ModuleLoadReport` e `FullyBooted` passam pelo
  `eventfilter` do bloco gerenciado (`grep eventfilter /etc/asterisk/manager_custom.conf`: o bloco da rc.4 tem `Reload`, `ModuleLoadReport` e `FullyBooted`); quantos `Reload` um unico Apply Config gera
  (`journalctl -u painel-ramais` ou a captura `capturar`); o texto exato das respostas de lista vazia (`asterisk -rx 'pjsip show endpoints'` e `sip show peers` sem ramais, e a resposta AMI `Error`
  correspondente); se o `EndpointList` traz `Callerid`; e se existe outro endpoint interno alem de `dummy_endpoint` (resumo da S10 do `docs/CONTRATOS.md`).
- **Upgrade rc.3 -> rc.4:** o `%post` reescreve o bloco gerenciado do `manager_custom.conf` (so entre `; BEGIN painel-ramais` e `; END painel-ramais`) quando o conteudo difere e roda `manager reload`;
  confira depois do upgrade que as duas linhas novas estao no bloco e que o resto do arquivo nao mudou.
- **Se for diferente:** anote o tempo medido, a saida de `asterisk -rx 'pjsip show endpoints'` e de `./instalar.py verificar`.

### Etapa 16d. Grupos pela tela (v0.1.2-rc.5) — NAO HOMOLOGADA (so laboratorio)

Esta etapa ainda nao foi homologada: so vale na VM de laboratorio, com checkpoint, e nada aqui foi confirmado em Issabel real. Entre como `administrator`, salvo onde o item diz Operator. Para os itens com duas abas, abra o painel pelo menu em duas abas (ou dois navegadores) com o mesmo usuario. Anote o que a tela mostra em cada item, mesmo quando bate com o esperado.

1. **Criar grupo por faixa:** em Grupos clique em "Novo grupo", nome `Teste A` e faixas `2000-2099, 3001`. Antes de salvar, a previa mostra quantos ramais existentes entram (confira contra `./instalar.py status` ou contando no Issabel). Salve. Tente de novo com o mesmo nome (`Teste A`, e tambem `teste a`) e com faixa invalida (`2099-2000`, `abc`, `2000-`).
   - **Esperado:** o grupo aparece na lista de grupos com o numero de ramais igual ao da previa; nome duplicado e faixa invalida sao recusados com mensagem na tela e nada e criado nem alterado.
2. **Mover um ramal pelo clique no card:** clique num ramal, no painel de detalhes escolha outro grupo no seletor "Grupo". Antes, na aba B rode no console do navegador `window.__painel` e anote o `epoch`; depois da troca rode de novo.
   - **Esperado:** o ramal muda de grupo na aba A e, na hora, na aba B, sem recarregar nenhuma das duas; o `epoch` de `window.__painel` e o mesmo de antes (a mudanca de grupo nao reinicia o estado do painel).
3. **Mover varios pela selecao:** marque 3 ramais pelo checkbox do card; depois clique num e use Shift+clique em outro para marcar o intervalo. Escolha o grupo de destino.
   - **Esperado:** uma confirmacao de resumo ("mover N ramais para <grupo>", com a origem) aparece antes de aplicar; cancelar nao muda nada; confirmar move todos os selecionados e so eles.
4. **Busca, "Selecionar todos os resultados" e grupo novo:** busque por parte de um nome que de mais ramais do que cabem na tela. Clique em "Selecionar todos os resultados (N)". Role a lista e refine a busca (acrescente uma letra). No seletor de destino escolha "+ Novo grupo", de um nome e confirme.
   - **Esperado:** a selecao continua marcada ao rolar e ao refinar a busca; uma barra fixa mostra "N ramais selecionados" o tempo todo; apos confirmar, o grupo novo e criado e **todos** os N mudam, inclusive os que estavam fora da tela (rolagem virtual): confira a contagem do grupo novo na lista de grupos e abra o grupo rolando ate o fim.
5. **Faixa x movimento manual:** (a) no Issabel crie um ramal cujo numero cai numa faixa de grupo (ex. `2050`) e de Apply Config: ele entra no grupo da faixa em ate 10 s. (b) Mova pela tela outro ramal da faixa para um grupo diferente e depois crie/edite faixas: o movido continua onde o movimento manual o pos. (c) Mova um ramal para "Sem grupo" manualmente: ele fica em "Sem grupo" mesmo estando dentro de uma faixa. (d) Apague um ramal que foi movido manualmente (Apply Config) e recrie com o mesmo numero.
   - **Esperado:** (a) entra no grupo da faixa; (b) a faixa nao mexe em ramal movido manualmente; (c) "Sem grupo" manual vence a faixa; (d) o ramal recriado volta ao mesmo grupo de antes.
6. **Renomear, editar faixas e excluir:** renomeie `Teste A` para `Teste B`; edite as faixas (tire `3001`, acrescente `4000-4010`); exclua o grupo.
   - **Esperado:** o nome novo aparece em todas as abas sem recarregar; a mudanca de faixa move so os ramais que dependem da faixa (os movidos manualmente ficam); ao excluir, pede confirmacao dizendo quantos ramais serao afetados, e depois os ramais manuais vao para "Sem grupo" e os de faixa passam para o grupo da faixa que os cobrir, se houver (senao "Sem grupo").
7. **Operator (nao administrador):** entre pelo menu como usuario do grupo `Operator`.
   - **Esperado na tela:** nao ha botao "Novo grupo", nem seletor "Grupo" no detalhe do ramal, nem checkbox/selecao.
   - **Esperado na API:** chamada direta devolve 403. Na VM (contrato: `numbers` e a lista de ramais; `group` com o nome do grupo, `"Sem grupo"` = manual sem grupo, `null` = volta para a faixa):

```
tok=$(runuser -u painel-ramais -- /usr/bin/painel-ramais token --issabel --sub issabel:user:roteiro-operator --ttl 60s)
OPCK=$(curl -si -H 'Content-Type: application/json' -d "{\"token\":\"$tok\"}" http://127.0.0.1:8790/api/token \
  | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(painel_session=[^;]*\).*/\1/p')
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Cookie: $OPCK" -H 'Content-Type: application/json' \
  -d '{"numbers":["2001"],"group":"Teste B"}' http://127.0.0.1:8790/api/groups/assign
# esperado: 403. Repita com o cookie de painel_login (administrator): esperado 200.
```

   - Confira depois que o ramal `2001` nao mudou de grupo.
8. **Auditoria, CSV/CLI e upgrade:**
   - `sudo -u painel-ramais painel-ramais group audit`: lista quem, o que e quando de cada alteracao feita acima (criar, mover um, mover varios, renomear, editar faixas, excluir), com o usuario (`administrator`) e sem registro das tentativas do Operator que deram 403 como alteracao feita.
   - Importe um CSV com `sudo -u painel-ramais painel-ramais group import <arquivo.csv>` incluindo um ramal movido manualmente e um em "Sem grupo" manual, e mova um ramal pela CLI: a mesma precedencia da tela vale (manual vence a faixa; "Sem grupo" manual vence a faixa) e cada operacao aparece no `group audit`.
   - **Limite de importacao:** importe um CSV com 5.001 linhas de dados e outro com 1.001 faixas no mesmo grupo. Esperado: os dois sao recusados inteiros, com a mensagem do limite (5.000 linhas por importacao; 1.000 faixas por grupo), e `group list` e `group audit` nao mudam.
   - **Administrador rebaixado:** com a tela aberta como `administrator`, tire esse usuario do grupo administrator no Issabel (Sistema > Usuarios) e tente mover um ramal. Esperado: 403 com "Seu usuario nao esta mais no grupo administrator do Issabel", linha `escrita de grupo recusada` no `journalctl -u painel-ramais`, e a tela continua atualizando (a visualizacao nao cai). Devolva o usuario ao grupo: a escrita volta a funcionar sem novo login.
   - **Atencao no upgrade com `--grupos --replace`:** `./instalar.py instalar --grupos arquivo.csv` com substituicao apaga tambem os grupos, as faixas e as atribuicoes manuais criados pela tela. Num servidor que ja usa a tela de grupos, nao passe `--grupos` no upgrade; para importar, use a tela ou `painel-ramais group import` sem `--replace`.
   - **Upgrade rc.4 -> rc.5** (checkpoint "instalado" com a rc.4 e grupos criados antes): depois do upgrade o schema do `painel.db` esta na versao 3, os grupos e as faixas de antes continuam iguais (compare `painel-ramais group list` antes e depois) e existe o backup `painel.db.bak-v2` ao lado do `painel.db` (`ls -l` na pasta de dados do painel). **Rollback** para a rc.4: restaure o `painel.db.bak-v2` como `painel.db` com o servico parado (`systemctl stop painel-ramais`, copiar, instalar a rc.4, `systemctl start painel-ramais`) e confira que o painel sobe e os grupos de antes da rc.5 estao la.
- **Se for diferente:** anote o item, o texto exato da tela ou da mensagem, o codigo HTTP, `journalctl -u painel-ramais -n 50 --no-pager` e, para os itens de auditoria e upgrade, a saida de `painel-ramais group audit` e `painel-ramais group list`.

### Etapa 16e. Novo visual da tela (v0.1.2-rc.5) — NAO HOMOLOGADA (so laboratorio)

Esta etapa ainda nao foi homologada: so vale na VM de laboratorio. Abra o painel pelo menu do Issabel (menu lateral aberto) e anote o que a tela mostra em cada item.

- **Tres colunas e empilhamento:** na area de conteudo do Issabel com o menu lateral aberto a tela mostra tres colunas; estreite a janela (ou use a tela cheia e depois uma janela menor) ate as colunas empilharem. Esperado: tres colunas na largura normal, empilhadas na largura menor, sem rolagem horizontal em nenhum dos dois casos.
- **Grupo inicial:** abra a tela. Esperado: nao ha linha "Todos os grupos" na lista; a tela abre no primeiro grupo da ordenacao ativa (Nome ou Mais problemas), como no design. Exclua o grupo aberto: a tela passa para o primeiro grupo da ordenacao.
- **Dado ausente:** IP, aparelho e latencia de ramal nao registrado mostram "—", como no design.
- **Compacto/Cartoes:** use a alternancia Compacto/Cartoes. Esperado: a lista de ramais troca de formato nos dois sentidos, sem perder a busca, o grupo aberto nem a selecao.
- **Detalhe do ramal:** clique num ramal. Esperado: abre o detalhe com grupo, chamada (quando houver), IP, aparelho (user agent) e latencia.
- **Chamadas ativas do grupo:** com varias chamadas em ramais do mesmo grupo (inclua uma com mais de 20 minutos), abra o grupo. Esperado: a lista fica ordenada da chamada mais longa para a mais curta; o tempo acima de 20 min aparece em laranja e os abaixo disso nao.
- **Indicadores, barras e grafico:** Esperado: indicadores no topo e barras por grupo coerentes com a contagem real (compare com `./instalar.py status`); o grafico de 24 h aparece com dados.
- **Busca e chips de estado:** busque por numero e por nome. Esperado: a busca olha todos os grupos, nao so o aberto; os chips de estado filtram a lista e a contagem de cada chip confere com a lista filtrada.
- **Fontes sem internet:** desligue a saida para a internet da VM/navegador. Abra o DevTools na aba Rede, recarregue a pagina inteira (F5) e filtre por "Fonte"/"Font". Esperado: nenhuma fonte vem de fora, as `.woff2` vem do proprio painel (`/painel-ramais/assets/fonts/...`, status 200) e o Console nao mostra erro de CSP.
- **AMI desconectado:** `systemctl stop asterisk` por alguns segundos. Esperado: indicadores com "—" (nunca 0), faixa de alerta de conexao perdida e ramais em "desconhecido"; ao subir o Asterisk de novo, tudo volta. Suba o Asterisk de novo.
- **Sem o visual antigo:** Esperado: nao ha a barra "PBX / Painel de Ramais" nem o mapa em canvas.
- **10.000 ramais:** num ambiente com 10.000 ramais (ou massa de teste), role a lista inteira. Esperado: a rolagem acompanha o dedo/mouse sem travar; anote qualquer engasgo e o navegador.
- **Conferencia visual lado a lado:** abra `docs/design/painel-ramais-design.dc.html` no Claude Design e o painel na VM, um ao lado do outro, e compare secao por secao: cabecalho, indicadores, grupos, historico, central (Compacto e Cartoes), detalhe do ramal e chamadas ativas. Use `docs/design/desvios.md` como a lista das diferencas conhecidas. Esperado: so aparecem diferencas que estao nessa lista; qualquer diferenca fora dela e defeito (anote a secao, a diferenca e uma captura de cada lado).
- **Se for diferente:** anote o item, o navegador, a largura da janela, uma captura de tela e o Console/Rede do DevTools.

## Parte 6: a prova final do LEIAME

### Etapa 17. Restaurar o checkpoint "limpo" e instalar so pelo LEIAME

- **Comando:** no hipervisor, restaure o checkpoint `limpo`. Feche este roteiro. Abra **somente** o `LEIAME-INSTALACAO.md` (o do bundle da release) e instale seguindo-o do passo 1 ao fim, sem consultar mais nada (nem este roteiro, nem o README). Anote cada ponto em que o LEIAME nao bastou, na tabela abaixo.
- **Resultado esperado:** o painel instalado e funcionando (`./instalar.py status` com AMI conectado e ramais) sem voce ter precisado de informacao de fora do LEIAME.
- **Se for diferente:** cada tropeco e um achado, nao um erro seu: preencha a tabela (onde parou, o que faltava ou estava ambiguo, o que voce fez para seguir e o que o documento deveria dizer).

| Passo do LEIAME | O que faltou ou confundiu | O que voce fez | O que o documento deveria dizer |
|---|---|---|---|
| | | | |

## Parte 7: instalar e atualizar pelo clone (inicializador do instalar.py)

Valida o caminho `git clone` + `./instalar.py instalar`: fora de um pacote (sem o RPM ao lado) o `instalar.py` escolhe a versao, baixa a Release do GitHub, confere o SHA256, extrai em `/root/painel-ramais-<versao>/` e roda o `instalar.py` de la. Dentro do pacote (tar.gz) nada muda. Requer a VM com internet, como root, e pelo menos uma pre-release publicada. Use `$TAG` para a tag da pre-release que esta testando (por exemplo `v0.1.2-rc.5`) e `${TAG#v}` para a versao sem o `v`.

### Etapa 18. Instalar pelo clone, com --pre

- **Comando:** restaure o checkpoint `limpo`. Na VM, como root: `command -v git >/dev/null || dnf install -y git`, depois `git clone https://github.com/fcarvalhosa17/painel-ramais.git && cd painel-ramais`, `ls -l instalar.py` e `./instalar.py instalar --pre; echo "rc=$?"`.
- **Resultado esperado:** `instalar.py` com bit de execucao (`-rwxr-xr-x`). A saida mostra, nesta ordem: `Esta pasta nao e um pacote (sem painel-ramais-*.rpm ao lado)`, `Versao escolhida: v... (ultima pre-release)`, `Baixando painel-ramais-....tar.gz`, `SHA256 conferido: <64 hex>`, `Pacote extraido em /root/painel-ramais-<versao>` e `Executando o instalar.py do pacote: ... instalar.py instalar` (sem `--pre` nessa linha), seguida da instalacao normal ate o resumo com a URL. `rc=0`. Depois: `ls -a /root` mostra `painel-ramais-<versao>/` e nenhum `.painel-ramais-*` sobrando; `cat /root/painel-ramais-<versao>/.sha256-targz` e igual a linha do tar.gz no `SHA256SUMS` da Release (`curl -fsSL https://github.com/fcarvalhosa17/painel-ramais/releases/download/$TAG/SHA256SUMS`); `./instalar.py status` com AMI conectado.
- **Se for diferente:** anote a saida inteira e `/var/log/painel-ramais/instalacao.log`. `Ainda nao ha Release estavel` com `--pre` nao deveria acontecer. `Endereco recusado` indica redirect para host fora da lista (anote o host: pode ser um CDN novo do GitHub que precisa entrar na lista).

### Etapa 19. Reexecutar, atualizar e avisar versao diferente

- **Reaproveitar:** `./instalar.py instalar --versao $TAG --sim; echo "rc=$?"` (mesma tag da etapa 18). Esperado: aparece `Reaproveitando /root/painel-ramais-${TAG#v}: o tar.gz tem o mesmo SHA256 da extracao anterior, mas o conteudo da pasta NAO foi reverificado`, NAO aparece `Baixando ...tar.gz`, o pacote reinstala a mesma versao (`rpm -U --replacepkgs`) e `rc=0`. `--sim` chegou ao pacote (se o pacote reclamasse de argumento desconhecido, o repasse estaria errado).
- **Upgrade pelo clone:** com uma rc anterior instalada (restaure o checkpoint `instalado` da etapa 7 com a versao antiga, ou instale-a com `--versao`), rode `cd ~/painel-ramais && git pull && ./instalar.py instalar --versao $TAG --sim` (ou `--pre` se `$TAG` e a ultima). Esperado: baixa a versao nova, faz o upgrade guardando o RPM anterior, `rpm -q painel-ramais` mostra a versao nova, `rc=0`, `./instalar.py rollback --sim` continua funcionando.
- **Aviso de versao no clone:** com o clone num commit diferente da versao instalada (por exemplo `git checkout main` com uma rc instalada), rode `./instalar.py status` e `./instalar.py verificar`. Esperado: antes da saida normal, `AVISO: este clone esta em '...' e a versao instalada e v... (...)`; o comando continua e nao baixa nada. Sem git, ou com `git clone --depth 1` (sem tags): `AVISO: nao deu para comparar a versao deste clone com a instalada`. Com o clone exatamente na tag instalada (`git checkout $TAG`): nenhum aviso.
- **Dentro do pacote nada muda:** `cd /root/painel-ramais-${TAG#v} && ./instalar.py verificar` nao imprime `AVISO` de clone nem baixa nada; `./instalar.py instalar --pre` ali dentro termina com `FALHA`/uso invalido (`rc=3`, `--pre e --versao so valem fora de um pacote`).
- **Se for diferente:** anote o comando, a saida e o `rc`.

### Etapa 20. Casos negativos do inicializador

Em todos, o criterio comum e: nada instalado (`rpm -q painel-ramais` inalterado), nenhuma pasta nova `/root/painel-ramais-*` (exceto onde dito) e nenhum `/root/.painel-ramais-*` sobrando (`ls -a /root`).

- **Sem Release estavel e sem --pre** (so vale enquanto o repositorio so tem pre-releases; se ja existe estavel, anote "nao se aplica"): `./instalar.py instalar; echo "rc=$?"`. Esperado: `FALHA: Ainda nao ha Release estavel publicada (so pre-releases)...` sugerindo `--pre`, `rc=2`, nada baixado alem da lista de Releases.
- **Tag invalida:** `./instalar.py instalar --versao v1.2`, `--versao 0.1.2-rc.4` (sem o `v`), `--versao '../../etc'`, `--versao 'v0.1.2-rc.4;id'` e `--pre --versao $TAG`. Esperado em todos: `rc=3`, mensagem `--versao invalida` (ou `Use --pre OU --versao`), nenhuma conexao de rede (nao aparece `Baixando`).
- **SHA256 adulterado:** crie uma Release de TESTE descartavel (por exemplo `v0.0.1-rc.9`, marcada como pre-release, com um arquivo qualquer chamado `painel-ramais-0.0.1-rc.9.tar.gz` e um `SHA256SUMS` com a linha desse arquivo mas com um hash trocado). Rode `./instalar.py instalar --versao v0.0.1-rc.9; echo "rc=$?"`. Esperado: `FALHA: SHA-256 do painel-ramais-0.0.1-rc.9.tar.gz nao confere ... Nada foi extraido nem instalado`, `rc=2`, e NAO existe `/root/painel-ramais-0.0.1-rc.9`. Variante: `SHA256SUMS` sem a linha do tar.gz: `nao consta (ou tem hash invalido) no SHA256SUMS`, `rc=2`. Apague a Release de teste depois.
- **Sem internet:** `echo '127.0.0.1 github.com api.github.com' >> /etc/hosts` (ou desconecte o adaptador de rede no hipervisor; nao use `ip link set down` pelo ssh), depois `./instalar.py instalar --versao $TAG; echo "rc=$?"` e, sem `--versao`, `./instalar.py instalar --pre`. Esperado: `FALHA: Sem acesso ao GitHub (<motivo>). Nada foi instalado.` seguido do caminho offline com os comandos `curl -fLO https://github.com/fcarvalhosa17/painel-ramais/releases/download/$TAG/painel-ramais-${TAG#v}.tar.gz`, `curl -fLO .../SHA256SUMS`, `sha256sum -c SHA256SUMS --ignore-missing`, `scp ... root@<servidor>:/root/` e `cd /root && tar xzf ... && cd painel-ramais-${TAG#v} && ./instalar.py instalar`; com `--versao` a tag vem preenchida, sem ele aparecem `vX.Y.Z`/`X.Y.Z` e o link da lista de Releases. `rc=2`. Remova a linha do `/etc/hosts` depois e confira com `grep github /etc/hosts`. Teste tambem o caminho offline completo: siga esses comandos num computador com internet e instale na VM pelo tar.gz copiado.
- **Nao-root:** `cp -r ~/painel-ramais /tmp/clone-teste && chmod -R a+rX /tmp/clone-teste && runuser -u nobody -- /usr/libexec/platform-python /tmp/clone-teste/instalar.py instalar --pre; echo "rc=$?"`. Esperado: `FALHA: Rode como root`, `rc=3`, sem `Baixando` e sem nada novo em `/root`. Apague `/tmp/clone-teste`.
- **Destino existente com conteudo diferente:** `mkdir /root/painel-ramais-${TAG#v} && touch /root/painel-ramais-${TAG#v}/arquivo-meu && ./instalar.py instalar --versao $TAG; echo "rc=$?"`. Esperado: `FALHA: /root/painel-ramais-... ja existe com conteudo diferente ... Nao vou sobrescrever`, `rc=2`, sem `Baixando ...tar.gz`, e `arquivo-meu` continua la, intacto. Apague a pasta depois (`rm -r /root/painel-ramais-${TAG#v}`).
- **Se for diferente:** anote o caso, a saida inteira e o `rc`. Qualquer caso que instale algo, ou que deixe `/root/.painel-ramais-*`, e defeito grave.

## Entrega

Devolva ao time: o `homologacao-*.tar.gz` de cada rodada (so depois de rodar o analisador e olhar as `[SUSPEITA]`), a tabela da etapa 17, as anotacoes dos passos que ficaram diferentes do esperado, as linhas `V1-i`, `V1-ii` e `M1` (ficam no `roteiro.log` do pacote) e o resultado de `systemd-analyze security painel-ramais` (esta em `pre.txt`; no Rocky 8 o comando pode nao existir e a mensagem de erro e o resultado).
