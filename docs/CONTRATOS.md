# Contratos da Fase 1: Painel de Ramais (Issabel 5)

**Data**: 2026-10-13
**Origem**: `docs/PROMPT-FASE-1.md` e `docs/ADENDO-FASE-1-INSTALADOR.md` (especificação do usuário, mesmo peso; o adendo troca o `discovery.sh` pelo `instalar.py verificar`). Em conflito, a especificação vence e este documento é corrigido.
**Decisões de arquitetura ligadas**: `docs/adr/ADR-001-protocolo-websocket.md` (Aceito), `docs/adr/ADR-002-sessao-e-token-hmac.md` (PROPOSTO, aguarda o usuário), `docs/adr/ADR-003-pacote-rpm-e-integracao-issabel.md` (Proposto, aguarda spike em Rocky 8 e 9).

Este documento é a fonte única dos formatos trocados entre as partes do coletor, e entre o coletor e quem o consome (fase 2: tela; fase 3: link do Issabel). Quem implementa não inventa campo: campo novo entra aqui primeiro.

---

## 0. Decisões de padrão já assumidas

Valem para todos os agentes. Reabrir só com o usuário.

| # | Decisão | Onde se materializa |
|---|---|---|
| D1 | Ramal = endpoint (PJSIP) ou peer (SIP) cujo nome casa **inteiro** com `extension_pattern` (padrão `^[0-9]+$`). O resto é tronco. Troncos não aparecem no snapshot; servem para resolver "com quem fala". | seção 7, seção 11 |
| D2 | Um grupo por ramal. A importação rejeita o arquivo inteiro se houver sobreposição de faixas (no arquivo ou com o que já existe). `--replace` troca todos os grupos numa transação só. | seções 8, 9, 10 |
| D3 | A CLI recusa rodar com euid 0. Uso: `sudo -u painel-ramais painel-ramais ...`. Motivo: root criaria o banco e os arquivos WAL com dono root e o daemon pararia de escrever. | seção 8 |
| D4 | Mudança de grupo (e de usuário) chega ao daemon por SIGHUP. A CLI manda o sinal sozinha; o daemon relê os grupos, gera epoch novo e revalida as sessões dos WebSockets abertos. | seção 8, ADR-001 |
| D5 | `/debug` e `/api/health` ficam fora do proxy do Apache (`ProxyPass "!"`) e o daemon recusa nessas rotas qualquer requisição com `X-Forwarded-For` ou `X-Forwarded-Host` (o mod_proxy sempre adiciona esses cabeçalhos). Duas barreiras independentes. | seção 1, ADR-003 |
| D6 | REST usa o envelope `{success, data, error, meta}`. WebSocket usa mensagens tipadas por `type`, sem envelope. | seções 1 a 5 |
| D7 | Histórico por minuto. Minuto sem AMI (ou sem daemon) sai como `gap: true` com contagens `null`, nunca como queda de registros. | seções 4.3, 10 |
| D8 | Chaves JSON e valores de enum em inglês, `snake_case`. Datas em RFC 3339 UTC com milissegundos (`2026-10-13T14:30:00.000Z`). | todas |
| D9 | O `instalar.py` orquestra e valida; nunca edita `manager_custom.conf`, Apache, SELinux nem o SQLite. Integração com o sistema é do `%post` do RPM; usuário e grupos, da CLI do daemon. | seção 15, ADR-003 |

---

## 1. Rotas, acesso e envelope

O daemon escuta só em loopback. O Apache publica o prefixo `/painel-ramais/` e o remove ao repassar: o navegador chama `/painel-ramais/api/snapshot`, o daemon recebe `/api/snapshot`.

| Método | Rota no daemon | Sessão | Quem alcança | Resposta |
|---|---|---|---|---|
| POST | `/api/login` | não | navegador via Apache | envelope |
| POST | `/api/logout` | não exige (idempotente) | navegador via Apache | envelope |
| POST | `/api/token` | não | navegador via Apache | envelope |
| GET | `/api/snapshot` | sim | navegador via Apache | envelope, gzip |
| GET | `/api/groups` | sim | navegador via Apache | envelope, gzip |
| GET | `/api/history` | sim | navegador via Apache | envelope, gzip |
| GET | `/ws` | sim (cookie no upgrade) | navegador via Apache (mod_proxy_wstunnel) | mensagens tipadas |
| GET | `/api/health` | não | só localhost direto | envelope |
| GET | `/debug` | sim | só localhost direto | HTML |

Regras:
- **Sessão**: toda rota `/api/*` e `/ws` exige cookie de sessão válido, exceto `login`, `logout`, `token` e `health`; `/debug` também exige sessão (além de ser só localhost). Sem sessão: `401 unauthorized`. Detalhes no ADR-002.
- **Só localhost** (`/api/health`, `/debug`): requisição com `X-Forwarded-For` ou `X-Forwarded-Host` recebe `404` (não revela que a rota existe). O teste é `curl http://127.0.0.1:<porta>/api/health` no próprio servidor.
- **POST** exige `Content-Type: application/json` (senão `415`) e corpo de no máximo 4 KiB (senão `413`). JSON com campo desconhecido é `400`. O daemon nunca responde cabeçalhos CORS. Toda resposta leva `X-Content-Type-Options: nosniff`. O `http.Server` tem `ReadTimeout` de 15 s (o handler do `/ws` limpa o prazo da conexão).
- **gzip**: respostas `/api/*` saem com gzip quando o cliente manda `Accept-Encoding: gzip` (stdlib `compress/gzip`).
- `/` e qualquer outra rota: `404` nesta fase (a tela entra na fase 2).

### 1.1 Envelope REST

Sucesso:

```json
{ "success": true, "data": { "exemplo": 1 }, "error": null, "meta": null }
```

Erro:

```json
{ "success": false, "data": null, "error": { "code": "unauthorized", "message": "Sessão ausente ou expirada" }, "meta": null }
```

- `meta` é `null` ou objeto com informação acessória (contagens, retenção). Nunca carrega dado que a tela precise para funcionar.
- `error.message` é texto em português para log e para a tela; nunca inclui segredo, hash, caminho de arquivo ou stack.

### 1.2 Catálogo de erros

| HTTP | `code` | Quando |
|---|---|---|
| 400 | `bad_request` | JSON inválido, campo desconhecido, parâmetro fora de faixa |
| 401 | `unauthorized` | sem sessão ou sessão expirada |
| 401 | `invalid_credentials` | login com usuário ou senha errados (mesma mensagem para os dois casos) |
| 401 | `invalid_token` | token com assinatura errada, expirado, já usado ou malformado (mesma mensagem para todos) |
| 404 | `not_found` | rota inexistente, ou rota só localhost acessada via proxy |
| 405 | `method_not_allowed` | método errado |
| 413 | `payload_too_large` | corpo acima de 4 KiB |
| 415 | `unsupported_media_type` | POST sem `application/json` |
| 429 | `too_many_attempts` | 5 logins errados do mesmo usuário em 5 min (ADR-002), ou 4 comparações bcrypt já em andamento (a tentativa recusada por saturação não conta contra o usuário) |
| 500 | `internal` | falha inesperada (detalhe só no log) |
| 503 | `degraded` | só em `/api/health`, quando AMI está fora ou alguma dependência falhou (corpo igual ao 200) |

---

## 2. Tipos compartilhados

### 2.1 Ramal (`Extension`)

```json
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
}
```

| Campo | Tipo | Regra |
|---|---|---|
| `number` | string | chave do ramal: nome do endpoint/peer, que no Issabel é o número. Igual em SIP e PJSIP (`SIP/2001` e `PJSIP/2001` viram `"2001"`) |
| `name` | string ou null | callerid configurado no endpoint/peer |
| `technology` | `"sip"` ou `"pjsip"` | tecnologia ativa da instalação |
| `group` | string | nome do grupo; `"Sem grupo"` quando nenhuma faixa casa |
| `registration` | enum | `registered`, `unregistered`, `unreachable`, `unknown`. `unknown` só quando o AMI está fora ou ainda não terminou o snapshot. **Nunca** `unregistered` por falta de AMI |
| `ip` | string ou null | IP do contato (PJSIP: via/contact; SIP: IP do peer) |
| `user_agent` | string ou null | quando a versão do Asterisk informa |
| `latency_ms` | inteiro ou null | latência do qualify; null sem qualify |
| `device_state` | enum | `unknown`, `not_inuse`, `inuse`, `busy`, `invalid`, `unavailable`, `ringing`, `ringinuse`, `onhold` (estados do Asterisk em minúsculas) |
| `calls` | array | chamadas ativas; vazio depois do Hangup e sempre vazio quando o AMI está fora |

### 2.2 Chamada (`Call`)

