Copyright (c) 2026 Fabio Carvalhosa Sanchez. Todos os direitos reservados.

# Painel de Ramais (Fase 1: coletor, empacotamento e instalador)

Painel **somente leitura** que substitui a visão do FOP em servidores **Issabel 5** (Rocky Linux 8 ou 9). Esta fase entrega o
coletor (daemon em Go, uma conexão AMI), a API local em `127.0.0.1`, o pacote RPM e o instalador `instalar.py`. A tela final é a
Fase 2; o link no menu do Issabel é a Fase 3. O painel não executa nenhuma ação de controle no Asterisk.

Documentos de referência: `docs/CONTRATOS.md` (contratos), `docs/adr/` (decisões), `docs/PROMPT-FASE-1.md` e
`docs/ADENDO-FASE-1-INSTALADOR.md` (especificação). **Este README resume; em caso de divergência vale o CONTRATOS.**

## Download e verificação

Os artefatos de cada versão ficam na página de Releases do GitHub. Baixe os três arquivos e confira o SHA256 antes de instalar:

```sh
VERSAO=0.1.1   # troque pela versao desejada
BASE=https://github.com/fcarvalhosa17/painel-ramais/releases/download/v${VERSAO}
curl -fLO ${BASE}/painel-ramais-${VERSAO}-1.x86_64.rpm
curl -fLO ${BASE}/painel-ramais-${VERSAO}.tar.gz
curl -fLO ${BASE}/SHA256SUMS
sha256sum -c SHA256SUMS          # todas as linhas devem terminar em ": OK"
```

Se alguma linha falhar, não instale: baixe de novo. Instalação a partir do `.tar.gz`: seção 1 e `packaging/LEIAME-INSTALACAO.md`.

## 1. Instalação em campo (recomendado)

No computador de desenvolvimento: `.\build.ps1 all` gera `dist/painel-ramais-<versao>.tar.gz`. No servidor (como root):

```sh
tar xzf painel-ramais-<versao>.tar.gz && cd painel-ramais-<versao>
./instalar.py verificar          # somente leitura; relatório em /var/log/painel-ramais/verificacao-<data>.txt
./instalar.py instalar           # pergunta tecnologia, usuário admin, senha e CSV de grupos
./instalar.py status
```

Não precisa de internet nem de `pip`. Funciona sem o pacote `python3` do Rocky 8 mínimo (usa `/usr/libexec/platform-python`).
Passo a passo de campo, modo não interativo (`--respostas`) e laço ssh para vários servidores: `packaging/LEIAME-INSTALACAO.md`
(vai dentro do bundle). Códigos de saída: 0 sucesso; 1 verificação com ERRO; 2 falha na operação; 3 uso inválido; 4 cancelado.

Instalação manual do RPM (sem o instalador): `rpm -i painel-ramais-<versao>-1.x86_64.rpm`; depois crie o usuário admin com a CLI
(seção 4). Nenhum pacote do Issabel é dependência do RPM.

## 2. O que o pacote instala

| Caminho | Dono e modo | Observação |
|---|---|---|
| `/usr/bin/painel-ramais` | root 0755 | binário estático linux/amd64 (el8 e el9) |
| `/etc/painel-ramais/config.yaml` | root:painel-ramais 0640 | `config (noreplace)`: upgrade não sobrescreve |
| `/etc/painel-ramais/secret`, `ami-secret` | painel-ramais 0600 | gerados no `%post` só se não existirem |
| `/var/lib/painel-ramais/` | painel-ramais 0750 | banco SQLite e histórico |
| `/usr/lib/systemd/system/painel-ramais.socket` | root 0644 | socket activation: `ListenStream=127.0.0.1:8790`; o systemd mantém a porta ocupada mesmo com o daemon parado ou em crash |
| `/usr/lib/systemd/system/painel-ramais.service` | root 0644 | `Restart=always`; `Requires=painel-ramais.socket`; recebe o socket via `LISTEN_FDS` (fora do systemd, em desenvolvimento e testes, o daemon abre a porta sozinho) |
| `/etc/httpd/conf.d/painel-ramais.conf` | root 0644 | gerado no `%post` a partir de `/usr/share/painel-ramais/httpd.conf.tmpl`; removido na desinstalação |
| bloco `; BEGIN painel-ramais` em `/etc/asterisk/manager_custom.conf` | asterisk 0640 | usuário AMI; **nunca** se edita `manager.conf` |

Se o `listen:` do `config.yaml` não for `127.0.0.1:8790`, o `%post` cria o drop-in `/etc/systemd/system/painel-ramais.socket.d/listen.conf` com o endereço certo (e o remove na desinstalação). Nenhuma porta nova é exposta: todo acesso passa pela 443 do Apache (`https://<servidor>/painel-ramais/`). `/debug` e
`/api/health` ficam fora do proxy e só respondem em `127.0.0.1`.

## 3. Configuração

Socket activation (decisão 2): sob o systemd o daemon usa o socket entregue em `LISTEN_FDS`/`LISTEN_PID` (protocolo `sd_listen_fds`: um único descritor, a partir do 3, só se `LISTEN_PID` for o PID do próprio processo) e ignora `listen`. O socket herdado precisa ser TCP em loopback, senão o daemon recusa iniciar. Fora do systemd (desenvolvimento, testes), o daemon abre `listen` sozinho. Unit a criar pelo @devops: `painel-ramais.socket` com `ListenStream=127.0.0.1:8790` (a mesma porta de `listen`; `Accept=no`, `Service=painel-ramais.service`) e, no `painel-ramais.service`, `Requires=painel-ramais.socket`. O systemd mantém a porta ocupada mesmo com o daemon parado.

Lido uma vez na subida. Mudança exige `systemctl restart painel-ramais` (SIGHUP não relê o YAML). O `instalar.py` só reescreve a linha `technology:` (e só se o valor mudou, com cópia `config.yaml.bak-<data>` antes); o resto do arquivo é do administrador. Por isso a linha `technology:` fica sempre no nível raiz, numa linha só, no formato `technology: <valor>`. Chave desconhecida, tipo errado ou valor fora da regra: o daemon recusa iniciar e diz qual chave (yaml.v3 com `KnownFields(true)`).

