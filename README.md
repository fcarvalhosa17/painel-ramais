Copyright (c) 2026 Fabio Carvalhosa Sanchez. Todos os direitos reservados.

# Painel de Ramais para Issabel 5

## 1. O que é

O Painel de Ramais substitui o FOP (Operator Panel) no Issabel 5. Ele **só mostra** informações: não faz nenhuma ação na central
(não liga, não desliga, não transfere). Fica dentro do menu **PBX** do Issabel e usa o login do próprio Issabel: não existe usuário
nem senha do painel para o uso do dia a dia.

## 2. Como funciona

- Roda como um serviço próprio (`painel-ramais`), separado do Issabel.
- Abre **uma única** conexão com o AMI do Asterisk. Essa conexão só usa comandos de consulta (uma lista fixa no código) e só recebe
  os eventos que interessam ao painel (filtro `eventfilter` na conta AMI do painel).
- O estado ao vivo (ramais, chamadas e filas) fica em memória. No disco, em `/var/lib/painel-ramais/painel.db`, ficam só grupos,
  nomes de fila, sessões, o cache de nomes dos ramais, o histórico e o relatório de registros.
- A tela recebe o estado completo uma vez e, depois, só o que mudou (um pacote a cada 250 ms, e só quando há mudança).
- O acesso passa pelo Apache do Issabel (HTTPS, porta 443). O serviço escuta só em `127.0.0.1:8790`: nenhuma porta nova fica aberta
  para a rede.

A tela tem três abas:

- **Ramais**: estado de cada ramal em tempo real, organizado em grupos. Um ramal pode estar em mais de um grupo. Inclui o bloco
  "Instabilidade de registro (24 h)", com os ramais que mais perderam o registro.
- **Registros**: quedas e registros de cada ramal em 24 h, 7 dias ou 30 dias, com gráfico (linha do tempo) e exportação em CSV.
- **Chamadas**: chamadas entrantes, saintes e internas ao vivo, e o painel de filas (quem está aguardando e há quanto tempo,
  agentes, atendidas, abandonadas e nível de serviço).

## 3. Requisitos

- Rocky Linux 8 com Issabel 5 (Asterisk 13 ou mais novo).
- Ramais em PJSIP ou SIP: o painel detecta sozinho qual tecnologia está em uso.
- Acesso como root ao PBX (SSH).
- Para instalar e atualizar pelo `git`, o PBX precisa acessar em HTTPS (porta 443): `github.com` (clone e download da release),
  `api.github.com` (escolha da versão), `objects.githubusercontent.com` e `release-assets.githubusercontent.com` (de onde o GitHub
  entrega os arquivos). Sem esse acesso, siga a seção 5.
- Relógio do servidor sincronizado pelo serviço `chronyd`. Os horários gravados no relatório de registros vêm do relógio do PBX:
  com o relógio errado, os horários do relatório ficam errados. Para conferir: `timedatectl` deve mostrar
  `System clock synchronized: yes` e `systemctl status chronyd` deve mostrar `active (running)`.
  O painel também mostra tempos (fora do ar há, último registro, períodos), que ficam errados com o relógio errado. O
  `./instalar.py verificar` e o `status` avisam, sem bloquear nada, quando o relógio não está sincronizado. Para corrigir:
  `systemctl enable --now chronyd && chronyc makestep`.

O `./instalar.py verificar` confere o resto antes de instalar (versão do Asterisk, Apache com `mod_proxy_wstunnel`, porta 8790 livre,
espaço em disco, SELinux, `manager.conf` incluindo o `manager_custom.conf`) e diz como corrigir cada item.

## 4. Instalação (PBX com internet)

Tudo como **root** no PBX:

```sh
cd /root
command -v git >/dev/null || dnf install -y git
git clone <URL deste repositorio> painel-ramais
cd painel-ramais
./instalar.py verificar
./instalar.py instalar --pre
./instalar.py status
```

O que cada passo faz:

1. `verificar`: só lê, não muda nada. Mostra linhas `[OK   ]`, `[AVISO]` e `[ERRO ]`; cada item com problema traz um
   "Como corrigir". O relatório fica salvo em `/var/log/painel-ramais/verificacao-<data>.txt`. Corrija todo `ERRO` antes de seguir:
   com `ERRO`, o `instalar` para sem alterar nada.
