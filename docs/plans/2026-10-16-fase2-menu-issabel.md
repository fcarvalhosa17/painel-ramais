# Plano da Fase 2: tela, menu do Issabel e acesso de emergência

**Data**: 2026-10-16
**Decisão de arquitetura**: `docs/adr/ADR-004-tela-e-menu-do-issabel.md` (Proposto). Base: ADR-001 (WebSocket), ADR-002 (sessão e token), ADR-003 (pacote), `docs/CONTRATOS.md`.
**Leitura da VM**: `docs/fase2/leitura-vm.txt` chegou em 2026-10-16; os valores reais estão na seção 8 e no ADR-004 ("Leitura da VM").

Regra deste plano: cada arquivo tem um dono só. Ninguém edita arquivo de outro agente; precisou, pede ao gerente. Contrato novo entra aqui primeiro e depois o backend o leva para o `docs/CONTRATOS.md` (tarefa B11).

---

## 1. Donos de arquivo

| Agente | Arquivos (cria ou altera) | Não toca |
|---|---|---|
| **backend** | tudo em `internal/` **exceto** `internal/web/static/**`; tudo em `cmd/` (inclui `cmd/loadtest` e o novo `cmd/telalocal`); todos os `*_test.go`; `go.mod`/`go.sum` (sem dependência nova); `docs/CONTRATOS.md` | `internal/web/static/**`, `tools/`, `instalar.py`, `tests/`, `packaging/`, `README.md` |
| **frontend** | `internal/web/static/**` (HTML, CSS, JS, SVG da tela e da página de aviso); `tools/tela/**` (testes `node --test` e o script de medição) | qualquer `.go`, `docs/CONTRATOS.md` |
| **devops** | `instalar.py` (inclui o modo do arquivo no índice do git), `tests/test_instalar.py`, `packaging/**`, `README.md`, `docs/homologacao/ROTEIRO-MANUAL.md` se precisar | `.go`, `internal/web/static/**`, `docs/CONTRATOS.md` |
| tech-lead / gerente | `docs/adr/**`, `docs/plans/**`, `docs/homologacao/pendencias.md` | código |

Ponto de encontro sem sobreposição: o embed. O backend escreve `internal/web/web.go` (diretiva `//go:embed`) e as rotas; o frontend é dono só do conteúdo de `internal/web/static/`. O `go:embed` não compila sem os arquivos, por isso a tarefa F0 (esqueleto) vem antes de tudo e é curta. Os testes do backend não dependem dela (usam `fstest.MapFS`).

Não tocar nesta fase (esperam o pacote de homologação): pendência "outro lado sem ramal", privilégios AMI ilegíveis (Asterisk 18) e `ContactStatus` sem `UserAgent`. Esperam a VM: seção 8.

---

## 2. Comandos que funcionam neste repositório

- Go (Git Bash, raiz do repo): `export GOPATH=$PWD/.tools/gopath GOCACHE=$PWD/.tools/gocache GOMODCACHE=$PWD/.tools/gopath/pkg/mod GOTOOLCHAIN=local; ./.tools/go/bin/go.exe vet ./... && ./.tools/go/bin/go.exe test ./...` (sem `-race`: não há cgo). `gofmt -l cmd internal`.
- Build de release (o que vai no RPM): `.\build.ps1 build` gera `dist/painel-ramais` (linux/amd64, `-s -w`, **sem build tags**).
- Python: `.tools/venv/Scripts/python.exe -m unittest discover -s tests -t .`; compatibilidade 3.6 com o vermin (ver `.claude/agent-memory/backend`).
- Node 22.14: `"/c/Program Files/nodejs/node.exe" --test "tools/tela/*.test.mjs"` (no Git Bash o `node` pode faltar no PATH; use o caminho completo ou o PowerShell).
- Playwright 1.60 (já instalado, não instalar outro): `require("D:/CLaude/leadpronto/node_modules/playwright")`; Chromium headless padrão funciona; WebKit precisa de `executablePath: "<LOCALAPPDATA>/ms-playwright/webkit-2336/Playwright.exe"` (barras normais).

---

## 3. Contratos

### C1. Token, chave do Issabel e papel

Formato do token e validação: ADR-002 itens 12 a 16, sem mudança (payload com exatamente `sub`, `exp`, `jti`; TTL máximo 60 s mais 5 s de folga; `jti` de uso único; falha = `401 invalid_token`).

Chave do Issabel:
- Caminho: configuração `auth.issabel_key_file`, padrão `/etc/painel-ramais-issabel.key`.
- Conteúdo: base64 (padrão, com `=`) que decodifica em 32 bytes ou mais; espaços nas pontas descartados. A chave HMAC são os **bytes decodificados** (igual ao `secret`).
- Permissão aceita: arquivo regular (não symlink; `Lstat`), dono = euid do processo, `mode.Perm() &^ 0o640 == 0`. No Windows (desenvolvimento) só existe a checagem de existência, como `checkSecretPerm`.
- Chave com os mesmos bytes do `auth.secret_file`: recusada (`issabel_key` = `fail`), porque anularia a separação entre token da CLI e token do Issabel.

Gramática do `sub`:

| Assinado com | `sub` aceito | Papel da sessão |
|---|---|---|
| chave do Issabel | `issabel:admin:<usuario>` | administrador |
| chave do Issabel | `issabel:user:<usuario>` | operador |
| segredo do daemon (`auth.secret_file`) | qualquer texto de 1 a 128 caracteres que **não** comece com `issabel:` (sem diferenciar maiúsculas) | operador |

`<usuario>`: 1 a 64 caracteres (runas), sem caractere de controle nem das categorias Unicode `Cf`, `Zl` e `Zp` (a mesma regra vale no emissor PHP), pode conter `:`. Total do `sub` continua 1 a 128 bytes. Papel administrador = `kind == "token"` e `subject` com prefixo exato `issabel:admin:`. Nenhuma coluna nova no banco.

Ordem de verificação: formato e tamanho; HMAC contra a chave do Issabel (se carregada) e contra o segredo do daemon, ambos com `hmac.Equal`; JSON estrito; regra do `sub` da tabela conforme a chave que casou; `exp`; `jti`. Chave do Issabel ausente ou recusada pela permissão: só a linha do segredo do daemon vale.

Vetores de teste (chaves de exemplo, nunca em produção). `exp = 1791902460` (2026-10-13T14:41:00Z); testes usam `Now` fixo antes disso.