| Campo | Tipo | Regra |
|---|---|---|
| `id` | string | Uniqueid do canal do próprio ramal. Ramal com chamada em espera e outra ativa tem dois itens |
| `direction` | enum | `internal`: o outro lado é ramal. `inbound`: o outro lado é tronco e o canal do tronco nasceu do lado de fora (não foi destino de Dial). `outbound`: o outro lado é tronco e o canal do tronco foi destino de Dial. A regra olha o canal do tronco, então sobrevive a transferência |
| `state` | enum | `ringing`: ainda não atendida, em qualquer ponta. `talking`: atendida (em bridge ou em aplicação, como URA ou correio de voz). `hold`: canal do ramal recebeu `Hold` sem `Unhold` |
| `peer.kind` | enum | `extension`, `external`, `unknown` (sem outro canal identificável, por exemplo URA) |
| `peer.number` | string ou null | ramal do outro lado, número externo, ou destino discado quando `unknown` |
| `peer.name` | string ou null | callerid ou connected line do outro lado, quando houver |
| `started_at` | data | criação do canal do ramal. Chamada já em curso no snapshot do AMI: `agora - Duration` do `CoreShowChannels` |
| `answered_at` | data ou null | quando o canal ficou `Up`; null enquanto `ringing` |

Correlação de "com quem fala" (implementação no `state`): Uniqueid, Linkedid, membros da bridge (`BridgeEnter`/`BridgeLeave`) e pares de Local channel casados pelo nome (`Local/x@ctx-0000001a;1` com `;2`).

---

## 3. Autenticação (ADR-002, PROPOSTO)

Os formatos abaixo valem se o ADR-002 for aceito como está. Se o usuário mudar algo, esta seção muda junto.

### 3.1 POST /api/login

Requisição:

```json
{ "username": "fabio", "password": "uma-senha-longa" }
```

Resposta 200, com `Set-Cookie: painel_session=<id>; Path=/painel-ramais/; Max-Age=604800; Secure; HttpOnly; SameSite=Strict`:

```json
{
  "success": true,
  "data": { "subject": "fabio", "kind": "password", "expires_at": "2026-10-13T22:30:00.000Z" },
  "error": null,
  "meta": null
}
```

Validade da sessão (decisão 5, ADR-002 aceito): é por **inatividade** (`auth.session_ttl`, padrão 12 h), não absoluta. Qualquer requisição autenticada passada a metade do prazo renova a sessão, e uma conexão `/ws` ativa a renova sozinha a cada `session_ttl/6`; uma tela de NOC em uso nunca expira. `expires_at` da resposta é a validade naquele instante. O cookie leva `Max-Age=604800` (7 dias) só para sobreviver à sessão: quem decide é o banco.

Validação: `username` 1 a 64 caracteres; `password` 1 a 72 bytes (limite do bcrypt). Falha: `401 invalid_credentials`; 5 falhas do mesmo usuário em 5 min: `429 too_many_attempts`. O limitador guarda no máximo 10.000 usuários; cheio, descarta as chaves vencidas e, se preciso, a de tentativa mais antiga (memória limitada sem negar usuário novo). A tentativa é contada **antes** do bcrypt (reserva atômica; o acerto zera a contagem), então tentativas paralelas não furam o limite; no máximo 4 bcrypt rodam ao mesmo tempo e o excedente recebe `429`. O log guarda do usuário digitado só o tamanho e 8 hex do SHA-256.

### 3.2 POST /api/logout

Sem corpo (ou `{}`). Apaga a sessão do banco se existir e devolve o cookie expirado (`Max-Age=0`). Sempre 200, com ou sem sessão:

```json
{ "success": true, "data": null, "error": null, "meta": null }
```

### 3.3 POST /api/token

Troca um token HMAC de uso único por uma sessão. Requisição:

```json
{ "token": "eyJzdWIiOiJpc3NhYmVsOmFkbWluIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.1FevVYbrsil2HqMU283VGL948fcwdxThKkYL6xtxxq0" }
```

Resposta 200, com o mesmo `Set-Cookie` do login:

```json
{
  "success": true,
  "data": { "subject": "issabel:admin", "kind": "token", "expires_at": "2026-10-13T22:40:30.000Z" },
  "error": null,
  "meta": null
}
```

Falha (assinatura, expirado, `jti` repetido, formato): `401 invalid_token`.

Formato do token: `base64url(payload) "." base64url(HMAC-SHA256(segredo, base64url(payload)))`, sem padding. Payload JSON com exatamente `sub` (texto livre, 1 a 128 caracteres), `exp` (unix segundos) e `jti` (16 a 64 caracteres base64url). Aceito só se `agora < exp <= agora + 65 s` (TTL 60 s mais 5 s de folga de relógio) e o `jti` não foi visto.

Vetor de teste (para o backend e para o emissor da fase 3; segredo de exemplo, nunca usar em produção):

| Item | Valor |
|---|---|
| conteúdo de `/etc/painel-ramais/secret` | `AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=` (base64 dos bytes 0x00 a 0x1f) |
| payload (bytes exatos) | `{"sub":"issabel:admin","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}` |
| `exp` | 2026-10-13T14:41:00Z |
| token | o do exemplo da requisição acima |

---

## 4. Dados (REST)

### 4.1 GET /api/snapshot

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

### 4.2 GET /api/groups

Grupos e ramais conhecidos agora, na mesma visão do snapshot (lida da projeção do estado, não do banco). `"Sem grupo"` vem por último quando tiver ramal.

```json
{
  "success": true,
  "data": [
    { "name": "Suporte", "ranges": [ { "start": 2000, "end": 2099 }, { "start": 2150, "end": 2150 } ], "extensions": ["2001", "2002", "2150"] },
    { "name": "Sem grupo", "ranges": [], "extensions": ["1500"] }
  ],
  "error": null,
  "meta": { "count": 2 }
}
```

### 4.3 GET /api/history

Parâmetros de query:

| Parâmetro | Obrigatório | Regra |
|---|---|---|
| `from` | não | RFC 3339; padrão `agora - 60 min`; truncado para o minuto |
| `to` | não | RFC 3339, exclusivo; padrão agora; `to > from`; `to - from <= 30 dias` |
| `group` | não | nome do grupo (inclui `"Sem grupo"`); ausente = total da instalação; até 64 caracteres |

Exemplo: `GET /api/history?group=Suporte&from=2026-10-13T13:00:00Z&to=2026-10-13T13:03:00Z`

```json
{
  "success": true,
  "data": {
    "group": "Suporte",
    "step_s": 60,
    "points": [
      { "t": "2026-10-13T13:00:00.000Z", "registered": 180, "unregistered": 15, "unreachable": 2, "in_call": 40, "gap": false },
      { "t": "2026-10-13T13:01:00.000Z", "registered": null, "unregistered": null, "unreachable": null, "in_call": null, "gap": true },
      { "t": "2026-10-13T13:02:00.000Z", "registered": 179, "unregistered": 16, "unreachable": 2, "in_call": 38, "gap": false }
    ]
  },
  "error": null,
  "meta": { "retention_days": 30, "count": 3 }
}
```

- Série densa: um ponto por minuto no intervalo. Minuto sem linha no banco (daemon parado) ou com `gap=1` sai como `gap: true` e contagens `null`.
- `in_call` conta ramais com pelo menos uma chamada, não chamadas.
- Grupo sem dados (inexistente ou fora da retenção) devolve 200 com todos os pontos em `gap: true`.

### 4.4 GET /api/health (só localhost)

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

### 4.5 GET /debug (só localhost, exige sessão)

HTML puro gerado no servidor, com `<meta http-equiv="refresh" content="2">`, sem JavaScript e sem envelope. Mostra: contagens por estado, chamadas ativas (ramal, direção, estado, outro lado, duração) e os últimos 20 lotes de delta do ring buffer. Não é contrato para a fase 2.

---

## 5. WebSocket `/ws` (ADR-001)

Mensagens JSON em frames de texto, uma mensagem por frame, tipadas por `type`. Fluxo:

1. Cliente faz `GET /api/snapshot` e guarda `epoch` e `version`.
2. Cliente abre `/ws` (o cookie de sessão vai no upgrade; sem sessão válida o upgrade é recusado com HTTP 401 e envelope `unauthorized`) e manda `hello` em até 5 s.
3. Servidor responde com os lotes que faltam (replay do ring buffer) e segue ao vivo, ou manda `reload` e fecha.

### 5.1 hello (cliente para servidor, primeira e única mensagem do cliente)

```json
{ "type": "hello", "epoch": "1791900000123-3", "version": 48213 }
```

### 5.2 delta (servidor para cliente)

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

### 5.3 reload (servidor para cliente)

Enviado no lugar do replay quando não dá para continuar, ou para todos os clientes quando o epoch muda. Depois dele o servidor fecha com código 4000.

```json
{ "type": "reload", "reason": "epoch_changed", "epoch": "1791900000123-4" }
```

`reason`: `epoch_changed` (epoch do hello é antigo, ou o epoch acabou de mudar), `version_gap` (o hello pede versão mais antiga que o ring buffer), `version_ahead` (versão do hello maior que a do servidor: cliente de outro processo ou bug).

### 5.5 session_expired (servidor para cliente)

Enviada imediatamente antes de fechar com 4001, para a tela mostrar o motivo em vez de congelar:

```json
{ "type": "session_expired", "reason": "expired", "message": "Sessao encerrada; faca login novamente" }
```

`reason`: `expired` (inatividade acima de `auth.session_ttl`, só acontece se a renovação falhar por muito tempo) ou `revoked` (usuário removido, senha trocada ou `session purge --all`).

### 5.4 Códigos de fechamento

