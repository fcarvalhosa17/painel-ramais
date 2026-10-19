# Contratos da Fase 1: Painel de Ramais (Issabel 5)

**Data**: 2026-10-13
**Origem**: `docs/PROMPT-FASE-1.md` e `docs/ADENDO-FASE-1-INSTALADOR.md` (especificação do usuário, mesmo peso; o adendo troca o `discovery.sh` pelo `instalar.py verificar`). Em conflito, a especificação vence e este documento é corrigido.
**Decisões de arquitetura ligadas**: `docs/adr/ADR-001-protocolo-websocket.md` (Aceito), `docs/adr/ADR-002-sessao-e-token-hmac.md` (PROPOSTO, aguarda o usuário), `docs/adr/ADR-003-pacote-rpm-e-integracao-issabel.md` (Proposto, aguarda spike em Rocky 8 e 9), `docs/adr/ADR-004-tela-e-menu-do-issabel.md` (Proposto; fase 2: tela, menu do Issabel, chave própria do Issabel e acesso de emergência), `docs/adr/ADR-005-grupos-editaveis-manual-sobre-faixa.md` (substituído em parte pelo ADR-006), `docs/adr/ADR-006-ramal-em-n-grupos.md` e `docs/adr/ADR-007-relatorio-de-registro.md` (Aceitos, rc.7), `docs/adr/ADR-008-chamadas-e-filas-ao-vivo.md` e `docs/adr/ADR-009-nome-da-fila-esquema-5.md` (Aceitos, rc.8). Planos: `docs/plans/2026-10-16-fase2-menu-issabel.md`, `docs/plans/2026-10-19-rc7-n-grupos-e-registros.md` e `docs/plans/2026-10-19-rc8-chamadas-e-filas.md`.

Este documento é a fonte única dos formatos trocados entre as partes do coletor, e entre o coletor e quem o consome (fase 2: tela e item no menu do Issabel). Quem implementa não inventa campo: campo novo entra aqui primeiro.

---

## 0. Decisões de padrão já assumidas

Valem para todos os agentes. Reabrir só com o usuário.

| # | Decisão | Onde se materializa |
|---|---|---|
| D1 | Ramal = endpoint (PJSIP) ou peer (SIP) cujo nome casa **inteiro** com `extension_pattern` (padrão `^[0-9]+$`). O resto é tronco. Troncos não aparecem no snapshot; servem para resolver "com quem fala". | seção 7, seção 11 |
| D2 | N grupos por ramal (rc.7, ADR-006): o ramal está em todo grupo cuja faixa o contém e em todo grupo onde tem vínculo manual; o manual soma à faixa. Faixas de grupos diferentes podem se sobrepor; do mesmo grupo, não. "Sem grupo" é implícito (ramal sem nenhum vínculo). `--replace` troca todos os grupos numa transação só. | seções 2.1, 4.6, 8, 9, 10 |
| D3 | A CLI recusa rodar com euid 0. Uso: `sudo -u painel-ramais painel-ramais ...`. Motivo: root criaria o banco e os arquivos WAL com dono root e o daemon pararia de escrever. | seção 8 |
| D4 | Mudança de grupo (e de usuário) chega ao daemon por SIGHUP. A CLI manda o sinal sozinha; o daemon relê os grupos (catálogo e atribuições manuais), publica a mudança por delta, **sem epoch novo**, e revalida as sessões dos WebSockets abertos. | seção 8, ADR-001, ADR-005 |
| D5 | `/debug` e `/api/health` ficam fora do proxy do Apache (`ProxyPass "!"`) e o daemon recusa nessas rotas qualquer requisição com `X-Forwarded-For` ou `X-Forwarded-Host` (o mod_proxy sempre adiciona esses cabeçalhos). Duas barreiras independentes. | seção 1, ADR-003 |
| D6 | REST usa o envelope `{success, data, error, meta}`. WebSocket usa mensagens tipadas por `type`, sem envelope. | seções 1 a 5 |
| D7 | Histórico por minuto. Minuto sem AMI (ou sem daemon) sai como `gap: true` com contagens `null`, nunca como queda de registros. | seções 4.3, 10 |
| D8 | Chaves JSON e valores de enum em inglês, `snake_case`. Datas em RFC 3339 UTC com milissegundos (`2026-10-13T14:30:00.000Z`). | todas |
| D9 | O `instalar.py` orquestra e valida; nunca edita `manager_custom.conf`, Apache, SELinux nem o SQLite. Integração com o sistema é do `%post` do RPM; usuário e grupos, da CLI do daemon. | seção 15, ADR-003 |
| D10 | Relatório de registro (rc.7, ADR-007): só transição ao vivo conta; janela de 120 s depois de `Shutdown`, `FullyBooted` ou conexão do AMI vira evento do sistema; gravação em lote fora da goroutine do estado; retenção fixa de 30 dias. rc.8: sublotes por custo, uma repetição, perda contada no health. | seções 4.7, 10, 11 |
| D11 | Chamadas e filas ao vivo (rc.8, ADR-008): uma linha por chamada, `id` = `Linkedid`; ramal é quem está no inventário, qualquer outro canal não Local é externo; projeção no estado, publicada no mesmo delta com chaves opcionais (`calls`, `calls_removed`, `queues`, `queues_removed`); tudo em memória. Filas só por `QueueStatus` e eventos de classe `agent`. Nome da fila no banco do painel (esquema 5, ADR-009). | seções 2.3, 2.4, 4.1, 4.8, 5.2, 7, 10, 11.6 |
| D12 | Sem teste automatizado no repositório (rc.8, decisão do usuário): critério de pronto é compilar (`go build` com `GOOS=linux ./...`, `go vet ./...`, `node --check`) e conferir por leitura; o teste é ao vivo no servidor. `testdata/capturas` e `cmd/loadtest` ficam (ferramenta de carga na VM, nunca no PC do usuário). | seções 7.1, 12, 14 |

---

## 1. Rotas, acesso e envelope

O daemon escuta só em loopback. O Apache publica o prefixo `/painel-ramais/` e o remove ao repassar: o navegador chama `/painel-ramais/api/snapshot`, o daemon recebe `/api/snapshot`.

| Método | Rota no daemon | Sessão | Quem alcança | Resposta |
|---|---|---|---|---|
| GET | `/` (exato) | não | navegador via Apache (`/painel-ramais/`) | tela (200) ou `303` para o aviso (seção 1.3) |
| GET | `/acesso` | não | navegador via Apache | HTML de aviso, sem script (seção 1.3) |
| GET | `/emergencia` | não | navegador via Apache | `301` para `/painel-ramais/emergencia/` |
| GET | `/emergencia/` (exato) | não | navegador via Apache | a mesma `index.html` da tela, cabeçalhos de emergência (seção 1.3) |
| GET | `/assets/<arquivo>` | não | navegador via Apache | arquivo de `assets/`; só `.js`, `.css`, `.svg`; e `assets/fonts/<arquivo>` só `.woff2` e três `.txt` por nome exato (seção 1.3) |
| POST | `/api/login` | não | navegador via Apache | envelope; só aceita o usuário `emergencia` (seção 3.1) |
| POST | `/api/logout` | não exige (idempotente) | navegador via Apache | envelope |
| POST | `/api/token` | não | navegador via Apache | envelope |
| GET | `/api/session` | sim | navegador via Apache | envelope (seção 3.4) |
| GET | `/api/snapshot` | sim | navegador via Apache | envelope, gzip |
| GET | `/api/groups` | sim | navegador via Apache | envelope, gzip |
| POST | `/api/groups/create` | sim, administrador | navegador via Apache | envelope (seção 4.6) |
| POST | `/api/groups/update` | sim, administrador | navegador via Apache | envelope (seção 4.6) |
| POST | `/api/groups/assign` | sim, administrador | navegador via Apache | envelope (seção 4.6) |
| POST | `/api/groups/import` | sim, administrador | navegador via Apache | envelope (seção 4.6) |
| POST | `/api/groups/delete` | sim, administrador | navegador via Apache | envelope (seção 4.6) |
| POST | `/api/queues/rename` | sim, administrador | navegador via Apache | envelope (seção 4.8, rc.8) |
| GET | `/api/history` | sim | navegador via Apache | envelope, gzip |
| GET | `/api/registrations/summary` | sim | navegador via Apache | envelope, gzip (seção 4.7) |
| GET | `/api/registrations` | sim | navegador via Apache | envelope, gzip (seção 4.7) |
| GET | `/api/registrations/extension` | sim | navegador via Apache | envelope, gzip (seção 4.7) |
| GET | `/api/registrations/series` | sim | navegador via Apache | envelope, gzip (seção 4.7) |
| GET | `/ws` | sim (cookie no upgrade) | navegador via Apache (mod_proxy_wstunnel) | mensagens tipadas |
| GET | `/api/health` | não | só localhost direto | envelope |
| GET | `/debug` | sim | só localhost direto | HTML |

Regras:
- **Sessão**: toda rota `/api/*` e `/ws` exige cookie de sessão válido, exceto `login`, `logout`, `token` e `health`; `/debug` também exige sessão (além de ser só localhost). Sem sessão: `401 unauthorized`. Detalhes no ADR-002.
- **Só localhost** (`/api/health`, `/debug`): requisição com `X-Forwarded-For` ou `X-Forwarded-Host` recebe `404` (não revela que a rota existe). O teste é `curl http://127.0.0.1:<porta>/api/health` no próprio servidor.
- **POST** só é aceito com `Sec-Fetch-Site` ausente, `same-origin` ou `none`; qualquer outro valor (`cross-site`, `same-site`, desconhecido) recebe `403 forbidden` antes de qualquer processamento. **POST** exige `Content-Type: application/json` (senão `415`) e corpo de no máximo 4 KiB (senão `413`); exceção: `/api/groups/import`, `create`, `update` e `delete` aceitam até 1 MiB, e `/api/groups/assign` até 2 MiB. JSON com campo desconhecido é `400`. O daemon nunca responde cabeçalhos CORS. Toda resposta leva `X-Content-Type-Options: nosniff`. O `http.Server` tem `ReadTimeout` de 15 s (o handler do `/ws` limpa o prazo da conexão).
- **gzip**: respostas `/api/*` saem com gzip quando o cliente manda `Accept-Encoding: gzip` (stdlib `compress/gzip`).
- Qualquer outra rota: `404 not_found` no envelope. Caminho com segmento `..` (inclusive `%2e%2e`): `404` no envelope, sem redirecionar. As rotas da tela (`/`, `/acesso`, `/emergencia`, `/emergencia/`, `/assets/*`) aceitam GET e HEAD; outro método dá `405`. Sem a tela embutida (`Options.Static` nulo, só em teste), elas dão `404`.
- **Papel**: a sessão é de administrador se, e só se, `kind = token` e o `subject` começa com `issabel:admin:`. O papel é derivado do `subject` gravado; nenhuma coluna nova no banco. As rotas marcadas "administrador" respondem `403 forbidden` a qualquer outra sessão (a checagem vem antes de ler o corpo).

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
| 400 | `invalid_csv` | só em `/api/groups/import`: CSV inválido ou com faixas que se cruzam **no mesmo grupo** (no arquivo ou com as que o grupo já tem; faixa igual é ignorada, não é erro); `message` é o texto do erro do parser (até 20 linhas `linha N: ...`); também mais de 5.000 linhas de dados por importação ou mais de 1.000 faixas de um mesmo grupo no arquivo (M2, vale para a API e para `painel-ramais group import`). Nada é gravado |
| 401 | `unauthorized` | sem sessão ou sessão expirada |
| 401 | `invalid_credentials` | login com usuário ou senha errados (mesma mensagem para os dois casos) |
| 401 | `invalid_token` | token com assinatura errada, expirado, já usado ou malformado (mesma mensagem para todos) |
| 403 | `forbidden` | sessão sem papel de administrador numa rota de administrador: "Apenas administradores do Issabel podem alterar grupos" (em `/api/queues/rename`: "Apenas administradores do Issabel podem renomear filas"); ou sessão `issabel:admin:<usuario>` cujo usuário não está mais no grupo `administrator` do `/var/www/db/acl.db` (conferido somente leitura a cada escrita de grupo; acl.db ilegível também dá 403, falha fechada; a sessão e a visualização continuam; M1); ou POST com `Sec-Fetch-Site` diferente de ausente, `same-origin` e `none` (por exemplo `cross-site` ou `same-site`; qualquer rota POST, antes de ler o corpo) |
| 404 | `not_found` | rota inexistente, ou rota só localhost acessada via proxy; `/api/groups/update`, `/assign` ou `/delete` de grupo que não existe ("Grupo inexistente"); `/api/queues/rename` de fila que não está na projeção atual ("Fila inexistente", rc.8) |
| 405 | `method_not_allowed` | método errado |
| 409 | `conflict` | só nas rotas de edição de grupo (`create`, `update`): nome de grupo que já existe (sem diferenciar maiúsculas; `message` = `grupo ja existe: "<nome>"`). Desde a rc.7 faixa que cruza faixa de **outro** grupo é aceita (ADR-006). Nada é gravado |
| 413 | `payload_too_large` | corpo acima de 4 KiB (1 MiB em `/api/groups/import`, `create`, `update` e `delete`; 2 MiB em `/api/groups/assign`) |
| 415 | `unsupported_media_type` | POST sem `application/json` |
| 429 | `too_many_attempts` | 5 logins errados do mesmo IP do cliente em 5 min, ou 20 do mesmo usuário em 5 min (seção 3.1), ou 4 comparações bcrypt já em andamento (a tentativa recusada por saturação não conta contra o usuário) |
| 500 | `internal` | falha inesperada (detalhe só no log) |
| 503 | `degraded` | só em `/api/health`, quando AMI está fora ou alguma dependência falhou (corpo igual ao 200) |

### 1.3 Tela, aviso e cabeçalhos (ADR-004)

O prefixo do navegador é fixo: `/painel-ramais/` (o mesmo `Path` do cookie). A tela (HTML, CSS e JavaScript de `internal/web/static/`) vai embutida no binário (`//go:embed`, pacote `internal/web`); arquivo começando com `.` ou `_` não entra no embed. `Content-Type` definido pelo daemon pela extensão, sem depender do `/etc/mime.types`: `.js` `text/javascript; charset=utf-8`, `.css` `text/css; charset=utf-8`, `.svg` `image/svg+xml`, `.html` `text/html; charset=utf-8`. `/assets/` aceita um nível só (`/assets/x.js`), mais a pasta `/assets/fonts/` (também um nível só), onde valem apenas `.woff2` (`font/woff2`) e três `.txt` por nome exato (`OFL-IBMPlex.txt`, `OFL-BarlowSemiCondensed.txt`, `ORIGEM.txt`; `text/plain; charset=utf-8`); `/assets/`, outra subpasta, nome começando com `.`, extensão fora da lista e arquivo inexistente dão `404 not_found` (envelope). A CSP da tela já tem `font-src 'self'` (sem `data:` e sem terceiros).

`GET /` decide pelo `Sec-Fetch-Dest`:

| `Sec-Fetch-Dest` | Resposta |
|---|---|
| `iframe` | `200`, `index.html`, cabeçalhos "tela" |
| ausente | `200`, `index.html`, cabeçalhos "tela" (navegador antigo; o script confere `window.top`) |
| qualquer outro (`document`, `empty`, ...) | `303`, `Location: /painel-ramais/acesso#`, corpo vazio, `Cache-Control: no-store` |

O `#` vazio no `Location` é obrigatório: impede que o fragmento `#t=<token>` da URL original seja herdado pelo redirecionamento. HEAD responde igual (`curl -I` mostra o 303).

Cabeçalhos (além de `X-Content-Type-Options: nosniff` e `X-Request-Id`, que saem em toda resposta):

| Página | Cabeçalhos |
|---|---|
| tela (`/`) | `Content-Type: text/html; charset=utf-8`; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`; `Vary: Sec-Fetch-Dest`; `X-Frame-Options: SAMEORIGIN`; `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'` |
| emergência (`/emergencia/`) | igual à tela, trocando `frame-ancestors 'self'` por `frame-ancestors 'none'` e `X-Frame-Options: DENY`; sem `Vary` (não olha o `Sec-Fetch-Dest`) |
| aviso (`/acesso`) | `Content-Type: text/html; charset=utf-8`; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`; `X-Frame-Options: SAMEORIGIN`; `Content-Security-Policy: default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'` |
| assets | `Content-Type` da extensão; `Cache-Control: no-cache` (fontes em `assets/fonts/`: `public, max-age=604800`, com o mesmo `ETag`); `ETag` (SHA-256 truncado do conteúdo; `If-None-Match` igual dá `304`); os `.svg` levam também `Content-Security-Policy: default-src 'none'` |

Consequências para a tela: nada inline (nem `<script>`, nem `<style>`, nem `style="..."` no HTML, nem `on*=`), nada externo (CDN, Google Fonts), nenhum `eval`/`new Function`. Estilo dinâmico pelo CSSOM é permitido. Texto vindo do Asterisk entra só por `textContent`/`createTextNode` ou canvas.

Fluxo de entrada da tela (frontend, daemon e módulo PHP): o módulo do Issabel emite um token (seção 3.3) e monta `<iframe src="/painel-ramais/#t=<token>" allow="fullscreen" title="Painel de Ramais">`. A tela, fora da rota de emergência, se `window.top === window.self` mostra o aviso e para; casando o fragmento `#t=<token>`, primeiro apaga-o da barra (`history.replaceState`) e depois faz `POST /api/token`; sem fragmento (ou token recusado), tenta `GET /api/session`; 401 mostra "Sessão encerrada. Abra de novo pelo menu PBX do Issabel.". Na rota `/painel-ramais/emergencia/`, 401 em `/api/session` mostra o formulário de login do usuário `emergencia`. Qualquer REST com 401, ou `session_expired` seguido de 4001, volta a esta mensagem (na emergência, ao formulário).

---

## 2. Tipos compartilhados

### 2.1 Ramal (`Extension`)