2. `instalar --pre`: escolhe a última pré-release publicada no GitHub, baixa o pacote e o `SHA256SUMS`, confere o SHA256, extrai em
   `/root/painel-ramais-<versao>/` e roda o instalador de dentro do pacote. Não faz perguntas. No fim mostra um bloco `== Resumo ==`
   com versão, tecnologia, ramais e grupos.
3. `status`: mostra versão, serviço, item do menu, AMI, ramais por estado e cada dependência (seção 8).

Sobre o `--pre`: hoje só existem pré-releases, marcadas no GitHub como "NÃO HOMOLOGADA. Uso exclusivo em laboratório.". Sem o
`--pre`, o comando procura uma versão estável, não acha e pede o `--pre`. Para instalar uma versão específica:
`./instalar.py instalar --versao v0.1.2-rc.9`.

Tudo o que o instalador executa fica em `/var/log/painel-ramais/instalacao.log` (a senha nunca é registrada).

### Onde o painel aparece e quem acessa

- No Issabel, menu **PBX > Painel de Ramais**.
- Têm acesso os grupos **administrator** e **Operator** do Issabel. Outros grupos podem ser liberados nas permissões de grupo do
  próprio Issabel.
- Renomear uma fila (aba Chamadas) é permitido só para quem está no grupo administrator.
- Abrir o endereço do painel direto no navegador, fora do menu, não funciona: a tela pede para abrir pelo menu PBX do Issabel.

### Acesso de emergência

Para o caso de o menu do Issabel não estar disponível, o instalador cria o usuário `emergencia`, com uma senha aleatória gravada em
`/etc/painel-ramais/admin-emergencia` (só root lê; atualizar o painel não troca a senha).

- Endereço: `https://<ip-do-servidor>/painel-ramais/emergencia/`, usuário `emergencia`.
- Ler a senha: `cat /etc/painel-ramais/admin-emergencia`

## 5. Instalação sem internet no PBX

1. Num computador com internet, abra https://github.com/fcarvalhosa17/painel-ramais/releases, escolha a versão e baixe dois arquivos:
   `painel-ramais-<versao>.tar.gz` e `SHA256SUMS` (exemplo de `<versao>`: `0.1.2-rc.9`).
2. Copie os dois para o `/root` do PBX, por exemplo: `scp painel-ramais-<versao>.tar.gz SHA256SUMS root@<ip-do-pbx>:/root/`
3. No PBX, como root:

```sh
cd /root
sha256sum -c SHA256SUMS --ignore-missing    # a linha do tar.gz deve terminar em ": OK"
tar xzf painel-ramais-<versao>.tar.gz
cd painel-ramais-<versao>
./instalar.py verificar
./instalar.py instalar
./instalar.py status
```

- Se o `sha256sum` não disser `OK`, não instale: baixe de novo.
- Dentro do pacote não se usa `--pre` nem `--versao`: a versão é a do pacote, e o instalador recusa essas opções.
- O pacote não precisa de internet nem de `pip`.
- O `SHA256SUMS` protege contra download corrompido ou incompleto. Ele não protege contra uma release adulterada (quem troca um
  arquivo troca o outro): baixe só da página oficial acima.

## 6. Atualização

Antes de atualizar, faça uma cópia do banco do painel. Como root:

```sh
systemctl stop painel-ramais.socket painel-ramais.service
runuser -u painel-ramais -- sh -c 'cd /var/lib/painel-ramais && B=painel.db.antes-$(date +%Y%m%d-%H%M) && cp -p painel.db $B && for s in -wal -shm; do [ -e painel.db$s ] && cp -p painel.db$s $B$s; done; ls -l painel.db*'
cd /root/painel-ramais && git pull && ./instalar.py instalar --pre
systemctl is-active painel-ramais.socket || systemctl start painel-ramais.socket
```

A cópia fica como `painel.db.antes-AAAAMMDD-HHMM` (com a data e a hora do dia, para uma segunda atualização no mesmo dia não sobrescrever a primeira cópia) na mesma pasta do banco. Por que fazer assim:

- O serviço e o socket (`painel-ramais.socket`, que guarda a porta do painel e religaria o serviço sozinho se alguém abrisse a tela) são
  parados antes para ninguém gravar no banco durante a cópia. Assim a cópia fica consistente.
- O banco é SQLite: dados recentes podem estar ainda no arquivo `painel.db-wal`. Por isso a cópia leva junto o `-wal` e o `-shm`,
  quando existem.
- `cp -p` mantém dono, permissões e datas. A cópia roda como o usuário do serviço (`runuser -u painel-ramais`) para não deixar
  arquivo de root dentro da pasta do painel.
