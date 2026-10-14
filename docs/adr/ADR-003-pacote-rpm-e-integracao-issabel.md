## ADR-003: Pacote RPM, integração descartável com o Issabel e instalador de campo

**Data**: 2026-10-13
**Status**: Proposto. O desenho está fechado; passa a Aceito depois do spike em containers Rocky 8 mínimo e Rocky 9 com a saída anexada a este arquivo (seção "Spike pendente"). Motivo: o comportamento de `%post`/`%postun` gerados pelo nfpm, do `ProxyPass` dentro de `<Location>` no httpd do Rocky 8 e do eventfilter na versão de Asterisk do Issabel 5 ainda não foi visto rodando.
**Porta**: 1 via. Caminhos instalados, marcadores do bloco AMI e nome do usuário de sistema ficam nos servidores dos clientes; mudar depois exige script de migração que reconheça o formato antigo.

### Contexto

Requisito principal da especificação (`docs/PROMPT-FASE-1.md`, "Isolamento do Issabel"): nenhuma atualização do Issabel, do IssabelPBX ou de módulos pode parar o serviço; a integração com o Issabel é uma camada fina e descartável. O pacote é gerado com nfpm, sem dependência de pacote do Issabel, fora dos repositórios dele. O IssabelPBX regera `manager.conf`, então o usuário AMI vai em `manager_custom.conf`. O Apache recebe arquivo próprio em `conf.d`. O firewall do Issabel regrava o iptables, então nenhuma porta nova é exposta.

O adendo (`docs/ADENDO-FASE-1-INSTALADOR.md`) acrescenta o instalador `instalar.py`, que o usuário roda em vários servidores de clientes, e que substitui o `discovery.sh`. Regra do adendo: o instalador orquestra e valida, nunca repete o que o `%post` faz.

### Decisão

**1. Camadas e quem faz o quê**

| Camada | Faz | Nunca faz |
|---|---|---|
| RPM (`%pre`, `%post`, `%preun`, `%postun`) | usuário de sistema, segredos, bloco AMI, conf do Apache, SELinux, unit systemd | perguntar algo, criar usuário do painel, importar grupos |
| `instalar.py` | verificar pré-requisitos, `rpm -i`/`-U`/`-e`, guardar RPM para rollback, chamar a CLI do daemon (usuário admin, grupos), trocar a linha `technology:` do config, esperar o health, testar o Apache, resumir | editar `manager_custom.conf`, conf do Apache, SELinux ou o SQLite |
| Daemon | verificação de dependências a cada 60 s, exposta no `/api/health` | editar qualquer arquivo fora de `/var/lib/painel-ramais` |

**2. Arquivos do pacote (nfpm)**

| Caminho | Dono e modo | Tipo |
|---|---|---|
| `/usr/bin/painel-ramais` | root:root 0755 | binário estático linux/amd64, um para el8 e el9 |
| `/usr/lib/systemd/system/painel-ramais.service` | root:root 0644 | unit |
| `/etc/painel-ramais/` | root:painel-ramais 0750 | diretório |
| `/etc/painel-ramais/config.yaml` | root:painel-ramais 0640 | `config|noreplace` |
| `/var/lib/painel-ramais/` | painel-ramais:painel-ramais 0750 | diretório (o rpm não apaga se tiver conteúdo) |
| `/usr/share/painel-ramais/httpd.conf.tmpl` | root:root 0644 | modelo da conf do Apache |

Gerados no `%post`, **fora** do pacote (o rpm não os apaga nem os sobrescreve): `/etc/painel-ramais/secret` e `/etc/painel-ramais/ami-secret` (painel-ramais 0600) e `/etc/httpd/conf.d/painel-ramais.conf` (root 0644). Nenhum `depends` de pacote do Issabel.

**3. Argumentos dos scripts.** No upgrade o rpm roda, nesta ordem: `%pre` novo (`$1=2`), `%post` novo (`$1=2`), `%preun` antigo (`$1=1`), `%postun` antigo (`$1=1`). Tudo que remove fica atrás de `[ "$1" -eq 0 ]`, senão o script do pacote antigo desfaria o que o novo acabou de fazer.