```yaml
# /etc/painel-ramais/config.yaml (exemplo instalado pelo RPM, config noreplace)
listen: "127.0.0.1:8790"
technology: auto
extension_pattern: '^[0-9]+$'
data_dir: /var/lib/painel-ramais
log_level: info

ami:
  address: "127.0.0.1:5038"
  username: painel-ramais
  secret_file: /etc/painel-ramais/ami-secret

auth:
  secret_file: /etc/painel-ramais/secret
  session_ttl: 12h

history:
  retention_days: 30
```

| Chave | Tipo | Padrão | Validação |
|---|---|---|---|
| `listen` | host:porta | `127.0.0.1:8790` | host obrigatoriamente loopback (`127.0.0.0/8` ou `::1`); senão recusa iniciar. 8790 evita a 8088 do HTTP do Asterisk |
| `technology` | enum | `auto` | `auto`, `sip`, `pjsip` |
| `extension_pattern` | regexp RE2 | `^[0-9]+$` | compila; o daemon sempre casa o nome inteiro (envolve em `^(?:...)$`) |
| `data_dir` | caminho absoluto | `/var/lib/painel-ramais` | existe e é gravável pelo usuário do serviço; banco em `<data_dir>/painel.db` |
| `log_level` | enum | `info` | `debug`, `info`, `warn`, `error`; log em texto no stdout (journald) |
| `ami.address` | host:porta | `127.0.0.1:5038` | host loopback |
| `ami.username` | string | `painel-ramais` | 1 a 64 caracteres; tem de bater com a seção do bloco em `manager_custom.conf` |
| `ami.secret_file` | caminho | `/etc/painel-ramais/ami-secret` | legível; permissão 0600; dono = usuário do serviço; conteúdo não vazio (espaços nas pontas descartados) |
| `auth.secret_file` | caminho | `/etc/painel-ramais/secret` | legível; 0600; base64 que decodifica em 32 bytes ou mais |
| `auth.session_ttl` | duração Go | `12h` | entre `15m` e `24h`; inatividade máxima da sessão (renovada pelo uso e pelo WebSocket) |
| `ami.name_fill_per_second` | inteiro | `20` | entre 1 e 200; teto de consultas de nome (`PJSIPShowEndpoint`/`SIPshowpeer`) em segundo plano (decisão 4) |
| `history.retention_days` | inteiro | `30` | entre 1 e 365 |

Constantes (não configuráveis, de propósito): lote WS 250 ms; ring buffer 40 lotes; fila de envio por cliente 64 mensagens; TTL máximo do token 60 s; backoff de reconexão AMI de 1 s a 30 s; verificação de dependências a cada 60 s; fila interna AMI para estado 65536 eventos; ReadTimeout HTTP 15 s; no máximo 4 bcrypt simultâneos no login. Viram configuração só com medição que justifique.

Não há variáveis de ambiente. O único "ambiente" é este arquivo e os dois segredos, todos validados na subida.

## 4. Usuários e grupos

Binário único `/usr/bin/painel-ramais`. Todo subcomando, exceto `version`, recusa euid 0 com a mensagem `não rode como root; use: sudo -u painel-ramais painel-ramais <comando>` e sai com código 1.

```
painel-ramais serve [--config /etc/painel-ramais/config.yaml]
painel-ramais user add <usuario> [--password-stdin]
painel-ramais user del <usuario>
painel-ramais user passwd <usuario> [--password-stdin]
painel-ramais group import [--replace] <arquivo.csv | ->
painel-ramais session purge [--all]
painel-ramais group list
painel-ramais group del <grupo>
painel-ramais token --sub <texto> [--ttl 60s]
painel-ramais capturar --minutos N --saida <arquivo> [--anonimizar] [--digitos-externos 7]
painel-ramais version
```

- `--config` vale para todos; a CLI usa o YAML só para achar `data_dir` e os segredos.
- Senha: lida do terminal sem eco, digitada duas vezes; `--password-stdin` lê uma linha do stdin (para script). Mínimo 10 caracteres, máximo 72 bytes. Hash bcrypt custo 12.
- `user passwd` e `user del` apagam as sessões do usuário (no `del`, via `ON DELETE CASCADE`).
- `group import ... -` lê o CSV do stdin (o `/dev/stdin` também funciona no Linux).
- `capturar` (decisão da homologação) grava frames brutos do AMI real, só com a allowlist, em arquivo novo (modo 0600; recusa sobrescrever): conecta, faz o snapshot e uma amostra de consultas de nome, grava o tráfego ao vivo por `--minutos` (fracionário; máx. 720; Ctrl-C encerra antes) e termina com um snapshot final. Segredos (`Secret`, `Password`, `Md5Cred`, `Challenge`…) saem sempre mascarados (`***`). `--anonimizar` troca números externos (sequências de pelo menos `--digitos-externos` dígitos), nomes e IPv4 por ficcionais, sempre o mesmo ficcional para o mesmo original; é melhor esforço (IPv6 não é tratado): revise o arquivo antes de compartilhar. Formato do arquivo: seção 18.
- `session purge` apaga as sessões vencidas; `--all` apaga todas (todo mundo faz login de novo) e manda o SIGHUP para o daemon fechar os WebSockets. Imprime a quantidade removida.
- `token` imprime o token no stdout e nada mais. `--ttl` máximo 60 s. Uso: testes e diagnóstico.
- Depois de `user ...` ou `group ...` com sucesso, a CLI pega o PID com `systemctl show -p MainPID --value painel-ramais` e manda SIGHUP (mesmo usuário, não precisa de root). Daemon parado: avisa e sai 0. Falha no sinal: avisa `rode: systemctl reload painel-ramais` e sai 0 (o banco já foi gravado).
- Códigos de saída: 0 sucesso; 1 erro de uso ou validação (mensagem cita a linha do CSV); 2 erro de ambiente (banco, arquivo, permissão); 3 `user add` de usuário que já existe (nada alterado). O 3 existe para o `instalar.py` ser idempotente sem ler o banco. Só no `serve`: 4 = Asterisk abaixo da versão mínima (13); a unit deve ter `RestartPreventExitStatus=4` para não reiniciar em laço.
- `version` imprime uma linha `painel-ramais <versao> schema <user_version que o binário conhece>` (exemplo: `painel-ramais 0.1.1 schema 2`). O `instalar.py` usa o `schema` para decidir o rollback (seção 15.5).
- Root (o instalador) chama a CLI com `runuser -u painel-ramais -- painel-ramais ...`; pessoa usa `sudo -u painel-ramais painel-ramais ...`.
- `group list` imprime `grupo<TAB>faixas` (faixas separadas por vírgula), uma linha por grupo, ordenado por nome.

