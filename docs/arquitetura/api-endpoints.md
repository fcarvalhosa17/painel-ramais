# API REST e WebSocket

Extraído de `internal/api/server.go` (função `Handler`) e dos handlers. O daemon escuta só em `127.0.0.1:8790`; o
navegador chama `/painel-ramais/<rota>` e o Apache tira o prefixo. Exemplos de JSON completos e regras de validação:
[CONTRATOS](../CONTRATOS.md), seções 1 a 5 (o número da seção está em cada linha).

## Regras comuns

- **Envelope** em toda resposta `/api/*`: `{ "success", "data", "error": {code, message}, "meta" }`. Erros e códigos:
  CONTRATOS 1.2.
- **Sessão**: cookie `painel_session` (`Path=/painel-ramais/`, `Secure`, `HttpOnly`, `SameSite=Strict`). Sem sessão:
  `401 unauthorized`.
- **Administrador**: sessão de token com `subject` começando por `issabel:admin:`; além disso, a cada escrita, o usuário
  é conferido no grupo `administrator` do `/var/www/db/acl.db` (`issabel_acl.go`). Senão `403 forbidden`.
- **POST**: só `Content-Type: application/json`, JSON estrito (campo desconhecido é `400`), `Sec-Fetch-Site` ausente,
  `same-origin` ou `none` (outro valor é `403`). Corpo até 4 KiB; rotas de visão até 1 MiB; `assign` até 2 MiB.
- **gzip** quando o cliente aceita. Toda resposta leva `X-Request-Id` e `X-Content-Type-Options: nosniff`.
- **Só localhost** (`/api/health`, `/debug`): com `X-Forwarded-For` ou `X-Forwarded-Host` responde `404`; o Apache também
  não repassa essas rotas (`ProxyPass "!"`).
- Caminho com segmento `..`: `404`. Rota inexistente: `404 not_found`.

## Autenticação (`internal/api/authn.go`)

| Método e rota | Sessão | Entrada | Devolve |
|---|---|---|---|
| `POST /api/login` | não | `{username, password}`; só o usuário `emergencia` entra | `{subject, kind: "password", admin: false, expires_at}` e o cookie. Limites: 5 falhas por IP e 20 por usuário em 5 min, 4 bcrypt simultâneos (`429`). CONTRATOS 3.1 |
| `POST /api/logout` | não exige | `{}` ou vazio | `data: null`, apaga a sessão e expira o cookie. CONTRATOS 3.2 |
| `POST /api/token` | não | `{token}` (HMAC, 60 s, uso único) | `{subject, kind: "token", admin, expires_at}` e cookie de sessão do navegador. Falha: `401 invalid_token`. CONTRATOS 3.3 |
| `GET /api/session` | sim | nada | o mesmo `data` do login/token (a tela usa `admin` para mostrar a edição). CONTRATOS 3.4 |

## Dados ao vivo (`internal/api/data.go`)

| Método e rota | Sessão | Devolve |
|---|---|---|
| `GET /api/snapshot` | sim | estado completo da projeção do hub: `epoch`, `version`, `server_time`, `technology`, `ami_connected`, `extensions[]` (ramal com `groups`, `range_groups`, `manual_groups`, registro, IP, user agent, latência, `device_state`, `calls[]`), `groups[]` (catálogo de visões com faixas), `calls[]` (chamadas ao vivo), `queues[]` (filas); `meta.count` = ramais. CONTRATOS 4.1, 2.1 a 2.4 |
| `GET /api/groups` | sim | lista de visões com `ranges` e `extensions` (números), lida da projeção; `"Sem grupo"` por último quando tem ramal. A tela atual não a usa (lê o catálogo do snapshot e do delta). CONTRATOS 4.2 |
| `GET /api/history` | sim | série por minuto: `group` (nome, só quando veio uma visão; senão `null`), `groups` (as visões pedidas), `step_s: 60`, `points[]` com `t`, `registered`, `unregistered`, `unreachable`, `in_call`, `gap`. Parâmetros `from`, `to` (RFC 3339, até 30 dias) e `group` **repetível** (até 20, sem repetir; ausente = total). Com várias visões soma por minuto, e o minuto só vale se todas têm dado. `meta`: `retention_days`, `count` e, com 2 ou mais visões, `overlap` (algum ramal está em mais de uma; a soma pode contá-lo duas vezes). CONTRATOS 4.3 |
| `GET /api/health` | não, só localhost | `status` (`ok` com HTTP 200, `degraded` com 503), `daemon_version`, `uptime_s`, `ami{connected, technology, asterisk_version, connected_since, reconnects}`, `events_per_s`, `queue{...}`, `lag_ms`, `epoch`, `version`, `extensions{total, registered, unregistered, unreachable, unknown}`, `trunks`, `active_calls`, `ws_clients`, `registrations{...}`, `live_calls`, `queues{...}`, `deps[]` (`ami_user`, `manager_custom_include`, `apache_proxy_conf`, `issabel_key`, `manager_custom_perm`, `issabel_menu`). CONTRATOS 4.4 |
| `GET /debug` | sim, só localhost | HTML sem envelope, refresh de 2 s: contagens, chamadas ativas, últimos 20 lotes. Não é contrato. CONTRATOS 4.5 |

## Visões, nome interno "groups" (`internal/api/groups.go`, só administrador)