| Script | Instalação (`$1=1`) | Upgrade | Remoção (`$1=0`) |
|---|---|---|---|
| `%pre` | `useradd -r -s /sbin/nologin -d /var/lib/painel-ramais -M painel-ramais` se não existir | o mesmo | não roda |
| `%post` | passos 4 a 8; `systemctl daemon-reload`; `enable --now` | passos 4 a 8; `daemon-reload`; `try-restart` | não roda |
| `%preun` | não roda | `$1=1`: nada | `systemctl disable --now painel-ramais` |
| `%postun` | não roda | `$1=1`: nada | remove o bloco AMI e faz `manager reload`; remove a conf do Apache, `httpd -t`, reload; `daemon-reload` |

O `%post` nunca sai com erro (falha de passo não pode deixar o rpm em estado quebrado): cada passo registra o resultado com `logger -t painel-ramais-rpm` e no stderr, e a verificação de dependências do daemon acusa o que ficou faltando.

**4. Segredos.** Se `/etc/painel-ramais/secret` não existe: `umask 077`, 32 bytes de `/dev/urandom` em base64, dono painel-ramais, 0600. O mesmo para `ami-secret` (32 bytes em hex, para não ter caractere especial no `.conf` do Asterisk). Arquivo existente nunca é tocado: upgrade e reinstalação preservam.

**5. Bloco AMI em `/etc/asterisk/manager_custom.conf`.** O `ami-secret` é a fonte da verdade e o bloco é sempre derivado dele:
- Seção `[painel-ramais]` existindo **fora** dos marcadores: não toca, registra erro (conflito com configuração manual).
- Senão, gera o bloco abaixo; se for diferente do que está entre os marcadores (ou se não houver bloco), troca ou acrescenta. Montagem em `mktemp -d` dentro de `/run` (só root). Toda leitura e escrita do `manager_custom.conf` roda **como o usuário `asterisk`** (`runuser -u asterisk`), dono de `/etc/asterisk`: um symlink plantado por ele só alcança o que ele já alcança, e nada é gravado fora de `/etc/asterisk`. A escrita é no lugar (mantém dono, modo e contexto SELinux; arquivo novo nasce `asterisk:asterisk` 0640) e termina com `chmod o-rwx`, porque o arquivo contém a senha AMI. O `instalar.py verificar` avisa se o arquivo estiver acessível a outros.
- Bloco mudou e o Asterisk está rodando: `asterisk -rx 'manager reload'`. Parado: registra e segue (vale na próxima subida).
- `manager.conf` sem include do `manager_custom.conf`: só registra aviso. Nunca edita `manager.conf`.

```
; BEGIN painel-ramais
; Gerado pelo pacote painel-ramais. Nao editar: o upgrade reescreve este bloco.
[painel-ramais]
secret = <conteudo de /etc/painel-ramais/ami-secret>
deny = 0.0.0.0/0.0.0.0
permit = 127.0.0.1/255.255.255.255
read = system,call
write = system,reporting
writetimeout = 5000
displayconnects = no
eventfilter = Event: ContactStatus
eventfilter = Event: PeerStatus
eventfilter = Event: DeviceStateChange
eventfilter = Event: Newchannel
eventfilter = Event: Newstate
eventfilter = Event: NewCallerid
eventfilter = Event: NewConnectedLine
eventfilter = Event: DialBegin
eventfilter = Event: DialEnd
eventfilter = Event: BridgeEnter
eventfilter = Event: BridgeLeave
eventfilter = Event: Hangup
eventfilter = Event: Hold
eventfilter = Event: Unhold
; END painel-ramais
```

**Decisão do usuário (2026-10-13, `docs/DECISOES-FASE-1.md`, decisão 1): manter `write = system,reporting`; risco residual aceito.** As actions PJSIP de consulta exigem a classe `system` (ver CONTRATOS 7.1), e a conta AMI do painel a recebe. Impacto real, com base na documentação do Asterisk e a conferir com `manager show command <Action>` na versão instalada: quem obtiver o segredo da conta (arquivo 0600 do `painel-ramais`, bloco no `manager_custom.conf`, só 127.0.0.1) poderia, pela classe `system`, **executar comandos de shell como o usuário `asterisk`** (`MixMonitor` com o parâmetro `Command`, o vetor do AST-2014-006), **ler os segredos SIP e AMI** (`GetConfig`), **desviar chamadas** (`DBPut` na AstDB), **reiniciar telefones em massa** (`PJSIPNotify`) e ainda `Reload`, `ModuleLoad`/`ModuleUnload` e `Hangup`. O código do painel nunca emite essas actions (allowlist fixa no cliente AMI, com teste), mas a allowlist protege só contra bug do painel, não contra quem roubar o segredo. Não foi mudado: alternativas são aceitar o risco, usar `write = reporting` onde a versão do Asterisk permitir (o `instalar.py verificar` coleta os privilégios por action para decidir), ou obter os dados por outro caminho. Decisão: manter o valor, com controles compensatórios (allowlist antes do socket, permit 127.0.0.1, segredo 0600, unit endurecida).