| Item | Valor |
|---|---|
| conteúdo de `auth.secret_file` | `AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=` (bytes 0x00 a 0x1f, o de sempre) |
| conteúdo de `auth.issabel_key_file` | `ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=` (bytes 0x20 a 0x3f) |
| V1 payload | `{"sub":"issabel:admin:fabio","exp":1791902460,"jti":"q9Xh3c1bV0e8m2Lr5tYwZA"}` |
| V1 assinado com a chave do Issabel: **aceito, administrador** | `eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.BvaFo9ur4_kzEeYjvM0EiRPhpy_6qmlZsU7nio54VU4` |
| V1 assinado com o segredo do daemon: **recusado** (prefixo reservado) | `eyJzdWIiOiJpc3NhYmVsOmFkbWluOmZhYmlvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.PbqEoW5r-kgT30bYkuhechQCldj_pGcp1iUCVi9haRQ` |
| V2 payload | `{"sub":"issabel:user:maria","exp":1791902460,"jti":"r8Wg2b0aU9d7l1Kq4sXvYB"}` |
| V2 assinado com a chave do Issabel: **aceito, operador** | `eyJzdWIiOiJpc3NhYmVsOnVzZXI6bWFyaWEiLCJleHAiOjE3OTE5MDI0NjAsImp0aSI6InI4V2cyYjBhVTlkN2wxS3E0c1h2WUIifQ.Q4LwgNiSLChzFaL2PT8hSU-vtRnAGdAUcacuuCe6MYc` |
| V3 payload | `{"sub":"roteiro","exp":1791902460,"jti":"s7Vf1a9ZT8c6k0Jp3rWuXC"}` |
| V3 assinado com o segredo do daemon: **aceito, operador** | `eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.CpvxeT22yxalo6wlbU5qUNnNvdADVR9DfzJ31NA2Yfc` |
| V3 assinado com a chave do Issabel: **recusado** (sem prefixo) | `eyJzdWIiOiJyb3RlaXJvIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJzN1ZmMWE5WlQ4YzZrMEpwM3JXdVhDIn0.DqPJ2WjOE9vIsaS0PehNvhUPxX8OSDdU_6DqhxD6QXY` |
| V0 (vetor da fase 1, `sub` `issabel:admin` com o segredo do daemon): **agora recusado** | `eyJzdWIiOiJpc3NhYmVsOmFkbWluIiwiZXhwIjoxNzkxOTAyNDYwLCJqdGkiOiJxOVhoM2MxYlYwZThtMkxyNXRZd1pBIn0.1FevVYbrsil2HqMU283VGL948fcwdxThKkYL6xtxxq0` |

Os tokens acima foram calculados com Node `crypto.createHmac('sha256', bytes)` sobre o texto base64url do payload; o V0 bate com o vetor que já está no `CONTRATOS.md` 3.3, o que confere o cálculo.

Emissor PHP (depois da VM, devops): mesma receita. `hash_hmac('sha256', $p64, base64_decode(trim(file_get_contents($arq)), true), true)`, `jti` de 16 bytes de `random_bytes` em base64url sem `=`, TTL de 60 s, `sub` pela tabela. O teste do módulo usa V1 e V2.

### C2. Rotas novas e alteradas no daemon

O Apache já repassa tudo de `/painel-ramais/` (sem mudança na conf). Prefixo do navegador fixo: `/painel-ramais/` (o mesmo do `Path` do cookie).

| Método | Rota no daemon | Navegador | Sessão | Resposta |
|---|---|---|---|---|
| GET | `/` (exato) | `/painel-ramais/` | não | tela ou 303 (C3) |
| GET | `/acesso` | `/painel-ramais/acesso` | não | HTML de aviso, sem script |
| GET | `/emergencia` | `/painel-ramais/emergencia` | não | `301` para `/painel-ramais/emergencia/` |
| GET | `/emergencia/` (exato) | `/painel-ramais/emergencia/` | não | a mesma `index.html` da tela, cabeçalhos de emergência (C3) |
| GET | `/assets/<arquivo>` | `/painel-ramais/assets/<arquivo>` | não | arquivo de `internal/web/static/assets/`; só `.js`, `.css`, `.svg`; outro caso `404 not_found` (envelope) |
| GET | `/api/session` | idem | sim | envelope com `sessionData` (C5) |
| POST | `/api/login` | idem | não | só aceita o usuário `emergencia` (C5) |
| POST | `/api/token` | idem | não | regra do C1; resposta ganha `admin` |
| POST | `/api/groups/import` | idem | sim, administrador | C6 |
| POST | `/api/groups/delete` | idem | sim, administrador | C6 |

Toda rota `POST` recusa `Sec-Fetch-Site: cross-site` com `403 forbidden` (cabeçalho ausente segue a regra atual).

Sem mudança: `/api/logout`, `/api/snapshot`, `/api/groups` (GET), `/api/history`, `/ws`, `/api/health` e `/debug` (só localhost). Qualquer outra rota segue `404 not_found` em envelope.

Arquivos servidos: `internal/web/static/index.html` (tela), `internal/web/static/acesso.html` (aviso), `internal/web/static/assets/*`. A diretiva do backend embute `static` inteiro; arquivo começando com `.` ou `_` não entra no embed (regra do Go), então não use esses nomes. Content-Type definido pelo backend pela extensão, sem depender do `/etc/mime.types`: `.js` `text/javascript; charset=utf-8`, `.css` `text/css; charset=utf-8`, `.svg` `image/svg+xml`, `.html` `text/html; charset=utf-8`.

### C3. Bloqueio fora do Issabel e cabeçalhos

`GET /` decide pelo `Sec-Fetch-Dest`:

| `Sec-Fetch-Dest` | Resposta |
|---|---|
| `iframe` | `200`, `index.html`, cabeçalhos "tela" |
| ausente | `200`, `index.html`, cabeçalhos "tela" (navegador antigo; o script confere `window.top`) |
| qualquer outro (`document`, `empty`, ...) | `303`, `Location: /painel-ramais/acesso#`, corpo vazio, `Cache-Control: no-store` |

O `#` vazio no `Location` é obrigatório: impede que o fragmento `#t=<token>` da URL original seja herdado pelo redirecionamento.

Cabeçalhos (além do `X-Content-Type-Options: nosniff` e `X-Request-Id`, que já saem em toda resposta):

| Página | Cabeçalhos |
|---|---|
| tela (`/`) | `Content-Type: text/html; charset=utf-8`; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`; `Vary: Sec-Fetch-Dest`; `X-Frame-Options: SAMEORIGIN`; `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'` |
| emergência (`/emergencia/`) | igual à tela, trocando `frame-ancestors 'self'` por `frame-ancestors 'none'` e `X-Frame-Options: DENY`; sem `Vary` (não olha o `Sec-Fetch-Dest`) |
| aviso (`/acesso`) | `Content-Type: text/html; charset=utf-8`; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`; `X-Frame-Options: SAMEORIGIN`; `Content-Security-Policy: default-src 'none'; style-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'` |
| assets | Content-Type do C2; `Cache-Control: no-cache`; `.svg` também com `Content-Security-Policy: default-src 'none'` |

Consequências para a tela: nada inline (nem `<script>`, nem `<style>`, nem `style="..."` no HTML, nem `on*=`), nada externo (CDN, Google Fonts), nenhum `eval`/`new Function`. Estilo dinâmico pelo CSSOM (`el.style.x = ...`) é permitido. Fontes do sistema. Texto que vem do Asterisk (nome, número, user agent, nome de grupo) entra só por `textContent`/`createTextNode` ou desenho em canvas, nunca `innerHTML`/`insertAdjacentHTML`/`outerHTML`.

### C4. Fluxo de entrada na tela (contrato frontend com o PHP e com o daemon)

1. O PHP do Issabel (depois da VM) gera `<iframe src="/painel-ramais/#t=<token>" allow="fullscreen" title="Painel de Ramais">`.
2. Na rota `/` (não emergência): se `window.top === window.self`, mostra o aviso "Acesse pelo menu PBX do Issabel." e **para** (não lê o fragmento, não chama API).
3. Veio `#t=`: primeiro `history.replaceState(null, "", location.pathname)`. Fragmento casando `^#t=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$`: `POST /painel-ramais/api/token` com `{"token": "<token>"}`; 200 segue com a sessão. Token recusado (401), malformado (não casa a expressão) ou erro de rede: mostra "Sessão encerrada. Abra de novo pelo menu PBX do Issabel." e **não** chama `/api/session` (o cookie anterior não é reaproveitado quando a entrada pelo menu falhou).
4. Sem fragmento (no iframe): `GET /painel-ramais/api/session`; 200 segue (a sessão válida continua valendo); 401 mostra a mesma mensagem.
5. Rota `/painel-ramais/emergencia/`: sem checagem de `window.top`; 401 em `/api/session` mostra o formulário (usuário fixo `emergencia`, só o campo senha) que faz `POST /painel-ramais/api/login` com `{"username":"emergencia","password":"..."}`; botão "Sair" faz `POST /painel-ramais/api/logout`.
6. Com sessão: `data.admin` do `/api/session` (ou da resposta do token/login) liga a área "Grupos" (C6). Daí em diante, fluxo do ADR-001: `GET /painel-ramais/api/snapshot`, `wss://<host>/painel-ramais/ws` (ou `ws://` se a página for `http:`), `hello`, deltas.
7. Qualquer REST com 401, ou mensagem `session_expired` seguida de 4001: troca o painel pela mensagem do passo 3 (na emergência, pelo formulário).