```
# grupo,ramal_ou_faixa
grupo,ramal
Suporte,2000-2099
Suporte,2150
Vendas,3000-3999
Diretoria,4000
```

O mesmo arquivo com `;` (`Suporte;2000-2099`) também é aceito, desde que o arquivo inteiro use `;`. Este formato também é o do `grupos-exemplo.csv` que vai no bundle.

- UTF-8, BOM aceito. Delimitador `,` ou `;`, detectado na primeira linha de dados e fixo para o arquivo (Excel em português exporta com `;`).
- Linhas vazias e linhas que começam com `#` são ignoradas. Cabeçalho opcional: só é pulado se a primeira linha de dados for exatamente `grupo<delim>ramal` (sem diferenciar maiúsculas). Qualquer outra linha é dado.
- `grupo`: 1 a 64 caracteres depois do trim, diferente de `Sem grupo`. Grupo que não existe é criado.
- `ramal_ou_faixa`: só dígitos (`2150`) ou `inicio-fim` com `inicio <= fim`. Comparação numérica. Ramal com zero à esquerda ou nome não numérico nunca cai em faixa e fica em `"Sem grupo"` (suposição registrada na seção 13).
- Tudo ou nada: qualquer erro ou sobreposição (dentro do arquivo ou com faixas já gravadas) rejeita o arquivo inteiro e lista as linhas em conflito. Com `--replace`, apaga todos os grupos e importa na mesma transação; a sobreposição é checada só dentro do arquivo.

## 5. API consumida pela Fase 2

### Snapshot (`GET /api/snapshot`)

Estado completo. Servido pela projeção do hub (ADR-001), então `version` é sempre fronteira de lote. Resposta 200, gzip:

```json
{
  "success": true,
  "data": {
    "epoch": "1791900000123-3",
    "version": 48213,
    "server_time": "2026-10-13T14:30:00.123Z",
    "technology": "pjsip",
    "ami_connected": true,
    "extensions": [
      {
        "number": "2001",
        "name": "Maria Souza",
        "technology": "pjsip",
        "group": "Suporte",
        "registration": "registered",
        "ip": "192.0.2.23",
        "user_agent": "Yealink SIP-T46U 108.86.0.20",
        "latency_ms": 12,
        "device_state": "inuse",
        "calls": [
          {
            "id": "1791901712.4521",
            "direction": "inbound",
            "state": "talking",
            "peer": { "kind": "external", "number": "11987654321", "name": "ACME LTDA" },
            "started_at": "2026-10-13T14:28:32.120Z",
            "answered_at": "2026-10-13T14:28:39.540Z"
          }
        ]
      },
      {
        "number": "2002",
        "name": "João Lima",
        "technology": "pjsip",
        "group": "Suporte",
        "registration": "unreachable",
        "ip": "192.0.2.24",
        "user_agent": null,
        "latency_ms": null,
        "device_state": "unavailable",
        "calls": []
      }
    ]
  },
  "error": null,
  "meta": { "count": 2 }
}
```

- `epoch`: string opaca. O cliente só compara igualdade. Formato atual `<unix ms do início do processo>-<contador>`, único entre reinícios do daemon.
- `version`: inteiro monotônico durante a vida do processo.
- `technology`: `null` só antes da primeira detecção.
- `extensions` ordenado por `number` (ordem de string). Antes da primeira conexão AMI vem vazio.
- `server_time` serve para a tela calcular a diferença de relógio e mostrar a duração certa.

### WebSocket `/ws`: hello (cliente para servidor)

```json
{ "type": "hello", "epoch": "1791900000123-3", "version": 48213 }
```

### WebSocket `/ws`: delta (servidor para cliente)

Um lote a cada 250 ms, só quando houve mudança. Ramal que mudou várias vezes na janela aparece uma vez, com o estado final completo (substituição inteira por `number`, não patch de campo).

```json
{
  "type": "delta",
  "epoch": "1791900000123-3",
  "from_version": 48213,
  "to_version": 48219,
  "ami_connected": true,
  "server_time": "2026-10-13T14:30:00.373Z",
  "extensions": [
    {
      "number": "2002",
      "name": "João Lima",
      "technology": "pjsip",
      "group": "Suporte",
      "registration": "registered",
      "ip": "192.0.2.24",
      "user_agent": "Grandstream GXP1625 1.0.11.23",
      "latency_ms": 9,
      "device_state": "ringing",
      "calls": [
        {
          "id": "1791902000.4602",
          "direction": "internal",
          "state": "ringing",
          "peer": { "kind": "extension", "number": "2001", "name": "Maria Souza" },
          "started_at": "2026-10-13T14:30:00.250Z",
          "answered_at": null
        }
      ]
    }
  ]
}
```

Regra de aplicação no cliente (com `v` = versão local):
- `from_version <= v < to_version`: aplica (substitui cada ramal pelo `number`; ramal novo é inserido) e faz `v = to_version`. A sobreposição é segura porque cada item é o estado completo.
- `to_version <= v`: ignora (repetido).
- `from_version > v`: lacuna; o cliente busca o snapshot e reconecta.
- `epoch` diferente do local: o mesmo que lacuna.