**Aceito pelo usuário (decisão 3): local do RPM guardado.** O adendo (`docs/ADENDO-FASE-1-INSTALADOR.md`) pede o RPM anterior em `/var/lib/painel-ramais/rpm-anterior/`. A implementação usa `/var/lib/painel-ramais-instalador/` (root 0700) porque `/var/lib/painel-ramais` pertence ao usuário do serviço e o instalador roda como root (risco de symlink plantado, achado H1 da auditoria). É um desvio do texto do adendo e precisa de aceite do usuário.

**Decisão 2 do usuário: socket activation do systemd.** O `painel-ramais.socket` segura `127.0.0.1:8790` (a porta continua ocupada com o daemon parado) e o daemon recebe o socket via `LISTEN_FDS`. Antes (M4, socket Unix): O daemon escuta em TCP 127.0.0.1:8790; qualquer usuário local do servidor alcança a porta (as barreiras são sessão/token e o bloqueio de `X-Forwarded-*`). Trocar por socket Unix com permissão de grupo (o Apache no grupo do painel) reduz isso, mas muda o `ProxyPass` (`unix:`) e a SELinux. Não foi mudado.

`writetimeout = 5000` porque o padrão do Asterisk (100 ms) derruba a conexão numa rajada se o leitor atrasar um instante. O leitor nunca bloqueia (contrato, seção 11.3), mas a folga evita desconexão por pico de agendamento.

**6. eventfilter estático com a união SIP e PJSIP.** A especificação diz "eventfilter montado conforme a tecnologia ativa"; a união cumpre o efeito (só chegam eventos da tecnologia ativa, porque o módulo inativo não está carregado e não gera evento) sem o custo de reescrever o bloco quando a tecnologia é detectada.

**7. Apache.** O `%post` lê a porta de `listen:` no `config.yaml` (padrão 8790), preenche o modelo e:
1. roda `httpd -t` **sem** o arquivo; se já falha, não instala a conf e registra "configuração do Apache já estava inválida";
2. grava `/etc/httpd/conf.d/painel-ramais.conf` e roda `httpd -t`; falhou: remove o arquivo, não recarrega e registra a saída;
3. passou e o httpd está ativo: `systemctl reload httpd`.

```
# Gerado pelo pacote painel-ramais a partir de /usr/share/painel-ramais/httpd.conf.tmpl. Nao editar.
<Location "/painel-ramais/">
    ProxyPreserveHost On
    ProxyPass "http://127.0.0.1:8790/"
    ProxyPassReverse "http://127.0.0.1:8790/"
</Location>
<Location "/painel-ramais/ws">
    ProxyPass "ws://127.0.0.1:8790/ws"
</Location>
<Location "/painel-ramais/debug">
    ProxyPass "!"
</Location>
<Location "/painel-ramais/api/health">
    ProxyPass "!"
</Location>
```

A conf não é arquivo do pacote de propósito: a regra "falhou o `httpd -t`, remove o arquivo" brigaria com o rpm (o `rpm -V` acusaria arquivo faltando e o upgrade recriaria a conf quebrada), e a porta vem do `config.yaml`. Mudar a porta: editar `config.yaml`, reinstalar a mesma versão pelo instalador (o `%post` regera a conf) e reiniciar o serviço. `ProxyPreserveHost` fica dentro do `<Location>` para não alterar outros proxies do Issabel.

**8. SELinux.** Se `getenforce` = `Enforcing` e `httpd_can_network_connect` está desligado: `setsebool -P httpd_can_network_connect 1` e registro no log. Na remoção o booleano **não** é revertido, porque outra coisa do servidor pode depender dele (documentado no README).

**9. Unit systemd** sem `Requires`/`After` de `asterisk.service` ou `httpd.service`: restart do Asterisk ou do Apache não derruba o painel; o daemon reconecta sozinho.

```
[Unit]
Description=Painel de Ramais (coletor AMI)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=painel-ramais
Group=painel-ramais
ExecStart=/usr/bin/painel-ramais serve --config /etc/painel-ramais/config.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=2
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ReadWritePaths=/var/lib/painel-ramais

[Install]
WantedBy=multi-user.target
```