URLs da API sempre absolutas a partir de `/painel-ramais/` (a tela é servida em dois caminhos).

### C5. Sessão e login

`sessionData` (resposta de `POST /api/login`, `POST /api/token` e `GET /api/session`):

```json
{ "subject": "issabel:admin:fabio", "kind": "token", "admin": true, "expires_at": "2026-10-13T22:40:30.000Z" }
```

- `admin`: `true` só se `kind == "token"` e `subject` começa com `issabel:admin:`.
- `GET /api/session`: exige sessão (renova como qualquer GET autenticado); sem sessão `401 unauthorized`.
- `POST /api/login`: valida o corpo como hoje; se o `username` não for `emergencia` (comparação sem diferenciar maiúsculas, como o `COLLATE NOCASE` da tabela), responde `401 invalid_credentials` depois da comparação bcrypt falsa (mesmo caminho do usuário inexistente), contando no limitador como hoje. Usuário `emergencia`: fluxo atual. Sessão criada: `kind = password`, `subject = emergencia`, `admin = false`.
- Constante do usuário reservado: `emergencia` (backend, `internal/api`); o instalador usa o mesmo texto.
- Limite de tentativas do login: por IP, 5 falhas em 5 min (IP = último valor do `X-Forwarded-For`, que é o que o Apache acrescenta), e por nome, 20 falhas em 5 min; estourou qualquer um, `429 too_many_attempts`.
- Cookie de sessão criada por token (`kind = token`): sem `Max-Age` (cookie de sessão do navegador); o cookie do login de emergência segue como na fase 1.

### C6. Grupos pela tela (só administrador)

Sessão sem papel de administrador: `403 forbidden`, mensagem "Apenas administradores do Issabel podem alterar grupos". As duas rotas aceitam corpo de até 1 MiB (`payload_too_large` acima disso); o resto da API segue com 4 KiB.

`POST /api/groups/import`

```json
{ "csv": "grupo,ramal\nSuporte,2000-2099\nVendas,3000-3999\n", "replace": true }
```

- `csv` obrigatório (texto do arquivo, mesmo formato da seção 9 do contrato), no máximo 20.000 linhas (acima disso, `400 invalid_csv`); `replace` opcional, padrão `false`. Campo desconhecido: `400 bad_request`.
- Validação e gravação: `store.ParseGroupsCSV` e `store.ImportGroups` (tudo ou nada, as mesmas regras da CLI).
- 200: `{"success":true,"data":{"rows":2,"replace":true},"error":null,"meta":null}`.
- CSV inválido ou sobreposição: `400 invalid_csv`, `message` = o texto do erro do parser (até 20 linhas `linha N: ...`), a tela mostra como texto.
- Depois de gravar: log Info `grupos importados` com `sub`, `rows`, `replace`; o daemon relê os grupos pelo mesmo caminho do SIGHUP (`loadGroups`): epoch novo, as telas abertas recebem `reload`.

`POST /api/groups/delete`

```json
{ "name": "Suporte" }
```

- `name` 1 a 64 caracteres. 200: `{"success":true,"data":{"name":"Suporte"},"error":null,"meta":null}`. Inexistente: `404 not_found`, "Grupo inexistente". Depois: log `grupo removido` e o mesmo `loadGroups`.

Leitura: `GET /api/groups` (seção 4.2 do contrato, sem mudança) para listar nomes, faixas e ramais.

Códigos novos no catálogo (seção 1.2): `403 forbidden`; `400 invalid_csv`.

### C7. CLI, configuração e verificação de dependências

CLI (seção 8 do contrato ganha):

```
painel-ramais session purge [--all | --issabel <usuario>]
painel-ramais token --sub <texto> [--ttl 60s] [--issabel]
```

- `session purge --issabel <usuario>`: apaga as sessões com `subject` igual a `issabel:admin:<usuario>` ou `issabel:user:<usuario>`; imprime `N sessão(ões) removida(s)`; se `N > 0`, manda o SIGHUP (os WebSockets dessas sessões fecham com `session_expired` `revoked` e 4001). `--all` e `--issabel` juntos: erro de uso (código 1).
- `token --issabel`: assina com a chave do Issabel (lida com a regra de permissão do C1); o `--sub` precisa seguir a gramática do C1. Sem `--issabel`, `--sub` começando com `issabel:` é erro de uso com a mensagem "use --issabel para sub issabel:". Uso: testes, homologação do fluxo do menu e a medição local.
- `user add|passwd|del`, `group import|list|del`, `capturar`, `filtrar`, `actions`, `version`: sem mudança.

Configuração (seção 6 do contrato ganha uma linha):

| Chave | Tipo | Padrão | Validação |
|---|---|---|---|
| `auth.issabel_key_file` | caminho absoluto | `/etc/painel-ramais-issabel.key` | não vazio e absoluto; o arquivo **pode não existir** (integração desligada); permissão e conteúdo nunca impedem a subida: são conferidos pela verificação de dependências (na subida e a cada 60 s) |

O `config.yaml.example` não muda (o padrão basta).

Itens novos em `/api/health` `deps[]` (o `status` geral segue a regra atual: qualquer item diferente de `ok` deixa `degraded`):

| `name` | `ok` | `fail` | `unknown` |
|---|---|---|---|
| `issabel_key` | arquivo ausente (detalhe: "chave do Issabel ausente: integração com o menu desligada") ou presente e válido ("chave do Issabel válida: integração ligada") | permissão, dono, tipo ou conteúdo inválido; detalhe com o problema e a correção (`chown painel-ramais:<grupo> ...; chmod 0640 ...`) | erro de leitura que não seja "não existe" nem regra violada |
| `manager_custom_perm` | modo sem nenhum bit de outros (detalhe com o modo) | algum bit de outros; detalhe "manager_custom.conf acessível a outros (modo NNN): contém a senha AMI. Rode: chmod o-rwx /etc/asterisk/manager_custom.conf" | `stat` falhou (inclui ausente e `EACCES`); detalhe com o erro. Nunca `ok` |

O item `issabel_key` é a fonte única da chave: a cada verificação (na subida, a cada 60 s e após cada Login AMI, como hoje) o resultado é entregue ao verificador de tokens (chave válida ou `nil`). O daemon só lê o modo do `manager_custom.conf` (`stat`), nunca o altera.

Pendência 6, lado Go: o aviso de `ResolveTechnology` com os dois módulos carregados passa a dizer que é o padrão do Issabel 5 e qual tecnologia o modo auto usa com a contagem, por exemplo `chan_pjsip.so e chan_sip.so carregados (padrão do Issabel 5); modo auto usa pjsip (ramais: pjsip=812, sip=0)`, e o supervisor registra em Info (não é anomalia).

### C8. Dados que a tela consome (lidos do código em 2026-10-16)

Fonte: `docs/CONTRATOS.md` seções 2, 4 e 5, conferidas contra `internal/api/frames.go` e `internal/api/data.go`.