Ramal nunca é removido por delta. Remoção só acontece com epoch novo (snapshot inteiro).

### WebSocket `/ws`: reload (servidor para cliente)

Enviado no lugar do replay quando não dá para continuar, ou para todos os clientes quando o epoch muda. Depois dele o servidor fecha com código 4000.

```json
{ "type": "reload", "reason": "epoch_changed", "epoch": "1791900000123-4" }
```

`reason`: `epoch_changed` (epoch do hello é antigo, ou o epoch acabou de mudar), `version_gap` (o hello pede versão mais antiga que o ring buffer), `version_ahead` (versão do hello maior que a do servidor: cliente de outro processo ou bug).

### Health (`GET /api/health`, só em 127.0.0.1)

200 quando `status = "ok"`; 503 quando `"degraded"` (AMI fora ou qualquer item de `deps` diferente de `ok`). Corpo igual nos dois casos, para monitor com `curl -f` funcionar sem parse.

```json
{
  "success": true,
  "data": {
    "status": "ok",
    "daemon_version": "0.1.0",
    "uptime_s": 86400,
    "ami": {
      "connected": true,
      "technology": "pjsip",
      "asterisk_version": "18.20.0",
      "connected_since": "2026-10-13T08:00:02.000Z",
      "reconnects": 1
    },
    "events_per_s": 142.5,
    "queue": { "len": 3, "cap": 65536, "overflows": 0 },
    "lag_ms": 4,
    "epoch": "1791900000123-3",
    "version": 48213,
    "extensions": { "total": 10000, "registered": 9120, "unregistered": 700, "unreachable": 180, "unknown": 0 },
    "trunks": 4,
    "active_calls": 312,
    "ws_clients": 7,
    "deps": [
      { "name": "ami_user", "status": "ok", "detail": "login aceito em 2026-10-13T08:00:01Z", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "manager_custom_include", "status": "ok", "detail": "manager.conf inclui manager_custom.conf e o bloco painel-ramais está presente", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "apache_proxy_conf", "status": "ok", "detail": "/etc/httpd/conf.d/painel-ramais.conf presente", "checked_at": "2026-10-13T14:30:00.000Z" }
    ]
  },
  "error": null,
  "meta": null
}
```

- `deps[].status`: `ok`, `fail` ou `unknown` (por exemplo, sem permissão de leitura em `/etc/asterisk/manager.conf`). `unknown` também deixa o status em `degraded` e o `detail` diz o que fazer. Exceção deliberada: o `%post` tira o acesso de "outros" de `manager_custom.conf` (o-rwx, dono asterisk) e o serviço não entra no grupo asterisk; então `EACCES` ao ler `manager.conf` ou `manager_custom.conf` é esperado e o item `manager_custom_include` passa a `ok` se o Login AMI foi aceito (`detail`: "não legível pelo serviço (esperado, o-rwx); login AMI aceito confirma o usuário"), `fail` se o Login foi recusado e `unknown` se ainda não houve Login. Arquivo ausente continua `fail`.
- `ami_user` vem do resultado do último Login da conexão única (não abre segunda conexão).
- `events_per_s`: média dos últimos 60 s. `lag_ms`: agora menos a hora de leitura do último evento processado. `queue.overflows`: quantas vezes a fila encheu e forçou ressincronização (seção 11.3); `queue.len` e `queue.cap` são da fila do leitor AMI (a que pode estourar).

## 6. Segurança

### Conta AMI: `write = system,reporting` (decisão do usuário, mantida)

Sem a classe `system` as actions `PJSIPShow*` e `ModuleCheck` não funcionam, e a maioria dos clientes usa PJSIP. Por isso o valor foi mantido.
Cada action usada pelo painel e a classe que ela exige (**a confirmar no Asterisk real com `manager show command <Action>`**; o
`./instalar.py verificar` coleta isso no relatório):

| Action | Uso | Tecnologia | Classe esperada (`write` do usuário AMI) |
|---|---|---|---|
| `Login` | autenticar a conexão única | ambas | nenhuma |
| `Logoff` | encerramento limpo | ambas | nenhuma |
| `Ping` | keepalive | ambas | nenhuma |
| `CoreSettings` | versão do Asterisk (recusa abaixo de 13) | ambas | system ou reporting |
| `ModuleCheck` | detectar `chan_pjsip.so` e `chan_sip.so` no modo auto | ambas | system |
| `PJSIPShowEndpoints` | lista de endpoints (ramais e troncos) | pjsip | system |
| `PJSIPShowEndpoint` | nome (callerid) de um ramal, em segundo plano e com taxa limitada (decisão 4) | pjsip | system |
| `PJSIPShowContacts` | contatos, IP, user agent, latência | pjsip | system |
| `PJSIPShowAors` | fallback quando `PJSIPShowContacts` não existe | pjsip | system |
| `SIPpeers` | lista de peers, IP, status, latência | sip | system ou reporting |
| `SIPshowpeer` | nome (callerid) e user agent de um peer, em segundo plano e com taxa limitada (decisão 4) | sip | system ou reporting |
| `DeviceStateList` | estado de dispositivo inicial | ambas | call ou reporting |
| `CoreShowChannels` | chamadas já em andamento (com `BridgeId` e `Duration`) | ambas | system ou reporting |

Fonte: `docs/CONTRATOS.md`, seção 7.1 (em caso de divergência vale o CONTRATOS).
Proibidas em qualquer hipótese: `Command`, `Originate`, `Redirect`, `Hangup`, `Atxfer`, `Setvar`, `Reload`, `ModuleLoad`, `UpdateConfig`, `GetConfig`, `Filter` e qualquer outra fora da lista.