- O `ls -l` no fim deve listar a cópia (e o `-wal` e o `-shm`, se havia) com o mesmo tamanho do original.

Com o socket parado a tela do painel fica fora do ar durante a cópia; é o esperado. O `instalar.py` religa o socket e o serviço no fim.
A última linha do bloco só confere isso: o socket deve voltar a `active`.

Depois da cópia:

- Não precisa religar nada: o `instalar.py` religa o socket e o serviço sozinho no fim da instalação.
- Se o `instalar.py` parar antes de instalar o pacote (por exemplo, `verificar` com `ERRO` ou sem acesso ao GitHub), o serviço
  continua parado. Religue com `systemctl start painel-ramais.socket painel-ramais.service` e corrija o problema antes de tentar de novo.
- O banco é convertido para o formato da versão nova na primeira subida, e o próprio painel guarda um backup automático
  (`painel.db.bak-v<N>`), usado pelo rollback (seção 7).
- Atualizando da rc.8 para a rc.9, o banco passa do esquema 5 para o 6 e o painel guarda sozinho `painel.db.bak-v5` na primeira
  subida. Quem vem direto da rc.6 tem o `painel.db.bak-v3` e o banco passa do esquema 3 para o 6 numa subida só.
- As telas abertas precisam ser abertas de novo pelo menu: a atualização encerra as sessões antigas.
- Confira com `./instalar.py status` (seção 8).

PBX sem internet: faça a mesma cópia e, no lugar da última linha, siga a seção 5 com o pacote da versão nova. O `instalar.py` do
pacote faz a atualização.

## 7. Voltar versão e remover

### Voltar para a versão anterior (rollback)

```sh
cd /root/painel-ramais
./instalar.py rollback
```

- Volta para a versão que estava instalada antes da última atualização. O instalador guardou esse pacote em
  `/var/lib/painel-ramais-instalador/` e confere o SHA-256 dele antes de usar. Sem versão anterior guardada, o comando não muda nada.
- Antes de executar, mostra o plano e pede confirmação. No fim, mostra o `status`.
- Se o banco já está num formato mais novo do que a versão anterior entende, o rollback troca o banco pelo backup automático feito
  na atualização. Da rc.9 para a rc.8 é o `painel.db.bak-v5`. O banco atual não é apagado: fica guardado como
  `painel.db.rollback-<data>`. Sem esse backup, o rollback não mexe em nada.
- Sobre o `painel.db.bak-v3`: é a cópia do banco da rc.6 feita pela rc.8 na primeira subida. Só serve para ir da rc.8 para a rc.6
  e não é usado a partir da rc.9.
- Ao voltar da rc.9 para a rc.8 **perde-se**: o estado de registro persistente ("fora do ar há" e "último registro") e tudo o que foi
  gravado depois da atualização.
- Ao voltar da rc.8 para a rc.6 (só quando a rc.8 é a versão anterior guardada) **perde-se**: os nomes de fila, os vínculos de ramal em mais de um grupo, o relatório de registros
  e tudo o que foi gravado depois da atualização (histórico, sessões, usuários e grupos). O instalador avisa isso antes de pedir a
  confirmação.

### Restaurar a copia manual do painel.db

Use só se a atualização deu errado e você quer o banco exatamente como estava na cópia feita na seção 6. Isso **perde tudo o que foi
gravado depois da cópia**. Como root (troque `AAAAMMDD-HHMM` pela data e hora da sua cópia):

```sh
systemctl stop painel-ramais.socket painel-ramais.service
cd /var/lib/painel-ramais
C=painel.db.antes-AAAAMMDD-HHMM
R=painel.db.antes-restauracao-$(date +%Y%m%d-%H%M)
ls -l $C*
for s in "" -wal -shm; do [ -e painel.db$s ] && mv painel.db$s $R$s; done
runuser -u painel-ramais -- sh -c 'for s in "" -wal -shm; do [ -e "$1$s" ] && cp -p "$1$s" "painel.db$s"; done; ls -l painel.db*' sh "$C"
systemctl start painel-ramais.socket painel-ramais.service
systemctl is-active painel-ramais.socket painel-ramais.service
cd /root/painel-ramais && ./instalar.py status
curl -s -w '\nHTTP %{http_code}\n' http://127.0.0.1:8790/api/health
```