- `GET /api/snapshot` → `data`: `epoch` (string opaca, só igualdade), `version` (inteiro), `server_time` (RFC 3339 UTC com ms), `technology` (`"sip"`, `"pjsip"` ou `null`), `ami_connected` (bool), `extensions[]` ordenado por `number`; `meta.count`.
- Ramal: `number`, `name` (ou null), `technology`, `group` (`"Sem grupo"` quando não casa), `registration` (`registered`, `unregistered`, `unreachable`, `unknown`), `ip` (ou null), `user_agent` (ou null), `latency_ms` (ou null), `device_state` (`unknown`, `not_inuse`, `inuse`, `busy`, `invalid`, `unavailable`, `ringing`, `ringinuse`, `onhold`), `calls[]`.
- Chamada: `id`, `direction` (`internal`, `inbound`, `outbound`), `state` (`ringing`, `talking`, `hold`), `peer` `{kind: extension|external|unknown, number|null, name|null}`, `started_at`, `answered_at` (ou null).
- `/ws`: cliente manda uma vez `{"type":"hello","epoch":"...","version":N}` em até 5 s. Servidor: `{"type":"delta","epoch","from_version","to_version","ami_connected","server_time","extensions":[ramal completo...]}` a cada 250 ms quando houve mudança; `{"type":"reload","reason":"epoch_changed|version_gap|version_ahead","epoch"}` seguido de fechamento 4000; `{"type":"session_expired","reason":"expired|revoked","message"}` seguido de 4001. Aplicação: `from_version <= v < to_version` aplica e `v = to_version`; `to_version <= v` ignora; `from_version > v` ou `epoch` diferente = lacuna: novo snapshot e reconexão. Ramal só some com epoch novo. Fechamentos: 1000/1001 reconecta com backoff; 4000 snapshot e reconecta; 4001 sessão encerrada; 4002 reconecta com hello; 4003 erro do cliente, reconecta depois de 5 s.
- `GET /api/history?from=<RFC3339>&to=<RFC3339>&group=<nome>` → `data.points[]` com `t`, `registered`, `unregistered`, `unreachable`, `in_call` (null quando `gap`), `gap`; `step_s` = 60; intervalo máximo 30 dias; série densa (7 dias = 10.080 pontos).
- `GET /api/groups` → `data[]` com `name`, `ranges[] {start,end}`, `extensions[]`; `"Sem grupo"` por último.
- Relógio: `desvio = Date.parse(server_time) - Date.now()` a cada snapshot e delta; cronômetro = `Date.now() + desvio - Date.parse(answered_at ?? started_at)`.

### C9. Estado exibido, cores e textos

Função pura `estadoDaTela(ramal, amiConectado)`, nesta ordem:

1. `!amiConectado` ou `registration == "unknown"` → `desconhecido`
2. `registration == "unreachable"` → `inalcancavel`
3. `registration == "unregistered"` → `nao_registrado`
4. alguma chamada `talking` → `em_conversa`
5. alguma chamada `hold` → `em_espera`
6. alguma chamada `ringing` → `tocando`
7. sem chamada, pelo `device_state`: `ringing` → `tocando`; `inuse`, `busy`, `ringinuse` → `em_conversa`; `onhold` → `em_espera`
8. senão → `livre`

| Chave | Rótulo (legenda e card) | Variável CSS | Cor de partida |
|---|---|---|---|
| `livre` | livre | `--estado-livre` | `#2E7D32` |
| `tocando` | tocando | `--estado-tocando` | `#F9A825` |
| `em_conversa` | em conversa | `--estado-em-conversa` | `#C62828` |
| `em_espera` | em espera | `--estado-em-espera` | `#6A1B9A` |
| `nao_registrado` | não registrado | `--estado-nao-registrado` | `#616161` |
| `inalcancavel` | inalcançável | `--estado-inalcancavel` | `#EF6C00` |
| `desconhecido` | desconhecido | `--estado-desconhecido` | `#90A4AE` |

O frontend pode ajustar o tom para contraste (amostra de cor com pelo menos 3:1 contra o fundo do card), mas mantém a família de cor, as chaves e os rótulos. A cor nunca é o único sinal: o card mostra o rótulo em texto.

Card (visão de grupo): número; nome; estado (cor e rótulo); outro lado da chamada principal (a primeira por prioridade `talking` > `hold` > `ringing`): `peer.name` e `peer.number` (`kind = extension` mostra "ramal <número>"; `unknown` sem número mostra "desconhecido"), com "+N" se houver mais chamadas; direção (`internal` "interna", `inbound` "entrada", `outbound` "saída"); cronômetro (C8; `h:mm:ss` acima de uma hora, senão `mm:ss`); IP. Sem chamada: outro lado, direção e cronômetro vazios.

Indicadores: total (`extensions.length`); registrados (`registered`); não registrados (`unregistered`); inalcançáveis (`unreachable`); em chamada (ramais com `calls.length > 0`, a mesma regra do `in_call` do histórico); variação de registrados na última hora = registrados agora menos `registered` do primeiro ponto com `gap: false` de `GET /api/history` (sem `group`, `from` = agora menos 60 min), atualizada a cada 60 s, "sem dados" se não houver ponto.

Faixa: `ami_connected == false` no snapshot ou no delta mostra no topo "Conexão com o Asterisk perdida" e todos os ramais em `desconhecido`; some quando chega `true`.

Ranking: até 10 grupos com mais `nao_registrado` (desempate pelo nome), mostrando quantidade e total do grupo; grupo com zero não entra; clique abre o grupo.

### C10. Gancho de medição (`window.__painel`)

Objeto só de leitura, mantido pela tela (sem segredo, sem dado pessoal além de contagens):

```js
window.__painel = {
  pronto: false,                 // true depois da primeira exibição com dados
  primeiraExibicaoMs: null,      // performance.now() do primeiro quadro pintado com o snapshot
  contagens: { total: 0, registered: 0, unregistered: 0, unreachable: 0, in_call: 0 },
  epoch: null, version: 0,
  ultimoDeltaMs: null,           // performance.now() do último quadro pintado depois de um delta
  tarefasLongas: []              // {inicio, duracao} do PerformanceObserver "longtask" (Chromium)
};
```

`primeiraExibicaoMs` é tomado num `requestAnimationFrame` depois de desenhar indicadores e a primeira tela de cards com os dados do snapshot.

---

## 4. Tarefas do backend

Cada tarefa termina com `go vet ./...`, `go test ./...` e `gofmt -l cmd internal` limpos.

