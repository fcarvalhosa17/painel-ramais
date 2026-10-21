# Arquivos e caminhos no servidor

Tudo o que existe num PBX Rocky 8 com o painel instalado. Fonte: `packaging/nfpm.yaml`, `packaging/scripts/*`,
`instalar.py` (constantes do topo) e `internal/config/config.go`. Tabela do pacote no ADR-003, seção 2.

## Programa e serviço

| Caminho | Dono e modo | O que é |
|---|---|---|
| `/usr/bin/painel-ramais` | root, 0755 | binário único (daemon `serve` e CLI) |
| `/usr/lib/systemd/system/painel-ramais.service` | root, 0644 | unit endurecida (`User=painel-ramais`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/painel-ramais`, `Restart=always`) |
| `/usr/lib/systemd/system/painel-ramais.socket` | root, 0644 | `ListenStream=127.0.0.1:8790`; segura a porta mesmo com o daemon parado |
| `/etc/systemd/system/painel-ramais.socket.d/listen.conf` | root | só existe quando o `listen` do config é diferente do padrão (gerado pelo `%post`) |
| `/run/painel-ramais/metrics.json` | painel-ramais, 0600 | memória do daemon a cada 30 s, lida pelo `instalar.py status` |

Usuário de sistema: `painel-ramais` (sem shell, criado no `%pre`). Log do serviço: journald,
`journalctl -u painel-ramais`.

## Configuração e segredos

| Caminho | Dono e modo | O que é |
|---|---|---|
| `/etc/painel-ramais/` | root:painel-ramais, 0750 | pasta de configuração |
| `/etc/painel-ramais/config.yaml` | root:painel-ramais, 0640, `noreplace` | configuração (formato no CONTRATOS 6); o `instalar.py` só troca a linha `technology:` e guarda `config.yaml.bak-<data>` antes |
| `/etc/painel-ramais/secret` | painel-ramais, 0600 | segredo do daemon (base64, 32 bytes ou mais), assina tokens da CLI |
| `/etc/painel-ramais/ami-secret` | painel-ramais, 0600 | senha da conta AMI `painel-ramais` |
| `/etc/painel-ramais/admin-emergencia` | root, 0600 | senha do usuário `emergencia` (criada pelo `instalar.py` só se não existir) |
| `/etc/painel-ramais-issabel.key` | painel-ramais:asterisk, 0640 | chave HMAC do módulo PHP do Issabel (fora de `/etc/painel-ramais/`, que o PHP não atravessa) |

Nenhum desses arquivos vai para o repositório, para log ou para commit.

## Dados

| Caminho | Dono e modo | O que é |
|---|---|---|
| `/var/lib/painel-ramais/` | painel-ramais, 0750 | pasta de dados (`data_dir`) |
| `/var/lib/painel-ramais/painel.db` (+ `-wal`, `-shm`) | painel-ramais | banco SQLite ([banco-migracoes.md](banco-migracoes.md)) |
| `/var/lib/painel-ramais/painel.db.bak-v<N>` | painel-ramais, 0600 | backup automático feito antes de migrar do esquema N |
| `/var/lib/painel-ramais/painel.db.rollback-<data>` | painel-ramais | banco guardado pelo `instalar.py rollback` antes de restaurar um `.bak-v<N>` |
| `/var/lib/painel-ramais/painel.db.antes-<AAAAMMDD-HHMM>` | painel-ramais | cópia manual antes de atualizar (README, seção 6) |
| `/var/lib/painel-ramais-instalador/` | root, 0700 | RPM da versão anterior (e o SHA-256 dele) para o rollback |

## Instalador

| Caminho | O que é |
|---|---|
| `/var/log/painel-ramais/instalacao.log` | tudo o que o `instalar.py` executou (0600; senha nunca entra) |
| `/var/log/painel-ramais/verificacao-<data>.txt` | relatório do `./instalar.py verificar` |
| `/root/painel-ramais-<versao>/` | pacote extraído pelo `instalar.py` quando roda de um clone |
| `/usr/share/doc/painel-ramais/grupos-exemplo.csv` | CSV de exemplo de visões (cabeçalho `visao,ramal`) |

## Integração com Apache, Issabel e Asterisk

| Caminho | Quem escreve | O que é |
|---|---|---|
| `/etc/httpd/conf.d/painel-ramais.conf` | `%post` (a partir de `/usr/share/painel-ramais/httpd.conf.tmpl`) | proxy de `/painel-ramais/` e `/painel-ramais/ws` para o daemon; `/debug` e `/api/health` fora do proxy |
| `/var/www/html/modules/painel_ramais/index.php` | RPM | módulo do Issabel que emite o token e monta o iframe |
| `/usr/share/painel-ramais/issabel/menu.xml` | RPM | definição do item `painel_ramais`, registrada com `issabel-menumerge` |
| `/etc/asterisk/manager_custom.conf` | `%post`, como usuário `asterisk` | **só** o bloco entre `; BEGIN painel-ramais` e `; END painel-ramais` (conta AMI, `permit` 127.0.0.1, 29 `eventfilter`); depois `manager reload` e `chmod o-rwx` no arquivo |
| `/var/www/db/menu.db`, `/var/www/db/acl.db` | ninguém do painel | lidos **somente leitura** pelo daemon (item do menu existe? usuário é administrator?) |
| `/etc/asterisk/manager.conf` | ninguém do painel | nunca alterado; o daemon e o instalador só leem para conferir o `#include` |

SELinux em Enforcing: o `%post` liga `httpd_can_network_connect` (a remoção não desliga).

## Portas

| Porta | Quem | Aberta para |
|---|---|---|
| `127.0.0.1:8790` | socket do painel | só loopback; o Apache encaminha |
| `127.0.0.1:5038` | AMI do Asterisk | o daemon conecta como cliente |
| 443 | Apache do Issabel | rede; o painel não abre porta nova |

## Remoção

`./instalar.py remover` tira o pacote, o bloco AMI (com `manager reload`), a conf do Apache, o item do menu e a chave do
Issabel, e mantém dados, configuração e usuário. `--purge` apaga também `/var/lib/painel-ramais`, `/etc/painel-ramais` e
`/var/lib/painel-ramais-instalador`. Detalhes: README, seção 7.