- O `ls -l $C*` tem de listar a cópia (e o `-wal` e o `-shm`, se foram copiados). Se não listar nada, pare: não há o que restaurar.
- O banco atual não é apagado: vira `painel.db.antes-restauracao-AAAAMMDD-HHMM` (com o `-wal` e o `-shm`, se havia). Os arquivos `-wal` e `-shm`
  atuais saem do lugar de propósito: um `-wal` que não é da cópia, deixado ao lado do banco restaurado, pode ser reaplicado sobre ele e
  misturar dados. Só voltam ao lugar os `-wal` e `-shm` que existiam na cópia.
- Depois do `ls -l` final, `painel.db` (e `-wal`/`-shm`, se voltaram) deve ter dono `painel-ramais` e grupo `painel-ramais`. Se não
  tiver: `chown painel-ramais:painel-ramais painel.db*`.
- Os dois serviços ficam habilitados no boot: o socket guarda a porta e o serviço (que depende do socket) mantém a coleta do AMI.
  Por isso sobem os dois, e o `is-active` deve responder `active` duas vezes.
- Se o banco restaurado for de um formato mais antigo que o painel instalado, o painel o converte de novo na subida e guarda um novo
  `painel.db.bak-v<N>`. Se for de um formato mais novo, o painel recusa abrir o banco (volte antes a versão, seção 7).

### Remover o painel

```sh
./instalar.py remover           # remove o pacote; mantém os dados, a configuração e o usuário de sistema
./instalar.py remover --purge   # também apaga /var/lib/painel-ramais, /etc/painel-ramais e /var/lib/painel-ramais-instalador
```

- Os dois pedem confirmação. O `--purge` ainda pede que você digite o nome do servidor (o mesmo que o comando `hostname` mostra);
  com o nome errado, os dados ficam.
- A remoção do pacote tira o serviço, o arquivo do painel no Apache, o item do menu do Issabel e a seção do painel no
  `manager_custom.conf` (e faz `manager reload`).
- Não são desfeitos: o usuário de sistema `painel-ramais` (para apagar: `userdel painel-ramais`) e o ajuste do SELinux
  `httpd_can_network_connect`.
- Para os comandos que pedem confirmação, a opção `--sim` aceita a confirmação sem perguntar (uso em script).

## 8. Como conferir se está funcionando

No PBX, como root:

```sh
./instalar.py status
curl -s -w '\nHTTP %{http_code}\n' http://127.0.0.1:8790/api/health
systemctl status painel-ramais
journalctl -u painel-ramais -n 50 --no-pager
```

- `./instalar.py status` (rode de dentro de `/root/painel-ramais`): socket e serviço `ativo`, `Daemon: ... (status ok)`,
  `AMI: conectado=True` e cada dependência `ok`. Termina com código 0 quando tudo está certo e 2 quando não está.
- `curl .../api/health`: responde `HTTP 200` com `"status":"ok"`. `HTTP 503` com `"status":"degraded"` quer dizer AMI fora do ar ou
  alguma dependência com problema (veja a lista `deps`: o `detail` de cada item explica o problema). Campos da rc.8 que valem olhar: `live_calls`
  (chamadas ao vivo da aba Chamadas), `queues` (filas; `unsupported: true` quando o Asterisk não tem o módulo de filas) e
  `registrations` (gravador do relatório; `lost_events` deve ficar em 0). Da rc.9: `queues.events_received` (quantos eventos de
  fila chegaram, por tipo), `queue.resyncs` (ressincronizações por estouro) e `registrations.decision_lines` (linhas de decisão de
  contagem), entre outros; a lista completa está em `docs/CONTRATOS.md`. Esse endereço só responde no próprio PBX.
- `systemctl status painel-ramais`: deve mostrar `active (running)`.
- `journalctl -u painel-ramais`: log do serviço. Se a configuração estiver errada, o log diz qual chave.
- Filtro de eventos AMI: o bloco do painel em `/etc/asterisk/manager_custom.conf` tem 29 linhas `eventfilter`. O `verificar` e o
  `status` avisam se o bloco do servidor estiver incompleto. Para conferir:
  `sed -n '/^; BEGIN painel-ramais/,/^; END painel-ramais/p' /etc/asterisk/manager_custom.conf | grep -c '^eventfilter'` deve
  mostrar 29. Se as filas não atualizam, rode `./instalar.py status` (filtro AMI e eventos de fila recebidos) e, se faltar evento,
  `./instalar.py instalar` e `systemctl restart painel-ramais`.
- Se o item sumir do menu do Issabel (por exemplo, depois de uma atualização do Issabel): `./instalar.py menu`.