| # | Tarefa | Arquivos | Critério verificável |
|---|---|---|---|
| B1 | Verificador com duas chaves, gramática do `sub`, papel (C1); `IssueToken` continua igual | `internal/auth/token.go`, `internal/auth/auth_test.go` | Teste com os vetores V0 a V3 dá exatamente o resultado da tabela do C1; `sub` `issabel:admin:` com usuário vazio, com 65 runas ou com caractere de controle: recusado; o vetor antigo do teste (V0) passa a ser caso negativo |
| B2 | `auth.issabel_key_file` e leitura da chave com a regra de permissão | `internal/config/config.go`, `perm_unix.go`, `perm_windows.go`, `config_test.go` | Teste (build unix, roda no Rocky; no Windows só a parte de conteúdo): 0640 e 0600 aceitos; 0644, 0660 e symlink recusados; conteúdo com menos de 32 bytes recusado; arquivo ausente devolve erro que casa com `errors.Is(err, fs.ErrNotExist)` |
| B3 | `sessionData.admin`, `GET /api/session`, login só para `emergencia` (C5) | `internal/api/authn.go`, `server.go`, `api_test.go` | Testes: login `emergencia` correto dá 200 com `admin:false`; login de outro usuário existente com senha certa dá 401 `invalid_credentials`; token V1 dá `admin:true`, V2 dá `admin:false`; `/api/session` sem cookie dá 401 |
| B4 | Rotas da tela, aviso, emergência e assets com os cabeçalhos do C3; embed | `internal/web/web.go` (novo: `//go:embed static`, expõe um `fs.FS`), `internal/api/server.go` (nova opção `Static fs.FS`), novo `internal/api/tela.go` se quiser separar, `api_test.go` | Testes com `fstest.MapFS`: `GET /` com `Sec-Fetch-Dest: document` dá 303 e `Location` exatamente `/painel-ramais/acesso#`; com `iframe` e sem o cabeçalho dá 200 com a CSP **exata** do C3; `/emergencia/` dá `frame-ancestors 'none'` e `DENY`; `/emergencia` dá 301; `/assets/x.js` dá `text/javascript`; `/assets/`, `/assets/../index.html` e `/assets/x.txt` dão 404 envelope |
| B5 | Itens `issabel_key` e `manager_custom_perm` (C7); a verificação entrega a chave ao verificador | `internal/depcheck/depcheck.go`, `depcheck_test.go`, `cmd/painel-ramais/serve.go` | Teste do depcheck: modo `o+r` dá `fail` com a dica do `chmod`; `o-rwx` dá `ok`; stat falho dá `unknown`. Teste do daemon (ou de integração): sem chave, V1 dá 401 e `issabel_key` `ok`; criar a chave e rodar `Check()` faz V1 (com `jti` novo) passar sem reiniciar |
| B6 | `POST /api/groups/import` e `/api/groups/delete` (C6) e recarga dos grupos | `internal/api/*.go` (nova opção `ReloadGroups func(context.Context) error`), `cmd/painel-ramais/serve.go`, testes | Testes: sessão V2 dá 403; V1 com CSV válido dá 200, grava (conferir pelo `store`) e chama `ReloadGroups` uma vez; CSV com sobreposição dá 400 `invalid_csv` e nada grava; corpo de 1 MiB + 1 dá 413; delete de grupo inexistente dá 404 |
| B7 | `session purge --issabel`, `token --issabel`, uso da CLI | `cmd/painel-ramais/cli_session.go`, `cli_token.go`, `main.go`, `internal/store/store.go` (função de apagar por lista de `subject`), testes | Teste: com sessões `issabel:admin:ana`, `issabel:user:ana` e `issabel:user:bia`, `--issabel ana` remove 2 e chama o notify; `--all --issabel x` sai 1; `token --sub issabel:x` sem `--issabel` sai 1; `token --issabel --sub issabel:admin:ana` gera token que o verificador aceita como administrador |
| B8 | Endpoint de debug do heap fora do binário de release (pendência, decisão 2: build tag) | `cmd/painel-ramais/debug.go` e `debug_test.go` ganham `//go:build debug`; novo `debug_off.go` (`//go:build !debug`, `startDebug` vazio); `cmd/loadtest` (texto do `-heap` diz que o daemon precisa de `-tags debug`; `testUser` passa a `emergencia`) | `.\build.ps1 build` e depois `grep -a -c -E "PAINEL_DEBUG_ADDR|/debug/pprof|/debug/mem|net/http/pprof" dist/painel-ramais` dá 0; o mesmo build com `-tags debug` dá mais de 0 (prova que a busca funciona); `go test -tags debug ./cmd/painel-ramais` passa; `instalar.py status` intocado |
| B9 | Pendência 6, lado Go (C7) | `internal/ami/detect.go`, `detect_test.go`, `cmd/painel-ramais/supervisor.go` | Teste do `ResolveTechnology` com os dois carregados confere o texto novo (padrão do Issabel 5, tecnologia escolhida e as duas contagens); empate continua `pjsip` |
| B10 | Ferramenta local `cmd/telalocal` (Issabel falso para a tela e a medição) | `cmd/telalocal/main.go` (e o que precisar dentro de `cmd/telalocal/`) | Ver seção 6: `curl -s http://localhost:8091/painel-ramais/api/session` dá 401 envelope; `curl -sI -H "Sec-Fetch-Dest: document" http://localhost:8091/painel-ramais/` dá 303; `curl -s http://localhost:8091/dev/token` dá um token que o daemon aceita |
| B11 | Levar C1, C2, C3, C5, C6, C7 para o `docs/CONTRATOS.md` (seções 1, 1.2, 3, 4, 6, 8 e o 15.5 do texto da seção 7 deste plano) | `docs/CONTRATOS.md` | Nenhum formato do plano ausente do contrato; vetor 3.3 trocado pelos V0 a V3; a linha 50 deixa de falar de 404 em `/` |

Ordem sugerida: B1, B2, B3 (base) → B4, B5, B6, B7 → B10 → B8, B9 (independentes, a qualquer momento) → B11.

---

## 5. Tarefas do frontend

Sem framework, sem bundler, sem dependência: HTML, CSS e módulos ES em `internal/web/static/` (ADR-004 item 8). Funções puras (estado, aplicação de delta, filtros, contagens, ranking, formatação do cronômetro) em módulos próprios, testadas com `node --test` em `tools/tela/` importando os arquivos de `internal/web/static/assets/`. Tudo em português. Nenhum teste dentro de `internal/web/static/` (tudo ali vai para o binário).

| # | Tarefa | Critério verificável |
|---|---|---|
| F0 | **Primeiro, e curto**: esqueleto `index.html` (só referências a `/painel-ramais/assets/app.css` e `<script type="module" src="/painel-ramais/assets/app.js">`), `acesso.html` (aviso "Acesse pelo menu PBX do Issabel." com link para `/`, sem script), `assets/app.js`, `assets/app.css` | Os quatro arquivos existem; `go build ./cmd/painel-ramais` compila com o embed do B4 |
| F1 | Núcleo de dados: snapshot, `/ws` com `hello`, aplicação de delta, lacuna/epoch/reload, reconexão com backoff (1 s dobrando até 30 s, variação de 20%), desvio de relógio (C8) | `node --test`: delta aplicado, repetido ignorado, lacuna pede snapshot, epoch diferente pede snapshot, ramal novo inserido, nunca removido por delta |
| F2 | Entrada (C4): aviso fora do Issabel, token do fragmento, sessão, emergência com formulário e "Sair", mensagem de sessão encerrada | Com o B10: abrir `http://localhost:8091/painel-ramais/` direto mostra o aviso (303); pela página do Issabel falso a tela carrega e a barra do iframe fica sem `#t=`; `session purge --issabel medicao` faz a tela mostrar "Sessão encerrada" em até 2 s |
| F3 | Indicadores, variação da última hora, faixa de AMI perdido, ranking (C9) | `node --test` das contagens e do ranking; com o B10, `POST /sim/ctl/down?d=5s` mostra a faixa e todos em "desconhecido", e ela some na volta |
| F4 | Gráfico de linha em canvas de `/api/history` com 1 h, 24 h e 7 d, quebrando a linha nos `gap`, filtrado pelo grupo selecionado | 7 d (10.080 pontos) desenha sem tarefa longa acima de 200 ms; ponto `gap` não liga as linhas |
| F5 | Mapa canvas por grupo: um bloco por grupo, um quadrado por ramal na cor do estado, hover com nome do grupo e contagens, clique abre o grupo; redesenho no máximo 4 por segundo | Hover e clique funcionam com 10.000 ramais; redesenho não passa de 4/s durante a rajada |
| F6 | Visão de grupo estilo FOP: cards de altura fixa com rolagem virtual (só as linhas visíveis no DOM, nós reaproveitados), cronômetro por um único `setInterval` de 1 s só nos cards visíveis, busca (número ou nome, sem diferenciar maiúsculas e acentos) e filtros por estado e grupo, contagem "N de M ramais", legenda | Com 10.000 ramais, no máximo cerca de 200 cards no DOM; rolar até o fim e filtrar não geram tarefa longa acima de 200 ms; `node --test` dos filtros |
| F7 | Tela cheia pela Fullscreen API (botão; no iframe depende do `allow="fullscreen"`) | Botão entra e sai da tela cheia na página do Issabel falso |
| F8 | Área "Grupos" só com `admin: true`: lista (`GET /api/groups`), importar CSV (arquivo ou colar, opção "substituir todos"), remover com confirmação; erros do `invalid_csv` mostrados como texto | Com o B10: papel `user` não vê a área; papel `admin` importa `packaging/grupos-exemplo.csv` e a tela recarrega com os grupos novos |
| F9 | Medição (seção 6) em `tools/tela/medir.mjs`, com o resultado colado na entrega | Metas da seção 6 cumpridas no Chromium; WebKit abre e mostra dados |

