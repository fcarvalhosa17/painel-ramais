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
`./instalar.py instalar --versao v0.1.2-rc.8`.

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
   `painel-ramais-<versao>.tar.gz` e `SHA256SUMS` (exemplo de `<versao>`: `0.1.2-rc.8`).
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
systemctl stop painel-ramais.service
runuser -u painel-ramais -- sh -c 'cd /var/lib/painel-ramais && B=painel.db.antes-$(date +%Y%m%d) && cp -p painel.db $B && for s in -wal -shm; do [ -e painel.db$s ] && cp -p painel.db$s $B$s; done; ls -l painel.db*'
cd /root/painel-ramais && git pull && ./instalar.py instalar --pre
```

A cópia fica como `painel.db.antes-AAAAMMDD` (com a data do dia) na mesma pasta do banco. Por que fazer assim:

- O serviço é parado antes para ninguém gravar no banco durante a cópia. Assim a cópia fica consistente.
- O banco é SQLite: dados recentes podem estar ainda no arquivo `painel.db-wal`. Por isso a cópia leva junto o `-wal` e o `-shm`,
  quando existem.
- `cp -p` mantém dono, permissões e datas. A cópia roda como o usuário do serviço (`runuser -u painel-ramais`) para não deixar
  arquivo de root dentro da pasta do painel.
- O `ls -l` no fim deve listar a cópia (e o `-wal` e o `-shm`, se havia) com o mesmo tamanho do original.

Cuidado: mesmo com o serviço parado, o systemd continua reservando a porta do painel (`painel-ramais.socket`). Se alguém abrir a tela
durante a cópia, o systemd religa o serviço sozinho. Feche as telas do painel antes e, depois da cópia, rode
`systemctl is-active painel-ramais.service`: deve dizer `inactive`. Se disser `active`, pare de novo e refaça a cópia.

Depois da cópia:

- Não precisa religar o serviço: o `instalar.py` religa sozinho no fim da instalação.
- Se o `instalar.py` parar antes de instalar o pacote (por exemplo, `verificar` com `ERRO` ou sem acesso ao GitHub), o serviço
  continua parado. Religue com `systemctl start painel-ramais.service` e corrija o problema antes de tentar de novo.
- O banco é convertido para o formato da versão nova na primeira subida, e o próprio painel guarda um backup automático
  (`painel.db.bak-v<N>`), usado pelo rollback (seção 7).
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
  na atualização: voltando da rc.8 para a rc.6, é o `painel.db.bak-v3`. O banco atual não é apagado: fica guardado como
  `painel.db.rollback-<data>`. Sem esse backup, o rollback não mexe em nada.
- Ao voltar da rc.8 para a rc.6 **perde-se**: os nomes de fila, os vínculos de ramal em mais de um grupo, o relatório de registros
  e tudo o que foi gravado depois da atualização (histórico, sessões, usuários e grupos). O instalador avisa isso antes de pedir a
  confirmação.

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
  `registrations` (gravador do relatório; `lost_events` deve ficar em 0). Esse endereço só responde no próprio PBX.
- `systemctl status painel-ramais`: deve mostrar `active (running)`.
- `journalctl -u painel-ramais`: log do serviço. Se a configuração estiver errada, o log diz qual chave.
- Se o item sumir do menu do Issabel (por exemplo, depois de uma atualização do Issabel): `./instalar.py menu`.

Tabela de problemas comuns e onde olhar: `docs/referencia-tecnica.md`, seção 7.

## 9. Comportamentos normais que parecem defeito

- **Contagem de registros pausa por 120 s depois de um reinício.** Quando o painel ou o Asterisk reinicia (ou a conexão AMI volta),
  as quedas e registros dos 120 s seguintes não entram no ranking de instabilidade: ficam marcados como evento do sistema. Assim, um
  reinício não aparece como se todos os ramais tivessem caído.
- **Ramais como "desconhecido".** Com o AMI fora do ar, os ramais aparecem como "desconhecido", nunca como "não registrado". O painel
  não sabe o estado real enquanto não houver AMI.
- **Números das filas zerados ou baixos.** Atendidas, abandonadas e nível de serviço contam desde o último reinício do Asterisk (ou
  desde que as estatísticas da fila foram zeradas). A tela avisa isso.
- **Relatório de registros sem dados antigos.** O relatório só tem dados desde que a versão com o relatório foi instalada; não existe
  histórico anterior. A tela mostra "Dados desde <data>". Os dados ficam guardados por 30 dias.
- **Nomes dos ramais vazios logo depois de instalar.** Os nomes são buscados em segundo plano, 20 por segundo, para não sobrecarregar
  o Asterisk. Com muitos ramais leva alguns minutos; depois ficam guardados e aparecem logo após um reinício.
- **Lista de filas vazia.** Em Asterisk sem o módulo de filas, a lista fica vazia, sem erro.

## 10. O que o painel não faz

- Nenhuma ação na central: não liga, não desliga, não transfere, não grava, não recarrega o Asterisk. Comandos fora da lista fixa de
  consulta são recusados pelo próprio painel antes de chegar ao Asterisk.
- Não altera a configuração do Issabel nem o `manager.conf`. No Asterisk, a única escrita é a seção do próprio painel no
  `/etc/asterisk/manager_custom.conf`, entre os marcadores `; BEGIN painel-ramais` e `; END painel-ramais`. Esse arquivo também
  passa a ficar sem acesso para "outros" (`chmod o-rwx`), porque contém a senha AMI do painel.
- Não lê os dados do Issabel no banco: ramais, chamadas e filas vêm só do AMI. Do Issabel, o painel só consulta, em modo somente
  leitura, o cadastro do menu (`/var/www/db/menu.db`, para saber se o item existe) e as permissões (`/var/www/db/acl.db`, para saber
  quem é do grupo administrator).

O que ele acrescenta ao servidor, além do próprio serviço (a remoção, seção 7, desfaz tudo menos o ajuste do SELinux):

- o arquivo próprio do Apache `/etc/httpd/conf.d/painel-ramais.conf`;
- o item **Painel de Ramais** no menu PBX, registrado com a ferramenta do Issabel (`issabel-menumerge`), e o módulo em
  `/var/www/html/modules/painel_ramais/`;
- com SELinux em modo Enforcing, liga o `httpd_can_network_connect` (a remoção não desliga).

## 11. Para saber mais

- `docs/referencia-tecnica.md`: configuração (`config.yaml`), CLI do serviço, formato do CSV de grupos, API, segurança, tabela de
  problemas comuns, roteiro de ensaio, relatório de carga e desenvolvimento.
- `docs/CONTRATOS.md`: contratos exatos da API, do banco e do instalador (vale em caso de divergência).
- `packaging/LEIAME-INSTALACAO.md`: opções avançadas do instalador (`--grupos`, `--respostas`, `--tecnologia`) e instalação em
  vários servidores.
- `docs/adr/`: decisões de arquitetura.