```json
{
  "number": "2001",
  "name": "Maria Souza",
  "technology": "pjsip",
  "groups": ["Plantão", "Suporte"],
  "range_groups": ["Suporte"],
  "manual_groups": ["Plantão"],
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
| `groups` | array de string | rc.7 (ADR-006): todos os grupos do ramal, a união de `range_groups` e `manual_groups`, sem repetição, ordenada como o catálogo (nome em minúsculas, `strings.ToLower`). **Nunca vazio**: sem nenhum vínculo vem `["Sem grupo"]`. Substitui `group` e `group_manual` (removidos; não há campo de compatibilidade) |
| `range_groups` | array de string | grupos cuja faixa contém o número (regra numérica da seção 9: só dígitos, sem zero à esquerda, até 18 dígitos). Mesma ordem; `[]` quando nenhum; nunca contém `"Sem grupo"` |
| `manual_groups` | array de string | grupos onde o número tem vínculo manual (`extension_groups`). Mesma ordem; `[]` quando nenhum; nunca contém `"Sem grupo"`. Um grupo pode estar nas duas listas (faixa e manual) |
| `registration` | enum | `registered`, `unregistered`, `unreachable`, `unknown`. `unknown` só quando o AMI está fora ou ainda não terminou o snapshot. **Nunca** `unregistered` por falta de AMI |
| `ip` | string ou null | IP do contato (PJSIP: via/contact; SIP: IP do peer) |
| `user_agent` | string ou null | `null` quando desconhecido. No PJSIP vem do snapshot (`PJSIPShowContacts`/`ContactList`) e é **mantido**: o `ContactStatus` do Asterisk 18 não traz `UserAgent`, e um evento sem o campo não apaga o valor já conhecido daquele endpoint e IP; evento com valor novo o atualiza; cada releitura do inventário o refaz |
| `latency_ms` | inteiro ou null | latência do qualify em ms (`RoundtripUsec` arredondado); `null` sem qualify ou com `RoundtripUsec` 0/ausente |
| `device_state` | enum | `unknown`, `not_inuse`, `inuse`, `busy`, `invalid`, `unavailable`, `ringing`, `ringinuse`, `onhold` (estados do Asterisk em minúsculas) |
| `calls` | array | chamadas ativas; vazio depois do Hangup e sempre vazio quando o AMI está fora |

Origem por grupo na tela (detalhe do ramal): grupo G em `range_groups` mostra "pela faixa A-B", onde A-B é a primeira faixa de G no catálogo (`groups` do snapshot) com `A <= número <= B`, comparando com `BigInt` (faixas de até 18 dígitos passam do inteiro seguro do JavaScript); G também em `manual_groups` mostra "pela faixa A-B e manual"; G só em `manual_groups` mostra "manual". "Remover deste grupo" fica habilitado só quando o grupo aberto está em `manual_groups` **e não** em `range_groups`.

### 2.2 Chamada (`Call`)

| Campo | Tipo | Regra |
|---|---|---|
| `id` | string | Uniqueid do canal do próprio ramal. Ramal com chamada em espera e outra ativa tem dois itens |
| `direction` | enum | `internal`: o outro lado é ramal. `inbound`: o outro lado é externo e o canal dele nasceu do lado de fora (não foi destino de Dial). `outbound`: o outro lado é externo e o canal dele foi destino de Dial. A regra olha o canal do outro lado, então sobrevive a transferência. rc.8 (ADR-008): **ramal é quem está no inventário** (endpoint do canal presente na lista de ramais do estado); qualquer outro canal que não seja Local é **externo**, inclusive canal sem endpoint da tecnologia ativa (DAHDI, IAX2, Khomp, `SIP/` com a tecnologia `pjsip`), que até a rc.7 virava `internal` com `peer.kind: unknown`. `peer.kind: unknown` fica só para "nenhum outro canal" (URA, correio de voz) e para Local sem par |
| `state` | enum | `ringing`: ainda não atendida, em qualquer ponta. `talking`: atendida (em bridge ou em aplicação, como URA ou correio de voz). `hold`: canal do ramal recebeu `Hold` sem `Unhold` |
| `peer.kind` | enum | `extension`, `external`, `unknown` (sem outro canal identificável, por exemplo URA) |
| `peer.number` | string ou null | ramal do outro lado, número externo, ou destino discado quando `unknown` |
| `peer.name` | string ou null | callerid ou connected line do outro lado, quando houver |
| `started_at` | data | criação do canal do ramal. Chamada já em curso no snapshot do AMI: `agora - Duration` do `CoreShowChannels` |
| `answered_at` | data ou null | quando o canal ficou `Up`; null enquanto `ringing` |

Correlação de "com quem fala" (implementação no `state`): Uniqueid, Linkedid, membros da bridge (`BridgeEnter`/`BridgeLeave`) e pares de Local channel casados pelo nome (`Local/x@ctx-0000001a;1` com `;2`).

### 2.3 Chamada ao vivo (`LiveCall`, rc.8, ADR-008)

Uma linha por **chamada** (não por ramal nem por canal), para a aba Chamadas. Vai no snapshot (`calls`, seção 4.1) e no delta (`calls`, `calls_removed`, seção 5.2). Tipo Go `model.LiveCall`, JSON = `json.Marshal` dele.

```json
{
  "id": "1791902000.4600",
  "direction": "inbound",
  "state": "talking",
  "extensions": ["2001"],
  "external": "11987654321",
  "trunk": "PJSIP/operadora_x",
  "queue": "600",
  "started_at": "2026-10-13T14:30:00.250Z"
}
```

| Campo | Tipo | Regra |
|---|---|---|
| `id` | string | `Linkedid` comum aos canais da chamada (= Uniqueid do canal âncora, o que começou a chamada). Estável enquanto a chamada existe, mesmo depois que o canal âncora desliga. Duas chamadas que viram uma (transferência assistida: o Asterisk dá aos canais o `Linkedid` mais antigo) continuam com o `id` mais antigo; a outra sai em `calls_removed` |
| `direction` | enum | `inbound`, `outbound`, `internal` (mesmas palavras da 2.2, regra diferente). Origem = canal com Uniqueid igual ao `id`; se ele é Local ou já não existia quando a linha foi montada pela primeira vez (snapshot), o canal não Local mais antigo da chamada. Origem fora do inventário de ramais: `inbound`, sempre (inclui tronco para tronco, siga-me). Origem ramal: `outbound` se há canal externo vivo na chamada, senão `internal`. Se a origem é externa ou ramal fica **guardado** na primeira montagem e não muda até a linha sumir |
| `state` | enum | precedência, a primeira que vale: `queued` (algum canal da chamada está aguardando numa fila: entrada de fila viva, seção 2.4); `hold` (algum canal não Local da chamada recebeu `Hold` sem `Unhold`); `talking` (alguma bridge tem 2 ou mais canais da chamada, ou nenhum canal não Local da chamada está fora de `Up`); `ringing` (o resto: algum canal tocando, ninguém em bridge) |
| `extensions` | array de string | números dos ramais (inventário) com canal vivo na chamada, sem repetição. Ordem: o ramal da origem primeiro (se a origem é ramal e está viva), depois por início do canal e Uniqueid. `[]` quando nenhum ramal (URA, tronco para tronco, só fila). Nunca `null`. Sem teto: grupo de toque com 30 ramais tocando traz os 30. Nome e grupos do ramal **não** vêm aqui: a tela os lê da lista de ramais do mesmo snapshot/delta (ramal com chamada nunca sai do estado antes do `Hangup`, seção 7.4) |
| `external` | string ou null | `inbound`: CallerID (número) do canal de origem; vazio vira `null`. `outbound`: número do `DialString` do `DialBegin` que criou o canal externo mais antigo da chamada (regra "Número discado" abaixo); sem ele (snapshot), o mesmo extraído do primeiro destino do `DialData` do canal que discou; sem os dois, o `Exten` do canal de origem (se diferente de `s`); senão `null`. `internal`: `null` |
| `trunk` | string ou null | `inbound`: rótulo (regra "Tronco" abaixo) do canal de origem, ou do canal externo vivo mais antigo se a origem já desligou. `outbound`: rótulo do canal externo vivo mais antigo. `internal`: `null` |
| `queue` | string ou null | `queued`: a fila em que espera. `talking`/`hold`: a fila que atendeu (`AgentConnect` com o Uniqueid de um canal da chamada; no snapshot, canal que está na aplicação `Queue` sem entrada viva na fila, primeiro argumento do `ApplicationData`). Senão `null`. Identificador da fila no Asterisk (`"600"`), não o nome |
| `started_at` | data | início do canal de origem (`agora - Duration` no snapshot); se a origem não existia quando a linha foi montada, o início do canal mais antigo. Guardado na primeira montagem. A duração é contada no navegador (`server_time` corrige o relógio) |

**Quando existe a linha.** Grupo de canais vivos com o mesmo `Linkedid` que tem pelo menos um canal não Local. Grupo só de Local channels não vira linha. A linha sai (`calls_removed`) quando o último canal não Local do grupo desliga ou muda de `Linkedid`.

**Tronco.** A partir do nome do canal: (1) tira o sufixo de sequência, o último `-` seguido só de dígitos hexadecimais até o fim; (2) fica só até a segunda `/`. Exemplos: `PJSIP/operadora_x-0000001a` vira `PJSIP/operadora_x`; `SIP/trunk-acme-0000001` vira `SIP/trunk-acme`; `DAHDI/i1/11999887766-1a` vira `DAHDI/i1`; `DAHDI/1-1` vira `DAHDI/1`; `IAX2/operadora-1234` vira `IAX2/operadora`. Nome fora dessas formas sai como veio depois dos dois passos (S17).

**Número discado** (do `DialString`, ou do destino do `DialData` sem o prefixo de tecnologia): com `@`, o texto antes do `@` (`011987654321@operadora_x`); sem `@` e com `/`, o texto depois da última `/` (`operadora_x/011987654321`, `g0/011987654321`); sem os dois, o texto inteiro. Vale só se casar `^[0-9+*#]{1,32}$`; senão passa para a reserva seguinte.

**Ramal ou externo.** Canal Local: nunca ramal nem externo (transparente). Canal com endpoint da tecnologia ativa presente no inventário do estado: ramal. Qualquer outro canal: externo.

**"Outro lado desconhecido"** (texto da tela, sem campo próprio): `internal` com `extensions` de tamanho 1 e `queue` nulo; `inbound` com `extensions` vazio e `queue` nulo. Se a chamada tem canal Local e fica assim por 1 s seguido, o daemon registra **uma vez** por `id` o log Info `chamada com Local sem par` com `uniqueid` (o canal Local), `linkedid` e `canal` (nome do canal Local). Sem canal Local (URA pura) ou com externo do outro lado: sem log.

### 2.4 Fila (`Queue`, rc.8, ADR-008 e ADR-009)

Vai no snapshot (`queues`, seção 4.1) e no delta (`queues`, `queues_removed`, seção 5.2). Tipo Go `model.Queue`, JSON = `json.Marshal` dele.

```json
{
  "queue": "600",
  "name": "Suporte N1",
  "completed": 120,
  "abandoned": 7,
  "service_level_s": 60,
  "service_level_perf": 93.5,
  "stats_at": "2026-10-13T14:29:41.000Z",
  "entries": [
    { "uniqueid": "1791902000.4600", "position": 1, "number": "11987654321", "name": null, "since": "2026-10-13T14:29:12.100Z" }
  ],
  "members": [
    { "interface": "Local/2001@from-queue/n", "name": "Maria Souza", "extension": "2001", "state": "busy", "paused": false, "pause_reason": null, "calls_taken": 12 },
    { "interface": "Local/2002@from-queue/n", "name": "João Lima", "extension": "2002", "state": "paused", "paused": true, "pause_reason": "almoço", "calls_taken": 4 }
  ]
}
```

| Campo | Tipo | Regra |
|---|---|---|
| `queue` | string | identificador da fila no Asterisk (`Queue` do AMI), chave da fila. 1 a 64 caracteres |
| `name` | string ou null | nome dado no painel (`queue_names`, seção 10, versão 5); `null` sem nome. A tela mostra `Fila <queue>` quando `null` |
| `completed`, `abandoned` | inteiro | `Completed` e `Abandoned` do `QueueParams` da última `QueueStatus` da fila; `0` antes da primeira. Contam desde o último reinício do Asterisk (ou reset de estatística da fila) |
| `service_level_s` | inteiro | `ServiceLevel` (segundos) do `QueueParams`; `0` antes da primeira `QueueStatus` |
| `service_level_perf` | número | `ServicelevelPerf` (percentual, 0 a 100, uma casa) do `QueueParams`; `0` antes da primeira |
| `stats_at` | data ou null | quando a última `QueueStatus` desta fila foi lida; `null` se a fila só apareceu por evento |
| `entries` | array | quem aguarda, ordenado por `since` (mais antigo primeiro). `[]` quando ninguém |
| `entries[].uniqueid` | string | Uniqueid do canal do chamador (casa com um canal da chamada da 2.3) |
| `entries[].position` | inteiro | posição pela ordem de `since` (1 = mais antigo). Fila com prioridade mostra a ordem de chegada (ADR-008) |
| `entries[].number`, `.name` | string ou null | `CallerIDNum` e `CallerIDName` (passam por `model.CleanName`); vazio vira `null` |
| `entries[].since` | data | entrada na fila: `At` do `QueueCallerJoin`, ou `agora - Wait` na `QueueStatus` |
| `members` | array | agentes, ordenados por `interface`. `[]` em fila sem agente (a fila continua aparecendo) |
| `members[].interface` | string | `Interface` do evento ou `Location` da `QueueStatus`; chave do membro dentro da fila |
| `members[].name` | string | `MemberName` (CleanName); vazio vira o próprio `interface` |
| `members[].extension` | string ou null | ramal do inventário extraído de `StateInterface`, senão de `interface`: texto entre o primeiro `/` ou `:` e o primeiro `@` ou `/` seguinte (`Local/2001@from-queue/n`, `PJSIP/2001`, `hint:2001@ext-local` dão `2001`); `null` se não for ramal do inventário (S16) |
| `members[].state` | enum | precedência: `unavailable` (`Status` 4 ou 5: inválido ou indisponível); `busy` (`InCall` 1, ou `Status` 2, 3, 6, 7 ou 8: em uso, ocupado, tocando, tocando em uso, em espera); `paused` (`Paused` 1); `free` (o resto, inclusive `Status` 0, desconhecido, que o `app_queue` trata como disponível) |
| `members[].paused` | booleano | `Paused` do AMI, independente do `state` (agente em ligação e pausado: `state: busy`, `paused: true`) |
| `members[].pause_reason` | string ou null | `PausedReason` (CleanName); vazio vira `null` |
| `members[].calls_taken` | inteiro | `CallsTaken` |

**Derivados no navegador (o servidor não manda, para não haver duas fontes):** aguardando = `entries.length`; maior espera desde = `entries[0].since` (contada ao vivo); agentes livres, em atendimento, pausados e indisponíveis = contagem de `members[].state` (`free`, `busy`, `paused`, `unavailable`; cada membro conta em um só). "Aguardando em fila" do topo da aba = número de linhas da 2.3 com `state: queued`.

**Quais filas aparecem.** Toda fila da última `QueueStatus` completa, mais as que aparecerem por evento depois dela. Fila vazia ou sem agente continua na lista. Fila que some de uma `QueueStatus` completa (ou cuja `QueueStatus` individual volta sem `QueueParams`) sai em `queues_removed`. AMI fora: `queues: []` (o `Reset` da queda leva a lista vazia).

---

## 3. Autenticação (ADR-002 aceito; revisão da fase 2 no ADR-004, Proposto)

Os formatos abaixo valem como estão no ADR-002 (sessão e token) e no ADR-004 (chave do Issabel, papel, login só de emergência). Se o usuário mudar algo, esta seção muda junto.

### 3.1 POST /api/login

Só existe um usuário de senha aceito, o reservado `emergencia` (rota de emergência, ADR-004 item 5; o instalador cria o usuário com este mesmo nome). Requisição:

```json
{ "username": "emergencia", "password": "uma-senha-longa" }
```

Resposta 200, com `Set-Cookie: painel_session=<id>; Path=/painel-ramais/; Max-Age=604800; Secure; HttpOnly; SameSite=Strict`:

```json
{
  "success": true,
  "data": { "subject": "emergencia", "kind": "password", "admin": false, "expires_at": "2026-10-13T22:30:00.000Z" },
  "error": null,
  "meta": null
}
```

Validade da sessão (decisão 5, ADR-002 aceito): é por **inatividade** (`auth.session_ttl`, padrão 12 h), não absoluta. Qualquer requisição autenticada passada a metade do prazo renova a sessão, e uma conexão `/ws` ativa a renova sozinha a cada `session_ttl/6`; uma tela de NOC em uso nunca expira. `expires_at` da resposta é a validade naquele instante. O cookie do login (emergência) leva `Max-Age=604800` (7 dias) só para sobreviver à sessão: quem decide é o banco. O cookie de sessão por token (menu do Issabel) vai **sem** `Max-Age` nem `Expires` (cookie de sessão do navegador: some ao fechar); a expiração por inatividade continua no servidor.

Validação: `username` 1 a 64 caracteres; `password` 1 a 72 bytes (limite do bcrypt). Qualquer `username` que não seja `emergencia` (sem diferenciar maiúsculas) recebe `401 invalid_credentials` depois da comparação bcrypt falsa (o mesmo caminho do usuário inexistente, o tempo não revela nada) e conta no limitador como qualquer outro; usuários criados na fase 1 continuam no banco e deixam de entrar. A sessão criada tem `kind = password`, `subject = emergencia` e `admin = false`. Falha: `401 invalid_credentials`. Dois limites, ambos `429 too_many_attempts`: **por IP do cliente**, 5 falhas em 5 min (o IP é o último valor do `X-Forwarded-For`, que o Apache acrescenta no fim; valores à esquerda são do cliente e não contam; sem o cabeçalho ou com valor inválido, o `RemoteAddr`), de modo que quem erra de um IP não trava a senha certa de outro; e **por nome de usuário**, 20 falhas em 5 min (contra tentativa distribuída em vários IPs). O acerto zera as duas contagens. Cada limitador guarda no máximo 10.000 chaves; cheio, descarta as chaves vencidas e, se preciso, a de tentativa mais antiga (memória limitada sem negar usuário novo). A tentativa é contada **antes** do bcrypt (reserva atômica; o acerto zera a contagem), então tentativas paralelas não furam o limite; no máximo 4 bcrypt rodam ao mesmo tempo e o excedente recebe `429`. O log guarda do usuário digitado só o tamanho e 8 hex do SHA-256.

### 3.2 POST /api/logout

Sem corpo (ou `{}`). Apaga a sessão do banco se existir e devolve o cookie expirado (`Max-Age=0`). Sempre 200, com ou sem sessão:

```json
{ "success": true, "data": null, "error": null, "meta": null }
```

### 3.3 POST /api/token

Troca um token HMAC de uso único por uma sessão. Requisição (o token do exemplo é o V1 da tabela abaixo):

```json
{ "token": "eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.BvaFo9ur4_kzEeYjvM0EiRPhpy_6qmlZsU7nio54VU4" }
```

Resposta 200, com o mesmo `Set-Cookie` do login:

```json
{
  "success": true,
  "data": { "subject": "issabel:admin:fabio", "kind": "token", "admin": true, "expires_at": "2026-10-13T22:40:30.000Z" },
  "error": null,
  "meta": null
}
```

Falha (assinatura, expirado, `jti` repetido, formato, regra do `sub`): `401 invalid_token`, sempre a mesma resposta.

Formato do token: `base64url(payload) "." base64url(HMAC-SHA256(chave, base64url(payload)))`, sem padding. Payload JSON com exatamente `sub` (1 a 128 bytes), `exp` (unix segundos) e `jti` (16 a 64 caracteres base64url). Aceito só se `agora < exp <= agora + 65 s` (TTL 60 s mais 5 s de folga de relógio) e o `jti` não foi visto. A recusa por regra do `sub` não consome o `jti`.

**Duas chaves.** O verificador confere a assinatura contra a chave do Issabel (se carregada) e contra o segredo do daemon, ambos com `hmac.Equal`:

- Chave do Issabel: arquivo `auth.issabel_key_file` (padrão `/etc/painel-ramais-issabel.key`), base64 (padrão, com `=`) que decodifica em 32 bytes ou mais, espaços nas pontas descartados; a chave HMAC são os **bytes decodificados**. Permissão aceita: o daemon abre o arquivo com `O_RDONLY|O_NOFOLLOW|O_NONBLOCK`, confere no mesmo descritor (`fstat`) e lê dele (no máximo 4 KiB): arquivo regular (symlink e FIFO são recusados), dono = euid do processo, `mode.Perm() &^ 0o640 == 0` (aceita 0640, 0600, 0440, 0400; recusa escrita de grupo e qualquer bit de outros). No Windows (desenvolvimento) só existe a checagem de existência. Chave igual ao segredo do daemon (mesmos bytes) também é recusada: quem lesse o segredo emitiria tokens de administrador do Issabel. Chave ausente, recusada pela permissão ou com conteúdo inválido: o daemon sobe normalmente e só a linha do segredo do daemon vale.
- Segredo do daemon: `auth.secret_file`, como na fase 1.

| Assinado com | `sub` aceito | Papel da sessão |
|---|---|---|
| chave do Issabel | `issabel:admin:<usuario>` | administrador |
| chave do Issabel | `issabel:user:<usuario>` | operador |
| segredo do daemon (`auth.secret_file`) | qualquer texto de 1 a 128 caracteres que **não** comece com `issabel:` (sem diferenciar maiúsculas) | operador |

`<usuario>`: 1 a 64 caracteres (runas), UTF-8 válido, sem caractere de controle (Cc), de formatação (Cf: marcas bidirecionais, largura zero, soft hyphen, BOM) nem separador de linha ou de parágrafo (Zl, Zp); pode conter `:` e espaço comum. O emissor PHP segue a mesma regra; o `sub` inteiro continua com 1 a 128 bytes. O prefixo do lado da chave do Issabel é exato (minúsculas). Papel administrador = `kind == "token"` e `subject` com prefixo exato `issabel:admin:`. Nenhuma coluna nova no banco: o papel é derivado do `subject` gravado.

Ordem de verificação: formato e tamanho; HMAC contra a chave do Issabel (se carregada) e contra o segredo do daemon; JSON estrito; regra do `sub` da tabela conforme a chave que casou; `exp`; `jti`.

A chave do Issabel é entregue ao verificador pela verificação de dependências (seção 4.4, item `issabel_key`): na subida, a cada 60 s e após cada Login AMI. Criar, trocar, apagar ou estragar a permissão da chave vale em até 60 s, sem reiniciar o serviço, e o que o `/api/health` mostra é exatamente o que o verificador usa.

Vetores de teste (para o backend e para o emissor PHP; chaves de exemplo, nunca usar em produção). `exp = 1791902460` (2026-10-13T14:41:00Z); os testes usam relógio fixo antes disso. Os tokens foram calculados com `HMAC-SHA256(bytes da chave, texto base64url do payload)`.

| Item | Valor |
|---|---|
| conteúdo de `auth.secret_file` | `AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=` (bytes 0x00 a 0x1f) |
| conteúdo de `auth.issabel_key_file` | `ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=` (bytes 0x20 a 0x3f) |
| V1 payload | `{"sub":"issabel:admin:fabio","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}` |
| V1 assinado com a chave do Issabel: **aceito, administrador** | `eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.BvaFo9ur4_kzEeYjvM0EiRPhpy_6qmlZsU7nio54VU4` |
| V1 assinado com o segredo do daemon: **recusado** (prefixo reservado) | `eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.PbqEoW5r-kgT30bYkuhechQCldj_pGcp1iUCVi9haRQ` |
| V2 payload | `{"sub":"issabel:user:maria","exp":1791902460,"jti":"r8Wg2b0aU9d7l1Kq4sXvYB"}` |
| V2 assinado com a chave do Issabel: **aceito, operador** | `eyJzdWIiOiJpc3NhYmVsOnVzZXI6bWFyaWEiLCJleHAiOjE3OTE5MDI0NjAsImp0aSI6InI4V2cyYjBhVTlkN2wxS3E0c1h2WUIifQ.Q4LwgNiSLChzFaL2PT8hSU-vtRnAGdAUcacuuCe6MYc` |
| V3 payload | `{"sub":"roteiro","exp":1791902460,"jti":"s7Vf1a9ZT8c6k0Jp3rWuXC"}` |
| V3 assinado com o segredo do daemon: **aceito, operador** | `eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.CpvxeT22yxalo6wlbU5qUNnNvdADVR9DfzJ31NA2Yfc` |
| V3 assinado com a chave do Issabel: **recusado** (sem prefixo) | `eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.DqPJ2WjOE9vIsaS0PehNvhUPxX8OSDdU_6DqhxD6QXY` |
| V0 (vetor da fase 1, payload `{"sub":"issabel:admin","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}`, segredo do daemon): **agora recusado** | `eyJzdWIiOiJpc3NhYmVsOmFkbWluIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.1FevVYbrsil2HqMU283VGL948fcwdxThKkYL6xtxxq0` |

Emissor PHP (módulo do Issabel): a mesma receita. `hash_hmac('sha256', $p64, base64_decode(trim(file_get_contents($arq)), true), true)`, `jti` de 16 bytes de `random_bytes` em base64url sem `=`, TTL de 60 s, `sub` pela tabela. O teste do módulo usa V1 e V2.

### 3.4 GET /api/session

Exige sessão (renova como qualquer GET autenticado); sem sessão, `401 unauthorized`. Devolve o mesmo `data` do login e do token (`sessionData`):

```json
{ "success": true, "data": { "subject": "issabel:admin:fabio", "kind": "token", "admin": true, "expires_at": "2026-10-13T22:40:30.000Z" }, "error": null, "meta": null }
```

`admin` é `true` só se `kind == "token"` e `subject` começa com `issabel:admin:`. A tela usa este campo para mostrar a área de grupos.

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
        "groups": ["Plantão", "Suporte"],
        "range_groups": ["Suporte"],
        "manual_groups": ["Plantão"],
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
        "groups": ["Suporte"],
        "range_groups": ["Suporte"],
        "manual_groups": [],
        "registration": "unreachable",
        "ip": "192.0.2.24",
        "user_agent": null,
        "latency_ms": null,
        "device_state": "unavailable",
        "calls": []
      }
    ],
    "groups": [
      { "name": "Plantão", "ranges": [] },
      { "name": "Suporte", "ranges": [ { "start": 2000, "end": 2099 } ] },
      { "name": "Vendas", "ranges": [] }
    ],
    "calls": [
      { "id": "1791901712.4520", "direction": "inbound", "state": "talking", "extensions": ["2001"], "external": "11987654321", "trunk": "PJSIP/operadora_x", "queue": null, "started_at": "2026-10-13T14:28:30.900Z" }
    ],
    "queues": [
      { "queue": "600", "name": null, "completed": 0, "abandoned": 0, "service_level_s": 60, "service_level_perf": 0, "stats_at": "2026-10-13T14:29:41.000Z", "entries": [], "members": [] }
    ]
  },
  "error": null,
  "meta": { "count": 2 }
}
```

- `epoch`: string opaca. O cliente só compara igualdade. Formato atual `<unix ms do início do processo>-<contador>`, único entre reinícios do daemon.
- `version`: inteiro monotônico durante a vida do processo.
- `technology`: `null` só antes da primeira detecção.
- `extensions` ordenado por `number` (ordem de string). Antes da primeira conexão AMI vem vazio. Servidor **sem ramais** (nenhum endpoint/peer de ramal configurado) também vem `[]`, mas com `ami_connected: true`, `technology` preenchido e `meta.count: 0`: é um estado válido, não erro nem `degraded` (seção 7.4); o primeiro ramal criado chega por delta.
- `server_time` serve para a tela calcular a diferença de relógio e mostrar a duração certa.
- `groups`: o catálogo **completo**, ordenado por nome sem diferenciar maiúsculas, incluindo grupos sem ramal e sem faixa (`"ranges": []`). Não inclui `"Sem grupo"` (é implícito). Aditivo: cliente antigo ignora. O delta (seção 5.2) traz o catálogo inteiro de novo, só quando ele muda.
- `calls` (rc.8, seção 2.3): todas as chamadas ao vivo, ordenadas por `started_at` e depois `id`; `queues` (rc.8, seção 2.4): todas as filas, ordenadas por `queue` (ordem de string). As duas chaves estão **sempre** presentes no snapshot (`[]` quando não há, inclusive com o AMI fora). Sem teto de itens. `meta.count` continua sendo a quantidade de ramais.

### 4.2 GET /api/groups

Grupos do catálogo (inclui grupo sem ramal e sem faixa) e os ramais conhecidos agora, na mesma visão do snapshot (lida da projeção do estado, montada a partir do catálogo, não do banco). `"Sem grupo"` vem por último quando tiver ramal. `extensions` de cada grupo traz os ramais cujo `groups` contém o grupo (faixa ou vínculo manual; rc.7: o mesmo ramal aparece em cada grupo dele, e só ramal sem nenhum vínculo aparece em "Sem grupo"); `ranges` traz só as faixas do grupo.

```json
{
  "success": true,
  "data": [
    { "name": "Suporte", "ranges": [ { "start": 2000, "end": 2099 } ], "extensions": ["2001", "2002", "2150"] },
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
- rc.7: as contagens de cada grupo incluem todo ramal que está no grupo (N:N); a do total conta cada ramal uma vez. As colunas novas de `history` (seção 10, versão 4) **não** saem nesta rota; servem a `/api/registrations/series` (seção 4.7). `group` volta `null` quando ausente na query (o campo `group_manual` dos exemplos antigos nunca existiu no código).

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
    "registrations": { "queue_len": 0, "queue_cap": 16384, "dropped": 0, "write_errors": 0, "lost_events": 0, "lost_since": null, "lost_until": null, "suppressed_until": null },
    "live_calls": 298,
    "queues": { "count": 6, "last_full_at": "2026-10-13T14:25:00.000Z", "refresh_errors": 0, "unsupported": false },
    "deps": [
      { "name": "ami_user", "status": "ok", "detail": "login aceito em 2026-10-13T08:00:01Z", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "manager_custom_include", "status": "ok", "detail": "manager.conf inclui manager_custom.conf e o bloco painel-ramais está presente", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "apache_proxy_conf", "status": "ok", "detail": "/etc/httpd/conf.d/painel-ramais.conf presente", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "issabel_key", "status": "ok", "detail": "chave do Issabel válida: integração ligada", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "manager_custom_perm", "status": "ok", "detail": "manager_custom.conf sem acesso para outros (modo 0640)", "checked_at": "2026-10-13T14:30:00.000Z" },
      { "name": "issabel_menu", "status": "ok", "detail": "item painel_ramais presente no menu do Issabel", "checked_at": "2026-10-13T14:30:00.000Z" }
    ]
  },
  "error": null,
  "meta": null
}
```

- `deps[].status`: `ok`, `fail` ou `unknown` (por exemplo, sem permissão de leitura em `/etc/asterisk/manager.conf`). `unknown` também deixa o status em `degraded` e o `detail` diz o que fazer. Exceção deliberada: o `%post` tira o acesso de "outros" de `manager_custom.conf` (o-rwx, dono asterisk) e o serviço não entra no grupo asterisk; então `EACCES` ao ler `manager.conf` ou `manager_custom.conf` é esperado e o item `manager_custom_include` passa a `ok` se o Login AMI foi aceito (`detail`: "não legível pelo serviço (esperado, o-rwx); login AMI aceito confirma o usuário"), `fail` se o Login foi recusado e `unknown` se ainda não houve Login. Arquivo ausente continua `fail`.
- Itens da fase 2 (o `status` geral segue a regra: qualquer item diferente de `ok` deixa `degraded`):

| `name` | `ok` | `fail` | `unknown` |
|---|---|---|---|
| `issabel_key` | arquivo ausente (detalhe: "chave do Issabel ausente: integração com o menu desligada") ou presente e válido ("chave do Issabel válida: integração ligada") | permissão, dono, tipo ou conteúdo inválido, ou chave igual ao `auth.secret_file`; detalhe com o problema e a correção (`chown painel-ramais:<grupo do PHP> <arquivo>; chmod 0640 <arquivo>`) | erro de leitura que não seja "não existe" nem regra violada |
| `issabel_menu` | linha `id = 'painel_ramais'` presente em `/var/www/db/menu.db` | linha ausente; detalhe "item do menu do Issabel ausente; rode ./instalar.py menu" | erro de abertura ou leitura (arquivo ausente, sem permissão, banco travado); detalhe com o erro. Nunca `ok` |
| `manager_custom_perm` | modo sem nenhum bit de outros (detalhe com o modo) | algum bit de outros; detalhe "manager_custom.conf acessível a outros (modo NNNN): contém a senha AMI. Rode: chmod o-rwx /etc/asterisk/manager_custom.conf" | `stat` falhou (inclui ausente e `EACCES` no diretório); detalhe com o erro. Nunca `ok` |

  `issabel_menu`: o daemon abre o `menu.db` do Issabel só para leitura (`file:/var/www/db/menu.db?mode=ro`, `busy_timeout` de 1 s; nunca cria nem grava) e consulta apenas `SELECT 1 FROM menu WHERE id = 'painel_ramais'`; não lê o `acl.db` (as permissões do item são conferidas pelo `instalar.py menu`). O caminho padrão é a constante `depcheck.DefaultMenuDB`; reavaliado a cada 60 s como os demais itens.

  `issabel_key` é a fonte única da chave do verificador de tokens (seção 3.3): a cada verificação (na subida, a cada 60 s e após cada Login AMI) o resultado é entregue ao verificador (chave válida ou nenhuma). O daemon só lê o modo do `manager_custom.conf` (`stat`), nunca o altera. Mudança de estado de qualquer item sai no log.
- `ami_user` vem do resultado do último Login da conexão única (não abre segunda conexão).
- `events_per_s`: média dos últimos 60 s. `lag_ms`: agora menos a hora de leitura do último evento processado. `queue.overflows`: quantas vezes a fila encheu e forçou ressincronização (seção 11.3); `queue.len` e `queue.cap` são da fila do leitor AMI (a que pode estourar).
- `registrations` (rc.7, ADR-007): fila do gravador do relatório de registro (seção 11.5). `queue_len` e `queue_cap` (16384, constante); `dropped`: ocorrências descartadas porque a fila estava cheia, desde a subida do daemon; `write_errors`: lotes que falharam ao gravar (o lote é perdido e o erro sai no log Warn); `suppressed_until`: fim da janela de evento do sistema em curso (RFC 3339) ou `null`. Nenhum destes campos muda o `status` (não é `degraded`). rc.8 (seção 10, "Gravação de um lote"): `write_errors` passa a contar **sublotes perdidos** (falharam na tentativa e na repetição); `lost_events` = notas perdidas nesses sublotes (ocorrências, fechamentos de intervalo e sinais), desde a subida; `lost_since` e `lost_until` = menor e maior `At` entre as notas perdidas desde a subida (RFC 3339) ou `null` quando nada se perdeu. `dropped` (fila cheia) continua separado.
- `live_calls` (rc.8): quantidade de linhas da projeção de chamadas (seção 2.3). Pode diferir de `active_calls`, que conta `Linkedid` com canal de ramal ou tronco da tecnologia ativa (não conta chamada só de DAHDI/IAX2).
- `queues` (rc.8): `count` = filas na projeção (seção 2.4); `last_full_at` = última `QueueStatus` completa lida com sucesso (RFC 3339) ou `null`; `refresh_errors` = `QueueStatus` (completa ou por fila) que falharam desde a subida; `unsupported` = `true` quando a última `QueueStatus` completa respondeu que a action não existe (módulo de filas não carregado: lista vazia, sem erro). Nenhum destes campos muda o `status`.

### 4.5 GET /debug (só localhost, exige sessão)

HTML puro gerado no servidor, com `<meta http-equiv="refresh" content="2">`, sem JavaScript e sem envelope. Mostra: contagens por estado, chamadas ativas (ramal, direção, estado, outro lado, duração) e os últimos 20 lotes de delta do ring buffer. Não é contrato para a fase 2.

### 4.6 POST /api/groups/create, update, assign, import e delete (só administrador)

Sessão sem papel de administrador (seção 1, "Papel"): `403 forbidden`, antes de ler o corpo. Todas são `POST` com `Content-Type: application/json`, corpo estrito (campo desconhecido: `400 bad_request`) de até 1 MiB (`413 payload_too_large` acima disso; `assign` aceita até 2 MiB, ver abaixo); o resto da API segue com 4 KiB. Teto de 20.000 linhas no CSV pela tela (a quebra de linha final não conta como linha; 20.001 ou mais dão `400 invalid_csv`; a CLI não tem esse teto). Cada escrita é **uma transação só** no SQLite, junto com a linha de auditoria (`group_audit`: `subject` da sessão, ação, detalhe JSON curto, `at`); falhou, nada foi gravado. Depois de gravar, o daemon relê o catálogo e as atribuições pelo mesmo caminho do SIGHUP (`loadGroups`) e o estado publica a diferença **por delta, sem epoch novo** (seções 5.2 e ADR-005): as telas abertas recebem os ramais afetados em `extensions` (com `groups`, `range_groups` e `manual_groups` novos) e o catálogo em `groups`. Toda escrita confere, antes de gravar, se o usuário ainda está no grupo `administrator` do `acl.db` (M1, seção 1.2). Se a recarga falhar, a resposta é `500 internal` (os dados já estão gravados; repetir é seguro). Cada escrita gera log Info com `request_id`, `sub` (quem) e o resumo (`grupo criado`, `grupo alterado`, `ramais atribuidos a grupo`, `grupos importados`, `grupo removido`).

`POST /api/groups/import`:

```json
{ "csv": "grupo,ramal\nSuporte,2000-2099\nVendas,3000-3999\n", "replace": true }
```

`csv` obrigatório (texto do arquivo, mesmo formato da seção 9); `replace` opcional, padrão `false`; campo desconhecido: `400 bad_request`. Sucesso: `200` com `{"success":true,"data":{"rows":2,"replace":true,"ignored":0},"error":null,"meta":null}` e log Info `grupos importados` com `sub`, `rows`, `ignored` e `replace`. `rows` = linhas de dados lidas; `ignored` (rc.7) = linhas que não mudaram nada (repetidas no arquivo, faixa ou vínculo que já existia, `Sem grupo,N`). Sem `replace` a importação **só acrescenta** (seção 9). CSV inválido ou com faixas que se cruzam no mesmo grupo: `400 invalid_csv`, `message` = o texto do erro do parser (até 20 linhas `linha N: ...`); nada é gravado.

`POST /api/groups/delete`:

```json
{ "name": "Suporte" }
```

`name` de 1 a 64 caracteres (senão `400 bad_request`). Sucesso: `200` com `{"success":true,"data":{"name":"Suporte"},"error":null,"meta":null}` e log Info `grupo removido`. Inexistente: `404 not_found`, "Grupo inexistente". Apagar o grupo apaga as faixas e os vínculos manuais dele (cascade): cada ramal sai só deste grupo e continua nos outros; quem fica sem nenhum vai para "Sem grupo".

`POST /api/groups/create`:

```json
{ "name": "Suporte", "ranges": [ { "start": 2000, "end": 2099 }, { "start": 2150, "end": 2150 } ] }
```

`name` obrigatório; `ranges` opcional (grupo sem faixa é válido; até 1000 faixas, cada uma com `start` e `end`, `0 <= start <= end`, até 18 dígitos). O nome tem as pontas aparadas e segue a regra única de nome de grupo (seção 9: 1 a 64 caracteres, sem controle/formatação invisível, diferente de `Sem grupo` e, desde a rc.8, de `Todos os ramais`, sem diferenciar maiúsculas). Sucesso: `200` com `{"success":true,"data":{"name":"Suporte"},"error":null,"meta":null}`. Erros: `400 bad_request` (nome inválido ou reservado, faixa inválida, faixas da própria requisição que se cruzam, faixa sem `start` ou `end`); `409 conflict` (nome já existe, sem diferenciar maiúsculas). rc.7: faixa que cruza faixa de outro grupo é aceita; o `checkRangeConflicts` sai do código.

`POST /api/groups/update`:

```json
{ "name": "Suporte", "new_name": "Atendimento", "ranges": [ { "start": 2000, "end": 2199 } ] }
```

`name` é o grupo atual (sem diferenciar maiúsculas); `new_name` e `ranges` são opcionais, mas pelo menos um é obrigatório (senão `400 bad_request`, "informe new_name ou ranges"). `ranges` **substitui todas** as faixas do grupo (`[]` deixa o grupo sem faixa): na mesma transação apaga as faixas do grupo e insere as novas (o trigger `group_ranges_no_overlap_same_group` só compara faixas do mesmo grupo, já apagadas; o trigger de imutabilidade proíbe `UPDATE`, por isso apagar e inserir). Renomear leva o histórico junto na mesma transação (`history.group_name`); se o nome novo já tinha histórico no mesmo minuto (grupo apagado antes com esse nome), os minutos com dado dos dois lados são somados e nenhum minuto é perdido. Mesmo nome (ou mesmas faixas) é aceito e não grava nada. Sucesso: `200` com `{"data":{"name":"<nome final>"}}`. Erros: `400 bad_request` (mesmas regras do `create`), `404 not_found` (grupo inexistente), `409 conflict` (nome novo já existe em outro grupo). Auditoria: `rename` e/ou `ranges`.

`POST /api/groups/assign`:

rc.7 (ADR-006): vínculo manual em massa com **modo**, **atômico** (tudo ou nada, uma transação). O formato antigo (`{"numbers", "group"}` sem `mode`, inclusive `group: null` e `"Sem grupo"`) deixa de existir e responde `400 bad_request`.

Adicionar (1 ou mais grupos; só acrescenta):

```json
{ "mode": "add", "numbers": ["2001", "2002", "2150"], "groups": ["Suporte", "Plantão"] }
```

Resposta `200`: `{"success":true,"data":{"changed":5},"error":null,"meta":null}`. `changed` = pares (ramal, grupo) gravados agora; par que já existia não conta e não é regravado. Ramal que já está no grupo pela faixa recebe o vínculo manual mesmo assim (redundante, inofensivo; passa a valer se a faixa mudar).

Remover (de um grupo; só vínculo manual):

```json
{ "mode": "remove", "numbers": ["2001", "2002"], "group": "Suporte" }
```

Resposta `200`: `{"success":true,"data":{"changed":1,"not_manual":1},"error":null,"meta":null}`. `changed` = vínculos manuais apagados; `not_manual` = números do pedido sem vínculo manual com o grupo (estão pela faixa ou nem estão no grupo): nada é criado para eles, não existe exclusão de faixa.

Regras:
- `mode` obrigatório, `add` ou `remove`. No `add`, `groups` obrigatório e `group` proibido; no `remove`, `group` obrigatório e `groups` proibido. Combinação errada, campo ausente ou desconhecido: `400 bad_request`.
- `groups`: de 1 a 20 nomes, cada um de 1 a 64 caracteres depois do trim, sem repetição (sem diferenciar maiúsculas). `group` e cada item de `groups`: nome de grupo existente (sem diferenciar maiúsculas); `"Sem grupo"` dá `400 bad_request` ("Sem grupo não é grupo: é onde fica o ramal sem nenhum vínculo"); grupo inexistente dá `404 not_found` e nada é aplicado.
- `numbers`: de 1 a 20.000 textos, cada um de 1 a 64 caracteres sem controle/formatação invisível (Cc, Cf, Zl, Zp), sem repetição (repetido: `400 bad_request`). `len(numbers) x len(groups)` até 100.000 (acima: `400 bad_request`). O vínculo é pelo **número do ramal como texto**, não exige que o ramal exista agora e sobrevive a ramal apagado e recriado.
- Corpo até 2 MiB (`maxAssignBodyBytes`): 20.000 números de 64 caracteres ocupam cerca de 1,34 MB; 10.000 ramais de 4 a 6 dígitos, cerca de 90 KB.
- `changed` igual a 0: sem escrita, sem auditoria e sem recarga. Auditoria: ação `assign`, detalhe `{"mode","groups","requested","changed","numbers"}` (até 20 números de amostra).
- Log Info `ramais atribuidos a grupo` (`add`) ou `ramais retirados de grupo` (`remove`), com `sub`, grupos, pedidos e alterados.

Leitura: `GET /api/groups` (seção 4.2). Ele lê a projeção do estado, não o banco: logo depois de uma escrita pode ficar defasado por até ~250 ms (um lote), até o estado publicar o delta; a tela usa o `groups` e os ramais do delta do WebSocket para atualizar sem reler.

### 4.7 Relatório de registro (rc.7, ADR-007)

Quatro rotas `GET`, só leitura, com sessão obrigatória de **qualquer papel** (administrador, operador do Issabel e emergência: a mesma permissão da tela), gzip e envelope. Parâmetro de query desconhecido é ignorado. Parâmetro inválido: `400 bad_request` com a mensagem em português. Nenhuma rota nova de escrita.

**Período.** `period` vale para `/api/registrations`, `/extension` e `/series` (o `summary` é sempre `24h`):

| `period` | `from` | Pontos da série (`step`) |
|---|---|---|
| `24h` (padrão) | início da hora atual menos 23 h | 24, `hour` |
| `7d` | meia-noite de hoje, no fuso do servidor (`time.Local`), menos 6 dias | 7, `day` |
| `30d` | meia-noite de hoje menos 29 dias | 30, `day` |

`to` = agora. Outro valor: `400 bad_request` ("period deve ser 24h, 7d ou 30d"). Todo `data` traz `period`, `from`, `to` e `since` (RFC 3339 UTC com ms). `since` é `reg_meta.since` (seção 10): quando a versão com o relatório foi instalada. A tela mostra "Dados desde <since>" e, se `since > from`, deixa claro que o período está incompleto.

**Linha de ramal** (`row`): o mesmo objeto em `/api/registrations` e em `/api/registrations/extension`.

```json
{
  "number": "2001",
  "name": "Maria Souza",
  "groups": ["Plantão", "Suporte"],
  "removed": false,
  "lost_registration": 5,
  "became_unreachable": 3,
  "registered": 5,
  "reachable_again": 3,
  "system_events": 2,
  "last_drop_at": "2026-10-19T13:42:10.000Z",
  "last_ip": "192.0.2.23",
  "last_port": 5060,
  "down_s": 7260,
  "down_open_since": null
}
```

| Campo | Tipo | Regra |
|---|---|---|
| `number` | string | número do ramal |
| `name` | string ou null | nome atual do ramal no estado; se o ramal não existe mais, o último nome gravado em `reg_hourly.name`; `null` se nenhum |
| `groups` | array de string | grupos **atuais** do ramal (mesma regra do `groups` da seção 2.1); `[]` quando `removed` |
| `removed` | booleano | `true` quando o número não está mais no estado (apagado no Issabel); a linha fica até expirar (30 dias) |
| `lost_registration`, `became_unreachable`, `registered`, `reachable_again` | inteiro | soma no período, só ocorrências fora da janela de sistema (`system = 0`) |
| `system_events` | inteiro | transições do ramal dentro da janela de sistema, no período (informativo, fora do ranking) |
| `last_drop_at` | data ou null | última `lost_registration` ou `became_unreachable` (não sistema) no período |
| `last_ip`, `last_port` | string ou null, inteiro ou null | IPv4 e porta da ocorrência mais recente do período que tinha endereço; endereço que não é IPv4 nunca é gravado |
| `down_s` | inteiro | segundos fora do ar **já fechados** dentro do período (o fechamento distribui os segundos pelas horas do intervalo, então o valor nunca passa do tamanho do período) |
| `down_open_since` | data ou null | início do intervalo fora do ar ainda aberto (ramal caído agora); `null` se não está caído, se o intervalo não foi aberto por queda contada, ou se `removed` |

Tempo total fora do ar mostrado na tela = `down_s + (down_open_since ? agora - max(down_open_since, from) : 0)`, recalculado a cada atualização. Linha sai na lista só se tiver algum dos quatro contadores maior que zero, `down_s > 0` ou `down_open_since` diferente de `null` (ramal só com `system_events` não aparece).

Rótulos na tela: `lost_registration` "Perdeu registro"; `registered` "Registrou"; `became_unreachable` "Ficou inalcançável"; `reachable_again` "Voltou a ser alcançável"; sinais `shutdown` "Asterisk desligando", `fully_booted` "Asterisk iniciado", `ami_connected` "Painel reconectado ao Asterisk".

#### 4.7.1 GET /api/registrations/summary

Bloco "Instabilidade de registro (24 h)" da tela principal. Query: `group` opcional (até 64 caracteres; ausente = todos os ramais; `"Sem grupo"` aceito; grupo inexistente devolve `items: []`). Pertença **atual** ao grupo.

```json
{
  "success": true,
  "data": {
    "period": "24h",
    "from": "2026-10-18T14:00:00.000Z",
    "to": "2026-10-19T13:47:05.120Z",
    "since": "2026-10-19T09:12:44.000Z",
    "group": "Suporte",
    "items": [
      { "number": "2001", "name": "Maria Souza", "removed": false, "lost_registration": 5, "became_unreachable": 3 },
      { "number": "2044", "name": null, "removed": false, "lost_registration": 0, "became_unreachable": 2 }
    ]
  },
  "error": null,
  "meta": { "count": 2 }
}
```

`items`: até 10, só com `lost_registration + became_unreachable > 0`, ordenados por essa soma (maior primeiro), depois `lost_registration` (maior primeiro), depois `number`. `group` volta `null` quando ausente.

#### 4.7.2 GET /api/registrations

Tabela da aba Registros. Query: `period`; `cursor` opcional (texto opaco devolvido pela chamada anterior, até 64 caracteres). **Sem** filtro de grupo nem busca no servidor: a tela filtra e ordena (grupo: linha cujo `groups` contém o grupo; "Todos os ramais": todas, inclusive `removed`; "Sem grupo": `groups` igual a `["Sem grupo"]`; busca: número ou nome contendo o texto, sem diferenciar maiúsculas e acentos, a mesma regra da busca da tela de ramais).

```json
{
  "success": true,
  "data": {
    "period": "24h",
    "from": "2026-10-18T14:00:00.000Z",
    "to": "2026-10-19T13:47:05.120Z",
    "since": "2026-10-19T09:12:44.000Z",
    "cursor": "1792414800.1532",
    "full": true,
    "rows": [
      { "number": "2001", "name": "Maria Souza", "groups": ["Plantão", "Suporte"], "removed": false, "lost_registration": 5, "became_unreachable": 3, "registered": 5, "reachable_again": 3, "system_events": 2, "last_drop_at": "2026-10-19T13:42:10.000Z", "last_ip": "192.0.2.23", "last_port": 5060, "down_s": 7260, "down_open_since": null }
    ]
  },
  "error": null,
  "meta": { "count": 1 }
}
```

- `full: true`: `rows` é a tabela inteira do período, ordenada por `number`; a tela **substitui** a dela. Acontece sem `cursor`, com cursor inválido, quando a hora atual mudou desde o cursor (a janela andou) ou quando a revisão do cursor é maior que a maior revisão gravada (banco limpo ou restaurado).
- `full: false`: `rows` traz só os ramais com alguma linha de `reg_hourly` gravada depois do cursor (coluna `rev`, seção 10), já com os valores do período inteiro; a tela **substitui ou insere** pelo `number` e não remove nada. Sem mudança: `rows: []`.
- `cursor`: sempre presente; a tela manda de volta exatamente o que recebeu. Formato interno (não depender dele): `<início da hora em unix>.<rev>`.
- **Atualização automática**: com a aba Registros visível e `document.visibilityState == "visible"`, uma chamada a cada 5 s com o último `cursor` (nunca duas ao mesmo tempo; a próxima só começa 5 s depois que a anterior terminou). Trocar o período descarta o cursor. Nunca por evento individual do WebSocket.

#### 4.7.3 GET /api/registrations/extension

Linha do tempo de um ramal. Query: `number` obrigatório (regra do `numbers` do assign: 1 a 64 caracteres, sem controle/formatação; senão `400 bad_request`); `period`.

```json
{
  "success": true,
  "data": {
    "period": "7d",
    "from": "2026-10-13T03:00:00.000Z",
    "to": "2026-10-19T13:47:05.120Z",
    "since": "2026-10-19T09:12:44.000Z",
    "extension": { "number": "2001", "name": "Maria Souza", "groups": ["Plantão", "Suporte"], "removed": false, "lost_registration": 1, "became_unreachable": 0, "registered": 1, "reachable_again": 0, "system_events": 1, "last_drop_at": "2026-10-19T13:42:10.000Z", "last_ip": "192.0.2.23", "last_port": 5060, "down_s": 95, "down_open_since": null },
    "events": [
      { "at": "2026-10-19T13:43:45.000Z", "kind": "registered", "system": false, "ip": "192.0.2.23", "port": 5060, "user_agent": "Yealink SIP-T46U 108.86.0.20" },
      { "at": "2026-10-19T13:42:10.000Z", "kind": "lost_registration", "system": false, "ip": "192.0.2.23", "port": 5060, "user_agent": null },
      { "at": "2026-10-19T09:13:02.000Z", "kind": "registered", "system": true, "ip": "192.0.2.23", "port": 5060, "user_agent": "Yealink SIP-T46U 108.86.0.20" }
    ],
    "events_limit": 200
  },
  "error": null,
  "meta": { "count": 3 }
}
```

- `extension`: sempre presente (contadores zerados, `groups` atuais, `removed` conforme o estado, quando não há nada no período); número nunca visto também dá `200`.
- `events`: as ocorrências brutas guardadas com `at >= from`, mais nova primeiro, inclusive as de sistema (`system: true`); no máximo `events_limit` (200 por ramal, seção 10). Com 200 itens a tela avisa "mostrando as 200 ocorrências mais recentes guardadas".
- A tela relê esta rota ao abrir o ramal, ao trocar o período e quando a linha do ramal chega num `full: false` da 4.7.2.

#### 4.7.4 GET /api/registrations/series

Gráfico da aba Registros. Query: `period`; `group` opcional (até 64 caracteres; ausente = total da instalação; `"Sem grupo"` aceito). Fonte: colunas `lost_registration` e `became_unreachable` de `history` (seção 10), com a pertença ao grupo **no minuto** em que aconteceu.

```json
{
  "success": true,
  "data": {
    "period": "24h",
    "from": "2026-10-18T14:00:00.000Z",
    "to": "2026-10-19T13:47:05.120Z",
    "since": "2026-10-19T09:12:44.000Z",
    "group": null,
    "step": "hour",
    "points": [
      { "t": "2026-10-18T14:00:00.000Z", "lost_registration": null, "became_unreachable": null },
      { "t": "2026-10-19T13:00:00.000Z", "lost_registration": 4, "became_unreachable": 1 }
    ],
    "system_events": [
      { "at": "2026-10-19T09:12:58.000Z", "kind": "ami_connected" }
    ]
  },
  "error": null,
  "meta": { "count": 24 }
}
```

- `points`: série densa (24, 7 ou 30 pontos, o exemplo mostra dois), `t` = início do intervalo. Valor = soma dos minutos do intervalo com coluna não nula; `null` quando nenhum minuto do intervalo tem dado (antes do `since`, daemon parado, AMI fora).
- `system_events`: sinais de `reg_system_events` no período, mais novo primeiro, até 100. A tela mostra a contagem e o último abaixo do gráfico ("Eventos do sistema no período: N; último: Asterisk iniciado em ...").
- A tela relê ao abrir a aba, ao trocar período ou grupo e a cada 60 s com a aba visível.

#### 4.7.5 Exportação CSV (montada no navegador)

Decisão do ADR-007: o servidor não gera CSV. O botão "Exportar CSV" da aba Registros gera o arquivo da tabela **filtrada e ordenada** que está na tela.

- Nome: `registros-<period>-<AAAAMMDD-HHMM>.csv` (hora local do navegador).
- UTF-8 com BOM, separador `;`, fim de linha CRLF. Célula com `;`, `"`, CR ou LF vai entre aspas, com `"` dobrada.
- **Injeção de fórmula**: toda célula de texto que começa com `=`, `+`, `-`, `@`, TAB ou CR recebe `'` na frente (antes das aspas).
- Cabeçalho e colunas, nesta ordem: `Ramal;Nome;Grupos;Perdeu registro;Ficou inalcançável;Registrou;Voltou a ser alcançável;Última queda;Último IP;Tempo fora do ar (s);Removido`. `Grupos` separados por `, `; `Última queda` em hora local `AAAA-MM-DD HH:MM:SS` ou vazio; `Último IP` como `ip:porta` (ou só `ip`, ou vazio); `Tempo fora do ar (s)` inteiro, calculado como na tela; `Removido` `sim` ou `não`.
- Download por `Blob` + `URL.createObjectURL` + `<a download>` criado por script (sem `innerHTML`; nada muda na CSP).