Regras para todas as tarefas: C3 (nada inline, nada externo, texto só por `textContent`); C9 (estado, rótulos, variáveis CSS); C10 (gancho). Interface em português, sem número de versão nem texto técnico para o operador.

---

## 6. Medição local (sem VM)

Simulador: `cmd/amisim` (programa) sobre `internal/amisim` (biblioteca), já usado pelo `cmd/loadtest`. Controle HTTP: `POST /ctl/burst?n=3000` (desregistra 3.000 de uma vez), `POST /ctl/restore`, `POST /ctl/down?d=3s`, `POST /ctl/load?calls=400&talk=20s`, `GET /truth`.

Ferramenta do backend (B10), um comando:

```
go build -o "$TEMP/painel-ramais.exe" ./cmd/painel-ramais
go run ./cmd/telalocal -daemon "$TEMP/painel-ramais.exe" -exts 10000 -calls 400 -listen localhost:8091
```

O `telalocal` faz, nesta ordem: cria uma pasta temporária com `config.yaml`, `ami-secret`, `secret` e a chave do Issabel (32 bytes aleatórios); cria o usuário `emergencia` com a senha `emergencia-local-123` pela CLI; sobe o simulador em processo (porta livre); sobe `painel-ramais serve`; espera `extensions.total` = `-exts` no `/api/health`; imprime as URLs. Ctrl-C derruba tudo. Em `-listen`:

| Caminho | Faz |
|---|---|
| `/painel-ramais/...` | proxy reverso para o daemon tirando o prefixo (como o Apache), inclusive WebSocket; mantém o `Host`; tira `Secure` do `Set-Cookie` (só aqui: é `http://localhost`; comentário `ponytail:` no código) |
| `/painel-ramais/api/health` | 404 sem passar ao daemon (como o `ProxyPass "!"`) |
| `/` | "Issabel falso": página que busca `/dev/token?papel=<admin|user>&usuario=medicao` (padrão admin; `?papel=user` na URL troca) e monta `<iframe src="/painel-ramais/#t=<token>" allow="fullscreen">` em tela inteira |
| `/dev/token` | token novo assinado com a chave do Issabel, `sub` `issabel:<papel>:<usuario>`, TTL 60 s, texto puro |
| `/sim/...` | o HTTP de controle do simulador (`/sim/ctl/burst?n=3000` etc.) |

Medição do frontend (F9), `tools/tela/medir.mjs`, Playwright 1.60 do caminho da seção 2, contra o `telalocal` já de pé:

| Medida | Como | Meta |
|---|---|---|
| Primeira exibição | 5 execuções, contexto novo (cache frio) cada; abrir `http://localhost:8091/`; ler `__painel.primeiraExibicaoMs` no frame da tela | mediana < 2000 ms; o pior valor vai no relatório |
| Rajada de 3.000 desregistros | `t0 = Date.now()` imediatamente antes de `POST /sim/ctl/burst?n=3000`; dentro do frame, esperar `__painel.contagens.unregistered >= base + 3000` registrando `Date.now()` (mesmo relógio da máquina); depois `/sim/ctl/restore` e esperar voltar; 3 repetições | as 3 abaixo de 1000 ms |
| Travamento | `__painel.tarefasLongas` depois da primeira exibição, cobrindo rajada, restauração, 10 s de rolagem da lista, busca digitada, troca do gráfico para 7 d e hover no mapa | nenhuma tarefa acima de 200 ms |
| WebKit | `pw.webkit.launch({ executablePath })`, uma abertura | a tela mostra dados e o `/ws` conecta (prova o `connect-src 'self'` com `ws:`) |

Saída: JSON com os números e código de saída 0 se as metas forem cumpridas, 1 se não.

Divergência registrada em 2026-10-16, por pedido do usuário: `docs/fase2/` (prints da tela e relatório de desempenho da medição) fica versionado no repositório, em vez de o relatório ir só no retorno do frontend.

---

## 7. Tarefas do devops (sem VM)

| # | Tarefa | Arquivos | Critério verificável |
|---|---|---|---|
| D1 | `instalar.py instalar` sem perguntar senha: gera a senha de emergência (`secrets.token_urlsafe(18)`, 24 caracteres), grava `/etc/painel-ramais/admin-emergencia` (root 0600, `os.open` com `O_CREAT|O_EXCL`, conteúdo = senha + `\n`) só se o arquivo não existir; entrega pelo stdin a `user add emergencia --password-stdin`; código 3 com arquivo recém-criado: `user passwd emergencia --password-stdin`; código 3 com arquivo antigo: segue. Pergunta de usuário e senha do admin removida; no `--respostas`, `admin_usuario` e `admin_senha_arquivo` passam a ser ignoradas com AVISO. Passo 9 do teste do Apache: `GET https://127.0.0.1/painel-ramais/api/session` precisa voltar 401 com o envelope `unauthorized` (prova que o proxy chega ao daemon; o `/painel-ramais/` agora devolve HTML). Resumo final cita o caminho do arquivo da senha (não a rota). | `instalar.py`, `tests/test_instalar.py` | Testes: sem arquivo, cria 0600 e chama `user add` com a senha no stdin (nunca no argv nem no log); arquivo existente não é reescrito; código 3 com arquivo novo chama `passwd`; respostas antigas dão AVISO e não falham; passo 9 aceita 401 envelope e recusa 200 HTML. vermin 3.6 limpo |
| D2 | Pendência 4: `git update-index --chmod=+x instalar.py` | índice do git | `git ls-files -s instalar.py` mostra `100755` |
| D3 | Pendência 5: `achar_rpm` sem RPM ao lado diz que a pasta é o código-fonte e mostra o comando de download | `instalar.py`, `tests/test_instalar.py` | Critério de aceite da pendência 5 em `docs/homologacao/pendencias.md` |
| D4 | Pendência 6, lado do instalador: texto do aviso e contagem das duas tecnologias com a regra do `detect.go` | `instalar.py`, `tests/test_instalar.py` | Critério de aceite da pendência 6 |
| D5 | Documentação de uso: `packaging/LEIAME-INSTALACAO.md` (instalação sem senha; onde está a senha de emergência; a rota `/painel-ramais/emergencia/`, **só aqui**; como trocar a senha: `sudo -u painel-ramais painel-ramais user passwd emergencia` e atualizar o arquivo; usuários da fase 1 deixam de entrar e podem ser removidos com `user del`; `session purge --issabel <usuario>`); `README.md` (instalação sem senha, tela da fase 2; **sem** citar a rota de emergência) | `packaging/LEIAME-INSTALACAO.md`, `README.md` | `grep -n emergencia README.md` vazio; LEIAME com as cinco informações |