Tabela de problemas comuns e onde olhar: `docs/referencia-tecnica.md`, seção 7.

## 9. Comportamentos normais que parecem defeito

- **Contagem de registros pausa por 120 s depois de um reinício real do Asterisk.** As quedas e registros dos 120 s seguintes não
  entram no ranking de instabilidade: ficam marcados como evento do sistema. Assim, um reinício não aparece como se todos os ramais
  tivessem caído. Reinício do painel ou volta da conexão AMI sem o Asterisk ter reiniciado não pausa a contagem.
- **Ramais como "desconhecido".** Com o AMI fora do ar, os ramais aparecem como "desconhecido", nunca como "não registrado". O painel
  não sabe o estado real enquanto não houver AMI.
- **Números das filas zerados ou baixos.** Atendidas, abandonadas e nível de serviço contam desde o último reinício do Asterisk (ou
  desde que as estatísticas da fila foram zeradas). A tela avisa isso.
- **Relatório de registros sem dados antigos.** O relatório só tem dados desde que a versão com o relatório foi instalada; não existe
  histórico anterior. A tela mostra "Dados desde <data>". Os dados ficam guardados por 30 dias.
- **Aba Registros (rc.9).** As colunas "Fora do ar há" e "Último registro" mostram o estado atual de cada ramal. A ordem padrão tem
  três faixas, e o filtro "Ocultar fora desde antes da instalação" esconde quem já estava fora do ar antes de o painel existir.
  "Fora desde antes da instalação do painel" usa a data do "Dados desde".
- **Aba Chamadas (rc.9).** Canais auxiliares (como o ChanSpy) não contam como chamada, e a fila de nome `default` é ignorada.
- **Tela de Ramais (rc.9).** A coluna de grupos mostra "X não registrados" e, quando houver, "Y inalcançáveis".
- **Nomes dos ramais vazios logo depois de instalar.** Os nomes são buscados em segundo plano, 20 por segundo, para não sobrecarregar
  o Asterisk. Com muitos ramais leva alguns minutos; depois ficam guardados e aparecem logo após um reinício.
- **Lista de filas vazia.** Em Asterisk sem o módulo de filas, a lista fica vazia, sem erro.

## 10. O que o painel faz e não faz no servidor

O painel só observa a central. Ele não liga, não desliga, não transfere, não grava e não recarrega o Asterisk. Comandos fora da
lista fixa de consulta são recusados pelo próprio painel antes de chegar ao Asterisk.

De onde vêm os dados:
- Ramais, chamadas e filas vêm só do AMI do Asterisk.
- O painel não lê o MySQL do Issabel.
- Do Issabel ele lê, em modo somente leitura, apenas dois arquivos: o cadastro do menu (`/var/www/db/menu.db`, para saber se o item
  do menu existe) e as permissões (`/var/www/db/acl.db`, para saber quem é do grupo administrator).

O que a instalação muda no servidor (a remoção, seção 7, desfaz tudo menos o ajuste do SELinux):
- Asterisk: a única escrita é a seção do próprio painel em `/etc/asterisk/manager_custom.conf`, entre `; BEGIN painel-ramais` e
  `; END painel-ramais`. O `manager.conf` não é alterado. O `manager_custom.conf` passa a ficar sem acesso para "outros"
  (`chmod o-rwx`), porque contém a senha AMI do painel.
- Apache: cria o arquivo próprio `/etc/httpd/conf.d/painel-ramais.conf`.
- Menu do Issabel: registra o item **Painel de Ramais** no menu PBX com a ferramenta do próprio Issabel (`issabel-menumerge`) e
  instala o módulo em `/var/www/html/modules/painel_ramais/`.
- SELinux: com o SELinux em modo Enforcing, liga o `httpd_can_network_connect` (a remoção não desliga).
Fora isso, nada da configuração do Issabel é alterada.

## 11. Para saber mais

- `docs/referencia-tecnica.md`: configuração (`config.yaml`), CLI do serviço, formato do CSV de grupos, API, segurança, tabela de
  problemas comuns, roteiro de ensaio, relatório de carga (histórico) e desenvolvimento.
- `docs/CONTRATOS.md`: contratos exatos da API, do banco e do instalador (vale em caso de divergência).
- `packaging/LEIAME-INSTALACAO.md`: opções avançadas do instalador (`--grupos`, `--respostas`, `--tecnologia`) e instalação em
  vários servidores.
- `docs/adr/`: decisões de arquitetura.