| Código | Motivo | O cliente faz |
|---|---|---|
| 1000 | normal | reconecta com backoff |
| 1001 | daemon parando | reconecta com backoff |
| 4000 | depois de `reload` | busca snapshot e reconecta |
| 4001 | sessão expirou, usuário removido ou senha trocada durante a conexão. **Sempre precedido** da mensagem `session_expired` (5.5) | mostra "sessão encerrada" e vai para o login |
| 4002 | cliente lento (fila de envio cheia ou escrita acima de 10 s) | reconecta com hello; replay ou reload resolve |
| 4003 | erro de protocolo (sem hello em 5 s, hello inválido, mensagem extra do cliente) | corrige o cliente; reconecta |

Heartbeat: o servidor manda ping de controle a cada 25 s (abaixo do `Timeout` do Apache). O navegador responde sozinho.

---

## 6. Arquivo `/etc/painel-ramais/config.yaml`

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

---

## 7. AMI

### 7.1 Allowlist de actions

O cliente AMI tem uma lista fixa no código. Pedido de action fora dela é erro de programação (retorna erro e um teste unitário garante que a lista é exatamente esta). Proibidas em qualquer hipótese: `Command`, `Originate`, `Redirect`, `Hangup`, `Atxfer`, `Setvar`, `Reload`, `ModuleLoad`, `UpdateConfig`, `GetConfig`, `Filter` e qualquer outra.

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

Permissões do usuário AMI (bloco do ADR-003): `read = system,call` e `write = system,reporting`. A coluna "classe esperada" vem da documentação do Asterisk e **precisa ser confirmada** na versão real com `manager show command <Action>` (o `instalar.py verificar` coleta isso no relatório). Risco aceito e documentado: a classe `system` de escrita é exigida pelas actions PJSIP e também liberaria actions de escrita; a defesa é a allowlist no código, o segredo 0600 e o `permit` só para 127.0.0.1. **Decisão do usuário em 2026-10-13** (`docs/DECISOES-FASE-1.md`, decisões 1 e 2: manter `write = system,reporting` com controles compensatórios; porta mantida por socket activation; detalhe no ADR-003): a classe `system` de escrita pode autorizar, para quem tiver o segredo, actions que não são de consulta (a conferir: `Reload`, `ModuleLoad`, `Hangup`); e o daemon escuta em TCP loopback, não em socket Unix.