Texto para o backend levar ao `CONTRATOS.md` 15.5 (B11), no lugar dos passos 2, 5 e 9 e da linha "mesmo bundle" da idempotência:
- passo 2: "Coleta todas as respostas antes de mudar qualquer coisa: tecnologia (`auto` por padrão) e CSV de grupos (opcional). Não pergunta senha."
- passo 5: "Senha de emergência: se `/etc/painel-ramais/admin-emergencia` não existe, gera 24 caracteres (`secrets.token_urlsafe(18)`) e grava o arquivo (root 0600, `O_EXCL`); entrega a senha pelo stdin a `user add emergencia --password-stdin`; código 3 com arquivo recém-criado: `user passwd emergencia --password-stdin`; código 3 com arquivo que já existia: segue."
- passo 9: "Testa o Apache: `GET https://127.0.0.1/painel-ramais/api/session` precisa responder `401` com o envelope `unauthorized` do daemon; `GET https://127.0.0.1/painel-ramais/api/health` precisa responder 404 do próprio Apache, sem JSON."
- idempotência, mesmo bundle: "arquivo da senha de emergência e usuário `emergencia` mantidos".

---

## 8. Integração com o menu do Issabel (valores da VM, 2026-10-16)

Fonte: `docs/fase2/leitura-vm.txt` e ADR-004, seção "Leitura da VM". Alvo único: Rocky Linux 8 com Issabel 5. Donos: devops (PHP, `menu.xml`, `packaging/`, `instalar.py`, roteiro); backend (item `issabel_menu` do depcheck).

**8a. Item de menu.** `menuid` = `painel_ramais`. Arquivo `packaging/issabel/menu.xml`, instalado em `/usr/share/painel-ramais/issabel/menu.xml` (root 0644):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<module>
  <menulist>
    <menuitem menuid="painel_ramais" desc="Painel de Ramais" parent="pbxconfig" module="yes" link="" order="10" type="module">
      <permissions>
        <group id="1" name="administrator" desc="total access"></group>
        <group id="2" name="Operator" desc="Operator"></group>
      </permissions>
    </menuitem>
  </menulist>