`StartLimitIntervalSec=0` faz o systemd nunca desistir de reiniciar (config inválida vira um erro claro no journal a cada 2 s, em vez de serviço parado em silêncio).

**10. Remoção e limpeza.** `rpm -e` apaga binário, unit, bloco AMI e conf do Apache; mantém `/var/lib/painel-ramais`, `/etc/painel-ramais` (segredos e `config.yaml.rpmsave` se alterado) e o usuário de sistema. Limpeza total: `./instalar.py remover --purge` (ou à mão: `rpm -e painel-ramais && rm -rf /var/lib/painel-ramais /etc/painel-ramais && userdel painel-ramais`).

**11. Bundle de distribuição.** Alvo de build `bundle` gera `dist/painel-ramais-<versao>.tar.gz` com `instalar.py`, o RPM, `grupos-exemplo.csv` e `LEIAME-INSTALACAO.md`. O tar é montado por `tools/bundle.py` (Python stdlib, `tarfile`; no repositório o `instalar.py` fica na raiz, não em `instalador/`), que grava o modo 0755 do `instalar.py` no cabeçalho do tar e recusa empacotar arquivo de texto com CR. Motivo: o desenvolvimento é no Windows, que não tem bit de execução, e o `tar` do Windows não garante o modo. Nada é baixado no servidor: tudo que o instalador usa está no tar ou no sistema base.

**12. Instalador `instalar.py`.** Contrato completo em `docs/CONTRATOS.md`, seção 15. Pontos de desenho:
- Python 3.6 ou superior, só stdlib; primeira linha poliglota sh/python que usa `python3` se existir e `/usr/libexec/platform-python` se não (Rocky 8 mínimo não traz `python3`, mas traz o `platform-python`).
- Roda como root. Chama a CLI do daemon com `runuser -u painel-ramais -- painel-ramais ...` (o `runuser` vem no util-linux do sistema mínimo; a CLI recusa root).
- Idempotente: usuário admin que já existe não é recriado (a CLI devolve código 3 e o instalador segue sem trocar a senha); grupos entram sempre com `group import --replace` (o CSV do instalador é a fonte da verdade); a linha `technology:` só é reescrita se o valor mudou.
- Rollback: antes de todo upgrade, o RPM instalado (guardado em `/var/lib/painel-ramais-instalador/rpm-instalado/` na instalação anterior, área só do root 0700 **fora** de `/var/lib/painel-ramais`, que pertence ao usuário do serviço) vai para `/var/lib/painel-ramais-instalador/rpm-anterior/`. Antes do `rpm -U --oldpackage` o instalador confere `rpm -qp --qf %{NAME}` = `painel-ramais` e o SHA-256 registrado na instalação. Operações em `/var/lib/painel-ramais` (restaurar o banco) rodam como o dono; o CSV de grupos vai pelo stdin da CLI (`group import --replace -`). O rollback roda `rpm -U --oldpackage` com ele. Se o banco já estiver num esquema mais novo do que o binário antigo conhece (o daemon recusa iniciar nesse caso), o rollback, com confirmação, guarda o banco atual como `painel.db.rollback-<data>` e restaura o backup `painel.db.bak-v<esquema antigo>` que o daemon fez antes de migrar. Isso perde o histórico, as sessões e as mudanças de usuário e grupo feitas depois do upgrade, e a mensagem diz isso antes de pedir confirmação.
- Log: todo comando e toda saída em `/var/log/painel-ramais/instalacao.log` (root 0600; a senha nunca entra, porque vai pelo stdin da CLI); relatório do `verificar` em `/var/log/painel-ramais/verificacao-<AAAAmmdd-HHMMSS>.txt`.

### Alternativas consideradas