Todas marcadas "a confirmar no Asterisk real com `manager show command <Action>`" (o `instalar.py verificar` coleta). Controle compensatório da decisão 1 (mantém `write = system,reporting`): a lista acima é fixa no código (`internal/ami/actions.go`); qualquer outra action (`Command`, `Originate`, `Reload`, `ModuleLoad`, `MixMonitor`, `GetConfig`, `DBPut`, `PJSIPNotify` e as demais) é recusada **antes** de escrever no socket, com log em nível **Error** (visível com `log_level: info`; só o nome da action vai ao log, nunca os parâmetros; `ami.Config.OnRefused`), e os parâmetros `Action`/`ActionID` são reservados ao cliente; um teste cobre a recusa e prova que nada chega ao socket. Risco residual aceito (para a auditoria): se o próprio daemon for comprometido, a classe `system` dá acesso a `GetConfig` e `ModuleUnload`; as barreiras são `permit` só para 127.0.0.1, segredo 0600 do usuário `painel-ramais` e o endurecimento da unit (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ReadWritePaths=/var/lib/painel-ramais`, `CapabilityBoundingSet=` vazio; validar com `systemd-analyze security painel-ramais`).

### 7.2 Eventos (eventfilter estático, união SIP e PJSIP)

`ContactStatus`, `PeerStatus`, `DeviceStateChange`, `Newchannel`, `Newstate`, `NewCallerid`, `NewConnectedLine`, `DialBegin`, `DialEnd`, `BridgeEnter`, `BridgeLeave`, `Hangup`, `Hold`, `Unhold`, `Reload` (decisão 4: o Asterisk recarregou, os nomes dos ramais são refeitos depois de 5 s sem novo `Reload`; classe de leitura `system`). O bloco de `manager_custom.conf` do ADR-003 precisa do `eventfilter = Event: Reload`.

Os eventos da tecnologia inativa nunca chegam (o módulo não está carregado), então a união não custa nada. Motivo de não montar o filtro por tecnologia: ADR-003. As respostas de action em lista (`EndpointList`, `ContactList`, `PeerEntry`, `DeviceStateChange` de lista, `CoreShowChannel`) não passam pelo eventfilter.

### 7.3 Mapeamento de registro

| Origem | Valor | `registration` |
|---|---|---|
| PJSIP `ContactStatus` | `Created`, `Reachable`, `NonQualified` | `registered` (latência de `RoundtripUsec` quando houver) |
| PJSIP `ContactStatus` | `Unreachable` | `unreachable` |
| PJSIP `ContactStatus` | `Removed` | `unregistered` se não sobrou contato no endpoint |
| PJSIP, vários contatos | agregado | `registered` se algum contato registrado; `unreachable` se todos inalcançáveis; `unregistered` se nenhum |
| SIP `PeerStatus` | `Registered`, `Reachable`, `Lagged` | `registered` |
| SIP `PeerStatus` | `Unreachable` | `unreachable` |
| SIP `PeerStatus` | `Unregistered`, `Rejected` | `unregistered` |
| Snapshot (lista) | endpoint sem contato / peer sem IP | `unregistered` |
| AMI fora ou snapshot em curso | qualquer | `unknown` |

A agregação de contatos é do adaptador `pjsip`, que entrega ao estado um único `RegistrationChanged` por endpoint.

---

## 8. CLI

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
painel-ramais capturar --minutos N --saida <arquivo|-> [--anonimizar] [--digitos-externos 7]
painel-ramais filtrar [--anonimizar] [--digitos-externos 7] [--host NOME]...
painel-ramais actions
painel-ramais version
```

- `--config` vale para todos; a CLI usa o YAML só para achar `data_dir` e os segredos.
- Senha: lida do terminal sem eco, digitada duas vezes; `--password-stdin` lê uma linha do stdin (para script). Mínimo 10 caracteres, máximo 72 bytes. Hash bcrypt custo 12.
- `user passwd` e `user del` apagam as sessões do usuário (no `del`, via `ON DELETE CASCADE`).
- `group import ... -` lê o CSV do stdin (o `/dev/stdin` também funciona no Linux).
- `capturar` (decisão da homologação) grava frames brutos do AMI real, só com a allowlist, em arquivo novo (modo 0600; recusa sobrescrever): conecta, faz o snapshot e uma amostra de consultas de nome, grava o tráfego ao vivo por `--minutos` (fracionário; máx. 720; Ctrl-C encerra antes) e termina com um snapshot final. Segredos (`Secret`, `Password`, `Md5Cred`, `Challenge`…) saem sempre mascarados (`***`). `--anonimizar` troca números externos (sequências de pelo menos `--digitos-externos` dígitos), nomes, usuários/contas, hosts, IPv4 e IPv6 por ficcionais, sempre o mesmo ficcional para o mesmo original; é melhor esforço (limites na seção 18): revise o arquivo antes de compartilhar. Formato do arquivo: seção 18.
- `capturar --saida -` escreve a captura no stdout (e o resumo no stderr): o `instalar.py homologar`, como root, lê pelo pipe e nunca abre arquivo da área do usuário do serviço.
- `filtrar` lê texto do stdin e escreve no stdout com as **mesmas regras** do `capturar` (`internal/capture`, `Sanitizer.Text`): segredos viram `***` sempre (formatos `Chave: valor`, `chave=valor` e `"chave":"valor"`; chaves `secret`, `passw*`, `pass`, `pwd`, `senha`, `token`, `*key`, `auth*`, `cookie`, `session`, `hmac`, `signature`, `credential`, `private`, `md5`, `challenge`; o valor inteiro: com aspas (duplas ou simples) até a aspa que fecha, sem aspas até o **fim da linha** (escolha de segurança; inclui valor dentro de aspas, URL com `?session=X` e repr de Python); cabeçalhos `Cookie`/`Set-Cookie`/`Authorization` na linha toda; senha de `usuario:senha@host`; `response`/`nonce` de Digest); com `--anonimizar`, números externos, nomes, usuários, hosts, IPv4 e IPv6 viram ficcionais, o mesmo ficcional para o mesmo original durante a execução; `--host NOME` (repetível) troca o hostname/FQDN em qualquer ponto do texto. Não cobre segredo de várias linhas (PEM), segredo em argv/`ps` fora de par chave=valor nem texto livre sem chave (`filtrar --help`). É o único filtro de máscara do pacote de homologação (não há segunda rotina em Python).
- `actions` imprime a lista fixa de actions AMI do daemon (seção 7.1), uma por linha, ordenada. O `instalar.py homologar` registra `manager show command <Action>` para cada uma.
- `session purge` apaga as sessões vencidas; `--all` apaga todas (todo mundo faz login de novo) e manda o SIGHUP para o daemon fechar os WebSockets. Imprime a quantidade removida.
- `token` imprime o token no stdout e nada mais. `--ttl` máximo 60 s. Uso: testes e diagnóstico.
- Depois de `user ...` ou `group ...` com sucesso, a CLI pega o PID com `systemctl show -p MainPID --value painel-ramais` e manda SIGHUP (mesmo usuário, não precisa de root). Daemon parado: avisa e sai 0. Falha no sinal: avisa `rode: systemctl reload painel-ramais` e sai 0 (o banco já foi gravado).
- Códigos de saída: 0 sucesso; 1 erro de uso ou validação (mensagem cita a linha do CSV); 2 erro de ambiente (banco, arquivo, permissão); 3 `user add` de usuário que já existe (nada alterado). O 3 existe para o `instalar.py` ser idempotente sem ler o banco. Só no `serve`: 4 = Asterisk abaixo da versão mínima (13); a unit deve ter `RestartPreventExitStatus=4` para não reiniciar em laço.
- `version` imprime uma linha `painel-ramais <versao> schema <user_version que o binário conhece>` (exemplo: `painel-ramais 0.1.1 schema 2`). O `instalar.py` usa o `schema` para decidir o rollback (seção 15.5).
- Root (o instalador) chama a CLI com `runuser -u painel-ramais -- painel-ramais ...`; pessoa usa `sudo -u painel-ramais painel-ramais ...`.
- `group list` imprime `grupo<TAB>faixas` (faixas separadas por vírgula), uma linha por grupo, ordenado por nome.

---

## 9. CSV de grupos

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

---

## 10. SQLite (`<data_dir>/painel.db`)

Driver `modernc.org/sqlite` (nome `sqlite`). Pragmas **no DSN**, para valer em toda conexão do pool (`foreign_keys` é por conexão): `?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`. Daemon e CLI escrevem no mesmo arquivo; WAL e `busy_timeout` resolvem a concorrência.

Migração: `PRAGMA user_version`. Só para frente. Antes de aplicar migração o daemon faz `VACUUM INTO '<data_dir>/painel.db.bak-v<versão antiga>'` (modo 0600; só quando já havia banco com `user_version` maior que 0). O backup vem antes de qualquer alteração de esquema, e o rollback do instalador (15.7) depende dele. Banco com `user_version` maior que o binário conhece: recusa iniciar (downgrade não suportado). Tempos em unix segundos (`INTEGER`).

```sql
PRAGMA user_version = 1;

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL COLLATE NOCASE UNIQUE
                CHECK (length(username) BETWEEN 1 AND 64),
  password_hash TEXT    NOT NULL CHECK (password_hash LIKE '$2%'),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;

-- id_hash = SHA-256 do valor bruto do cookie; o valor do cookie nunca é gravado
CREATE TABLE sessions (
  id_hash    BLOB    PRIMARY KEY CHECK (length(id_hash) = 32),
  kind       TEXT    NOT NULL CHECK (kind IN ('password', 'token')),
  user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE,
  subject    TEXT    NOT NULL CHECK (length(subject) BETWEEN 1 AND 128),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  CHECK ((kind = 'password') = (user_id IS NOT NULL)),
  CHECK (expires_at > created_at)
) STRICT, WITHOUT ROWID;
CREATE INDEX sessions_user_id    ON sessions(user_id);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

CREATE TABLE groups (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL COLLATE NOCASE UNIQUE
             CHECK (length(name) BETWEEN 1 AND 64 AND name = trim(name) AND name <> 'Sem grupo'),
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE group_ranges (
  id          INTEGER PRIMARY KEY,
  group_id    INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  range_start INTEGER NOT NULL CHECK (range_start >= 0),
  range_end   INTEGER NOT NULL,
  CHECK (range_end >= range_start)
) STRICT;
CREATE INDEX group_ranges_group_id ON group_ranges(group_id);
CREATE INDEX group_ranges_start    ON group_ranges(range_start);

-- Um grupo por ramal garantido pelo banco, não só pela CLI
CREATE TRIGGER group_ranges_no_overlap BEFORE INSERT ON group_ranges
WHEN EXISTS (SELECT 1 FROM group_ranges r
             WHERE NEW.range_start <= r.range_end AND r.range_start <= NEW.range_end)
BEGIN SELECT RAISE(ABORT, 'faixa sobreposta'); END;

-- Faixa é imutável: troca = apagar e inserir (o trigger acima cobre o insert)
CREATE TRIGGER group_ranges_immutable BEFORE UPDATE ON group_ranges
BEGIN SELECT RAISE(ABORT, 'faixa imutável'); END;

-- group_name '' = total da instalação. Nome do grupo gravado como texto:
-- o histórico sobrevive a --replace e a remoção de grupo.
CREATE TABLE history (
  minute       INTEGER NOT NULL CHECK (minute % 60 = 0),
  group_name   TEXT    NOT NULL,
  registered   INTEGER,
  unregistered INTEGER,
  unreachable  INTEGER,
  in_call      INTEGER,
  gap          INTEGER NOT NULL CHECK (gap IN (0, 1)),
  CHECK (
    (gap = 1 AND registered IS NULL AND unregistered IS NULL AND unreachable IS NULL AND in_call IS NULL)
    OR
    (gap = 0 AND registered >= 0 AND unregistered >= 0 AND unreachable >= 0 AND in_call >= 0)
  ),
  PRIMARY KEY (group_name, minute)
) STRICT, WITHOUT ROWID;
CREATE INDEX history_minute ON history(minute);
```

Versão 2 (cache dos nomes dos ramais, decisão 4; `PRAGMA user_version = 2`, aplicada por migração sobre a v1, com o backup `painel.db.bak-v1` antes):

```sql
CREATE TABLE extension_names (
  number     TEXT    PRIMARY KEY CHECK (length(number) BETWEEN 1 AND 64),
  name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
  updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
```

Todo nome que entra (callerid do Asterisk, display name do telefone, cache antigo, nome do outro lado da chamada) passa por `model.CleanName`: sem caracteres de controle ou de formatação (categorias Unicode `C*`: C0/C1, bidi U+202A..202E e U+2066..2069, largura zero, BOM), espaços colapsados e no máximo 64 caracteres. A tela da fase 2 deve mostrar nomes só como texto (nunca `innerHTML`) e com CSP estrita. Ao fim de cada sincronização completa o daemon expurga do cache os ramais que não existem mais no PBX (lista vazia não expurga).

O daemon grava em lote (a cada 5 s e no desligamento) todo nome aprendido ao vivo (consulta de nome ou `CallerIDName` de quem origina uma chamada) e o lê na subida: o nome aparece já no primeiro snapshot, e o nome real do Asterisk sempre vence o do cache. Preenchimento em segundo plano: depois de cada sincronização, a 20 consultas/s no máximo (`ami.name_fill_per_second`), de novo a cada 6 h e depois de um evento `Reload`; o painel funciona com o nome vazio enquanto isso. Limite: nome apagado no Asterisk (callerid vazio) não limpa o nome já conhecido do ramal.

Regras de uso:
- **Amostra de histórico**: na virada de cada minuto, o daemon grava uma linha para o total (`''`) e uma por grupo (inclui `Sem grupo`), com as contagens daquele instante. Se o AMI estava fora em qualquer momento do minuto, grava `gap=1`. Tamanho esperado com 100 grupos: cerca de 4,4 milhões de linhas em 30 dias.
- **Retenção**: a cada hora, `DELETE FROM history WHERE minute < agora - retention_days`. Usa `history_minute`.
- **Sessões vencidas**: a cada hora, `DELETE FROM sessions WHERE expires_at <= agora`.
- **Grupos no daemon**: carregados na subida e a cada SIGHUP, ordenados por `range_start`, busca binária por ramal.

---

## 11. Eventos normalizados (pacote `model`)

Os adaptadores `sip` e `pjsip` traduzem mensagens AMI nestes tipos. O supervisor da conexão (em `cmd/painel-ramais`) emite os de controle. O `state` consome só estes tipos, de um canal, numa goroutine única, e nunca vê texto AMI. Todo evento carrega `At time.Time` (hora em que o leitor tirou a mensagem do socket), usado para `lag_ms` e para `started_at`.

### 11.1 Controle

| Tipo | Campos | Efeito no estado |
|---|---|---|
| `AMIDisconnected` | `Reason` | todos os ramais para `unknown`, `calls` vazias, `ami_connected=false`, epoch novo |
| `SyncStarted` | `Technology`, `AsteriskVersion`, `Reason` (`connect` ou `overflow`) | começa estado de montagem separado; a visão publicada continua a anterior |
| `SyncFinished` | (nenhum) | troca a visão pela montada, `ami_connected=true`, epoch novo |
| `NamesLoaded` | `Names map[string]string` | cache de nomes (número para callerid); só preenche ramal sem nome; sem epoch novo (emitido pelo `cmd` na subida) |
| `GroupsLoaded` | `Ranges []GroupRange{Start, End, Name}` | reatribui grupos, epoch novo (emitido pelo `cmd` na subida e no SIGHUP) |

Regra do epoch: mudança em massa (queda do AMI, fim de sincronização, recarga de grupos) gera epoch novo e `reload`; mudança de um ramal gera delta.

### 11.2 Registro e dispositivo

| Tipo | Campos | Origem |
|---|---|---|
| `EndpointSeen` | `Name`, `CallerIDName`, `Technology` | `PJSIPShowEndpoints` / `SIPpeers` durante a sincronização |
| `RegistrationChanged` | `Name`, `State` (enum de `registration`), `IP`, `UserAgent`, `LatencyMs *int` | `ContactStatus` agregado / `PeerStatus` / listas de contato |
| `DeviceStateChanged` | `Name`, `State` (enum de `device_state`) | `DeviceStateChange`, `DeviceStateList` |

`Name` é o nome do endpoint/peer, sem prefixo de tecnologia. O `state` decide ramal ou tronco com `extension_pattern` (D1). Evento de endpoint desconhecido cria o registro dentro do epoch atual.

### 11.3 Chamadas (nível de canal; a correlação é do `state`)

| Tipo | Campos | Evento AMI |
|---|---|---|
| `ChannelCreated` | `Uniqueid`, `Linkedid`, `Channel`, `Endpoint` (vazio se Local ou outro), `IsLocal`, `CallerNum`, `CallerName`, `Exten`, `Up bool`, `BridgeID` (só no snapshot), `Duration` (só no snapshot), `Dialed` (só no snapshot: `Application=AppDial`/`(Outgoing Line)`, canal destino de um Dial), `DialData` (só no snapshot: argumentos do Dial, quando `Application=Dial`) | `Newchannel`, `CoreShowChannel` |
| `ChannelStateChanged` | `Uniqueid`, `State` (`down`, `ring`, `ringing`, `up`, `busy`, `other`) | `Newstate` |
| `CallerIDChanged` | `Uniqueid`, `Num`, `Name` | `NewCallerid` |
| `ConnectedLineChanged` | `Uniqueid`, `Num`, `Name` | `NewConnectedLine` |
| `DialStarted` | `SrcUniqueid`, `DstUniqueid` | `DialBegin` |
| `DialEnded` | `SrcUniqueid`, `DstUniqueid`, `Status` | `DialEnd` |
| `BridgeEntered` | `BridgeID`, `Uniqueid` | `BridgeEnter` |
| `BridgeLeft` | `BridgeID`, `Uniqueid` | `BridgeLeave` |
| `HoldChanged` | `Uniqueid`, `OnHold bool` | `Hold`, `Unhold` |
| `ChannelHungup` | `Uniqueid`, `Cause` | `Hangup` |

`Endpoint` sai do nome do canal: `PJSIP/2001-0000002a` e `SIP/2001-0000002a` viram `2001`. Essa extração é a única parte de chamada que muda entre as tecnologias; a tradução dos eventos de chamada é compartilhada em `ami/calls`.

Restauração por snapshot: o `CoreShowChannels` não traz os `DialBegin`. No fim da sincronização o `state` refaz as relações de Dial por Linkedid (quem executa `Dial` casado com o canal `AppDial` pelo argumento do Dial; o que não casa por nome liga quando só resta um par). `Dialed` mantém a direção `outbound` de chamada de saída restaurada. `hold` não vem do `CoreShowChannels` (limite do protocolo): chamada em espera volta como `talking`. Com vários Dial sem casar por nome e vários destinos soltos no mesmo Linkedid, o peer fica `unknown` até atender.

Fila entre leitor AMI e estado: canal com capacidade 65536. A goroutine leitora só faz parse e envio sem bloqueio (`select` com `default`). Fila cheia: o leitor descarta, conta em `queue.overflows`, registra log e, quando a fila esvazia, o supervisor dispara uma ressincronização (`SyncStarted{Reason: overflow}`) sem derrubar a conexão. Nada é perdido em silêncio: o estado é refeito inteiro.

### 11.4 Saída do estado para o hub (`api`)

| Tipo | Campos | Quando |
|---|---|---|
| `Batch` | `Epoch`, `FromVersion`, `ToVersion`, `AMIConnected`, `Extensions []Extension` | a cada 250 ms com mudança |
| `Reset` | `Epoch`, `Version`, `AMIConnected`, `Technology`, `Extensions []Extension` (todos), `Groups []GroupRange` (para o hub servir `/api/groups`) | a cada epoch novo |

`Extension` e `Call` do `model` têm as tags JSON da seção 2: o formato da seção 2 é o próprio `json.Marshal` desses tipos.

---

## 12. Pacotes Go

Módulo único. Dependências externas: `modernc.org/sqlite`, `gopkg.in/yaml.v3`, `golang.org/x/crypto/bcrypt`, `github.com/coder/websocket`. Cliente AMI próprio (protocolo de texto simples; nenhuma lib AMI). Fora isso, só stdlib.

```
cmd/painel-ramais/     main: serve e CLI; supervisor da conexão AMI (Adapter); amostrador de histórico; SIGHUP
cmd/amisim/            simulador de AMI (modos sip e pjsip)
cmd/loadtest/          teste de carga contra o simulador
internal/api/          HTTP, sessão, hub WebSocket (projeção, ring buffer, fan-out), /debug
internal/state/        estado em memória, correlação de canais, versão, epoch, lotes de 250 ms
internal/model/        tipos: Extension, Call, enums, eventos normalizados, Batch, Reset
internal/ami/          conexão, framing, login, allowlist de actions, leitura sem bloqueio
internal/ami/calls/    tradução dos eventos de chamada (comum a sip e pjsip)
internal/ami/sip/      adaptador chan_sip
internal/ami/pjsip/    adaptador chan_pjsip
internal/history/      amostrador por minuto (gap, retenção); importa model e store
internal/store/        SQLite: usuários, sessões, grupos, histórico, migrações
internal/auth/         bcrypt, token HMAC, cache de jti, geração de id de sessão
internal/config/       leitura e validação do config.yaml
internal/depcheck/     verificação de dependências (arquivos e resultado de login AMI)
instalar.py            instalador (raiz do repositório; Python 3.6, stdlib)
tools/bundle.py        monta dist/painel-ramais-<versao>.tar.gz (modo 0755 e LF garantidos; seção 15.1); tools/check-lf.ps1
tests/                 testes unitários do instalar.py (unittest)
packaging/             nfpm.yaml, unit, scripts do RPM, httpd.conf.tmpl, config.yaml.example, LEIAME-INSTALACAO.md, grupos-exemplo.csv
```

Grafo de importação interna (uma linha por pacote; `->` lista o que ele importa). Verificado sem ciclo por script (seção 14).

```deps
cmd/painel-ramais -> internal/api, internal/state, internal/model, internal/ami, internal/ami/sip, internal/ami/pjsip, internal/store, internal/auth, internal/config, internal/depcheck, internal/history
internal/api -> internal/state, internal/model, internal/store, internal/auth, internal/depcheck
internal/state -> internal/model
internal/ami/sip -> internal/ami, internal/ami/calls, internal/model
internal/ami/pjsip -> internal/ami, internal/ami/calls, internal/model
internal/ami/calls -> internal/ami, internal/model
internal/ami ->
internal/model ->
internal/history -> internal/model, internal/store
internal/store ->
internal/auth ->
internal/config ->
internal/depcheck ->
cmd/amisim ->
cmd/loadtest ->
```

Regras:
- Direção única: `cmd -> api -> state -> model <- ami/{sip,pjsip} -> ami`. `state` não conhece AMI nem HTTP; `ami` não conhece `model`.
- Folhas (`model`, `store`, `auth`, `config`, `depcheck`, `ami`) não importam nenhum pacote interno. `store` devolve os próprios tipos de linha; o `cmd` converte para `model` (por exemplo `store.Range` para `model.GroupRange`).
- A interface `Adapter` (duas implementações: `sip` e `pjsip`) é declarada em `cmd/painel-ramais`, onde é consumida: `Snapshot(ctx, *ami.Client) error` emite os eventos da sincronização; `Translate(ami.Message) []model.Event` traduz um evento ao vivo; `EndpointFromChannel(string) (string, bool)`.
- Detecção de tecnologia (`CoreSettings` e `ModuleCheck`) fica em `ami` e devolve texto (`"sip"`, `"pjsip"`); o `cmd` escolhe o adaptador.
- `cmd/amisim` e `cmd/loadtest` não importam nada de `internal/`. O simulador tem framing AMI próprio de propósito: se dividisse o código do cliente, um bug de framing seria espelhado e o teste não pegaria. O teste de carga decodifica o JSON pelos tipos dele, como cliente de caixa preta deste contrato.
- `ami/calls` é uma adição ao desenho pedido. O bundle é `tools/bundle.py` (Python, `tarfile`, executado por `build.ps1 bundle`), e não um `cmd/bundle` em Go: o build roda no Windows, sem bit de execução, e o `tar` do Windows não garante o modo 0755 no cabeçalho, então o script grava `TarInfo.mode` explícito. `ami/calls` evita duplicar a tradução de onze eventos de chamada nos dois adaptadores, e não cria ciclo.
- Se o `cmd/painel-ramais` passar de 800 linhas, o supervisor sai para `internal/collector` (entre `cmd` e `ami/*`), sem mudar a direção.

---

## 13. Suposições e itens a validar

| # | Suposição ou item | Como validar | Quem |
|---|---|---|---|
| S1 | No Issabel 5 o nome do endpoint/peer de ramal é o número | `instalar.py verificar`: amostra de `pjsip show endpoints` / `sip show peers` no relatório | backend |
| S2 | Ramal com zero à esquerda não existe ou não precisa de faixa | perguntar ao usuário se aparecer no relatório do `verificar` | gerente |
| S3 | Classes de privilégio da tabela 7.1 | `manager show command <Action>` para cada action, no relatório do `verificar` | backend |
| S4 | `ContactStatus` traz `EndpointName`, `UserAgent` e `RoundtripUsec` na versão do Issabel 5 | `manager show event ContactStatus` no relatório do `verificar` + simulador fiel à versão | backend |
| S5 | `CoreShowChannel` traz `BridgeId` e `Duration` (Asterisk 13 em diante) | `manager show event CoreShowChannel` no relatório do `verificar` | backend |
| S6 | eventfilter no formato de regex (`Event: X`) é aceito pela versão do Asterisk do Issabel 5 | spike do ADR-003 | backend |
| S7 | O daemon consegue ler `/etc/asterisk/manager.conf` para a checagem do include; se não conseguir, o item fica `unknown` e o README explica | `verificar` registra `ls -l /etc/asterisk/manager*.conf` no relatório | backend |
| S8 | Hora dos eventos ao vivo = hora de leitura no daemon (o Asterisk não manda timestamp sem `timestampevents`, que fica no `[general]` do manager.conf, que não editamos) | aceito; erro esperado abaixo de 1 s | nenhum |

---

## 14. Verificação deste documento

Script de conferência (rodado no scratchpad, fora do repositório): extrai os blocos `json`, `sql` e `deps` deste arquivo e confere que (a) todo bloco JSON faz parse, (b) cada endpoint e mensagem (snapshot, delta, hello, reload, health, history, groups, login, logout, token) tem exemplo, (c) a DDL roda num SQLite em memória e o trigger rejeita faixa sobreposta, (d) o grafo não tem ciclo e as folhas não importam pacote interno (o bundle agora é `tools/bundle.py`, fora do grafo Go). As seções 15 a 17 (instalador, critérios, documentação) são texto e tabela; a conferência delas é a leitura contra o adendo. Resultado registrado no retorno do tech-lead de 2026-10-13.

---

## 15. Instalador `instalar.py` e bundle (adendo)

Origem: `docs/ADENDO-FASE-1-INSTALADOR.md`. Substitui o `discovery.sh`, que não existe mais no plano: a coleta que ele faria virou o subcomando `verificar`. Fronteira (D9): o instalador orquestra e valida; integração com Asterisk, Apache e SELinux é do `%post` (ADR-003); usuário e grupos são da CLI do daemon (seção 8).

### 15.1 Bundle

- Alvo de build `bundle` (no Makefile e no `build.ps1`), executado por `tools/bundle.py` (Python, stdlib). Gera `dist/painel-ramais-<versao>.tar.gz` com uma pasta `painel-ramais-<versao>/` contendo: `instalar.py` (modo 0755), o RPM `painel-ramais-<versao>-1.x86_64.rpm`, o `SHA256SUMS` do RPM, `grupos-exemplo.csv` e `LEIAME-INSTALACAO.md` (0644). O `instalar.py` fica na raiz do repositório (não em `instalador/`).
- `tools/bundle.py` grava o modo no cabeçalho do tar (o Windows não tem bit de execução) e recusa empacotar arquivo de texto que tenha CR. O `.gitattributes` inclui `*.py` e `*.ini` na lista de LF, além do que a especificação já pede.
- Offline: o instalador não baixa nada. Usa só o que está no tar e no sistema base do Rocky (`rpm`, `systemctl`, `runuser`, `ss`, `httpd`, `asterisk`).
- Uso no servidor: `tar xzf painel-ramais-<versao>.tar.gz && cd painel-ramais-<versao> && ./instalar.py verificar`.

### 15.2 Compatibilidade

- Python 3.6 ou superior, só biblioteca padrão, sem pip.
- Proibido (não existe no 3.6): `dataclasses`, `subprocess.run(capture_output=...)` e `text=` (usar `stdout=PIPE, stderr=PIPE, universal_newlines=True`), `from __future__ import annotations`, operador `:=`, `datetime.fromisoformat`, `asyncio.run`.
- Conferência no desenvolvimento: `vermin --target=3.6- instalar.py tools/bundle.py` sem violação (resultado em 2026-10-13: mínimo 3.5, venv em `.tools/venv`). **Exceção de tamanho:** o `instalar.py` passa de 800 linhas (cerca de 980). Não foi dividido porque o instalador precisa funcionar a partir de um único arquivo executável copiado do bundle, com a primeira linha poliglota (`sh`/`python`) e testes que fazem `mock.patch` das funções do módulo; dividir em módulo auxiliar mudaria o `sys.path` do `platform-python` e a forma de patch. Aceito como exceção.
- Primeiras linhas poliglotas: o `sh` executa o próprio arquivo com `python3` se existir, senão com `/usr/libexec/platform-python` (presente no Rocky 8 mínimo e no Rocky 9); para o Python as mesmas linhas são uma string sem efeito. Forma de referência: em 2026-10-13 o `sh` do Git Bash escolheu o `python3` falso quando existia e o `platform-python` falso quando não, repassando os argumentos, e o Python 3.12 executou o arquivo; falta confirmar no Rocky 8 mínimo (A7):

```
#!/bin/sh
"true" '''\'
exec "$(command -v python3 || echo /usr/libexec/platform-python)" "$0" "$@"
'''
```

- O teste HTTPS local (15.5, passo 9) usa contexto SSL sem verificação de certificado só para `https://127.0.0.1` (o certificado do Issabel costuma ser autoassinado).

### 15.3 Subcomandos, opções e códigos de saída

```
./instalar.py verificar
./instalar.py instalar [--respostas ARQUIVO.ini] [--sim]
./instalar.py status
./instalar.py rollback [--sim]
./instalar.py remover [--purge] [--sim]
./instalar.py homologar [--duracao MIN] [--saida DIR]
```

| Código | Significado |
|---|---|
| 0 | sucesso (no `verificar`: nenhum ERRO; AVISO não muda o código) |
| 1 | `verificar` com ERRO (no `instalar`, aborta antes de tocar no sistema) |
| 2 | falha durante a operação (rpm, CLI, health, Apache) ou, no `status`, serviço degradado ou fora |
| 3 | uso inválido: argumento, arquivo de respostas com chave desconhecida, arquivo de senha com permissão errada, pergunta sem resposta sem terminal |
| 4 | cancelado pelo operador (confirmação negada) |

- `homologar` (root; padrão 45 min, máx. 720): registra o estado antes do roteiro (`manager show command` de cada action, versão do Asterisk, `getenforce`, `systemctl status` do socket e do serviço, `systemd-analyze security`, resultado do `verificar`), roda `painel-ramais capturar --anonimizar` em paralelo, tira um snapshot a cada 2 s (painel + `core show channels concise` + `pjsip show contacts` ou `sip show peers`) mostrando relógio e o passo atual do roteiro (Enter = próximo passo, texto = anotação), e ao fim gera `homologacao-<data-hora>.tar.gz` (0600) com tudo, o journal do serviço e o log do instalador, passando todo o texto pelo `painel-ramais filtrar --anonimizar --host <hostname> --host <fqdn>`, executado como `nobody`; `capturar`, `token` e todo `runuser` rodam em sessão nova (`setsid`) e sem stdin; o `capturar` recebe SIGINT explícito no fim antecipado e a sessão do painel termina com `POST /api/logout`. Se o filtro falhar ou estourar o tempo, nenhum pacote é gerado; SIGTERM/SIGHUP apagam os dados brutos e não geram pacote. Ctrl-C encerra antes e ainda gera o pacote. Análise do pacote: `tools/analisar_homologacao.py`.
- Todos exigem root (no `verificar`, falta de root é um item ERRO; nos outros, aborta com 3).
- Sem terminal (stdin não é TTY, caso do laço ssh) e sem a resposta no `--respostas`: sai com 3 e diz qual resposta faltou. Nunca fica esperando entrada.
- `--sim` responde "sim" a toda confirmação, inclusive a do `--purge` (no modo não interativo, `remover --purge --sim` são dois sinais explícitos).

### 15.4 `verificar` (somente leitura)

Não altera nada no sistema. A única escrita é o relatório em `/var/log/painel-ramais/verificacao-<AAAAmmdd-HHMMSS>.txt` (diretório root 0700, arquivo 0600). Cada item sai como `OK`, `AVISO` ou `ERRO`, com uma linha "Como corrigir" quando não é `OK`.

| Item | ERRO quando | AVISO quando | Como lê |
|---|---|---|---|
| root | euid diferente de 0 | | `os.geteuid()` |
| sistema | não é família EL (`ID_LIKE` sem `rhel`) ou `VERSION_ID` menor que 8 | `ID` diferente de `rocky` | `/etc/os-release` |
| Issabel 5 | pacote base do Issabel 5 ausente | | `rpm -q` (nome do pacote a confirmar, S9) |
| Asterisk | parado ou versão menor que 13 | | `asterisk -rx 'core show version'` |
| tecnologia | nenhum de `chan_pjsip.so` e `chan_sip.so` carregado | os dois carregados | `asterisk -rx 'module show like chan_'`; contagem de ramais e troncos com `pjsip show endpoints` ou `sip show peers` e o `extension_pattern` |
| actions AMI | falta action obrigatória da tecnologia ativa (seção 7.1) | falta `PJSIPShowContacts` (há fallback) ou `SIPshowpeer` | `asterisk -rx 'manager show command <Action>'`; a classe de privilégio vai para o relatório (S3) |
| eventos AMI | | campo esperado ausente em `ContactStatus` ou `CoreShowChannel` | `asterisk -rx 'manager show event <Evento>'` (S4, S5) |
| include AMI | `manager.conf` sem include de `manager_custom.conf` | | leitura do arquivo |
| conflito AMI | seção `[painel-ramais]` em `manager_custom.conf` fora dos marcadores | | leitura do arquivo |
| httpd | inativo | | `systemctl is-active httpd` |
| mod_proxy_wstunnel | ausente | | `httpd -M` |
| SELinux | | `Enforcing` com `httpd_can_network_connect` desligado (o RPM liga) | `getenforce`, `getsebool` |
| porta interna | porta de `listen` (do `config.yaml` instalado, ou 8790) ocupada por processo que não é o `painel-ramais` | | `ss -ltnp` |
| disco em `/var/lib` | menos de 512 MiB livres | menos de 2 GiB | `os.statvfs` |
| versão instalada | versão instalada maior que a do bundle (use `rollback` ou um bundle mais novo) | | `rpm -q painel-ramais` |
| informativo | | | versões de Rocky, Issabel, Asterisk e httpd; `Timeout` do Apache (ADR-001); regras de firewall com 443 (`iptables -S`); `ls -l /etc/asterisk/manager*.conf` (S7); amostra de nomes de endpoint (S1) |

### 15.5 `instalar`

1. Roda o `verificar`. Qualquer ERRO: sai com 1 sem tocar no sistema.
2. Coleta **todas** as respostas antes de mudar qualquer coisa: tecnologia (`auto` por padrão), usuário admin, senha (`getpass`, digitada duas vezes, ou arquivo do `--respostas`), CSV de grupos (opcional; o arquivo precisa existir).
3. Pacote:
   - não instalado: `rpm -i`;
   - mesma versão: `rpm -U --replacepkgs` (reaplica o `%post`, que é idempotente);
   - versão mais antiga instalada: move `/var/lib/painel-ramais-instalador/rpm-instalado/` (root 0700, fora do diretório do serviço) para `rpm-anterior/` (substitui o que havia) e roda `rpm -U`. Sem RPM guardado (pacote instalado à mão): AVISO "rollback indisponível para esta versão" e segue;
   - depois de instalar, copia o RPM do bundle e a saída de `painel-ramais version` (`versao.txt`) para `rpm-instalado/` (root 0700), junto com o `SHA256` do RPM. Antes de instalar, o instalador confere o RPM do bundle contra o `SHA256SUMS` do bundle e `rpm -qp --qf %{NAME}` = `painel-ramais`.
4. `technology:` no `config.yaml`: reescreve a linha só se o valor mudou, com cópia `config.yaml.bak-<data>` antes.
5. Usuário admin: `runuser -u painel-ramais -- painel-ramais user add <usuario> --password-stdin`. Código 3 (já existe): segue sem trocar a senha e avisa.
6. Grupos, se houver CSV: `runuser -u painel-ramais -- painel-ramais group import --replace -` (o root lê o CSV e o entrega pelo stdin; nenhum arquivo é criado em `/var/lib/painel-ramais`). O CSV do instalador é a fonte da verdade: rodar de novo com o mesmo arquivo dá o mesmo resultado.
7. `systemctl enable --now painel-ramais`; `systemctl restart painel-ramais` se a tecnologia mudou.
8. Espera até 60 s por `GET http://127.0.0.1:<porta>/api/health` com `ami.connected = true` e `extensions.total > 0`, consultando a cada 2 s.
9. Testa o Apache: `GET https://127.0.0.1/painel-ramais/` precisa responder com o envelope JSON do daemon (na fase 1, `404 not_found`; prova que o proxy chega ao daemon); `GET https://127.0.0.1/painel-ramais/api/health` precisa responder 404 do próprio Apache, sem JSON (prova a exclusão do D5).
10. Resumo: versão, tecnologia, ramais por estado, quantidade de grupos, URL `https://<endereço do servidor>/painel-ramais/` e caminho do log.

Falha do passo 3 em diante: não desfaz nada, mostra o passo, o erro e o comando para diagnosticar (`journalctl -u painel-ramais -n 50`, `./instalar.py status`, `httpd -t`), sai com 2.

Idempotência:

| Rodar de novo | Resultado |
|---|---|
| mesmo bundle | `rpm -U --replacepkgs`; usuário existente mantido; grupos iguais (replace com o mesmo CSV); `config.yaml` intocado se a tecnologia é a mesma |
| bundle mais novo | upgrade com o RPM anterior guardado para rollback |
| segredos, bloco AMI, conf do Apache | regenerados pelo `%post` sem duplicar (ADR-003, itens 4, 5 e 7) |

### 15.6 `status`

Lê `GET http://127.0.0.1:<porta>/api/health` (porta do `config.yaml`) e `systemctl is-active painel-ramais`. Mostra versão, serviço, AMI (conectado, tecnologia, versão do Asterisk), ramais por estado, clientes WebSocket e cada item de `deps`. Sai com 0 se `status = ok`, 2 se `degraded` ou se o health não responde.

### 15.7 `rollback`

1. Exige `rpm-anterior/` com um RPM e o `versao.txt` dele; senão sai com 2 ("não há versão anterior guardada").
2. Compara o `schema` do `versao.txt` anterior com o `PRAGMA user_version` do banco (leitura com `sqlite3` da stdlib em modo `mode=ro`; o instalador nunca grava no SQLite).
3. Mostra o plano e pede confirmação (ou `--sim`). Se o banco está num esquema mais novo, o plano diz com todas as letras que o histórico, as sessões e as mudanças de usuário e grupo feitas depois do upgrade serão perdidas; sem o backup `painel.db.bak-v<schema anterior>`, sai com 2 sem mexer em nada.
4. Confere `rpm -qp --qf %{NAME}` = `painel-ramais` e o SHA-256 registrado na instalação; `systemctl stop painel-ramais`; `rpm -U --oldpackage <rpm-anterior>`.
5. Se precisou: guarda `painel.db` (e `-wal`, `-shm`) como `painel.db.rollback-<data>` e copia o backup para `painel.db`, dono painel-ramais.
6. `systemctl start painel-ramais`, espera o health como no passo 8 do `instalar`, troca `rpm-instalado/` e `rpm-anterior/`.

Um nível só de rollback.

### 15.8 `remover`

- Confirmação (ou `--sim`), depois `rpm -e painel-ramais` (o `%preun`/`%postun` tiram bloco AMI e conf do Apache; ADR-003).
- `--purge`: depois do `rpm -e`, pede que o operador digite o nome do servidor (`hostname`), o que evita apagar o servidor errado num laço ssh; com `--sim`, dispensa. Apaga `/var/lib/painel-ramais` (inclui RPMs guardados e backups do banco) e `/etc/painel-ramais`. Mantém `/var/log/painel-ramais` (registro da própria remoção) e o usuário de sistema (`userdel painel-ramais` documentado no LEIAME).

### 15.9 Arquivo de respostas (`--respostas`)

```ini
[painel]
tecnologia = auto
admin_usuario = admin
admin_senha_arquivo = /root/painel-admin.senha
grupos_csv = /root/grupos.csv
```

- `configparser` da stdlib. Seção e chaves exatamente estas; chave desconhecida sai com 3.
- `tecnologia`: `auto`, `sip` ou `pjsip`. `grupos_csv` vazio ou ausente: não importa grupos.
- `admin_senha_arquivo`: arquivo comum, dono root, modo 0600 ou 0400; senão sai com 3. Usa a primeira linha, sem a quebra de linha. A senha nunca vai na linha de comando (aparece no `ps`) nem no log.

### 15.10 Log

- `/var/log/painel-ramais/instalacao.log` (root 0600), só acréscimo. Para cada comando: data e hora, subcomando do instalador, argv executado, código de saída, stdout e stderr.
- A senha vai para a CLI pelo stdin e o conteúdo do stdin nunca é registrado.

---

## 16. Critérios de aceite consolidados

Especificação mais adendo. "Linux" = precisa de container ou VM Rocky (o gerente confirma com o usuário se usa WSL2 ou Docker Desktop antes; pedido da especificação). Todos estão **pendentes**: este documento só define o que conferir.

| # | Critério | Origem | Precisa de Linux |
|---|---|---|---|
| C1 | Simulador com 10.000 ramais e rajada de 3.000 desregistros, modos sip e pjsip: p99 evento até delta abaixo de 1 s, nenhuma perda de estado, memória sem crescimento contínuo | especificação | sim (CPU e memória medidas em Linux) |
| C2 | Modo `auto` detecta a tecnologia no simulador SIP e no PJSIP | especificação | não |
| C3 | AMI derrubado no meio da carga: nenhum ramal `unregistered`, todos `unknown`; na volta, snapshot refeito e clientes com epoch novo | especificação | não |
| C4 | "Com quem fala" correto em chamada interna, externa e depois de transferência | especificação | não |
| C5 | `rpm -i`, `rpm -U` e `rpm -e` em Rocky 8 e Rocky 9 com o simulador no lugar do Asterisk | especificação | sim |
| C6 | Atualização simulada do Issabel (regera `manager.conf`, reinicia httpd e Asterisk): serviço segue, reconecta sozinho, verificação de dependências acusa o que sumiu | especificação | sim |
| C7 | Todo `.sh`, `.service`, `.conf`, `.yaml` (e `.py`, `.ini`) do repositório com fim de linha LF | especificação e adendo | não |
| A1 | `./instalar.py verificar` em Rocky 8 mínimo e Rocky 9 com o simulador | adendo | sim |
| A2 | `instalar` interativo e com `--respostas` | adendo | sim |
| A3 | `instalar` de novo com bundle mais novo (upgrade) e com o mesmo bundle (idempotência: sem usuário, grupo ou config duplicados) | adendo | sim |
| A4 | `status` | adendo | sim |
| A5 | `rollback` (com e sem mudança de esquema do banco) | adendo | sim |
| A6 | `remover` e `remover --purge` | adendo | sim |
| A7 | Rocky 8 mínimo **sem** o pacote `python3`: `./instalar.py` roda via `platform-python` | adendo | sim |
| A8 | Falha forçada de verificação (`mod_proxy_wstunnel` ausente): `instalar` aborta antes de tocar no sistema. Prova: `rpm -qa`, `manager_custom.conf` e `ls /etc/httpd/conf.d` iguais antes e depois | adendo | sim |
| A9 | Instalação offline: container com rede desligada durante o `instalar` | adendo | sim |
| A10 | `vermin --target=3.6-` sem violação no `instalar.py` | adendo | não (exige instalar o `vermin`, com ok do usuário) |
| A11 | No tar.gz, `instalar.py` com modo 0755 e sem CR | adendo | não (`tar -tvzf` mostra o modo) |
| A12 | `LEIAME-INSTALACAO.md` com passo a passo de campo e exemplo de laço ssh para vários servidores | adendo | não |

Infraestrutura de teste que os critérios com Linux pedem (a desenhar pelo qa/devops, sem decisão aqui): imagem Rocky 8 mínima e Rocky 9 com systemd como init; httpd com e sem `mod_proxy_wstunnel`; um `asterisk` falso que responde aos `-rx` usados pelo `verificar` e pelo `%post`, mais o simulador na 5038; um marcador de "Issabel 5 presente" (S9); `manager.conf` e `manager_custom.conf` de teste. Montar a imagem baixa pacotes dentro do container: precisa de ok do usuário.

Suposição nova, **S9**: o nome do pacote que marca "Issabel 5 presente" (candidatos `issabel-framework` ou `issabelPBX`) precisa ser confirmado num servidor real; até lá o `verificar` aceita qualquer um dos dois.

---

## 17. Estrutura planejada da documentação (o devops escreve o texto final)

`README.md` (raiz do repositório, português):
1. O que é e o que não é (somente leitura; fase 1 sem tela)
2. Requisitos: Rocky 8 ou 9, Issabel 5, Asterisk 13 ou mais, SIP ou PJSIP
3. Instalação em campo com o bundle e o `instalar.py` (aponta para o `LEIAME-INSTALACAO.md`)
4. Instalação manual com `rpm -i` e o que o `%post` faz em cada arquivo
5. Configuração (`config.yaml`, tabela da seção 6)
6. Usuários e grupos pela CLI; formato do CSV
7. Token de teste
8. Formato do snapshot e dos deltas para a fase 2 (resumo e link para as seções 2, 4 e 5 deste contrato)
9. Atualização, rollback, remoção e limpeza total
10. Solução de problemas: `/api/health` e `deps`, `journalctl`, AMI não conecta, include ausente, `httpd -t` falhou, SELinux, cookie `Secure` em `http://`, porta ocupada, banco com esquema mais novo que o binário
11. Desenvolvimento: alvos `build`, `test`, `rpm`, `bundle` (Makefile e `build.ps1`), simulador, teste de carga, LF e `.gitattributes`, `vermin`

`LEIAME-INSTALACAO.md` (vai no bundle, para técnico de campo):
1. Copiar e extrair o tar.gz
2. `./instalar.py verificar` e como ler OK, AVISO e ERRO
3. `./instalar.py instalar` interativo
4. Modo não interativo: `respostas.ini` (exemplo da seção 15.9), arquivo de senha 0600, `--sim`, códigos de saída
5. Laço ssh para vários servidores, listando as falhas pelo código de saída
6. `status`, `rollback`, `remover`, `remover --purge`
7. Onde ficam os logs e o que mandar para o suporte

---

## 18. Arquivo de captura de AMI (`painel-ramais capturar`)

Texto, versão 1; serve de entrada do modo "reproduzir" do simulador e dos testes de "com quem fala" sobre `testdata/`. Pacote Go de referência: `internal/capture` (`Read` devolve meta, quadros e fim).

```
#PAINEL-RAMAIS-CAPTURA 1
#meta {"versao_daemon":"0.1.1","inicio":"2026-10-14T13:00:00Z","minutos":10,"anonimizado":true,"digitos_externos":7}
< 2026-10-14T13:00:00.123456789Z 29
Asterisk Call Manager/5.0.1\r\n
\n
> 2026-10-14T13:00:00.124000000Z 69
Action: Login\r\n
ActionID: p1\r\n
Secret: ***\r\n
Username: painel-ramais\r\n
\r\n
\n
...
#fim {"quadros":4812,"tecnologia":"pjsip","asterisk":"18.20.0","encerrado_por":"prazo"}
```

- Linha de cabeçalho do quadro: `<dir> <data> <n>` e `\n`. `dir`: `<` recebido do Asterisk, `>` enviado pelo daemon. `data`: RFC 3339 UTC com nanossegundos, a hora em que o quadro terminou de chegar (ou de ser escrito). `n`: tamanho em bytes do quadro **depois** da limpeza.
- Em seguida vêm exatamente `n` bytes (o quadro como no socket: linhas `Chave: valor` com CRLF, terminadas por linha em branco) e um `\n` separador. O banner `Asterisk Call Manager/x.y.z` é o primeiro quadro, uma linha só, sem linha em branco.
- Linhas que começam com `#` fora de um quadro são comentários; `#meta` e `#fim` carregam JSON. `encerrado_por`: `prazo`, `interrupcao` ou `conexao: …`.
- Respostas de lista (`CoreShowChannels`, `PJSIPShowEndpoints`…) são vários quadros consecutivos com o mesmo `ActionID`, como no socket. O simulador deve trocar o `ActionID` das respostas pelo da action recebida.
- Limpeza (a anonimização é **por exclusão**: todo valor passa pela limpeza, exceto as chaves estruturais `Event`, `Response`, `Action`, `ActionID`, `Uniqueid`, `Linkedid`, `Bridge*`, `*Time`, `*Date`, `*Usec`, `*Count`, `Duration`, `State`, `Status`, `Cause`, `Context`, `UserAgent` e semelhantes): valores de chaves `*secret*`, `*password*`, `*md5*`, `*challenge*`, `*token*` viram `***` sempre. Com `--anonimizar`: números com `--digitos-externos` dígitos ou mais (nos campos de número, `Callerid`, `Channel`, `DialString`, `ApplicationData`, URIs) viram um ficcional de mesmo tamanho e mesmo primeiro dígito; nomes (`*IDName`, `*LineName`, parte entre aspas de `Callerid`) viram `Pessoa NNN`; IPv4 (menos 127.x e 0.0.0.0) vira `10.200.x.y`. Cobertura: números longos em qualquer campo (DIDs em `Username`, `ObjectName`, `Peer`, `AccountCode`, `Value`, `Exten`…); IPv4 e IPv6 (inclusive dentro de URIs e de `Via`); host de URI (`usuario@host.dominio`) e chaves `Host`/`Domain`/`Realm`/`Server` viram `host-NNN.invalid`; `Username`/`AccountCode`/`Mailbox` não numéricos viram `Conta NNN` (o `Username` do quadro `Action: Login` é o da conta do painel e fica); `Value` de `VarSet` de variável de nome (`CALLERID(name)` etc.) vira pseudônimo; IPv6 vira `fd00:200::N`. Ramais internos curtos, `Uniqueid`/`Linkedid`, sufixos de canal (`-0000002a`), nomes de tronco e horas (`14:30:05`) ficam como estão.
- **Limitação (melhor esforço):** a anonimização é por padrão e não por entendimento do conteúdo. Fica de fora: dado pessoal em texto livre sem chave de nome (por exemplo dentro de `Message`), números curtos (abaixo de `--digitos-externos`) que sejam de pessoas, MACs e nomes de tronco que identifiquem o cliente. **Revise a captura (`grep` pelos seus números, nomes e IPs) antes de versionar em `testdata/` ou compartilhar.** O teste `TestAnonymizedCaptureLeavesNoOriginalValue` prova a ausência de uma lista de valores originais num arquivo sintético. O mesmo original vira sempre o mesmo ficcional em toda a captura.

Roteiro de chamadas para a captura e comandos: `docs/ROTEIRO-CAPTURA.md`.