### 4.8 POST /api/queues/rename (rc.8, ADR-009; só administrador)

Dá ou tira o nome de uma fila. Mesmas regras de transporte das rotas de grupo: sessão de administrador (senão `403 forbidden` antes de ler o corpo), `Content-Type: application/json`, corpo estrito de até **4 KiB** (`413` acima), campo desconhecido `400 bad_request`, `Sec-Fetch-Site` da seção 1. Antes de gravar, confere o usuário no grupo `administrator` do `acl.db` (M1; falha fechada, `403`).

```json
{ "queue": "600", "name": "Suporte N1" }
```

| Campo | Regra |
|---|---|
| `queue` | obrigatório; 1 a 64 caracteres sem controle/formatação invisível (a regra de `store.ValidNumber`); precisa existir na projeção de filas do hub agora, senão `404 not_found` "Fila inexistente" |
| `name` | obrigatório (pode ser `""`); pontas aparadas; `""` **apaga** o nome (a tela volta a "Fila 600"); senão 1 a 64 caracteres com as regras de caractere do nome de grupo (seção 9: sem Cc, Cf, Zl, Zp; só o espaço U+0020), sem nome reservado. Fora da regra: `400 bad_request` com a mensagem |

Resposta `200`: `{"success":true,"data":{"queue":"600","name":"Suporte N1"},"error":null,"meta":null}` (`"name": null` depois de apagar). Mesmo nome que já está gravado (ou apagar o que não existe): `200` sem escrita. Escrita: upsert (ou `DELETE`) em `queue_names` numa transação, com `updated_at` e `updated_by` = `subject` da sessão; log Info `nome de fila alterado` com `request_id`, `sub`, `queue` e `name` (ou `removido`). Sem linha em `group_audit` (ADR-009). Depois de gravar, o daemon relê `queue_names` e entrega `QueueNamesLoaded` ao estado: a fila vai no próximo delta com o `name` novo, sem epoch novo. Releitura falhou: `500 internal` (o nome já está gravado; repetir é seguro). Erros possíveis: `400`, `403`, `404`, `405`, `413`, `415`, `500`.

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