- **A (mais simples possível): RPM só com arquivos e um README com os passos manuais (bloco AMI, Apache, SELinux).** Descartada. A especificação exige que o `%post` faça a integração, e o adendo prevê instalação em vários servidores; passo manual em campo é onde o erro acontece.
- **B: spec do rpmbuild.** Descartada: a especificação fixa nfpm, e o nfpm roda no Windows sem rpmbuild.
- **C: eventfilter por tecnologia, reescrito pelo daemon na detecção.** Descartada. O daemon precisaria de escrita em `/etc/asterisk` e de rodar `manager reload`, o que quebra o isolamento e amplia o privilégio do serviço.
- **D: eventfilter por tecnologia detectado no `%post`.** Descartada. Com o Asterisk parado na instalação a detecção falha, e a troca de SIP para PJSIP depois da instalação deixaria o filtro errado.
- **E: action `Filter` por sessão.** Descartada. Mais uma action de classe `system` na allowlist, e a combinação de filtro estático com dinâmico é mais uma coisa a validar, sem ganho sobre a união.
- **F: conf do Apache como arquivo `%config` do pacote.** Descartada pelo motivo do item 7.
- **G: `semanage port` em vez do booleano.** Descartada: a especificação escolhe `setsebool`, e o `semanage` exige `policycoreutils-python-utils`, que pode faltar no sistema mínimo.
- **H: instalador em shell.** Descartada pelo adendo (Python 3.6 com stdlib), e com razão: relatório com OK/AVISO/ERRO, arquivo de respostas INI e espera do health com timeout são mais legíveis em Python.
- **I: instalador grava usuário e grupos direto no SQLite.** Descartada pelo adendo: duplicaria validação e migração que já moram na CLI.

### Suposições feitas

- O `manager.conf` do Issabel 5 inclui `manager_custom.conf` (o `verificar` confirma; sem isso a instalação aborta).
- `httpd.conf` do Rocky inclui `conf.d/*.conf` e o `mod_proxy_wstunnel` vem no pacote httpd (`conf.modules.d/00-proxy.conf`); o `verificar` confirma.
- `asterisk` em `/usr/sbin/asterisk`, com `-rx` disponível para root.
- O nfpm repassa `$1` aos scripts do rpm como o rpmbuild faz.
- `ProxyPass` dentro de `<Location>`, com `ProxyPass "!"` numa `<Location>` mais específica declarada depois, funciona no httpd 2.4.37 do Rocky 8 (exemplo da própria documentação do Apache).

### Consequências

- Positivas: o Issabel pode regerar `manager.conf`, reiniciar Asterisk e Apache sem parar o serviço; remover o pacote devolve o servidor ao estado anterior (menos o booleano do SELinux); o bloco AMI evolui no upgrade sem intervenção; o técnico de campo roda um comando e recebe OK/AVISO/ERRO com a correção.
- Negativas / trade-offs:
  - A conta AMI precisa de `write = system` para as actions PJSIP, o que liberaria actions de escrita se o segredo vazasse. Mitigação: allowlist no código, segredo 0600, `permit` só 127.0.0.1.
  - Mudar a porta exige reinstalar a mesma versão pelo instalador.
  - Rollback por cima de migração de esquema perde dados posteriores ao upgrade (avisado e confirmado).
  - O booleano `httpd_can_network_connect` fica ligado depois da remoção.

### Criterio de validação

Ver "Spike pendente" e os critérios consolidados em `docs/CONTRATOS.md`, seção 16. Em resumo: `rpm -i`, `-U` e `-e` em Rocky 8 mínimo e Rocky 9 com o simulador; `%post` rodado duas vezes deixa um bloco só; upgrade não remove bloco nem conf; atualização simulada do Issabel (regera `manager.conf`, reinicia httpd e Asterisk) não para o serviço e a verificação de dependências acusa o que sumiu.

### Spike pendente (promove este ADR a Aceito)

Precisa de Linux. Antes, o gerente confirma com o usuário se usa WSL2 ou Docker Desktop (pedido da especificação). Itens, com a saída de cada um anexada aqui:
1. Container Rocky 9: `rpm -i` do pacote gerado pelo nfpm; conferir usuário, modos dos arquivos, bloco no `manager_custom.conf` de teste, conf do Apache e `httpd -t`.
2. Mesmo container: `rpm -U` para versão N+1; bloco e conf continuam lá (prova do `$1`).
3. `rpm -e`: bloco e conf saem; `/var/lib/painel-ramais` fica.
4. Rocky 8: `httpd -t` aceita a conf; `curl` em `/painel-ramais/debug` via Apache dá 404 do Apache; upgrade de WebSocket em `/painel-ramais/ws` chega ao daemon.
5. Rocky 8 mínimo sem `python3`: `./instalar.py verificar` roda via `platform-python`.
6. Versão do Asterisk do Issabel 5 (ou o simulador fiel a ela): eventfilter no formato `Event: X` aceito.

### Agentes impactados

backend (nfpm, scripts, unit), devops (instalador, bundle, README e LEIAME-INSTALACAO), seguranca (permissões da conta AMI, segredos, SELinux), qa (spike e critérios da seção 16 do contrato).