**Risco residual aceito.** Se o próprio daemon for comprometido (ou o segredo da conta AMI vazar), a classe `system` dá acesso, pelo AMI, a actions
que o painel nunca usa, como `GetConfig` (leitura de segredos SIP e AMI) e `ModuleUnload`, além de `Reload`/`ModuleLoad`, `DBPut`, `PJSIPNotify`,
`Hangup` e `MixMonitor` com o parâmetro `Command` (execução como o usuário `asterisk`, AST-2014-006). A mitigação é compensatória, não elimina o risco.

**Controles compensatórios (obrigatórios):**
1. Lista fixa de actions no código do cliente AMI do daemon: qualquer action fora dela é recusada **antes de chegar ao socket**, com log de erro, e há teste unitário (veja `docs/CONTRATOS.md` 7.1).
2. `permit = 127.0.0.1/255.255.255.255` e `deny = 0.0.0.0/0.0.0.0` na conta; segredo `/etc/painel-ramais/ami-secret` com permissão 0600 e dono `painel-ramais`.
3. Unit systemd endurecida: `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ReadWritePaths=/var/lib/painel-ramais` (só isso), `CapabilityBoundingSet=` vazio, `RestrictAddressFamilies`, `MemoryDenyWriteExecute`, `UMask=0077` e outras (veja `packaging/painel-ramais.service`).
   Para validar no laboratório: `systemd-analyze security painel-ramais` (a nota exposta é o ponto de partida; registre o resultado na homologação).

### Porta 8790 e acesso local

O `painel-ramais.socket` (systemd) segura `127.0.0.1:8790` mesmo com o daemon parado ou em crash, então nenhum outro processo ocupa a porta. Qualquer usuário local ainda alcança a porta em TCP; as barreiras são login/sessão e token de uso único,
e o bloqueio de `X-Forwarded-*` em `/api/health` e `/debug`. Acesso externo: só pela 443 do Apache.

### RPM de rollback e instalador

- O instalador guarda o RPM anterior em `/var/lib/painel-ramais-instalador/` (acessível só ao root, 0700), **aceito pelo usuário** (decisão 3). Antes do `rollback` confere o nome do pacote e o **SHA-256** gravado na instalação. `./instalar.py remover --purge` apaga esse diretório também.
- O instalador roda como root e nunca lê nem grava como root em diretório do usuário do serviço: usa `runuser -u painel-ramais`, e o CSV de grupos vai pelo stdin da CLI. O `%post` edita o `manager_custom.conf` como o usuário `asterisk`.
- O RPM do bundle é conferido pelo `SHA256SUMS` antes de instalar. Obtenha o tar.gz de origem confiável.
- O `%post` nunca edita `manager.conf` nem arquivos do Issabel/IssabelPBX, e só adiciona o arquivo próprio do Apache.

## 7. Troubleshooting

| Sintoma | Onde olhar |
|---|---|
| Serviço não sobe | `systemctl status painel-ramais.socket painel-ramais.service`; `journalctl -u painel-ramais -n 50 --no-pager`. O `.socket` continua ouvindo mesmo com o daemon parado (conexões esperam). Config inválida: o daemon diz a chave. Exit 4 (banco com esquema mais novo que o binário): o systemd não reinicia; instale a versão certa ou use `./instalar.py rollback` |
| `ami: connected=false` no health | Asterisk parado, bloco AMI ausente (veja `deps[].ami_user`) ou `manager.conf` sem `#include manager_custom.conf`. Rode `asterisk -rx 'manager reload'` |
| Dependência `manager_custom_include` ou `apache_proxy_conf` em `fail` | uma atualização do Issabel regravou o `manager.conf` ou apagou a conf do Apache. Reaplique: `./instalar.py instalar` (mesma versão: `rpm -U --replacepkgs`, o `%post` é idempotente) |
| `https://<servidor>/painel-ramais/` dá 502/503 | `httpd -t`; `ls /etc/httpd/conf.d/painel-ramais.conf`; SELinux: `getsebool httpd_can_network_connect` deve estar `on`; `mod_proxy_wstunnel` carregado (`httpd -M \| grep wstunnel`) |
| WebSocket cai a cada minuto | `Timeout` do Apache (ADR-001); o `verificar` mostra o valor |
| Porta 8790 ocupada | `ss -ltnp \| grep :8790` (deve aparecer o `systemd`, dono do `painel-ramais.socket`). Se for outro processo, mude `listen:` no `config.yaml` e reinstale a mesma versão (o `%post` regera a conf do Apache e o drop-in do socket) e reinicie |
| Ramais todos "desconhecido" | AMI fora do ar (comportamento previsto: nunca vira "não registrado" sem AMI) |
| Cookie `Secure` e acesso por `http://` | o painel exige HTTPS; use a URL da 443 |
| Instalação abortou | `/var/log/painel-ramais/instalacao.log` (todo comando e saída; a senha nunca é registrada) |

## 8. Remoção e limpeza total

```sh
./instalar.py remover            # rpm -e; mantém /var/lib/painel-ramais, /etc/painel-ramais e o usuário
./instalar.py remover --purge    # também apaga /var/lib/painel-ramais, /etc/painel-ramais e /var/lib/painel-ramais-instalador (pede o nome do servidor; --sim dispensa)
userdel painel-ramais            # usuário de sistema (manual)
# à mão, sem o instalador:
rpm -e painel-ramais && rm -rf /var/lib/painel-ramais /var/lib/painel-ramais-instalador /etc/painel-ramais && userdel painel-ramais
```

O `rpm -e` remove binário, unit, conf do Apache e o bloco AMI. O booleano SELinux `httpd_can_network_connect` **não** é revertido.

## 9. Roteiro de ensaio em Rocky 8 e Rocky 9 (container ou VM)

Pendente de execução: este repositório foi desenvolvido no Windows, sem Linux disponível. Rode em **Rocky 8 mínimo** e **Rocky 9**,
com o simulador (`cmd/amisim`, modos `sip` e `pjsip`) no lugar do Asterisk. Preparação: copie o bundle, extraia e entre na pasta.

