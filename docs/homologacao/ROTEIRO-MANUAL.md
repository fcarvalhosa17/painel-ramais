# Roteiro manual de homologacao (Issabel 5 / Rocky)

Este roteiro e para quem instala na VM e acompanha as chamadas. Ninguem do time acessa a VM: tudo o que voce ve
deve ser anotado (o `homologar` guarda as suas anotacoes com a hora) e o pacote gerado volta para analise.
Texto sem acentos de proposito: e o mesmo que o `./instalar.py homologar` mostra no terminal.

Cada etapa tem tres campos: **Comando**, **Resultado esperado**, **Se for diferente**. Os **Passo N** da etapa 9 sao os
mesmos passos (titulo e texto) que o `homologar` mostra, um por vez.

## 0. Antes de comecar

Voce precisa de:

- a VM Issabel 5 em Rocky 8 ou 9 com acesso root e dois checkpoints planejados: **limpo** (agora, antes de instalar) e **instalado** (depois da etapa 7);
- tres ramais de teste registrados (A, B e C), um tronco (T) com um numero de entrada, um celular seu (X), uma fila (F) com um ramal agente e um ring group (R);
- um segundo terminal ssh na VM (o primeiro fica preso no `homologar`);
- o computador onde o pacote sera analisado, com este repositorio e Python 3.

Legenda usada em todo o roteiro: **A, B, C** = os tres ramais; **T** = tronco; **X** = seu celular; **F** = fila; **R** = ring group.

Variaveis da versao (troque pelos valores da release que vai testar):

```
export TAG=v0.1.2-rc.1
export VERSAO_RPM=0.1.2-0.1.rc.1    # versao-release do RPM: rc = release 0.1.rc.N; a final = 0.1.2-1
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
```

  O bundle (`painel-ramais-0.1.2-rc.1.tar.gz`) ja traz o RPM dentro e o `instalar.py` o usa; o RPM avulso (`painel-ramais-${VERSAO_RPM}.x86_64.rpm`) so e preciso para conferir o pacote sozinho (`rpm -qp`) ou instalar sem o instalador. Nesse caso baixe tambem: `gh release download "$TAG" -R fcarvalhosa17/painel-ramais -p 'painel-ramais-*.rpm'` ou `curl -fLO "$BASE/painel-ramais-${VERSAO_RPM}.x86_64.rpm"`.
- **Resultado esperado:** na pasta atual ficam o tar.gz do bundle (e o RPM, se pedido) e o `SHA256SUMS`, sem erro de rede.
- **Se for diferente:** `404` = a TAG nao existe ou o arquivo ainda nao foi publicado; confirme `echo $TAG` e a lista de arquivos da release. Sem internet na VM: baixe no seu computador e copie com `scp` (veja o LEIAME-INSTALACAO, passo 1b).

### Etapa 3. Conferir o SHA256

- **Comando:** `sha256sum -c --ignore-missing SHA256SUMS` (o `SHA256SUMS` lista o RPM e o tar.gz; `--ignore-missing` pula o que voce nao baixou)
- **Resultado esperado:** uma linha `OK` para cada arquivo baixado, por exemplo `painel-ramais-0.1.2-rc.1.tar.gz: OK` (e `painel-ramais-0.1.2-0.1.rc.1.x86_64.rpm: OK` se baixou o RPM), e nenhuma linha `FAILED`.
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

- **Comando:** `./instalar.py instalar` (interativo: tecnologia `auto`, usuario admin, senha com 10 caracteres ou mais, CSV de grupos opcional)
- **Resultado esperado:** `== Instalacao ==`, a verificacao sem erro, `Aguardando o painel (ate 60s)...` e o `== Resumo ==` com `Versao`, `Tecnologia`, `Ramais` (total maior que 0), `Grupos`, `Acesso: https://<ip>/painel-ramais/` e `Log`. Codigo de saida 0.
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

- **Comando:** com `TAG` e `VERSAO_RPM` da versao mais nova, repita as etapas 2 a 4 (baixar, `sha256sum -c --ignore-missing SHA256SUMS`, extrair numa pasta nova; o RPM vem dentro do bundle, nao baixe separado) e rode, na pasta nova: `./instalar.py instalar` (interativo ou `--respostas`; o usuario admin ja existe).
- **Resultado esperado:** `Upgrade <versao antiga> -> <versao nova>.`, o `== Resumo ==` com a versao nova, `AVISO: usuario '<admin>' ja existe; senha mantida`, dados e grupos preservados, painel de volta em ate 60 s.
- **Se for diferente:** do rc para a versao final (`0.1.2-0.1.rc.1` para `0.1.2-1`) e entre rcs (`rc.1` para `rc.2`, `rc.2` para `rc.10`) o instalador trata como upgrade. `Versao instalada maior que a do bundle` so deve aparecer ao tentar o contrario (final para rc). Qualquer outra mensagem: anote o texto exato e `rpm -q painel-ramais`.

### Etapa 12. Rollback

- **Comando:** `./instalar.py rollback` (responda `s`) e depois `./instalar.py status`
- **Resultado esperado:** `Plano: reinstalar painel-ramais-<versao anterior>...rpm (rpm -U --oldpackage), preservando dados.`, `Rollback concluido.` e o `status` com a versao anterior, servico ativo, AMI conectado. Se o esquema do banco mudou entre as versoes, o instalador avisa que o historico recente sera perdido e pede confirmacao.
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
- **Resultado esperado:** o navegador avisa que o certificado nao e confiavel (autoassinado; por IP ou por nome o aviso e esperado, inclusive "nome nao confere"); depois de aceitar, `/painel-ramais/` mostra uma resposta JSON do daemon (`{"success":false,...}`, nao uma pagina de erro do Apache) e `/painel-ramais/api/health` mostra a pagina 404 do Apache, sem JSON (a saude so existe em 127.0.0.1).
- **Se for diferente:** pagina de erro do Apache em `/painel-ramais/`: `httpd -t`, `getsebool httpd_can_network_connect` (deve estar `on`) e `curl -s http://127.0.0.1:8790/api/health` na VM. Nome que nao abre e IP que abre: e DNS/hosts, nao o painel. JSON em `/api/health`: anote (o proxy estaria expondo a rota interna).

## Parte 5: a prova final do LEIAME

### Etapa 17. Restaurar o checkpoint "limpo" e instalar so pelo LEIAME

- **Comando:** no hipervisor, restaure o checkpoint `limpo`. Feche este roteiro. Abra **somente** o `LEIAME-INSTALACAO.md` (o do bundle da release) e instale seguindo-o do passo 1 ao fim, sem consultar mais nada (nem este roteiro, nem o README). Anote cada ponto em que o LEIAME nao bastou, na tabela abaixo.
- **Resultado esperado:** o painel instalado e funcionando (`./instalar.py status` com AMI conectado e ramais) sem voce ter precisado de informacao de fora do LEIAME.
- **Se for diferente:** cada tropeco e um achado, nao um erro seu: preencha a tabela (onde parou, o que faltava ou estava ambiguo, o que voce fez para seguir e o que o documento deveria dizer).

| Passo do LEIAME | O que faltou ou confundiu | O que voce fez | O que o documento deveria dizer |
|---|---|---|---|
| | | | |

## Entrega

Devolva ao time: o `homologacao-*.tar.gz` de cada rodada (so depois de rodar o analisador e olhar as `[SUSPEITA]`), a tabela da etapa 17, as anotacoes dos passos que ficaram diferentes do esperado e o resultado de `systemd-analyze security painel-ramais` (esta em `pre.txt`; no Rocky 8 o comando pode nao existir e a mensagem de erro e o resultado).