| Método e rota | Entrada | Devolve |
|---|---|---|
| `POST /api/groups/create` | `{name, ranges?: [{start, end}]}` | `{name}`; `409 conflict` se o nome existe. CONTRATOS 4.6 |
| `POST /api/groups/update` | `{name, new_name?, ranges?}` (`ranges` substitui todas as faixas) | `{name}` com o nome final. CONTRATOS 4.6 |
| `POST /api/groups/assign` | `{mode: "add", numbers, groups}` ou `{mode: "remove", numbers, group}` | `{changed}` ou `{changed, not_manual}`. CONTRATOS 4.6 |
| `POST /api/groups/import` | `{csv, replace?}` (CSV com cabeçalho `visao,ramal` ou `grupo,ramal`) | `{rows, replace, ignored}`; `400 invalid_csv` com as linhas do erro. CONTRATOS 4.6 e 9 |
| `POST /api/groups/delete` | `{name}` | `{name}`; `404` "Visão inexistente". CONTRATOS 4.6 |

Toda escrita grava auditoria em `group_audit` na mesma transação, relê as visões (`ReloadGroups`) e chega às telas por
delta, sem epoch novo. As mensagens de erro já dizem "visão".

## Filas (`internal/api/queues.go`)

| Método e rota | Sessão | Entrada | Devolve |
|---|---|---|---|
| `POST /api/queues/rename` | administrador | `{queue, name}`; `name: ""` apaga o nome | `{queue, name}` (`name: null` depois de apagar); `404` "Fila inexistente". CONTRATOS 4.8 |

## Relatório de registro (`internal/api/registrations.go`, qualquer sessão)

Toda resposta traz o cabeçalho `period`, `from`, `to`, `since` ("Dados desde"), `paused_until`, `pause_reason`,
`state_unknown_since`. `period`: `24h` (padrão), `7d` ou `30d`. CONTRATOS 4.7.

| Rota | Parâmetros | Devolve |
|---|---|---|
| `GET /api/registrations/summary` | `group` (uma visão; ausente = todos) | `group`, `items[]` com até 10 ramais que mais perderam registro ou ficaram inalcançáveis em 24 h. CONTRATOS 4.7.1 |
| `GET /api/registrations` | `period`, `cursor` | `cursor`, `full`, `rows[]` (linha por ramal: contadores, última queda, último IP, `down_since`, `down_kind`, `last_registered` etc.). `full: false` traz só o que mudou desde o cursor. CONTRATOS 4.7.2 |
| `GET /api/registrations/extension` | `number`, `period` | `extension` (a mesma linha) e `events[]` (até 200 ocorrências, mais nova primeiro), `events_limit`. CONTRATOS 4.7.3 |
| `GET /api/registrations/series` | `period`, `group` **repetível** (até 20) | `group` (uma só visão, senão `null`), `groups`, `step` (`hour` ou `day`), `points[]` com `lost_registration` e `became_unreachable`, `system_events[]`; com várias visões soma os minutos de todas; `meta.overlap` como no `/api/history`. CONTRATOS 4.7.4 |

A exportação CSV da aba Registros é montada no navegador (`registros.js`), sem rota: CONTRATOS 4.7.5.

## Tela embutida (`internal/api/tela.go`, sem sessão, GET e HEAD)

| Rota | Devolve |
|---|---|
| `/` | `index.html` só com `Sec-Fetch-Dest` `iframe` ou ausente; qualquer outro destino recebe `303` para `/painel-ramais/acesso#` |
| `/acesso` | `acesso.html` (aviso sem script) |
| `/emergencia` e `/emergencia/` | `301` para a barra; depois a mesma `index.html` com `frame-ancestors 'none'` |
| `/assets/<arquivo>` | só `.js`, `.css`, `.svg`, `.png`, um nível; `Cache-Control: no-cache` e `ETag` |
| `/assets/fonts/<arquivo>` | só `.woff2` e os textos `OFL-Barlow.txt`, `OFL-Exo2.txt`, `ORIGEM.txt`; cache de uma semana com `ETag` |

Cabeçalhos de segurança da tela (CSP sem nada inline nem externo): CONTRATOS 1.3.

## WebSocket `/ws` (`internal/api/ws.go`, `hub.go`, `frames.go`)

Exige sessão no upgrade (sem ela, HTTP 401). Fluxo: a tela busca `/api/snapshot`, abre `/ws` e manda **uma** mensagem
`{"type":"hello","epoch","version"}` em até 5 s. CONTRATOS 5.

| Mensagem do servidor | Quando | Conteúdo |
|---|---|---|
| `delta` | a cada 250 ms, só se algo mudou (e no replay de quem reconecta) | `epoch`, `from_version`, `to_version`, `ami_connected`, `server_time`, `extensions[]` (ramal inteiro), `removed[]`; opcionais `groups` (catálogo inteiro, só quando mudou), `calls`, `calls_removed`, `queues`, `queues_removed`. CONTRATOS 5.2 |
| `reload` | epoch mudou, versão fora do ring buffer ou à frente | `reason` (`epoch_changed`, `version_gap`, `version_ahead`), `epoch`; fecha com 4000. CONTRATOS 5.3 |
| `session_expired` | sessão vencida ou revogada | `reason` (`expired`, `revoked`), `message`; fecha com 4001. CONTRATOS 5.5 |

Fechamentos: 1000/1001 normal ou daemon parando, 4000 depois de `reload`, 4001 sessão, 4002 cliente lento, 4003 erro de
protocolo. Ping a cada 25 s. A conexão ativa renova a sessão a cada `session_ttl/6`.