Um lote a cada 250 ms, só quando houve mudança. Ramal que mudou várias vezes na janela aparece uma vez, com o estado final completo (substituição inteira por `number`, não patch de campo). `removed` é a lista (ordenada, sempre presente, `[]` quando vazia) dos números que **saíram** do estado neste lote; nunca repete um número de `extensions`. O exemplo acima traz as duas coisas (e `groups`) só para ilustrar o formato.

```json
{
  "type": "delta",
  "epoch": "1791900000123-3",
  "from_version": 48213,
  "to_version": 48219,
  "ami_connected": true,
  "server_time": "2026-10-13T14:30:00.373Z",
  "removed": ["2999"],
  "groups": [
    { "name": "Suporte", "ranges": [ { "start": 2000, "end": 2099 } ] },
    { "name": "Vendas", "ranges": [] }
  ],
  "extensions": [
    {
      "number": "2002",
      "name": "João Lima",
      "technology": "pjsip",
      "groups": ["Suporte"],
      "range_groups": ["Suporte"],
      "manual_groups": [],
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
- `from_version <= v < to_version`: aplica (substitui cada ramal pelo `number`; ramal novo é inserido; cada número de `removed` é apagado da lista e da tela, e remover número que o cliente não tem é ignorado) e faz `v = to_version`. A sobreposição é segura porque cada item é o estado completo e remover duas vezes é inofensivo. `extensions` e `removed` não têm número em comum, então a ordem entre os dois não importa.
- `to_version <= v`: ignora (repetido).
- `from_version > v`: lacuna; o cliente busca o snapshot e reconecta.
- `epoch` diferente do local: o mesmo que lacuna.

Ramal criado ou apagado no Issabel **não** muda o epoch: o novo chega em `extensions` (não registrado, com o grupo calculado pelas faixas) e o apagado em `removed` (seção 7.4). Apagado com chamada ativa continua em `extensions` até o `Hangup` da última chamada e só então vai para `removed`. O epoch novo continua só para queda do AMI e ressincronização.

**Chamadas e filas (rc.8, ADR-008).** O mesmo delta pode trazer quatro chaves **opcionais**, presentes só quando têm item (ausente = nada mudou daquele tipo; cliente antigo ignora):

| Chave | Conteúdo | Aplicação no cliente |
|---|---|---|
| `calls` | array de `LiveCall` (seção 2.3), cada uma com o estado completo | substitui (ou insere) pelo `id` |
| `calls_removed` | array de `id` (ordenado) das chamadas que acabaram ou foram absorvidas por outra | apaga pelo `id`; `id` desconhecido é ignorado |
| `queues` | array de `Queue` (seção 2.4), cada uma completa (inclusive `entries` e `members`) | substitui (ou insere) pelo `queue` |
| `queues_removed` | array de `queue` (ordenado) que saíram | apaga pelo `queue`; desconhecido é ignorado |

`calls` e `calls_removed` nunca têm `id` em comum; `queues` e `queues_removed` nunca têm `queue` em comum. Cada item das quatro listas conta uma mudança na versão (como os ramais), então lote só com chamada ou fila também avança a versão e entra no replay. Chamada que mudou várias vezes na janela de 250 ms aparece uma vez, com o estado final. O `reload` (epoch novo) vale para tudo: o cliente relê o snapshot, que traz `calls` e `queues` completos. Exemplo de lote só com chamada:

```json
{ "type": "delta", "epoch": "1791900000123-3", "from_version": 48219, "to_version": 48221, "ami_connected": true, "server_time": "2026-10-13T14:30:00.623Z", "extensions": [], "removed": [], "calls": [ { "id": "1791902000.4600", "direction": "inbound", "state": "queued", "extensions": [], "external": "11987654321", "trunk": "PJSIP/operadora_x", "queue": "600", "started_at": "2026-10-13T14:29:10.000Z" } ], "calls_removed": ["1791901990.4590"] }
```

**Mudança de grupo não muda o epoch** (ADR-005). Criar, renomear, editar faixas, apagar grupo, atribuir ou retirar ramais (tela, CLI/SIGHUP ou importação de CSV) chega assim: cada ramal cujo `groups`, `range_groups` ou `manual_groups` mudou vem em `extensions` com o estado completo (versão avança, como em qualquer delta), e, **só quando o catálogo mudou**, o lote traz `"groups"`: o catálogo **completo** (`[{"name","ranges":[{"start","end"}]}]`, inclui grupo sem ramal e sem faixa, sem `Sem grupo`), que o cliente usa para **substituir** o catálogo local inteiro. Sem a chave `groups` o catálogo não mudou. Pode haver lote com `groups` e `extensions: []` (grupo novo vazio, ou faixa sem ramal): a versão avança mesmo assim, então quem reconecta recebe o lote no replay. Atribuir 10.000 ramais de uma vez produz um lote com 10.000 ramais (cerca de 3 MB de JSON, uma vez, sem epoch novo).

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
  inventory_refresh: 10m                              # releitura completa dos ramais do PBX

auth:
  secret_file: /etc/painel-ramais/secret
  issabel_key_file: /etc/painel-ramais-issabel.key   # opcional; o padrão basta
  session_ttl: 12h

history:
  retention_days: 30
```

| Chave | Tipo | Padrão | Validação |
|---|---|---|---|
| `listen` | host:porta | `127.0.0.1:8790` | host obrigatoriamente loopback (`127.0.0.0/8` ou `::1`); senão recusa iniciar. 8790 evita a 8088 do HTTP do Asterisk |
| `technology` | enum | `auto` | `auto`, `sip`, `pjsip`. No `auto` com os dois módulos carregados (o padrão do Issabel 5) o daemon conta os ramais das duas tecnologias, usa `sip` só se `sip > pjsip` (empate = `pjsip`) e registra em Info (não é anomalia): `chan_pjsip.so e chan_sip.so carregados (padrão do Issabel 5); modo auto usa pjsip (ramais: pjsip=812, sip=0)` |
| `extension_pattern` | regexp RE2 | `^[0-9]+$` | compila; o daemon sempre casa o nome inteiro (envolve em `^(?:...)$`) |
| `data_dir` | caminho absoluto | `/var/lib/painel-ramais` | existe e é gravável pelo usuário do serviço; banco em `<data_dir>/painel.db` |
| `log_level` | enum | `info` | `debug`, `info`, `warn`, `error`; log em texto no stdout (journald) |
| `ami.address` | host:porta | `127.0.0.1:5038` | host loopback |
| `ami.username` | string | `painel-ramais` | 1 a 64 caracteres; tem de bater com a seção do bloco em `manager_custom.conf` |
| `ami.secret_file` | caminho | `/etc/painel-ramais/ami-secret` | legível; permissão 0600; dono = usuário do serviço; conteúdo não vazio (espaços nas pontas descartados) |
| `auth.secret_file` | caminho | `/etc/painel-ramais/secret` | legível; 0600; base64 que decodifica em 32 bytes ou mais |
| `auth.issabel_key_file` | caminho absoluto | `/etc/painel-ramais-issabel.key` | não vazio e absoluto; o arquivo **pode não existir** (integração desligada); permissão e conteúdo nunca impedem a subida: são conferidos pela verificação de dependências (na subida e a cada 60 s; seções 3.3 e 4.4) |
| `auth.session_ttl` | duração Go | `12h` | entre `15m` e `24h`; inatividade máxima da sessão (renovada pelo uso e pelo WebSocket) |
| `ami.name_fill_per_second` | inteiro | `20` | entre 1 e 200; teto de consultas de nome (`PJSIPShowEndpoint`/`SIPshowpeer`) em segundo plano (decisão 4) |
| `ami.inventory_refresh` | duração Go | `10m` | entre `1m` e `24h`; intervalo da releitura completa dos ramais do PBX (seção 7.4), rede de segurança para recarga que o Asterisk não anunciou. A releitura por evento de recarga não depende dela |
| `history.retention_days` | inteiro | `30` | entre 1 e 365 |