1. **Sem python3 (só Rocky 8 mínimo):** `rpm -q python3` deve dizer `package python3 is not installed`; `ls /usr/libexec/platform-python` existe.
   `./instalar.py verificar` deve rodar mesmo assim (primeira linha poliglota). Esperado: linhas `[OK   ] ...`, `[AVISO] ...`, `[ERRO ] ...` e
   `Resultado: N erro(s), M aviso(s).`. Sem Asterisk/Issabel reais, esperam-se ERRO/AVISO nesses itens; o objetivo é provar que roda.
2. **Falha forçada:** `sed -i 's/^LoadModule proxy_wstunnel_module/#&/' /etc/httpd/conf.modules.d/*.conf && systemctl restart httpd`.
   `./instalar.py verificar` → `[ERRO ] mod_proxy_wstunnel ausente`, saída 1. `./instalar.py instalar` aborta com `Verificacao com ERRO: nada foi alterado`
   e `rpm -q painel-ramais` continua dizendo que não está instalado. Reverta o sed.
3. **Instalação interativa:** `./instalar.py instalar` (tecnologia `auto`, admin, senha com 10+ caracteres, CSV `grupos-exemplo.csv`).
   Esperado ao fim: bloco `== Resumo ==` com versão, tecnologia, ramais, grupos e `Acesso: https://<ip>/painel-ramais/`; saída 0.
   Confira: `systemctl is-active painel-ramais` → `active`; `ls -l /etc/painel-ramais/` (`secret` e `ami-secret` 0600 `painel-ramais`);
   `grep -c 'BEGIN painel-ramais' /etc/asterisk/manager_custom.conf` → `1`; `stat -c '%a %U' /etc/asterisk/manager_custom.conf` sem acesso para outros;
   `curl -fsS http://127.0.0.1:8790/api/health` → JSON com `"status":"ok"` (**anexe a saída real**).
   **Socket activation:** `systemctl is-active painel-ramais.socket` → `active`; `ss -ltnp | grep :8790` mostra o `systemd`. Mate o daemon
   (`kill -9 $(systemctl show -p MainPID --value painel-ramais)`): a porta continua ocupada pelo systemd e o serviço volta em ~2 s
   (`Restart=always`). Anexe as saídas reais.
4. **Instalação com `--respostas`:** crie `respostas.ini` e o arquivo de senha 0600 do root (modelo no `LEIAME-INSTALACAO.md`);
   `./instalar.py instalar --respostas respostas.ini --sim`; saída 0.
5. **Idempotência / upgrade:** rode `instalar` de novo com o mesmo bundle: `Mesma versao (...): reaplicando.`, usuário existente mantido
   (`AVISO: usuario ... ja existe`), grupos iguais. Com um bundle de versão maior: `Upgrade A -> B.`, e passa a existir
   `/var/lib/painel-ramais-instalador/rpm-anterior/`.
6. **`rpm -i / -U / -e` direto:** `rpm -i`, `rpm -U` (config e segredos preservados: compare `sha256sum` antes e depois) e `rpm -e`
   (binário, unit, `/etc/httpd/conf.d/painel-ramais.conf` e bloco AMI somem; `/var/lib/painel-ramais` e `/etc/painel-ramais` ficam).
7. **Atualização simulada do Issabel:** reescreva `manager.conf` sem o include (`sed -i '/manager_custom/d' /etc/asterisk/manager.conf`), apague
   `/etc/httpd/conf.d/painel-ramais.conf`, `systemctl restart httpd asterisk` (ou reinicie o simulador). Esperado: o serviço segue `active`,
   reconecta sozinho e, em até 60 s, `curl -s http://127.0.0.1:8790/api/health` mostra `manager_custom_include` e `apache_proxy_conf` com `"status":"fail"`
   e o `status` geral `degraded` (HTTP 503). `./instalar.py instalar` (mesma versão) corrige o Apache; o include do `manager.conf` é manual.
8. **`status`:** `./instalar.py status` mostra versão, serviço, AMI, ramais por estado, clientes WS e cada dependência; saída 0 se `ok`, 2 se `degraded`.
9. **`rollback`:** após um upgrade, `./instalar.py rollback --sim`. Esperado: `Plano: reinstalar painel-ramais-<anterior>...`, `Rollback concluido.`,
   versão anterior em `rpm -q painel-ramais` e dados preservados. Sem RPM anterior guardado, sai com 2 e não altera nada.
10. **`remover` e `remover --purge`:** `./instalar.py remover --sim` mantém os dados; `./instalar.py remover --purge` pede o nome do servidor
    (`hostname`); com o nome certo apaga `/var/lib/painel-ramais` e `/etc/painel-ramais`.
11. **Medições de CPU e memória do teste de carga** devem ser feitas em Linux; o relatório deve dizer onde cada medição foi feita.

## 10. Relatório de carga

Resumo de `docs/relatorio-carga.md` (gerado por `cmd/loadtest` em 2026-10-13 20:49, daemon **0.1.1**). **Medido no Windows** (windows/amd64, go1.27.2; o binário
do teste foi o build Windows do daemon, não o ELF do RPM), com o simulador de AMI (`cmd/amisim`) no mesmo processo do cliente WebSocket e o daemon como
processo separado. Cenário: 10.000 ramais, 400 chamadas simultâneas alvo, regime de ~10 min (inclui o preenchimento de nomes), rajada de 3.000 desregistros, AMI fora por 4 s.
Números copiados do relatório:

| Medida | sip | pjsip |
|---|---|---|
| p99 evento -> delta na rajada (critério: < 1 s) | 238 ms | 245 ms |
| p99 evento -> delta em todas as fases, exceto queda | 252 ms | 250 ms |
| p99 evento -> delta **durante** o preenchimento de nomes | 250 ms | 250 ms |
| rajada: desregistros entregues ao cliente | 3000/3000 em 98 ms | 3000/3000 em 257 ms |
| divergência do snapshot final e do estado WS em relação à verdade | 0 de 10000 | 0 de 10000 |
| preenchimento de nomes a 20/s: corretos ao fim / tempo desde o daemon pronto | 10000/10000 em 543.0 s | 10000/10000 em 541.9 s |
| snapshot ocioso: cru / gzip | 1829.2 KiB / 70.0 KiB | 2018.6 KiB / 86.3 KiB |
| snapshot sob carga: cru / gzip | 1952.8 KiB / 94.2 KiB | 2143.1 KiB / 111.4 KiB |
| queda do AMI: `unregistered` (máximo) / `unknown` (mínimo) | 0 / 10000 | 0 / 10000 |
| queda do AMI: epoch novo na queda e outro na volta | sim | sim |
| "com quem fala" ao vivo | 106/106 passos | 106/106 passos |
| chamadas em andamento restauradas pelo snapshot após reconexão | 48/48 | 48/48 |
| detecção em `technology: auto` | sip | pjsip |
| RSS no fim do regime / pico (Windows) | 64.6 MiB / 89.1 MiB | 75.4 MiB / 110.7 MiB |
| CPU do daemon no regime (Windows, % de 1 núcleo) | 0.7% | 1.9% |

**Capturas reproduzidas** (`amisim -replay`): "com quem fala" correto em **226/226** pontos em 10 capturas (**sintéticas**, geradas pelo simulador; capturas reais vêm da homologação), sem nenhuma action fora da allowlist.

**Ressalvas registradas no relatório (leia a seção correspondente em `docs/relatorio-carga.md`):**
- "nenhum estado fugaz errado visto no WS": RESSALVA (sip: 4 fugazes errados e 6 aceitos por terem só dispositivo/chamadas de instantes diferentes; pjsip: 3 e 3).
- "memória sem crescimento contínuo": **NÃO CONCLUSIVO no Windows** (sip 0.79 MiB/min em 580 s; pjsip 0.37 MiB/min em 582 s de regime).

**O que falta em Linux (não substituível pelo Windows):** a memória em 30 min e a CPU. Os números de memória e CPU acima são do Windows e valem só como ordem de grandeza.
Para refazer em Linux (WSL2, container ou Rocky), com o binário do daemon e o `loadtest` compilados para linux/amd64:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o painel-ramais ./cmd/painel-ramais
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o loadtest ./cmd/loadtest
./loadtest -daemon ./painel-ramais -duration 30m -out docs/relatorio-carga-linux.md
```

O relatório informa onde cada medição foi feita. Também ficam para Linux `rpm -i/-U/-e`, systemd, SELinux, Apache e `instalar.py` (seção 9).

### Reproduzir uma captura: `amisim -replay` e `loadtest -captures`

```sh
# simulador reproduzindo um arquivo de captura (CONTRATOS 18) em vez de simular; o daemon conecta no AMI do simulador
go run ./cmd/amisim -replay testdata/capturas/sintetica-transferencia-pjsip.txt -speed 20 -listen 127.0.0.1:5038 -http 127.0.0.1:5039
#   -speed 1 = tempo gravado; 0 = sem esperas

# teste de carga: o "com quem fala" por reprodução roda sobre todas as capturas da pasta (padrão testdata/capturas)
go run ./cmd/loadtest -captures testdata/capturas -replay-speed 20 -out docs/relatorio-carga.md
#   -captures "" desliga essa parte
```

Os `.txt` de `testdata/capturas/` têm CRLF e tamanho exato por quadro, por isso o `.gitattributes` os marca como `binary` (o git não pode normalizá-los).
Cada `.esperado.json` ao lado é o oráculo do ponto a ponto. Detalhes: `testdata/capturas/README.md`.

## 11. Desenvolvimento

Windows, PowerShell: `.\build.ps1 build | test | rpm | bundle | all` (Go e nfpm do PATH ou de `.tools\`; `bundle` usa `python` para `tools/bundle.py`).
Testes do instalador: `python -m unittest` (stdlib, Python 3.6+). Compatibilidade: `vermin --target=3.6- instalar.py tools/bundle.py`
(venv local em `.tools\venv`, ignorado pelo git). Fim de linha LF em tudo que vai para o Linux: `tools\check-lf.ps1`.

### Ferramentas de build reproduzíveis (sem administrador)

`.tools\` fica no `.gitignore` e guarda as ferramentas portáteis. Versões usadas neste projeto:

| Ferramenta | Versão | Onde fica |
|---|---|---|
| Go | **1.27.2** (windows/amd64) | `.tools\go\bin\go.exe` (caches em `.tools\gopath` e `.tools\gocache`, definidos pelo `build.ps1`) |
| nfpm | **2.48.0** | `.tools\nfpm\nfpm.exe` |
| vermin | pip, em venv | `.tools\venv` |

Como obter em outra máquina (zip oficial + checagem de checksum, sem instalador e sem administrador):

```powershell
# Go: baixe go1.27.2.windows-amd64.zip em https://go.dev/dl/ e confira o SHA-256 listado nessa página
Get-FileHash .tools\dl-go\go.zip -Algorithm SHA256        # compare com o valor de https://go.dev/dl/
Expand-Archive .tools\dl-go\go.zip -DestinationPath .tools\          # cria .tools\go

# nfpm: baixe nfpm_2.48.0_Windows_x86_64.zip e checksums.txt em https://github.com/goreleaser/nfpm/releases/tag/v2.48.0
Get-FileHash .tools\dl-nfpm\nfpm.zip -Algorithm SHA256    # compare com a linha de nfpm_2.48.0_Windows_x86_64.zip no checksums.txt
Expand-Archive .tools\dl-nfpm\nfpm.zip -DestinationPath .tools\nfpm