</module>
```

- Registro: `%post` roda `/usr/bin/issabel-menumerge /usr/share/painel-ramais/issabel/menu.xml` como root se o executável existir (camada do RPM, ADR-003); falha só registra no log, como os outros passos.
- Armadilha confirmada no código: com o item já existente, o `issabel-menumerge` atualiza nome, pai, tipo e ordem, mas **nunca** as permissões. Por isso ninguém edita `acl.db` à mão; quando faltar grupo, o caminho é `instalar.py menu --recriar` (8c), que remove e registra de novo.
- Remoção: `%postun` com `$1=0` roda `/usr/bin/issabel-menuremove painel_ramais` se o executável existir. O `deleteMenu` do Issabel só sobe para o pai quando o item é filho único; `pbxconfig` tem vários filhos, então só o nosso item sai.

**8b. Módulo PHP.** Arquivo `packaging/issabel/painel_ramais/index.php`, instalado em `/var/www/html/modules/painel_ramais/index.php` (root:root 0644; diretório root:root 0755). O arquivo só define funções: nenhum código com efeito fora delas (o `index.php` do módulo é alcançável direto pela URL, sem ACL).

```php
<?php
function _moduleContent(&$smarty, $module_name)
{
    global $arrConf, $pACL;  // $pACL vem do index.php do Issabel
    $raw = @file_get_contents('/etc/painel-ramais-issabel.key');
    $key = ($raw === false) ? false : base64_decode(trim($raw), true);
    if ($key === false || strlen($key) < 32) {
        return '<p>Painel de Ramais: integração com o Issabel desligada (chave ausente ou ilegível). '
             . 'Avise o administrador do servidor (./instalar.py status).</p>';
    }
    $user = isset($_SESSION['issabel_user']) ? (string)$_SESSION['issabel_user'] : '';
    if (!preg_match('/^[^\p{Cc}\p{Cf}\p{Zl}\p{Zp}]{1,64}$/u', $user)) {
        return '<p>Painel de Ramais: usuário do Issabel inválido para o painel.</p>';
    }
    if (!isset($pACL)) {
        $pACL = new paloACL(new paloDB($arrConf['issabel_dsn']['acl']));
    }
    $papel = (bool)$pACL->isUserAdministratorGroup($user) ? 'admin' : 'user';
    $b64 = function ($s) { return rtrim(strtr(base64_encode($s), '+/', '-_'), '='); };
    $p = $b64(json_encode(array(
        'sub' => 'issabel:' . $papel . ':' . $user,
        'exp' => time() + 60,
        'jti' => $b64(random_bytes(16)),
    ), JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE));
    $t = $p . '.' . $b64(hash_hmac('sha256', $p, $key, true));
    return '<iframe src="/painel-ramais/#t=' . htmlspecialchars($t, ENT_QUOTES) . '" allow="fullscreen" '
         . 'title="Painel de Ramais" style="width:100%;height:calc(100vh - 140px);min-height:560px;border:0"></iframe>';
}
```

- Papel: `isUserAdministratorGroup` devolve o nome do grupo (membro do grupo id 1), `false` ou `NULL` (usuário sem grupo); usar só como booleano, nunca `!== false`.
- Teste do emissor: não há teste automatizado (regra do usuário: nenhum teste novo neste computador). Verificar na VM, Etapa 16b: o token do módulo é aceito pelo daemon (tela carrega sem senha, sem `invalid_token` no journal) e o papel `administrator`/`Operator` aparece certo. Idem para `instalar.py menu`.
- Altura do iframe: ponto de partida; a Etapa 16b confere na área de conteúdo (cerca de 1250 x 700 px).

**8c. `instalar.py menu`** (root; só stdlib; leitura com `sqlite3.connect("file:/var/www/db/<db>?mode=ro", uri=True)`):

1. Pré-requisitos: `/usr/bin/issabel-menumerge` executável, `/var/www/db/menu.db` e `/var/www/db/acl.db` legíveis, `/usr/share/painel-ramais/issabel/menu.xml` e `/var/www/html/modules/painel_ramais/index.php` presentes. Faltou: ERRO com o que falta.
2. Item: `SELECT IdParent, Type, order_no FROM menu WHERE id = 'painel_ramais'`. Ausente: roda `issabel-menumerge <menu.xml>` e lê de novo. Esperado `pbxconfig`, `module`, `10`; diferente: AVISO com o valor lido.
3. Permissões: `SELECT p.id_group FROM acl_group_permission p JOIN acl_resource r ON r.id = p.id_resource JOIN acl_action a ON a.id = p.id_action WHERE r.name = 'painel_ramais' AND a.name = 'access'`. Precisa conter 1 e 2. Faltou: AVISO "grupo sem acesso ao item; rode ./instalar.py menu --recriar" (grupo a mais, colocado pelo administrador, só aparece no relatório).
4. `--recriar`: `issabel-menuremove painel_ramais` e `issabel-menumerge <menu.xml>`, depois os passos 2 e 3. Apaga permissões que o administrador tenha dado a outros grupos: a mensagem diz isso antes de rodar.
5. Chave: confere `/etc/painel-ramais-issabel.key` com dono `painel-ramais`, grupo `asterisk`, modo 0640; diferente: AVISO com o `chown`/`chmod`.
6. `instalar` chama o `menu` (passos 1 a 3 e 5) depois do passo 9; `verificar` (antes de instalar) só informa: `issabel-menumerge` presente e `fop2` com `order_no` 9; `status` mostra os passos 2, 3 e 5 em uma linha cada; `remover` não precisa de passo extra (o `%postun` remove o item).

Depcheck do daemon (backend, item novo `issabel_menu`): o usuário `painel-ramais` lê `menu.db` (0644). Abre com `file:/var/www/db/menu.db?mode=ro` e `busy_timeout` de 1 s, consulta só `SELECT 1 FROM menu WHERE id = 'painel_ramais'`. Presente: `ok`. Ausente: `fail`, detalhe "item do menu do Issabel ausente; rode ./instalar.py menu". Erro de abertura ou leitura: `unknown`. O daemon não lê `acl.db` (permissões são do `instalar.py`). Constante do caminho em `internal/depcheck`, ao lado das de `manager.conf`.

**8d. Chave no pacote.** Não entra em `contents` do `nfpm.yaml` (é gerada, como os segredos). `%post`, antes do `issabel-menumerge`:
- arquivo não existe: `umask 077`, `head -c 32 /dev/urandom | base64 -w0` num temporário em `/etc`; se `cmp -s` com `/etc/painel-ramais/secret` (mesmo conteúdo), gera de novo; `mv` para `/etc/painel-ramais-issabel.key`;
- sempre (novo ou existente, sem tocar no conteúdo): se `getent group asterisk` existe, `chown painel-ramais:asterisk` e `chmod 0640`; senão `chown painel-ramais:painel-ramais`, `chmod 0600` e registra "grupo asterisk ausente: integração com o menu desligada";
- `%postun` com `$1=0`: `rm -f /etc/painel-ramais-issabel.key`. o `%postun` já a apaga na remoção, então o `remover --purge` não precisa de passo extra.
- `contents` ganha: `packaging/issabel/menu.xml` → `/usr/share/painel-ramais/issabel/menu.xml` e `packaging/issabel/painel_ramais/index.php` → `/var/www/html/modules/painel_ramais/index.php` (root:root 0644) e o diretório `/var/www/html/modules/painel_ramais` (root:root 0755).

**8e. Versão 0.1.2-rc.3** (busca no repositório em 2026-10-16):
- `build.ps1`: nenhum valor a trocar; a versão vem do argumento (`.\build.ps1 release -Version 0.1.2 -Pre rc.3`). O padrão `0.1.1` da linha 8 e os exemplos das linhas 1 a 3 ficam.
- `packaging/nfpm.yaml`: nenhum (usa `${VERSION}` e `${RELEASE}`).
- `cmd/painel-ramais/main.go:12` `var version = "0.1.1"`: trocado pelo `-ldflags` no build; não precisa mudar.
- `instalar.py:157`: exemplo em docstring; não muda.
- `docs/homologacao/ROTEIRO-MANUAL.md`: linhas 24 e 25 (`TAG=v0.1.2-rc.3`, `VERSAO_RPM=0.1.2-0.1.rc.3`), 77 e 84 (nome do bundle) e a seção "Upgrade da rc.1 para a rc.2" (linhas 272 a 281) vira "da rc.2 para a rc.3".
- `README.md:17` e `packaging/LEIAME-INSTALACAO.md:12` (`VERSAO=0.1.1`): exemplo de versão final; a URL do RPM avulso só funciona com versão final. Trocar para `0.1.2-rc.3` só se o texto ganhar o nome de RPM de rc (`0.1.2-0.1.rc.3`).

**8f. Etapa 16b, sessão do Issabel de 24 min** (devops leva para o roteiro). Fluxo real: cada carga de `index.php?menu=painel_ramais` executa o módulo e gera token novo; a sessão do painel é própria (12 h sem uso, renovada pelo WebSocket) e não depende da sessão do Issabel depois de criada.
- **Comando:** abra o painel pelo menu, deixe a tela aberta mais de 24 min sem usar o Issabel (pode ser na TV); depois recarregue a página inteira (F5).
- **Resultado esperado:** durante os 24 min a tela continua atualizando. No F5: se a sessão do Issabel expirou, aparece o login do Issabel; depois de entrar, abrir o menu mostra a tela sem pedir outra senha. Se a sessão do Issabel ainda existir (a coleta do PHP é por amostragem), a tela abre direto. Nunca "Sessão encerrada" com o Issabel logado.
- **Anotar também:** se o próprio Issabel troca a página inteira pelo login sem F5 (algum aviso periódico do tema), a TV perde a tela; anote o tempo e o que apareceu.

---

## 9. Dependências entre agentes

| Precisa | De | Para quê |
|---|---|---|
| backend B4 compilar o binário | frontend F0 | o `go:embed` exige os arquivos |
| frontend F2 a F9 testarem de ponta a ponta | backend B1, B3, B4, B6 e B10 | rotas, sessão, token do Issabel falso e simulador; até lá o frontend trabalha contra os contratos C2 a C10 e com `node --test` |
| frontend F8 | backend B6 | rotas de grupos |
| devops D1 conferir o passo 9 num servidor | backend B3 (`/api/session`) | os testes do D1 usam resposta simulada e não esperam |
| backend B8 | ninguém | `cmd/loadtest` é do backend |
| seguranca (auditoria) | B1 a B7, F2 e F8 prontos | fluxo de token, CSP, chave, emergência, grupos |
| qa (roteiro na VM e medição repetida) | F9 e a seção 8 | |

Nada da seção 4, 5 ou 7 depende da VM.

---

## 10. Riscos

| Risco | Mitigação |
|---|---|
| `/etc/asterisk` sem `x` para outros: `manager_custom_perm` fica `unknown` para sempre e o health fica `degraded` | Item 10 da VM. Se acontecer, decisão do usuário: aceitar `unknown`, ou tratar `EACCES` do diretório como `ok` (se o serviço, que é "outro", não atravessa a pasta, nenhum outro usuário atravessa) |
| Cabeçalho global do Issabel (`X-Frame-Options: DENY` ou CSP) somado ao do daemon bloqueia o iframe | Fechado pela VM: o Apache não define nenhum (ADR-004, item 5 da leitura) |
| `connect-src 'self'` não cobrir `ws:`/`wss:` num navegador antigo | Medição no WebKit (F9); se falhar, o daemon monta `connect-src 'self' wss://<Host>` com o `Host` validado |
| Primeira exibição acima de 2 s com 10.000 ramais | Desenhar indicadores e a primeira tela de cards antes do mapa e do gráfico; medir de novo antes de otimizar o protocolo |
| Vetores novos aparecem no gitleaks | Atualizar o baseline em `.claude/agent-memory/seguranca` com os arquivos e linhas dos vetores V0 a V3 |
| Usuários `admin` da fase 1 ficam inertes no banco | LEIAME manda remover com `user del`; nada é apagado automaticamente |
| Travamento do login de emergência por tentativa errada repetida (5 min) | Aceito no ADR-004; a CLI do root continua disponível |

---

## 11. Verificação deste plano

- Cada arquivo tem um dono só (seção 1); o único ponto compartilhado, o embed, tem a regra do F0.
- Contratos com formato exato: token e vetores (C1), rotas (C2), cabeçalhos e CSP (C3), entrada (C4), sessão (C5), grupos (C6), CLI, configuração e dependências (C7), dados lidos do código (C8), estados e cores (C9), gancho de medição (C10).
- Toda tarefa tem critério que um comando ou teste confere.
- Nada depende de valor da VM fora da seção 8.
- Pendências que esperam o pacote não aparecem em nenhuma tarefa.