Constantes (não configuráveis, de propósito): lote WS 250 ms; ring buffer 40 lotes; fila de envio por cliente 64 mensagens; TTL máximo do token 60 s; backoff de reconexão AMI de 1 s a 30 s; verificação de dependências a cada 60 s; fila interna AMI para estado 65536 eventos; ReadTimeout HTTP 15 s; no máximo 4 bcrypt simultâneos no login; rc.7: janela de evento do sistema 120 s, fila do gravador de registro 16.384, lote de registro a cada 2 s ou 500 itens, 200 ocorrências brutas por ramal, retenção do relatório 30 dias, atualização da aba Registros a cada 5 s. Viram configuração só com medição que justifique.

Não há variáveis de ambiente no binário de release. O único "ambiente" é este arquivo e os dois segredos, todos validados na subida (a chave do Issabel é a exceção deliberada: opcional e vigiada pela verificação de dependências). O endpoint de heap/pprof do teste de carga (`PAINEL_DEBUG_ADDR`) só existe no binário compilado com `-tags debug` e não faz parte da release.

---

## 7. AMI

### 7.1 Allowlist de actions

O cliente AMI tem uma lista fixa no código (`internal/ami/actions.go`). Pedido de action fora dela é erro de programação (retorna erro). Desde a rc.8 não há teste automatizado (D12): a lista é conferida por leitura contra esta tabela em cada revisão. Proibidas em qualquer hipótese: `Command`, `Originate`, `Redirect`, `Hangup`, `Atxfer`, `Setvar`, `Reload`, `ModuleLoad`, `UpdateConfig`, `GetConfig`, `Filter`, `QueueReload`, `QueueReset`, `QueueAdd`, `QueueRemove`, `QueuePause`, `QueuePenalty`, `QueueLog`, `QueueSummary` e qualquer outra.

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
| `QueueStatus` | rc.8 (ADR-008): filas, membros e quem aguarda; sem parâmetro (todas) ou com `Queue: <fila>` (uma). Resposta em lista: `QueueParams`, `QueueMember`, `QueueEntry`, fim em `QueueStatusComplete` | ambas | nenhuma (a conferir; S15) |

Permissões do usuário AMI (bloco do ADR-003, atualizado na rc.8): `read = system,call,agent` e `write = system,reporting`. A classe `agent` só libera **leitura** dos eventos de fila e agente (seção 7.2); `write` não muda. A coluna "classe esperada" vem da documentação do Asterisk e **precisa ser confirmada** na versão real com `manager show command <Action>` (o `instalar.py verificar` coleta isso no relatório). Risco aceito e documentado: a classe `system` de escrita é exigida pelas actions PJSIP e também liberaria actions de escrita; a defesa é a allowlist no código, o segredo 0600 e o `permit` só para 127.0.0.1. **Decisão do usuário em 2026-10-13** (`docs/DECISOES-FASE-1.md`, decisões 1 e 2: manter `write = system,reporting` com controles compensatórios; porta mantida por socket activation; detalhe no ADR-003): a classe `system` de escrita pode autorizar, para quem tiver o segredo, actions que não são de consulta (a conferir: `Reload`, `ModuleLoad`, `Hangup`); e o daemon escuta em TCP loopback, não em socket Unix.