python -m venv .tools\venv ; .tools\venv\Scripts\pip install vermin
```

O SHA-256 publicado em `checksums.txt` para `nfpm_2.48.0_Windows_x86_64.zip` é
`8ce12620212f5db557e370e35e2279ac7b106ea51e6a546a2be37a1a5d5bd8de`. Nada disso exige instalar software no sistema.
Mapa de decisões técnicas: `docs/CONTRATOS.md`, `docs/adr/` e `docs/DECISOES-FASE-1.md`.

## 12. Nome do ramal e sessão (decisões 4 e 5)

Comportamento decidido (chaves de configuração no `config.yaml`, ver seção 3 e `docs/CONTRATOS.md` seção 6):

- **Nome do ramal:** `PJSIPShowEndpoint` (pjsip) e `SIPshowpeer` (sip) estão na lista fixa. Nunca em rajada: o preenchimento roda em segundo plano depois do snapshot,
  com taxa limitada (**`ami.name_fill_per_second`, padrão 20 consultas/s, de 1 a 200**); enquanto isso o painel funciona com o nome vazio. Também atualiza com o `CallerIDName` dos eventos de chamada originados pelo ramal.
  O ciclo completo refaz-se quando chega o evento `Reload` do Asterisk (no eventfilter) e **a cada 6 h**. Os nomes ficam em cache no SQLite e aparecem logo após um restart.
- **Login e token:** login obrigatório para o painel e a API; token de entrada válido por 60 s e de uso único; `/api/health` só via localhost.
- **Painel em TV de NOC, 24 h:** a sessão se renova sozinha enquanto houver WebSocket ativo. Expiração por **inatividade de 12 h: `auth.session_ttl: 12h`** (de 15m a 24h).
  Ao expirar, o WebSocket envia um aviso explícito antes de fechar, para a tela da Fase 2 mostrar isso em vez de congelar.
- O teste de carga deve ser refeito com o preenchimento de nomes ativo (meta: p99 abaixo de 1 s).

## 13. Captura de AMI real: roteiro de chamadas (homologação)

O subcomando `painel-ramais capturar` está descrito no CONTRATOS, seção 18. Texto abaixo copiado de `docs/ROTEIRO-CAPTURA.md`.

Objetivo: gravar o tráfego AMI de um Asterisk real durante um conjunto conhecido de chamadas, para que o simulador reproduza o arquivo e os testes de "com quem fala" rodem sobre dados reais (hoje as amostras foram escritas de memória). Texto para o README (seção de homologação); o formato do arquivo está no `docs/CONTRATOS.md`, seção 18.

### Antes de começar

1. O painel instalado e conectado ao Asterisk (`/api/health` com `ami.connected = true`).
2. Anote os ramais que vai usar: dois ramais internos (A e B), um terceiro (C), um ramal com fila ou ring group, um tronco e um número externo seu para ligar e receber.
3. Rode como o usuário do serviço, num diretório onde ele grave:

```
sudo -u painel-ramais painel-ramais capturar --minutos 20 --saida /var/lib/painel-ramais/captura-AAAAMMDD.txt --anonimizar
```

- `--minutos` é o tempo de captura ao vivo (fracionário aceito; Ctrl-C encerra antes e ainda grava o snapshot final).
- A captura usa a conta AMI do painel e só as actions da lista fixa; nada é alterado no Asterisk.
- Senhas e segredos saem mascarados sempre. `--anonimizar` troca números externos, nomes e IPs por valores fictícios (o mesmo fictício para o mesmo original). **Revise o arquivo antes de compartilhar** (`grep` pelos seus números e nomes); IPv6 não é tratado.
- O arquivo tem modo 0600 e o comando recusa sobrescrever.

### Roteiro (execute na ordem, com 20 a 30 s de intervalo entre os itens)

Anote a hora de cada passo para cruzar com o arquivo.

1. **Chamada interna**: A liga para B, B atende, conversam 20 s, A desliga.
2. **Chamada interna não atendida**: A liga para B e B não atende (deixe tocar até cair no timeout ou no correio de voz). Repita desligando do lado de A antes de tocar muito.
3. **Entrada por tronco**: de um celular, ligue para o número do tronco; A atende, 20 s, desligue.
4. **Saída por tronco**: A liga para o seu celular (prefixo de saída), atenda, 20 s, desligue.
5. **Chamada em espera**: A em chamada com B coloca B em espera, 15 s, retoma, desliga.
6. **Transferência cega**: externo liga para A; A atende e transfere (cega) para B; B atende, 10 s, desliga. Repita com chamada interna (B liga para A e A transfere para C).
7. **Transferência assistida**: externo liga para A; A consulta C (assistida), C atende, A completa a transferência; 10 s, desliga. Repita cancelando a consulta (A volta para o externo).
8. **Fila**: ligue de fora para a fila; um agente (ramal) atende; 15 s; desligue. Faça uma segunda ligação deixando a fila tocar sem atender até o transbordo.
9. **Ring group**: ligue para o ring group; um dos ramais atende; 15 s; desligue. Faça outra em que ninguém atende.
10. **Ramal desregistrando e voltando**: desligue o telefone (ou o softphone) de C da rede por 40 s; ligue de novo e espere registrar. Faça isso também com o ramal em chamada.
11. **Restart do Asterisk no meio de uma chamada**: com uma chamada em conversa (A com B) e outra tocando (externo para a fila), rode `systemctl restart asterisk`. Espere o painel reconectar e mantenha a captura por mais 1 minuto.
12. **Reload**: aplique uma alteração qualquer no IssabelPBX (sem mudar ramais) para gerar `Reload`; observe que os nomes são refeitos.

Ao final espere o prazo de `--minutos`: o comando grava um snapshot final (com as chamadas ainda em andamento) e imprime a quantidade de quadros. Guarde o arquivo e a lista de horários e resultados esperados de cada passo (quem falou com quem, direção, atendida ou não); o QA usa essa lista como oráculo do replay.

### O que conferir no arquivo antes de entregar

- Não aparece nenhum segredo (`grep -i 'secret\|password'` mostra só `***`).
- Com `--anonimizar`, não aparecem seus números externos nem os IPs reais (`grep` por eles).
- O `#fim` traz `tecnologia`, `asterisk` e `encerrado_por`.