Todas marcadas "a confirmar no Asterisk real com `manager show command <Action>`" (o `instalar.py verificar` coleta). Controle compensatório da decisão 1 (mantém `write = system,reporting`): a lista acima é fixa no código (`internal/ami/actions.go`); qualquer outra action (`Command`, `Originate`, `Reload`, `ModuleLoad`, `MixMonitor`, `GetConfig`, `DBPut`, `PJSIPNotify` e as demais) é recusada **antes** de escrever no socket, com log em nível **Error** (visível com `log_level: info`; só o nome da action vai ao log, nunca os parâmetros; `ami.Config.OnRefused`), e os parâmetros `Action`/`ActionID` são reservados ao cliente; a recusa acontece em `validate`, antes de qualquer escrita no socket (conferido por leitura). Risco residual aceito (para a auditoria): se o próprio daemon for comprometido, a classe `system` dá acesso a `GetConfig` e `ModuleUnload`; as barreiras são `permit` só para 127.0.0.1, segredo 0600 do usuário `painel-ramais` e o endurecimento da unit (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ReadWritePaths=/var/lib/painel-ramais`, `CapabilityBoundingSet=` vazio; validar com `systemd-analyze security painel-ramais`).

### 7.2 Eventos (eventfilter estático, união SIP e PJSIP)

`ContactStatus`, `PeerStatus`, `DeviceStateChange`, `Newchannel`, `Newstate`, `NewCallerid`, `NewConnectedLine`, `DialBegin`, `DialEnd`, `BridgeEnter`, `BridgeLeave`, `Hangup`, `Hold`, `Unhold`, `Reload`, `ModuleLoadReport`, `FullyBooted` (recarga do Asterisk: o inventário de ramais e os nomes são refeitos 3 s depois do último desses eventos, seção 7.4; classe de leitura `system`) e, desde a rc.7, `Shutdown` (Asterisk desligando: abre a janela de evento do sistema do relatório de registro, seção 11.5; classe `system`). O `%post` acrescenta `eventfilter = Event: Shutdown` **só** no bloco do painel em `manager_custom.conf` (o bloco é regerado no upgrade, seguido de `manager reload`); o simulador (`internal/amisim/proto.go`, `filterSet`) ganha o mesmo nome. `FullyBooted` e `Shutdown` são lidos no laço ao vivo do supervisor e viram `model.AsteriskSignal` (seção 11.1) sem passar pelos adaptadores. O bloco de `manager_custom.conf` do ADR-003 precisa de um `eventfilter` para cada um (`Event: Reload`, `Event: ModuleLoadReport`, `Event: FullyBooted`); os nomes exatos no Asterisk 18 do Issabel são confirmados na captura da homologação (S10).

**rc.8 (ADR-008): eventos de fila e agente**, classe de leitura `agent`: `QueueCallerJoin`, `QueueCallerLeave`, `QueueCallerAbandon`, `QueueMemberStatus`, `QueueMemberPause`, `QueueMemberAdded`, `QueueMemberRemoved`, `AgentCalled`, `AgentConnect`, `AgentComplete`, `AgentRingNoAnswer`. O `%post` (`packaging/scripts/postinstall`, função que monta o bloco) passa o `read` para `system,call,agent` e acrescenta **um `eventfilter = Event: <Nome>` para cada um dos onze**, só no bloco do painel; o upgrade regrava o bloco e faz `manager reload` (como na rc.7). A nota de eventos do `postinstall` (comentário do topo da função) lista os onze e o motivo. O simulador (`internal/amisim/proto.go`, `filterSet`) passa a ter **exatamente** a lista do bloco: os 14 da fase 1, `Reload`, `ModuleLoadReport`, `FullyBooted`, `Shutdown` e os onze da rc.8. Bloco vigente, na ordem:

```
read = system,call,agent
write = system,reporting
...
eventfilter = Event: Shutdown
eventfilter = Event: QueueCallerJoin
eventfilter = Event: QueueCallerLeave
eventfilter = Event: QueueCallerAbandon
eventfilter = Event: QueueMemberStatus
eventfilter = Event: QueueMemberPause
eventfilter = Event: QueueMemberAdded
eventfilter = Event: QueueMemberRemoved
eventfilter = Event: AgentCalled
eventfilter = Event: AgentConnect
eventfilter = Event: AgentComplete
eventfilter = Event: AgentRingNoAnswer
```

Uso no daemon: `QueueCallerJoin`, `QueueCallerLeave`, `QueueCallerAbandon`, `QueueMember*` e `AgentConnect` viram eventos normalizados (seção 11.6); `QueueCallerLeave`, `QueueCallerAbandon` e `AgentComplete` também agendam a `QueueStatus` da fila (seção 11.6, "Reconciliação"); `AgentCalled` e `AgentRingNoAnswer` são lidos e descartados na rc.8 (o estado do agente vem do `QueueMemberStatus`). As respostas da `QueueStatus` (`QueueParams`, `QueueMember`, `QueueEntry`, `QueueStatusComplete`) são resposta de action e não passam pelo eventfilter.

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

### 7.4 Inventário de ramais sempre atualizado

O inventário vem dos endpoints/peers **configurados** (`PJSIPShowEndpoints` / `SIPpeers`), nunca só dos contatos registrados: todo ramal configurado aparece, registrado ou não (os contatos, ou `PJSIPShowAors`, só dão o registro).

**Quando relê.** (a) Evento de recarga do Asterisk: qualquer evento cujo nome seja um de `Reload`, `ModuleLoadReport`, `FullyBooted` (lista única `reloadEvents` em `cmd/painel-ramais/supervisor.go`, comentada) ou contenha `reload`. Debounce: a leitura roda 3 s depois do **último** evento da rajada, ou 15 s depois do **primeiro** se a rajada não acalmar. Evento lido antes do snapshot da sessão é ignorado (o `FullyBooted` que o Asterisk manda logo depois do Login já está no snapshot). (b) Ciclo periódico `ami.inventory_refresh` (padrão 10 min), com a mesma diferença incremental. (c) Nova tentativa 30 s depois de uma releitura que falhou (a falha nunca remove ramal). Tudo roda na mesma goroutine que traduz os eventos AMI, em ordem; o que chega durante a leitura fica na fila e é aplicado depois da lista.

**O que a leitura faz** (sem mudar o epoch e sem recarregar a tela): o adaptador emite `InventoryListed` (todos os nomes) e depois, como no snapshot, `EndpointSeen`, `RegistrationChanged` (agregado dos contatos) e `DeviceStateChanged` (só de dispositivos que estão na lista) de cada endpoint; chamadas não são refeitas (os eventos ao vivo as mantêm). O mesmo filtro vale para o `DeviceStateList` do snapshot de conexão e de overflow: o Asterisk guarda dispositivo de endpoint já apagado, e sem o filtro o ramal apagado voltaria como fantasma. **Tudo é lido antes de emitir**: endpoints, contatos e `DeviceStateList` da releitura são lidos antes de qualquer evento sair e antes de o adaptador trocar o seu estado; se qualquer leitura falhar, nada foi emitido, nada mudou, e a releitura inteira se repete 30 s depois (os endpoints que seriam "novos" continuam novos na tentativa seguinte). Na **releitura** (não no evento ao vivo), registro que só difere na latência (`RoundtripUsec` no pjsip, `OK (N ms)` no sip) mantém a latência anterior, para não marcar quase todo ramal registrado como alterado a cada ciclo (com 10.000 ramais seria um delta de alguns MB por cliente a cada 10 min). Lista de endpoints só com endpoints internos (`dummy_endpoint`) conta como vazia para a confirmação em duas leituras. O `state` então:
- **ramal novo** (listado, sem estado): criado como `unregistered` (ou o registro que a leitura trouxe) com o grupo calculado pelas faixas; sai no delta, em `extensions`;
- **ramal que ficou**: nada muda se nada mudou (registro, IP e chamadas intactos); mudança de registro ou de nome vira delta normal;
- **ramal removido** (no estado, fora da lista): sai do estado e vai em `removed` no próximo delta; o nome dele sai do cache de nomes (também quando o ramal que ficou na tela por causa de uma chamada sai no `Hangup`). Com chamada ativa, continua até o `Hangup` da última e só então sai. Evento ao vivo atrasado de ramal já removido (por exemplo `DeviceStateChange` `INVALID`) não o recria; a releitura que voltar a listá-lo o recria;
- lista **vazia** quando já havia ramais é confirmada por uma segunda leitura 3 s depois antes de valer (lista vazia durante recarga pode ser transitória);
- log de toda releitura, nível Info: `inventario de ramais relido motivo=reload entraram=2 sairam=1 ficaram=810 aguardando_fim_de_chamada=0`.

**Pendente (sem teste automatizado e sem medição).** O inventário incremental (`InventoryListed`, `reconcile`/`removeExt`/`tomb`/`gone`, `removed` no delta, `DoList`/`IsEmptyList`, `IsExtension`, filtro `OnlyListed`, manutenção da latência na releitura) foi entregue **sem testes automatizados novos** por decisão do usuário nesta rodada, e nada foi compilado nem executado; os testes existentes só foram ajustados por leitura. Também não foi medido o custo de CPU e de tempo da goroutine `live` (releitura bloqueia a tradução de eventos enquanto lê, o que cresce com o número de ramais) com 10.000 ramais: fica para a medição na VM. Conferência ao vivo no servidor (rc.8, D12: não há teste automatizado no repositório): criar, apagar, apagar com chamada ativa, renomear, servidor sem ramais, rajada de `Reload`, fantasma do `DeviceStateList`, falha parcial da releitura.

**Nome.** Depois da leitura, se o motivo foi recarga ou entrou ramal novo, o ciclo de nomes (`PJSIPShowEndpoint`/`SIPshowpeer`, taxa `ami.name_fill_per_second`) recomeça com os **recém-chegados primeiro**. Nome editado no PBX é pego quando o `Callerid` já vem na lista de endpoints (se a captura mostrar que vem) ou na passada de nomes da recarga; sem isso, no ciclo de 6 h.

**Servidor sem ramais.** `Response: Error` com mensagem `No ... found` (`No endpoints found`, `No AORs found`, `No contacts found`; comparação sem diferenciar maiúsculas) e a lista vazia do `SIPpeers` significam **zero**, não erro (`ami.Client.DoList`, `ami.IsEmptyList`): o snapshot termina, a sessão AMI segue conectada, `ami_connected` fica `true`, `extensions` vem `[]` e o `status` do health não vira `degraded` por isso. `PJSIPShowContacts` vazio não aciona o fallback para `PJSIPShowAors`. O primeiro ramal criado entra pela releitura.

**Endpoints internos ignorados.** Lista única `model.InternalEndpoints`: `["dummy_endpoint"]` (comparação sem diferenciar maiúsculas, nome inteiro). Regra única de ramal, `model.IsExtension(pattern, nome)`: `nome != ""` **e** nome fora da lista **e** `extension_pattern` casa o nome inteiro. A detecção de tecnologia (`ami.CountExtensions`), o estado, o preenchimento de nomes e a amostra do `capturar` usam esta função; endpoint da lista não é ramal, não é tronco, não conta no `/api/health` (`trunks`) nem aparece na tela. O `instalar.py verificar` deve contar ramais com a mesma regra. Só `dummy_endpoint` está na lista: nenhuma captura ou documento do repositório mostra outro endpoint interno do Issabel; outros nomes entram aqui quando a captura da homologação os mostrar.

---

## 8. CLI

Binário único `/usr/bin/painel-ramais`. Todo subcomando, exceto `version`, recusa euid 0 com a mensagem `não rode como root; use: sudo -u painel-ramais painel-ramais <comando>` e sai com código 1.

```
painel-ramais serve [--config /etc/painel-ramais/config.yaml]
painel-ramais user add <usuario> [--password-stdin]
painel-ramais user del <usuario>
painel-ramais user passwd <usuario> [--password-stdin]
painel-ramais group import [--replace] <arquivo.csv | ->
painel-ramais session purge [--all | --issabel <usuario>]
painel-ramais group list
painel-ramais group audit [--limit N]
painel-ramais group del <grupo>
painel-ramais token --sub <texto> [--ttl 60s] [--issabel]
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
- `session purge` apaga as sessões vencidas; `--all` apaga todas (todo mundo faz login de novo) e manda o SIGHUP para o daemon fechar os WebSockets. Imprime a quantidade removida. `--issabel <usuario>` apaga as sessões de token com `subject` igual a `issabel:admin:<usuario>` ou `issabel:user:<usuario>` (sessão de senha e token da CLI nunca casam), imprime `N sessão(ões) removida(s)` e, se `N > 0`, manda o SIGHUP (os WebSockets dessas sessões fecham com `session_expired` `revoked` e 4001). `--all` e `--issabel` juntos, ou `--issabel` com usuário fora da regra (1 a 64 caracteres, sem caractere de controle): erro de uso (código 1).
- `token` imprime o token no stdout e nada mais. `--ttl` máximo 60 s. Sem `--issabel`, assina com o segredo do daemon e um `--sub` que comece com `issabel:` (sem diferenciar maiúsculas) é erro de uso (código 1) com a mensagem "use --issabel para sub issabel:". Com `--issabel`, assina com a chave do Issabel (lida com a regra de permissão da seção 3.3; falha de leitura sai com 2) e o `--sub` precisa seguir a gramática da seção 3.3 (`issabel:admin:<usuario>` ou `issabel:user:<usuario>`; senão código 1). Uso: testes e homologação do fluxo do menu.
- Depois de `user ...` ou `group ...` com sucesso, a CLI pega o PID com `systemctl show -p MainPID --value painel-ramais` e manda SIGHUP (mesmo usuário, não precisa de root). Daemon parado: avisa e sai 0. Falha no sinal: avisa `rode: systemctl reload painel-ramais` e sai 0 (o banco já foi gravado).
- Códigos de saída: 0 sucesso; 1 erro de uso ou validação (mensagem cita a linha do CSV); 2 erro de ambiente (banco, arquivo, permissão); 3 `user add` de usuário que já existe (nada alterado). O 3 existe para o `instalar.py` ser idempotente sem ler o banco. Só no `serve`: 4 = Asterisk abaixo da versão mínima (13); a unit deve ter `RestartPreventExitStatus=4` para não reiniciar em laço.
- `version` imprime uma linha `painel-ramais <versao> schema <user_version que o binário conhece>` (exemplo: `painel-ramais 0.1.1 schema 3`). O `instalar.py` usa o `schema` para decidir o rollback (seção 15.5).
- Root (o instalador) chama a CLI com `runuser -u painel-ramais -- painel-ramais ...`; pessoa usa `sudo -u painel-ramais painel-ramais ...`.
- `group list` imprime `grupo<TAB>faixas` (faixas separadas por vírgula; vazio se o grupo não tem faixa), uma linha por grupo, ordenado por nome. Quando o grupo tem ramais com vínculo manual, vem uma **terceira coluna** (`<TAB>2150,2151`, números separados por vírgula, ordenados). rc.7: o mesmo número pode aparecer na terceira coluna de vários grupos; a linha final `Sem grupo<TAB><TAB>...` deixa de existir (não há "Sem grupo" manual). Linhas sem terceira coluna têm o formato antigo.
- `group list` e `group audit` escapam como `\uXXXX` (ou `\UXXXXXXXX`; `\xNN` para byte inválido) os caracteres de controle (Cc, inclui DEL e C1), de formatação (Cf) e separadores de linha/parágrafo (Zl, Zp) de nomes, ramais, `quem` e detalhe, para texto vindo da tela não enganar o terminal.
- `group audit [--limit N]` imprime as últimas N linhas (padrão 50, de 1 a 1000, mais nova primeiro) de `group_audit`: `data RFC 3339 UTC<TAB>quem<TAB>ação<TAB>detalhe JSON`. Só leitura (não manda SIGHUP). `group import` e `group del` gravam a auditoria com `quem = cli`; `group import` imprime `N linha(s) importada(s)` (faixas e vínculos) e, desde a rc.7, `M linha(s) ignorada(s)` quando houver (repetidas, já existentes ou `Sem grupo,N`). `--limit` fora do `audit` é erro de uso (código 1).

---

## 9. CSV de grupos

```
# grupo,ramal_ou_faixa
grupo,ramal
Suporte,2000-2099
Suporte,2150
Vendas,3000-3999
Diretoria,4000
Plantão,2150
Andar 2,2000-2299
```

rc.7 (ADR-006): `2150` está em Suporte (manual), Plantão (manual) e Andar 2 (faixa); os ramais 2000 a 2099 estão em Suporte e em Andar 2.

O mesmo arquivo com `;` (`Suporte;2000-2099`) também é aceito, desde que o arquivo inteiro use `;`. Este formato também é o do `grupos-exemplo.csv` que vai no bundle.

- UTF-8, BOM aceito. Delimitador `,` ou `;`, detectado na primeira linha de dados e fixo para o arquivo (Excel em português exporta com `;`).
- Linhas vazias e linhas que começam com `#` são ignoradas. Cabeçalho opcional: só é pulado se a primeira linha de dados for exatamente `grupo<delim>ramal` (sem diferenciar maiúsculas). Qualquer outra linha é dado.
- `grupo`: 1 a 64 caracteres depois do trim (`store.MaxGroupNameRunes`, a mesma constante que as rotas `create` e `update` validam), diferente de `Sem grupo` (exceto em linha de um ramal, que é ignorada, ver abaixo) e, desde a rc.8, diferente de `Todos os ramais` (nome reservado da tela, D17 do `desvios.md`; sem diferenciar maiúsculas; erro `"Todos os ramais" e nome reservado`), só com o espaço comum U+0020 (NBSP U+00A0, U+3000 e os outros separadores Zs são recusados; nas pontas o trim os remove), sem caractere de controle (Cc, inclui C1), de formatação (Cf: marcas bidirecionais U+202A a U+202E e U+2066 a U+2069, largura zero U+200B/E/F, U+061C, U+FEFF) nem separador de linha ou de parágrafo (Zl, Zp: U+2028/2029); é a mesma regra do `<usuario>` do `sub` (seção 3.3). Grupo que não existe é criado.
- `ramal_ou_faixa`: só dígitos (`2150`, **um ramal**) ou `inicio-fim` com `inicio <= fim` (**faixa**). Comparação numérica nas faixas.
- **Um ramal por linha (`Suporte,2150`) é vínculo manual**: grava o par (número como texto, grupo) em `extension_groups`. Desde a rc.7 ele **soma** à faixa (ADR-006): o ramal fica no grupo da linha e continua nos grupos das faixas que o contêm. Faixa `N-M` continua sendo faixa do grupo (para uma faixa de um número só use `2150-2150`). Como o vínculo é pelo texto digitado, `02150` (zero à esquerda) vira vínculo manual de `02150`; ramal com zero à esquerda ou nome não numérico nunca cai numa faixa (seção 13).
- **`Sem grupo,2150`**: aceito e **ignorado** (conta em `ignored`), para arquivos antigos continuarem importando; "Sem grupo" é implícito e não tem vínculo. `Sem grupo,2000-2999` continua erro. A linha não cria grupo nenhum.
- **Pertença** (CSV, CLI, API e tela): o ramal está em todo grupo de faixa que o contém mais todo grupo de vínculo manual; sem nenhum, em `"Sem grupo"`.
- **`Todos os ramais` reservado (rc.8).** A regra mora num ponto só, `store.ValidGroupName`, que vale para `create`, `new_name` do `update`, CSV pela API (`400 invalid_csv`/`bad_request`) e pela CLI (`group import`, código 1). Sem migração: grupo antigo com esse nome continua no banco, aparece, pode ser **renomeado** e **excluído** (a validação só olha o nome **novo**; `update` e `delete` acham o grupo atual sem validar o nome dele) e continua aceito em `assign` (`validAssignGroup` não muda). Na subida, se o catálogo tiver esse nome, log Warn uma vez: `grupo com nome reservado "Todos os ramais"; renomeie pela tela`.
- **O mesmo ramal em várias linhas** é aceito (um vínculo por grupo). Linha repetida (mesmo grupo sem diferenciar maiúsculas e mesmo valor) é ignorada sem erro, e também a linha cujo vínculo ou faixa idêntica já existe no banco.
- **Faixas**: faixas de grupos diferentes podem se sobrepor. Faixas do **mesmo grupo** que se cruzam (no arquivo, ou com as faixas que o grupo já tem quando não há `--replace`) são erro, exceto a faixa idêntica, que é ignorada: `linhas N e M: faixas A-B e C-D do grupo "X" se sobrepoem` ou `linha N: faixa A-B cruza a faixa C-D ja gravada no grupo "X"`. Total de faixas por grupo depois da importação até 1.000 (contando as que já existiam).
- Tudo ou nada: qualquer erro rejeita o arquivo inteiro e lista até 20 linhas. Com `--replace`, apaga todos os grupos (faixas e vínculos manuais saem por cascade) e importa na mesma transação. **Sem `--replace` a importação só acrescenta**: nunca apaga nem troca vínculo ou faixa existente; grupo citado que não existe é criado. A importação grava uma linha de auditoria (`import`: linhas, `replace`, faixas, vínculos e ignoradas).

---

## 10. SQLite (`<data_dir>/painel.db`)

Driver `modernc.org/sqlite` (nome `sqlite`). Pragmas **no DSN**, para valer em toda conexão do pool (`foreign_keys` é por conexão): `?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate`. `_txlock=immediate` faz toda transação de escrita começar com `BEGIN IMMEDIATE` (o conflito com a CLI espera o `busy_timeout` em vez de falhar com `database is locked` no meio da transação); a leitura em transação (`LoadGroupData`) é `ReadOnly` e começa com `BEGIN` simples. `VACUUM INTO` do backup roda fora de transação. Daemon e CLI escrevem no mesmo arquivo; WAL e `busy_timeout` resolvem a concorrência.

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

-- Um grupo por ramal garantido pelo banco, não só pela CLI (até a v3; a v4 troca por group_ranges_no_overlap_same_group)
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

Versão 3 (grupos editáveis pela tela e atribuição manual de ramal; `PRAGMA user_version = 3`, aplicada por migração sobre a v2, com o backup `painel.db.bak-v2` antes):

```sql
-- Vínculo MANUAL ramal -> grupo, por número do ramal (texto): sobrevive a ramal apagado e recriado.
-- (Até a v3; a v4 reconstrói esta tabela, ver abaixo.) group_id NULL = "Sem grupo" manual (também vence a faixa). Apagar o grupo apaga as atribuições dele
-- (CASCADE): os ramais voltam para a faixa que casar ou para "Sem grupo".
CREATE TABLE extension_groups (
  number     TEXT    PRIMARY KEY CHECK (length(number) BETWEEN 1 AND 64),
  group_id   INTEGER REFERENCES groups(id) ON DELETE CASCADE,
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128)
) STRICT, WITHOUT ROWID;
CREATE INDEX extension_groups_group_id ON extension_groups(group_id);

-- Auditoria de toda alteração de grupo (quem, o quê, quando), gravada na MESMA transação da alteração.
-- detail é JSON curto; listas de números/faixas são truncadas em 20 itens (o resto vira só a contagem).
CREATE TABLE group_audit (
  id      INTEGER PRIMARY KEY,
  at      INTEGER NOT NULL,
  subject TEXT    NOT NULL CHECK (length(subject) BETWEEN 1 AND 128),
  action  TEXT    NOT NULL CHECK (action IN ('create', 'rename', 'ranges', 'delete', 'assign', 'import')),
  detail  TEXT    NOT NULL CHECK (json_valid(detail))
) STRICT;
CREATE INDEX group_audit_at ON group_audit(at);

-- A auditoria só cresce: UPDATE é barrado; DELETE segue permitido (expurgo por retenção).
CREATE TRIGGER group_audit_immutable BEFORE UPDATE ON group_audit
BEGIN SELECT RAISE(ABORT, 'auditoria imutavel'); END;
```

`subject` é o `subject` da sessão (`issabel:admin:<usuario>` na tela) ou `cli`. Mudança só aditiva (expand): nenhuma tabela antiga muda, então o upgrade é seguro e o binário antigo ignoraria as tabelas novas; mas o binário antigo **recusa abrir** banco com `user_version` 3 (downgrade não suportado), então o rollback do instalador (15.7) precisa do backup `painel.db.bak-v2`: voltar da rc.5 para a rc.4 **descarta** as atribuições manuais, a auditoria de grupos e qualquer grupo/faixa/usuário/histórico criado depois do upgrade (ver 15.5 e 15.7).

Versão 4 (rc.7: ramal em N grupos, ADR-006, e relatório de registro, ADR-007; `PRAGMA user_version = 4`, **uma** migração sobre a v3, com o backup `painel.db.bak-v3` antes, no mesmo `VACUUM INTO` de sempre). Antes de rodar o DDL, na mesma transação, o daemon lê `SELECT number FROM extension_groups WHERE group_id IS NULL` e, se houver linhas, registra em log Warn `vinculos "Sem grupo" manual descartados na migracao` com a contagem e até 20 números (o ramal volta a seguir as faixas).

```sql
-- 1) N:N. Chave (number, group_id), sem NULL: "Sem grupo" manual deixa de existir.
CREATE TABLE extension_groups_v4 (
  number     TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  group_id   INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128),
  PRIMARY KEY (number, group_id)
) STRICT, WITHOUT ROWID;
INSERT INTO extension_groups_v4 (number, group_id, updated_at, updated_by)
  SELECT number, group_id, updated_at, updated_by FROM extension_groups WHERE group_id IS NOT NULL;
DROP TABLE extension_groups;
ALTER TABLE extension_groups_v4 RENAME TO extension_groups;
CREATE INDEX extension_groups_group_id ON extension_groups(group_id);

-- 2) Faixas de grupos diferentes podem se sobrepor; do mesmo grupo, nao.
--    A mensagem continua contendo "faixa sobreposta" (o codigo procura esse texto).
DROP TRIGGER group_ranges_no_overlap;
CREATE TRIGGER group_ranges_no_overlap_same_group BEFORE INSERT ON group_ranges
WHEN EXISTS (SELECT 1 FROM group_ranges r
             WHERE r.group_id = NEW.group_id
               AND NEW.range_start <= r.range_end AND r.range_start <= NEW.range_end)
BEGIN SELECT RAISE(ABORT, 'faixa sobreposta no mesmo grupo'); END;

-- 3) history: quedas por minuto (grupo e total '') para o grafico da aba Registros.
--    NULL nas linhas anteriores a v4 e em minuto de lacuna (gap = 1); >= 0 nos demais.
ALTER TABLE history ADD COLUMN lost_registration  INTEGER CHECK (lost_registration  IS NULL OR lost_registration  >= 0);
ALTER TABLE history ADD COLUMN became_unreachable INTEGER CHECK (became_unreachable IS NULL OR became_unreachable >= 0);

-- 4) Relatorio de registro.
-- Data de instalacao da versao com relatorio ("Dados desde ..."), gravada uma vez pela migracao.
CREATE TABLE reg_meta (
  id    INTEGER PRIMARY KEY CHECK (id = 1),
  since INTEGER NOT NULL
) STRICT;
INSERT INTO reg_meta (id, since) VALUES (1, CAST(strftime('%s', 'now') AS INTEGER));

-- Ocorrencia bruta. No maximo 200 por ramal (o gravador apaga as mais antigas a cada lote).
-- system = 1: transicao dentro da janela de 120 s apos Shutdown, FullyBooted ou conexao do AMI.
CREATE TABLE reg_events (
  id         INTEGER PRIMARY KEY,
  at         INTEGER NOT NULL,
  number     TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  kind       TEXT    NOT NULL CHECK (kind IN ('lost_registration', 'registered', 'became_unreachable', 'reachable_again')),
  system     INTEGER NOT NULL CHECK (system IN (0, 1)),
  ip         TEXT    CHECK (ip IS NULL OR length(ip) BETWEEN 7 AND 15),
  port       INTEGER CHECK (port IS NULL OR port BETWEEN 1 AND 65535),
  user_agent TEXT    CHECK (user_agent IS NULL OR length(user_agent) BETWEEN 1 AND 256)
) STRICT;
CREATE INDEX reg_events_number_id ON reg_events(number, id);
CREATE INDEX reg_events_at        ON reg_events(at);

-- Agregado por hora e ramal. Contadores so de system = 0; system_events conta os de sistema.
-- name: ultimo nome conhecido (o relatorio nao depende do cache extension_names, que o pruneNames apaga).
-- rev: lote de gravacao que tocou a linha por ultimo (cursor da secao 4.7.2).
CREATE TABLE reg_hourly (
  number             TEXT    NOT NULL CHECK (length(number) BETWEEN 1 AND 64),
  hour               INTEGER NOT NULL CHECK (hour % 3600 = 0),
  name               TEXT    CHECK (name IS NULL OR length(name) BETWEEN 1 AND 128),
  lost_registration  INTEGER NOT NULL DEFAULT 0 CHECK (lost_registration  >= 0),
  registered         INTEGER NOT NULL DEFAULT 0 CHECK (registered         >= 0),
  became_unreachable INTEGER NOT NULL DEFAULT 0 CHECK (became_unreachable >= 0),
  reachable_again    INTEGER NOT NULL DEFAULT 0 CHECK (reachable_again    >= 0),
  system_events      INTEGER NOT NULL DEFAULT 0 CHECK (system_events      >= 0),
  down_s             INTEGER NOT NULL DEFAULT 0 CHECK (down_s BETWEEN 0 AND 3600),
  last_drop_at       INTEGER,
  last_ip            TEXT    CHECK (last_ip IS NULL OR length(last_ip) BETWEEN 7 AND 15),
  last_port          INTEGER CHECK (last_port IS NULL OR last_port BETWEEN 1 AND 65535),
  rev                INTEGER NOT NULL CHECK (rev >= 1),
  PRIMARY KEY (number, hour)
) STRICT, WITHOUT ROWID;
CREATE INDEX reg_hourly_hour ON reg_hourly(hour);
CREATE INDEX reg_hourly_rev  ON reg_hourly(rev);

-- Sinais do sistema que abrem a janela de supressao.
CREATE TABLE reg_system_events (
  id   INTEGER PRIMARY KEY,
  at   INTEGER NOT NULL,
  kind TEXT    NOT NULL CHECK (kind IN ('shutdown', 'fully_booted', 'ami_connected'))
) STRICT;
CREATE INDEX reg_system_events_at ON reg_system_events(at);
```

Notas da versão 4:
- **Expand e contract na mesma versão, de propósito.** `extension_groups` é reconstruída (contract) porque a chave muda; não existe janela em que dois binários usem o mesmo banco (o serviço para no `rpm -U`). O rollback depende do `painel.db.bak-v3` (seção 15.7).
- **Retenção fixa de 30 dias** para `reg_events`, `reg_hourly` e `reg_system_events` (constante, não segue `history.retention_days`): uma vez por dia (e na subida), `DELETE ... WHERE at < agora - 30 d` (`hour <` no agregado). rc.8: **em blocos**, para não segurar a única conexão: `reg_events` e `reg_system_events` com `DELETE FROM <t> WHERE id IN (SELECT id FROM <t> WHERE at < ? LIMIT 5000)`; `reg_hourly` (WITHOUT ROWID) com `DELETE FROM reg_hourly WHERE (number, hour) IN (SELECT number, hour FROM reg_hourly WHERE hour < ? LIMIT 5000)`; cada bloco é um comando (autocommit) com prazo de 15 s, repetido até apagar menos de 5.000 linhas; entre blocos a conexão volta ao pool. Falha num bloco: log Warn, para a limpeza e tenta de novo no dia seguinte (dado velho a mais não é perda). Não há tabela de intervalo aberto: o intervalo fora do ar aberto vive no estado e se perde num restart do **daemon** (ADR-007, alternativa E).
- **Gravação de um lote, rc.8 (ADR-007, nota rc.8).** O gravador junta notas como antes (a cada 2 s ou 500 notas, o que vier antes) e **divide o lote em sublotes por custo** antes de gravar. Custo de uma nota em comandos SQL: ocorrência (`Kind`) = 2 (insert e upsert); fechamento de intervalo (`DownFrom`) = número de horas entre `DownFrom` e `At`, no máximo 720; sinal = 1; mais 1 por ramal distinto do sublote (o corte de 200). Notas entram na ordem em que chegaram; uma nota nunca é partida; o sublote fecha quando a próxima nota passaria de **5.000** (constante `reglog.SubBatchCost`); nota sozinha acima do teto vai num sublote só dela. Cada sublote é **uma transação** com a sua própria `rev` (`rev` + 1 por sublote) e o prazo de 15 s de sempre. Sublote que falha (rollback) é **repetido uma vez** logo em seguida (nova transação, mesma `rev`). Falhou de novo: `write_errors++`, `lost_events += notas do sublote`, `lost_since`/`lost_until` ajustados (seção 4.4) e log Warn `relatorio de registro: ocorrencias perdidas` com `ocorrencias` (quantidade), `de` e `ate` (primeiro e último `At` do sublote, RFC 3339) e `erro`. A perda **nunca** é silenciosa. Repetição que deu certo: log Info `relatorio de registro: sublote gravado na segunda tentativa`. Um único escritor no daemon: todo acesso de escrita usa o mesmo `*store.Store` (pool de uma conexão, `SetMaxOpenConns(1)`), com `busy_timeout(5000)` e `_txlock=immediate` no DSN; nenhum `sql.Open` de escrita novo. Conteúdo de cada sublote, igual ao lote da rc.7:
- **Gravação de um lote, conteúdo** (`rev` = maior `rev` gravada + 1, lida na subida): (1) `INSERT` em `reg_events`; (2) upsert em `reg_hourly` da hora de cada ocorrência (`ON CONFLICT(number, hour) DO UPDATE` somando o contador do tipo ou `system_events`, `last_drop_at = max(...)` para queda contada, `last_ip`/`last_port` quando a ocorrência tem endereço, `name` quando conhecido, `rev`); (3) para cada fechamento de intervalo fora do ar, upsert de `down_s` em cada hora entre o início e o fim (só os segundos daquela hora, no máximo as últimas 720 horas; soma com teto, `min(3600, down_s + excluded.down_s)`, para um erro de conta nunca derrubar o lote no `CHECK`); (4) `INSERT` em `reg_system_events` dos sinais; (5) para cada ramal do lote, `DELETE FROM reg_events WHERE number = ? AND id < (SELECT id FROM reg_events WHERE number = ? ORDER BY id DESC LIMIT 1 OFFSET 199)` (fica com as 200 mais novas). Falha: rollback e a regra de repetição do item anterior (até a rc.7 o lote inteiro era descartado na primeira falha).
- **Tamanho esperado** com 10.000 ramais e 30 dias: `reg_events` até 2 milhões de linhas (teto de 200 por ramal); `reg_hourly` proporcional a ramal x hora com ocorrência (dezenas de milhares em operação normal).

Versão 5 (rc.8: nome da fila, ADR-009; `PRAGMA user_version = 5`, aplicada por migração sobre a v4). Só aditiva.

```sql
-- Nome dado pelo painel a uma fila do Asterisk (a chave e o identificador do AMI, ex.: '600').
-- Sem linha = sem nome (a tela mostra "Fila 600"). updated_by = subject da sessao que gravou.
CREATE TABLE queue_names (
  queue      TEXT    PRIMARY KEY CHECK (length(queue) BETWEEN 1 AND 64),
  name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64 AND name = trim(name)),
  updated_at INTEGER NOT NULL,
  updated_by TEXT    NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 128)
) STRICT, WITHOUT ROWID;
```

Notas da versão 5:
- **Um backup só por subida.** O `store.migrate` faz o `VACUUM INTO painel.db.bak-v<versão inicial>` **uma vez**, antes do laço, e aplica cada migração numa transação própria (`internal/store/store.go:86-95` na tag `v0.1.2-rc.7`; a rc.8 não muda essa forma). Banco 3 (rc.6, o caso do usuário) passa por 3, 4 e 5 na mesma subida e gera **só** `painel.db.bak-v3`; banco 4 (rc.7) gera `painel.db.bak-v4`. Falha da 4 para 5 depois de a 3 para 4 passar: o banco fica no esquema 4, o daemon não sobe e o `.bak-v3` continua valendo para o rollback (seção 15.7).
- Lida inteira na subida e depois de cada escrita (`LoadQueueNames`), entregue ao estado como `QueueNamesLoaded` (seção 11.1). Sem retenção: uma linha por fila nomeada.
- `painel-ramais version` passa a dizer `schema 5`.

Todo nome que entra (callerid do Asterisk, display name do telefone, cache antigo, nome do outro lado da chamada) passa por `model.CleanName`: sem caracteres de controle ou de formatação (categorias Unicode `C*`: C0/C1, bidi U+202A..202E e U+2066..2069, largura zero, BOM), espaços colapsados e no máximo 64 caracteres. A tela da fase 2 deve mostrar nomes só como texto (nunca `innerHTML`) e com CSP estrita. Ao fim de cada sincronização completa o daemon expurga do cache os ramais que não existem mais no PBX (lista vazia não expurga).

O daemon grava em lote (a cada 5 s e no desligamento) todo nome aprendido ao vivo (consulta de nome ou `CallerIDName` de quem origina uma chamada) e o lê na subida: o nome aparece já no primeiro snapshot, e o nome real do Asterisk sempre vence o do cache. Preenchimento em segundo plano: depois de cada sincronização, a 20 consultas/s no máximo (`ami.name_fill_per_second`), de novo a cada 6 h e depois de um evento `Reload`; o painel funciona com o nome vazio enquanto isso. Limite: nome apagado no Asterisk (callerid vazio) não limpa o nome já conhecido do ramal.

Regras de uso:
- **Amostra de histórico**: na virada de cada minuto, o daemon grava uma linha para o total (`''`) e uma por grupo (inclui `Sem grupo`), com as contagens daquele instante. Se o AMI estava fora em qualquer momento do minuto, grava `gap=1`. rc.7: o ramal conta em cada grupo dele (N:N) e uma vez no total; `lost_registration` e `became_unreachable` da linha são a diferença, desde a amostra anterior, dos contadores acumulados do estado (`Stats.RegTotal` e `Stats.RegByGroup`, seção 11.4; grupo sem amostra anterior conta a partir de 0; diferença negativa vira 0); em `gap=1` as duas ficam `NULL`. Tamanho esperado com 100 grupos: cerca de 4,4 milhões de linhas em 30 dias.
- **Retenção**: a cada hora, `DELETE FROM history WHERE minute < agora - retention_days`. Usa `history_minute`.
- **Sessões vencidas**: a cada hora, `DELETE FROM sessions WHERE expires_at <= agora`.
- **Auditoria de grupos**: a cada hora, `DELETE FROM group_audit WHERE at < agora - retention_days` (a mesma retenção do histórico).
- **Grupos no daemon**: catálogo (grupos com ou sem faixa) e vínculos manuais lidos juntos, numa transação de leitura, na subida, a cada SIGHUP e depois de cada escrita pela tela (`loadGroups`). Resolução dos grupos de um ramal (rc.7): `range_groups` = todo grupo com faixa que contém o número (faixas de grupos diferentes se sobrepõem, então a busca binária única deixa de servir; dentro de um grupo elas não se sobrepõem, então vale **uma busca binária por grupo**, nas faixas do próprio grupo ordenadas por `range_start`: custo grupos x log(faixas) por ramal; ponytail: árvore de intervalos só com medição); `manual_groups` = grupos de `extension_groups` do número; `groups` = união ordenada, ou `["Sem grupo"]`.

---

## 11. Eventos normalizados (pacote `model`)

Os adaptadores `sip` e `pjsip` traduzem mensagens AMI nestes tipos. O supervisor da conexão (em `cmd/painel-ramais`) emite os de controle. O `state` consome só estes tipos, de um canal, numa goroutine única, e nunca vê texto AMI. Todo evento carrega `At time.Time` (hora em que o leitor tirou a mensagem do socket), usado para `lag_ms` e para `started_at`.

### 11.1 Controle

| Tipo | Campos | Efeito no estado |
|---|---|---|
| `AMIDisconnected` | `Reason` | todos os ramais para `unknown`, `calls` vazias, `ami_connected=false`, epoch novo; rc.8: projeção de chamadas e de filas vazia (o `Reset` leva `calls: []` e `queues: []`) |
| `QueueNamesLoaded` | `Names map[string]string` (fila para nome; ausente = sem nome) | rc.8: troca os nomes das filas; cada fila cujo `name` mudou fica suja e vai no próximo delta; **sem epoch novo** (emitido pelo `cmd` na subida e depois de cada `POST /api/queues/rename`) |
| `SyncStarted` | `Technology`, `AsteriskVersion`, `Reason` (`connect` ou `overflow`) | começa estado de montagem separado; a visão publicada continua a anterior |
| `SyncFinished` | (nenhum) | troca a visão pela montada, `ami_connected=true`, epoch novo |
| `NamesLoaded` | `Names map[string]string` | cache de nomes (número para callerid); só preenche ramal sem nome; sem epoch novo (emitido pelo `cmd` na subida) |
| `GroupsLoaded` | `Groups []Group{Name, Ranges []Range{Start, End}}` (catálogo completo, inclui grupo sem faixa), `Manual map[string][]string` (rc.7: número para a lista de grupos com vínculo manual; ausente = nenhum) | recalcula os grupos de cada ramal (seção 10, "Grupos no daemon"), marca como sujos só os que mudaram (`groups`, `range_groups` ou `manual_groups`) e liga a bandeira de catálogo mudado; **sem epoch novo** (emitido pelo `cmd` na subida, no SIGHUP e depois de cada escrita pela tela) |
| `AsteriskSignal` | `Kind` (`shutdown` ou `fully_booted`) | rc.7, emitido pelo supervisor no laço ao vivo ao ler `Event: Shutdown` ou `Event: FullyBooted` (inclusive o `FullyBooted` logo após o Login). Abre a janela de evento do sistema de 120 s a partir de `At` e gera o sinal para `reg_system_events` (seção 11.5). Não muda epoch nem versão |
| `InventoryListed` | `Reason`, `Names []string` (todos os endpoints/peers do PBX, troncos inclusos) | releitura do inventário (seção 7.4): remove do estado os ramais que não estão na lista (ou os mantém até o fim da chamada), **sem epoch novo**; ignorado durante sincronização ou com o AMI fora. Vem antes dos `EndpointSeen`/`RegistrationChanged` da mesma leitura |

Regra do epoch: mudança em massa (queda do AMI, fim de sincronização) gera epoch novo e `reload`; mudança de grupo (catálogo ou atribuição) e inventário vão por delta; mudança de um ramal, inclusive ramal criado ou apagado no PBX (`InventoryListed`), gera delta.

### 11.2 Registro e dispositivo

| Tipo | Campos | Origem |
|---|---|---|
| `EndpointSeen` | `Name`, `CallerIDName`, `Technology` | `PJSIPShowEndpoints` / `SIPpeers` durante a sincronização |
| `RegistrationChanged` | `Name`, `State` (enum de `registration`), `IP`, `Port int` (rc.7; 0 = desconhecida), `UserAgent`, `LatencyMs *int`, `Live bool` (rc.7) | `ContactStatus` agregado / `PeerStatus` / listas de contato. `Live = true` **só** em `pjsip.contactStatus` e `sip.peerStatus` (evento ao vivo); snapshot, releitura (`load`) e qualquer outro caminho deixam `false`. `Port`: pjsip, do `ViaAddress` ou do host do `URI`; sip, do `Address` (`ip:porta`) |
| `DeviceStateChanged` | `Name`, `State` (enum de `device_state`) | `DeviceStateChange`, `DeviceStateList` |

`Name` é o nome do endpoint/peer, sem prefixo de tecnologia. O `state` decide ramal ou tronco com `extension_pattern` (D1). Evento de endpoint desconhecido cria o registro dentro do epoch atual.

### 11.3 Chamadas (nível de canal; a correlação é do `state`)

| Tipo | Campos | Evento AMI |
|---|---|---|
| `ChannelCreated` | `Uniqueid`, `Linkedid`, `Channel`, `Endpoint` (vazio se Local ou outro), `IsLocal`, `CallerNum`, `CallerName`, `Exten`, `Up bool`, `BridgeID` (só no snapshot), `Duration` (só no snapshot), `Dialed` (só no snapshot: `Application=AppDial`/`(Outgoing Line)`, canal destino de um Dial), `DialData` (só no snapshot: argumentos do Dial, quando `Application=Dial`), `Queue` (rc.8, só no snapshot: primeiro argumento do `ApplicationData` quando `Application=Queue`) | `Newchannel`, `CoreShowChannel` |
| `ChannelStateChanged` | `Uniqueid`, `Linkedid` (rc.8), `State` (`down`, `ring`, `ringing`, `up`, `busy`, `other`) | `Newstate` |
| `CallerIDChanged` | `Uniqueid`, `Linkedid` (rc.8), `Num`, `Name` | `NewCallerid` |
| `ConnectedLineChanged` | `Uniqueid`, `Linkedid` (rc.8), `Num`, `Name` | `NewConnectedLine` |
| `DialStarted` | `SrcUniqueid`, `DstUniqueid`, `SrcLinkedid` (rc.8, `Linkedid`), `DstLinkedid` (rc.8, `DestLinkedid`), `DialString` (rc.8, `DialString`) | `DialBegin` |
| `DialEnded` | `SrcUniqueid`, `DstUniqueid`, `Status` | `DialEnd` |
| `BridgeEntered` | `BridgeID`, `Uniqueid`, `Linkedid` (rc.8) | `BridgeEnter` |
| `BridgeLeft` | `BridgeID`, `Uniqueid`, `Linkedid` (rc.8) | `BridgeLeave` |
| `HoldChanged` | `Uniqueid`, `Linkedid` (rc.8), `OnHold bool` | `Hold`, `Unhold` |
| `ChannelHungup` | `Uniqueid`, `Cause` | `Hangup` |

`Endpoint` sai do nome do canal: `PJSIP/2001-0000002a` e `SIP/2001-0000002a` viram `2001`. Essa extração é a única parte de chamada que muda entre as tecnologias; a tradução dos eventos de chamada é compartilhada em `ami/calls`.

**`Linkedid` atualizado (rc.8, ADR-008).** `Linkedid` vazio no evento = sem mudança. Não vazio e diferente do guardado: o estado move o canal de grupo (`byLinked`) **antes** de aplicar o resto do evento, dentro do mesmo `mutate` (os ramais afetados antes e depois incluem os dois grupos). No `DialStarted`, `SrcLinkedid` vale para o canal de origem e `DstLinkedid` para o de destino. `ChannelHungup` não muda grupo. Quem atualiza é só `internal/state/channels.go`; `internal/state/view.go` não muda na rc.8.

Restauração por snapshot: o `CoreShowChannels` não traz os `DialBegin`. No fim da sincronização o `state` refaz as relações de Dial por Linkedid (quem executa `Dial` casado com o canal `AppDial` pelo argumento do Dial; o que não casa por nome liga quando só resta um par). `Dialed` mantém a direção `outbound` de chamada de saída restaurada. `hold` não vem do `CoreShowChannels` (limite do protocolo): chamada em espera volta como `talking`. Com vários Dial sem casar por nome e vários destinos soltos no mesmo Linkedid, o peer fica `unknown` até atender.

Fila entre leitor AMI e estado: canal com capacidade 65536. A goroutine leitora só faz parse e envio sem bloqueio (`select` com `default`). Fila cheia: o leitor descarta, conta em `queue.overflows`, registra log e, quando a fila esvazia, o supervisor dispara uma ressincronização (`SyncStarted{Reason: overflow}`) sem derrubar a conexão. Nada é perdido em silêncio: o estado é refeito inteiro.

### 11.4 Saída do estado para o hub (`api`)

| Tipo | Campos | Quando |
|---|---|---|
| `Batch` | `Epoch`, `FromVersion`, `ToVersion`, `AMIConnected`, `Extensions []Extension`, `Removed []string` (números que saíram, ordenados, disjuntos de `Extensions`; cada remoção conta uma mudança na versão), `Groups []Group` (catálogo completo, só quando mudou; `nil` = não mudou; a versão avança pelo menos 1 mesmo sem ramal afetado) | a cada 250 ms com mudança |
| `Reset` | `Epoch`, `Version`, `AMIConnected`, `Technology`, `Extensions []Extension` (todos), `Groups []Group` (catálogo, para o hub servir `/api/groups` e o `groups` do snapshot), rc.8: `Calls []LiveCall` e `Queues []Queue` (todos, nunca `nil`) | a cada epoch novo |

rc.8 (ADR-008): `Batch` ganha `Calls []LiveCall` (mudadas, estado completo), `CallsRemoved []string`, `Queues []Queue` (mudadas, completas), `QueuesRemoved []string`; `nil`/vazio = nada daquele tipo, e o hub só escreve a chave no delta quando a lista tem item (seção 5.2). O hub guarda as duas projeções (mapa por `id` e por `queue`) para o snapshot e para a rota de renomear (existência da fila). `ToVersion - FromVersion` = mudanças de ramal (como antes) + itens das quatro listas novas; o lote sai se **qualquer** das listas (ramais, removidos, catálogo, chamadas, chamadas removidas, filas, filas removidas) tiver item.

`Extension` e `Call` do `model` têm as tags JSON da seção 2: o formato da seção 2 é o próprio `json.Marshal` desses tipos. rc.7: `Extension` troca `Group`/`GroupManual` por `Groups`, `RangeGroups`, `ManualGroups []string` (tags `groups`, `range_groups`, `manual_groups`, nunca `nil` no JSON) e ganha `DownSince *time.Time` com tag `json:"-"` (fora do snapshot e do delta; o hub o guarda para a seção 4.7 montar `down_open_since`).

`state.Stats` (rc.7) ganha: `RegTotal model.RegCounts` e `RegByGroup map[string]model.RegCounts` (`RegCounts{Lost, Unreachable uint64}`, acumulados desde a subida, só ocorrências contadas, `system = 0`; o ramal soma em cada grupo dele no momento da ocorrência, inclusive `Sem grupo`) e `SuppressedUntil time.Time` (zero = sem janela). `ByGroup` passa a contar o ramal em cada grupo dele.

### 11.5 Relatório de registro no estado e no gravador (rc.7, ADR-007)

**No estado** (`view.onRegistration`, goroutine do estado, nunca bloqueia):
1. Só quando `e.Live`, a visão é a publicada (`!isBuilding`) e `x.reg != e.State`: aplica a tabela de transições do ADR-007 (estado anterior `x.reg`, novo `e.State`; `unknown` dos dois lados não conta).
2. `system = e.At < suppressedUntil`. A janela é `max(suppressedUntil, At + 120 s)` e abre em `AsteriskSignal` e no `SyncFinished` de uma sincronização com `Reason = connect` (o estado guarda o `Reason` do `SyncStarted`; inclusive a primeira conexão depois da subida; `overflow` não abre). Cada abertura também gera um sinal (`shutdown`, `fully_booted` ou `ami_connected`).
3. Intervalo fora do ar (`extState.downSince`): abre na queda contada (`lost_registration` ou `became_unreachable`, `system = false`) se estiver fechado; fecha em `registered` ou `reachable_again` (de sistema ou não). No `SyncFinished`, o `downSince` de cada ramal da visão antiga passa para a nova; se o ramal voltou `registered`, fecha ali (fechamento sem ocorrência). Ramal removido do estado (`removeExt`) fecha o intervalo na hora da remoção.
4. Endereço da ocorrência: IP e porta do estado novo; se vazio (queda), os do estado anterior. Só IPv4 (`net.ParseIP(ip).To4() != nil`); senão vazio. User agent passa por `model.CleanName` com teto de 256 caracteres (não 64).
5. Contadores acumulados de `Stats.RegTotal`/`RegByGroup` somam só `lost_registration` e `became_unreachable` não sistema.
6. Entrega: `Options.OnReg func(model.RegNote)`, chamada na goroutine do estado. `RegNote{At, Number, Name, Kind, System, IP, Port, UserAgent, DownFrom time.Time, Signal string}`: ocorrência (`Kind` preenchido), fechamento de intervalo (`Kind` vazio, `DownFrom` não zero, `At` = fim) ou sinal (`Signal` preenchido, `Number` vazio). Uma ocorrência que fecha intervalo leva o `DownFrom` junto.

**No gravador** (`internal/reglog`, goroutine própria, criado e ligado pelo `cmd`):
- O `cmd` implementa `OnReg` como envio **sem bloqueio** (`select` com `default`) num canal de 16.384 `RegNote`; cheio, descarta e soma `dropped`. Constante, não configurável.
- Lote a cada 2 s ou 500 itens, o que vier antes, numa transação (seção 10, "Gravação de um lote"); no desligamento grava o que sobrou. Limpeza da retenção de 30 dias uma vez por dia e na subida.
- rc.8: o lote é gravado em sublotes por custo, com uma repetição e perda contada (seção 10, "Gravação de um lote, rc.8"); a limpeza é em blocos de 5.000.
- Expõe `model.RegStatus{QueueLen, QueueCap int; Dropped, WriteErrors uint64}` (rc.8: mais `LostEvents uint64` e `LostSince, LostUntil time.Time`, zero = nenhuma perda), injetado no `api.Server` como `RegStatus func() model.RegStatus` (o `api` não importa `reglog`); `suppressed_until` vem de `Stats.SuppressedUntil`.
- As rotas da seção 4.7 leem do `store` (consultas novas) e da projeção do hub (nome, grupos, presença e `DownSince` atuais).

### 11.6 Filas e projeção de chamadas (rc.8, ADR-008)

**Eventos normalizados de fila** (tradução em `internal/ami/queues`, comum às duas tecnologias, chamada no laço ao vivo do supervisor ao lado do `Translate` do adaptador):

| Tipo | Campos | Evento AMI |
|---|---|---|
| `QueueCallerJoined` | `Queue`, `Uniqueid`, `CallerNum`, `CallerName` | `QueueCallerJoin` |
| `QueueCallerLeft` | `Queue`, `Uniqueid` | `QueueCallerLeave`, `QueueCallerAbandon` |
| `QueueMemberChanged` | `Queue`, `Member QueueMember` | `QueueMemberStatus`, `QueueMemberPause`, `QueueMemberAdded` |
| `QueueMemberRemoved` | `Queue`, `Interface` | `QueueMemberRemoved` |
| `QueueAgentConnected` | `Queue`, `Uniqueid` (canal do chamador) | `AgentConnect` |
| `QueuesListed` | `Full bool`, `Queue string` (só com `Full = false`), `Queues []QueueSnapshot`, `Unsupported bool` | resposta da `QueueStatus` |

`QueueMember{Interface, StateInterface, Name, PausedReason string; Status, CallsTaken int; Paused, InCall bool}` (eventos: `Interface`, `StateInterface`, `MemberName`, `Status`, `Paused`, `PausedReason`, `InCall`, `CallsTaken`; `QueueStatus`: `Location` no lugar de `Interface`, `Name` no lugar de `MemberName`). `QueueSnapshot{Queue string; Completed, Abandoned, ServiceLevel int; ServiceLevelPerf float64; Members []QueueMember; Entries []QueueEntry}`; `QueueEntry{Uniqueid, CallerNum, CallerName string; Wait time.Duration}`. Todo texto passa por `model.CleanName`. `AgentCalled`, `AgentComplete` e `AgentRingNoAnswer` não geram evento normalizado.

**No estado** (goroutine única, nunca bloqueia; os dois tipos moram no `State`, em arquivos novos de `internal/state`, fora da `view`):
- Filas: mapa por `queue`. `QueueCallerJoined` cria a fila se não existe (`stats_at: null`) e insere ou atualiza a entrada pelo `Uniqueid` (idempotente). `QueueCallerLeft` remove a entrada (ausente: nada). `QueueMemberChanged` insere ou substitui o membro pela `Interface`; `QueueMemberRemoved` remove. `QueueAgentConnected` marca o Uniqueid como "atendido via fila X" (vale até o `Hangup` do canal). `QueuesListed{Full: true}` substitui **todas** as filas (as ausentes saem); `{Full: false}` substitui só `Queue` (sem `QueueParams` na resposta: a fila sai). `Unsupported: true` deixa a lista vazia. Cada fila alterada fica suja; a mudança das entradas também suja a chamada do chamador.
- Chamadas: mapa por `Linkedid` com o que é guardado na primeira montagem (origem externa ou ramal, `started_at`) e a última `LiveCall` publicada. Cada evento de canal aplicado na visão **publicada** marca como sujo o `Linkedid` dos canais citados antes e depois de aplicar (e, em `BridgeEntered`/`BridgeLeft`, o dos canais daquela bridge); evento de fila marca o `Linkedid` do canal do chamador. Eventos aplicados na visão em montagem não marcam nada: no `SyncFinished` a projeção inteira é refeita da visão nova (o `Reset` leva tudo). `InventoryListed` que mudou o inventário marca todas as chamadas.
- `Flush`: para cada `Linkedid` sujo, remonta a linha em O(k) (k = canais do grupo), compara com a publicada e põe em `Calls` (mudou ou nova) ou `CallsRemoved` (sumiu); o mesmo para as filas sujas. Depois reavalia as chamadas que esperam o prazo de 1 s do "Local sem par" (seção 2.3) e registra o log. A versão avança pela soma da seção 11.4.

**Reconciliação com `QueueStatus`** (no laço ao vivo do supervisor, mesma goroutine que traduz os eventos e lê o inventário: o que chega durante a leitura é aplicado depois da lista, então os eventos de fila precisam ser idempotentes como descrito acima):
- **Completa** (sem parâmetro): logo depois de cada sincronização (`connect` e `overflow`), depois de cada releitura de inventário com motivo `reload`, e a cada **10 min** (constante `queueFullEvery`, ticker próprio). Prazo de 30 s.
- **Por fila** (`Queue: <fila>`): `QueueCallerLeave`, `QueueCallerAbandon` ou `AgentComplete` marcam a fila como pendente; a cada volta do `poll` (250 ms) roda para cada fila pendente cuja última leitura (completa ou por fila) tem 30 s ou mais (constante `queueMinGap`). Prazo de 10 s.
- **Erro**: resposta `Error` cujo texto contém `Invalid/unknown command` (sem diferenciar maiúsculas) na completa = módulo de filas ausente: `QueuesListed{Full: true, Unsupported: true}`, log Info uma vez por sessão, `queues.unsupported = true`. Qualquer outro erro (ou prazo): `queues.refresh_errors++`, log Warn com `fila` (vazio na completa) e `erro`; o estado continua como estava (nada é apagado por erro). Queda da conexão durante a leitura segue o caminho normal de reconexão.

---

## 12. Pacotes Go

Módulo único. Dependências externas: `modernc.org/sqlite`, `gopkg.in/yaml.v3`, `golang.org/x/crypto/bcrypt`, `github.com/coder/websocket`. Cliente AMI próprio (protocolo de texto simples; nenhuma lib AMI). Fora isso, só stdlib.

```
cmd/painel-ramais/     main: serve e CLI; supervisor da conexão AMI (Adapter); amostrador de histórico; SIGHUP
cmd/amisim/            simulador de AMI (modos sip e pjsip)
cmd/loadtest/          teste de carga contra o simulador (`-heap` exige o daemon compilado com `-tags debug`)internal/api/          HTTP, sessão, rotas da tela e dos grupos, hub WebSocket (projeção, ring buffer, fan-out), /debug
internal/web/          tela embutida no binário (`//go:embed static`); o conteúdo de `static/` é do frontend
internal/state/        estado em memória, correlação de canais, versão, epoch, lotes de 250 ms
internal/model/        tipos: Extension, Call, enums, eventos normalizados, Batch, Reset
internal/ami/          conexão, framing, login, allowlist de actions, leitura sem bloqueio
internal/ami/calls/    tradução dos eventos de chamada (comum a sip e pjsip)
internal/ami/queues/   rc.8: tradução dos eventos de fila e leitura da QueueStatus (comum a sip e pjsip)
internal/ami/sip/      adaptador chan_sip
internal/ami/pjsip/    adaptador chan_pjsip
internal/history/      amostrador por minuto (gap, retenção); importa model e store
internal/reglog/       rc.7: gravador do relatório de registro (fila, lote, retenção de 30 dias); importa model e store
internal/store/        SQLite: usuários, sessões, grupos, histórico, migrações
internal/auth/         bcrypt, token HMAC, cache de jti, geração de id de sessão
internal/config/       leitura e validação do config.yaml
internal/depcheck/     verificação de dependências (arquivos, resultado de login AMI, chave do Issabel e item do menu no `menu.db`, lido com o driver sqlite do store)
instalar.py            instalador (raiz do repositório; Python 3.6, stdlib)
tools/bundle.py        monta dist/painel-ramais-<versao>.tar.gz (modo 0755 e LF garantidos; seção 15.1); tools/check-lf.ps1
packaging/             nfpm.yaml, unit, scripts do RPM, httpd.conf.tmpl, config.yaml.example, LEIAME-INSTALACAO.md, grupos-exemplo.csv
```

Grafo de importação interna (uma linha por pacote; `->` lista o que ele importa). Verificado sem ciclo por script (seção 14).

```deps
cmd/painel-ramais -> internal/api, internal/state, internal/model, internal/ami, internal/ami/sip, internal/ami/pjsip, internal/ami/queues, internal/store, internal/auth, internal/config, internal/depcheck, internal/history, internal/reglog, internal/web
internal/api -> internal/state, internal/model, internal/store, internal/auth, internal/depcheck
internal/state -> internal/model
internal/ami/sip -> internal/ami, internal/ami/calls, internal/model
internal/ami/pjsip -> internal/ami, internal/ami/calls, internal/model
internal/ami/calls -> internal/ami, internal/model
internal/ami/queues -> internal/ami, internal/model
internal/ami ->
internal/model ->
internal/history -> internal/model, internal/store
internal/reglog -> internal/model, internal/store
internal/store ->
internal/auth ->
internal/config ->
internal/depcheck ->
internal/web ->
internal/amisim -> internal/capture
internal/capture ->
cmd/amisim ->
cmd/loadtest ->
```

Regras:
- Direção única: `cmd -> api -> state -> model <- ami/{sip,pjsip} -> ami`. `state` não conhece AMI nem HTTP; `ami` não conhece `model`.
- Folhas (`model`, `store`, `auth`, `config`, `depcheck`, `ami`) não importam nenhum pacote interno. `store` devolve os próprios tipos de linha; o `cmd` converte para `model` (por exemplo `store.Range` para `model.GroupRange`).
- A interface `Adapter` (duas implementações: `sip` e `pjsip`) é declarada em `cmd/painel-ramais`, onde é consumida: `Snapshot(ctx, *ami.Client) error` emite os eventos da sincronização; `Reread(ctx, *ami.Client, reason string) (fresh int, err error)` rele o inventário (seção 7.4); `Translate(ami.Message) []model.Event` traduz um evento ao vivo; `EndpointFromChannel(string) (string, bool)`.
- Detecção de tecnologia (`CoreSettings` e `ModuleCheck`) fica em `ami` e devolve texto (`"sip"`, `"pjsip"`); o `cmd` escolhe o adaptador.
- `internal/web` é folha: só embute os arquivos; o conteúdo de `static/` é do frontend e as rotas que o servem são de `internal/api` (opção `Static`).
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
| S11 | rc.7: o Asterisk 18 do Issabel manda `Event: Shutdown` (privilégio `system`) antes de parar e o `eventfilter = Event: Shutdown` o deixa passar; `FullyBooted` chega depois do Login e depois do boot | no servidor: `asterisk -rx "core restart gracefully"` com o painel ligado; `reg_system_events` deve ganhar `shutdown`, `ami_connected` e `fully_booted` | backend |
| S12 | rc.7: `ContactStatus` traz a porta no `ViaAddress` (ou no `URI`) e `PeerStatus` no `Address` (`ip:porta`) | no servidor: `reg_events.port` preenchido depois de um telefone re-registrar; vazio não quebra nada | backend |
| S10 | Nomes dos eventos AMI de recarga no Asterisk 18 do Issabel (`Reload`, `ModuleLoadReport`, `FullyBooted`, outro?) e se passam pelo `eventfilter`; se criar ou apagar ramal no IssabelPBX (Apply Config) gera `Reload` e quantos; texto exato da resposta de lista vazia (`No endpoints found`, `No AORs found`, `No contacts found`); se `EndpointList` traz `Callerid`; outros endpoints internos além de `dummy_endpoint` | captura da homologação: criar, renomear e apagar um ramal e aplicar a configuração com `capturar` rodando | backend |
| S13 | rc.8: `DialBegin` traz `DialString` com o destino do Dial (`num@tronco` no PJSIP, `tronco/num` no SIP, `g0/num` no DAHDI) | chamada de saída no servidor: a linha de "Externas saintes" mostra o número discado; sem o campo, mostra o `Exten` do ramal | backend |
| S14 | rc.8: depois de transferência assistida e cega, o canal que fica recebe o `Linkedid` da chamada mais antiga e um evento seguinte (`BridgeEnter`, `Newstate`, `NewConnectedLine`) o traz | transferir uma entrante de A para B: a linha mantém o `id`, mostra B, e a linha interna A para B some | backend |
| S15 | rc.8: nomes, campos e privilégio `agent` dos eventos de fila (seção 7.2, 11.6) e formato da resposta da `QueueStatus` (`QueueParams`, `QueueMember` com `Location`, `QueueEntry` com `Wait`, `QueueStatusComplete`) no Asterisk 18; `QueueStatus` sem privilégio de escrita | `instalar.py homologar` (`manager show event QueueCallerJoin` e os demais, `manager show command QueueStatus`) e uma ligação para uma fila com `capturar` rodando | backend |
| S16 | rc.8: interface dos membros no Issabel (`Local/2001@from-queue/n`, `StateInterface` `hint:2001@ext-local` ou `PJSIP/2001`) | fila com agentes no servidor: a coluna de ramal dos agentes vem preenchida | backend |
| S17 | rc.8: nome de canal DAHDI, IAX2 e Khomp (regra do tronco da seção 2.3) | chamada por cada tronco existente: o rótulo do tronco sai curto e legível | backend |

---

## 14. Verificação deste documento

Script de conferência (rodado no scratchpad, fora do repositório): extrai os blocos `json`, `sql` e `deps` deste arquivo e confere que (a) todo bloco JSON faz parse, (b) cada endpoint e mensagem (snapshot, delta, hello, reload, health, history, groups, login, logout, token) tem exemplo, (c) a DDL roda num SQLite em memória e o trigger rejeita faixa sobreposta, (d) o grafo não tem ciclo e as folhas não importam pacote interno (o bundle agora é `tools/bundle.py`, fora do grafo Go). As seções 15 a 17 (instalador, critérios, documentação) são texto e tabela; a conferência delas é a leitura contra o adendo. Resultado registrado no retorno do tech-lead de 2026-10-13.

rc.7 (2026-10-19): as seções 2.1, 4.6, 4.7, 9, 10 (versão 4), 11.5 e 12 foram conferidas **só por leitura**, sem executar nada (regra do usuário: nenhum teste no PC dele; a validação é ao vivo no servidor). A DDL da versão 4 não foi rodada em SQLite; o @revisor a confere por leitura contra a `ddlV4` do código, e a primeira subida no servidor é a prova (se falhar, a migração é uma transação: o banco continua na versão 3, o daemon não sobe e o `instalar.py rollback` reinstala a rc.6 sem precisar do `.bak-v3`).

rc.8 (2026-10-19): as seções 0 (D11, D12), 1, 1.2, 2.2 a 2.4, 4.1, 4.4, 4.8, 5.2, 7.1, 7.2, 9, 10 (versão 5 e gravação em sublotes), 11.1, 11.3, 11.4, 11.5, 11.6, 12, 13, 15.5 e 15.7 foram conferidas **só por leitura** (D12: não há mais teste automatizado no repositório; nada foi executado). Os 32 blocos JSON do documento fazem parse (script `node` de leitura do próprio documento, sem tocar no produto); o @revisor confere `json.Marshal` dos tipos novos contra eles e a `ddlV5` contra o bloco SQL. A DDL da versão 5 não foi rodada.

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
- Conferência no desenvolvimento: `vermin --target=3.6- instalar.py tools/bundle.py` sem violação (resultado em 2026-10-13: mínimo 3.5, venv em `.tools/venv`). **Exceção de tamanho:** o `instalar.py` passa de 800 linhas (cerca de 980). Não foi dividido porque o instalador precisa funcionar a partir de um único arquivo executável copiado do bundle, com a primeira linha poliglota (`sh`/`python`); dividir em módulo auxiliar mudaria o `sys.path` do `platform-python`. Aceito como exceção. (rc.8: os testes em `tests/` foram removidos, D12; a conferência é `vermin` e `ast.parse`, sem executar.)
- Primeiras linhas poliglotas: o `sh` executa o próprio arquivo com `python3` se existir, senão com `/usr/libexec/platform-python` (presente no Rocky 8 mínimo); para o Python as mesmas linhas são uma string sem efeito. Forma de referência: em 2026-10-13 o `sh` do Git Bash escolheu o `python3` falso quando existia e o `platform-python` falso quando não, repassando os argumentos, e o Python 3.12 executou o arquivo; falta confirmar no Rocky 8 mínimo (A7):

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
2. Coleta **todas** as respostas antes de mudar qualquer coisa: tecnologia (`auto` por padrão) e CSV de grupos (opcional; o arquivo precisa existir). Não pergunta senha.
3. Pacote:
   - não instalado: `rpm -i`;
   - mesma versão: `rpm -U --replacepkgs` (reaplica o `%post`, que é idempotente);
   - versão mais antiga instalada: move `/var/lib/painel-ramais-instalador/rpm-instalado/` (root 0700, fora do diretório do serviço) para `rpm-anterior/` (substitui o que havia) e roda `rpm -U`. Sem RPM guardado (pacote instalado à mão): AVISO "rollback indisponível para esta versão" e segue;
   - depois de instalar, copia o RPM do bundle e a saída de `painel-ramais version` (`versao.txt`) para `rpm-instalado/` (root 0700), junto com o `SHA256` do RPM. Antes de instalar, o instalador confere o RPM do bundle contra o `SHA256SUMS` do bundle e `rpm -qp --qf %{NAME}` = `painel-ramais`.
4. `technology:` no `config.yaml`: reescreve a linha só se o valor mudou, com cópia `config.yaml.bak-<data>` antes.
5. Senha de emergência: se `/etc/painel-ramais/admin-emergencia` não existe, gera 24 caracteres (`secrets.token_urlsafe(18)`) e grava o arquivo (root 0600, `O_EXCL`); entrega a senha pelo stdin a `user add emergencia --password-stdin`; código 3 com arquivo recém-criado: `user passwd emergencia --password-stdin`; código 3 com arquivo que já existia: segue.
6. Grupos, se houver CSV: `runuser -u painel-ramais -- painel-ramais group import --replace -` (o root lê o CSV e o entrega pelo stdin; nenhum arquivo é criado em `/var/lib/painel-ramais`). O CSV do instalador é a fonte da verdade: rodar de novo com o mesmo arquivo dá o mesmo resultado.
7. `systemctl enable --now painel-ramais`; `systemctl restart painel-ramais` se a tecnologia mudou.
8. Espera até 60 s por `GET http://127.0.0.1:<porta>/api/health` com `ami.connected = true` e `extensions.total > 0`, consultando a cada 2 s.
9. Testa o Apache: `GET https://127.0.0.1/painel-ramais/api/session` precisa responder `401` com o envelope `unauthorized` do daemon; `GET https://127.0.0.1/painel-ramais/api/health` precisa responder 404 do próprio Apache, sem JSON.
10. Resumo: versão, tecnologia, ramais por estado, quantidade de grupos, URL `https://<endereço do servidor>/painel-ramais/` e caminho do log.

Upgrade que muda o esquema do banco (rc.4 para rc.5: schema 2 para 3, tabelas `extension_groups` e `group_audit`, seção 10): o daemon faz a migração na primeira subida, com o backup `painel.db.bak-v2` antes; `grupos`, usuários, sessões e histórico existentes são preservados e `group import --replace` do instalador continua valendo (a rc.5 também apaga as atribuições manuais no replace, seção 9). O `versao.txt` do RPM instalado registra `schema 3`.

Upgrade rc.6 para rc.7 (schema 3 para 4, seção 10, versão 4): uma migração só, com o backup `painel.db.bak-v3` antes; vínculos manuais com grupo migram sem perda, "Sem grupo" manual é descartado (log Warn com a lista), `reg_meta.since` recebe a hora do upgrade. O `%post` regrava o bloco AMI com `eventfilter = Event: Shutdown` e faz `manager reload`. O `group import --replace` do instalador continua valendo (CSV antigo com `Sem grupo,N` é aceito e a linha ignorada). O `versao.txt` registra `schema 4`.

Upgrade rc.6 para rc.8 (o caso do usuário: a rc.7 não foi instalada; schema 3 para 5): **uma** subida do daemon aplica 3 para 4 e 4 para 5, cada uma na sua transação, com **um** backup só, `painel.db.bak-v3` (seção 10, notas da versão 5); tudo o que a rc.7 fazia no upgrade vale aqui (vínculos manuais migram, "Sem grupo" manual descartado com log Warn, `reg_meta.since` = hora do upgrade). O `%post` regrava o bloco AMI com `read = system,call,agent`, o `eventfilter = Event: Shutdown` e os onze eventos de fila (seção 7.2) e faz `manager reload`. CSV do instalador com o nome `Todos os ramais` passa a ser recusado (seção 9). O `versao.txt` registra `schema 5`. De rc.7 para rc.8: só 4 para 5, backup `painel.db.bak-v4`.

Falha do passo 3 em diante: não desfaz nada, mostra o passo, o erro e o comando para diagnosticar (`journalctl -u painel-ramais -n 50`, `./instalar.py status`, `httpd -t`), sai com 2.

Idempotência:

| Rodar de novo | Resultado |
|---|---|
| mesmo bundle | `rpm -U --replacepkgs`; arquivo da senha de emergência e usuário `emergencia` mantidos; grupos iguais (replace com o mesmo CSV); `config.yaml` intocado se a tecnologia é a mesma |
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

Rollback da rc.7 (schema 4) para a rc.6 (schema 3): o passo 2 detecta o banco mais novo e o passo 5 restaura **automaticamente** o `painel.db.bak-v3` (o banco 4 fica guardado como `painel.db.rollback-<data>`). Antes da confirmação o plano mostra, com todas as letras: "o banco volta ao estado de antes da atualização; vínculos de ramal em vários grupos, o relatório de registros, e todo histórico, sessão, usuário e grupo gravados depois da atualização serão PERDIDOS". A rc.6 nunca abre banco 4: `store.migrate` recusa iniciar com "banco com esquema 4, este binario conhece ate 3 (downgrade nao suportado)" (conferido por leitura na tag `v0.1.2-rc.6`, `internal/store/store.go:66-67`). Sem o `.bak-v3`, o rollback sai com 2 sem mexer em nada (regra já existente).

Rollback da rc.8 (schema 5) para a rc.6 (schema 3), o caso do usuário: igual ao anterior, restaurando o `painel.db.bak-v3`. O `instalar.py` muda **só** a condição e o texto do aviso específico: condição `sch_ant == 3 and sch_db >= 4` (era `sch_db == 4`); texto "ATENCAO: voltando para a rc.6 (esquema 3); o banco esta no esquema N. O banco volta ao estado de antes da atualizacao (painel.db.bak-v3). Os vinculos de ramal em mais de um grupo, o relatorio de registros, os nomes de fila e tudo o que foi gravado depois da atualizacao (historico, sessoes, usuarios e grupos) serao PERDIDOS." (`N` = `sch_db`; a lista de perdas da rc.7 continua, acrescida de "os nomes de fila"). O aviso genérico que vem depois continua. A rc.6 nunca abre banco 4 ou 5 (recusa na subida). Rollback da rc.8 para a rc.7 (quem instalou a rc.7 antes): restaura `painel.db.bak-v4` pelo caminho genérico (perde os nomes de fila e o que foi gravado depois).

Um nível só de rollback. Voltar da rc.5 (schema 3) para a rc.4 (schema 2) restaura `painel.db.bak-v2`: as atribuições manuais de ramal, a auditoria de grupos e todas as alterações de grupo, usuário e histórico feitas depois do upgrade são **perdidas** (o binário da rc.4 recusa abrir banco schema 3). O plano do rollback avisa isso quando detecta schema mais novo.

### 15.8 `remover`

- Confirmação (ou `--sim`), depois `rpm -e painel-ramais` (o `%preun`/`%postun` tiram bloco AMI e conf do Apache; ADR-003).
- `--purge`: depois do `rpm -e`, pede que o operador digite o nome do servidor (`hostname`), o que evita apagar o servidor errado num laço ssh; com `--sim`, dispensa. Apaga `/var/lib/painel-ramais` (inclui RPMs guardados e backups do banco) e `/etc/painel-ramais`. Mantém `/var/log/painel-ramais` (registro da própria remoção) e o usuário de sistema (`userdel painel-ramais` documentado no LEIAME).

### 15.9 Arquivo de respostas (`--respostas`)

```ini
[painel]
tecnologia = auto
grupos_csv = /root/grupos.csv
```

- `configparser` da stdlib. Seção e chaves exatamente estas; chave desconhecida sai com 3 (as chaves `admin_usuario` e `admin_senha_arquivo` da fase 1 continuam conhecidas, só são ignoradas).
- `tecnologia`: `auto`, `sip` ou `pjsip`. `grupos_csv` vazio ou ausente: não importa grupos.
- `admin_usuario` e `admin_senha_arquivo` (fase 1) passam a ser ignoradas com AVISO e não falham: a senha de emergência é gerada pelo instalador (seção 15.5, passo 5). A senha nunca vai na linha de comando (aparece no `ps`) nem no log.

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
| C5 | `rpm -i`, `rpm -U` e `rpm -e` em Rocky 8 com o simulador no lugar do Asterisk | especificação | sim |
| C6 | Atualização simulada do Issabel (regera `manager.conf`, reinicia httpd e Asterisk): serviço segue, reconecta sozinho, verificação de dependências acusa o que sumiu | especificação | sim |
| C7 | Todo `.sh`, `.service`, `.conf`, `.yaml` (e `.py`, `.ini`) do repositório com fim de linha LF | especificação e adendo | não |
| A1 | `./instalar.py verificar` em Rocky 8 mínimo com o simulador | adendo | sim |
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

Infraestrutura de teste que os critérios com Linux pedem (a desenhar pelo qa/devops, sem decisão aqui): imagem Rocky 8 mínima com systemd como init; httpd com e sem `mod_proxy_wstunnel`; um `asterisk` falso que responde aos `-rx` usados pelo `verificar` e pelo `%post`, mais o simulador na 5038; um marcador de "Issabel 5 presente" (S9); `manager.conf` e `manager_custom.conf` de teste. Montar a imagem baixa pacotes dentro do container: precisa de ok do usuário.

Suposição nova, **S9**: o nome do pacote que marca "Issabel 5 presente" (candidatos `issabel-framework` ou `issabelPBX`) precisa ser confirmado num servidor real; até lá o `verificar` aceita qualquer um dos dois.

---

## 17. Estrutura planejada da documentação (o devops escreve o texto final)

`README.md` (raiz do repositório, português):
1. O que é e o que não é (somente leitura; fase 1 sem tela)
2. Requisitos: Rocky Linux 8 com Issabel 5, Asterisk 13 ou mais, SIP ou PJSIP
3. Instalação em campo com o bundle e o `instalar.py` (aponta para o `LEIAME-INSTALACAO.md`)
4. Instalação manual com `rpm -i` e o que o `%post` faz em cada arquivo
5. Configuração (`config.yaml`, tabela da seção 6)
6. Usuários e grupos pela CLI; formato do CSV
7. Token de teste
8. Formato do snapshot e dos deltas para a fase 2 (resumo e link para as seções 2, 4 e 5 deste contrato)
9. Atualização, rollback, remoção e limpeza total
10. Solução de problemas: `/api/health` e `deps`, `journalctl`, AMI não conecta, include ausente, `httpd -t` falhou, SELinux, cookie `Secure` em `http://`, porta ocupada, banco com esquema mais novo que o binário
11. Desenvolvimento: alvos `build`, `rpm`, `bundle`, `all`, `release` do `build.ps1` (rc.8: o alvo `test` saiu, D12), simulador, teste de carga (só na VM), LF e `.gitattributes`, `vermin`

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
