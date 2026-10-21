# Modelo de segurança

Decisões de origem: ADR-002 (sessão e token), ADR-003 (pacote e conta AMI), ADR-004 (menu do Issabel, chave separada,
emergência). Regras exatas: [CONTRATOS](../CONTRATOS.md), seções 1, 3 e 7.1.

## Superfície exposta

- O daemon escuta só em `127.0.0.1:8790` (o config recusa host que não seja loopback). Quem chega de fora passa pelo
  Apache do Issabel em HTTPS (443), no prefixo `/painel-ramais/`.
- `/api/health` e `/debug` só respondem direto no servidor: o Apache não os repassa (`ProxyPass "!"`) e o daemon responde
  `404` a qualquer pedido com `X-Forwarded-For` ou `X-Forwarded-Host`. Duas barreiras independentes.
- Qualquer usuário local do servidor alcança a porta 8790 em TCP; sem sessão ele só vê `401` (exceto o health).

## Entrada pelo menu: token HMAC

1. O módulo PHP (`packaging/issabel/painel_ramais/index.php`) roda dentro do Issabel, que já autenticou a pessoa.
2. Ele lê `/etc/painel-ramais-issabel.key` (32 bytes ou mais em base64; `painel-ramais:asterisk 0640`), decide o papel pelo
   `isUserAdministratorGroup` do Issabel e assina `base64url(payload).base64url(HMAC-SHA256(chave, base64url(payload)))`.
   Payload com exatamente `sub`, `exp` (60 s) e `jti` (16 bytes aleatórios).
3. O token vai no fragmento da URL do iframe (`/painel-ramais/#t=<token>`): não chega ao Apache nem ao log de acesso. A
   tela apaga o fragmento e troca o token por sessão em `POST /api/token`.
4. O daemon (`internal/auth/token.go`) confere a assinatura com `hmac.Equal` contra a chave do Issabel e contra o próprio
   segredo (`/etc/painel-ramais/secret`), exige `agora < exp <= agora + 65 s` e recusa `jti` repetido.

| Assinado com | `sub` aceito | Papel |
|---|---|---|
| chave do Issabel | `issabel:admin:<usuario>` | administrador |
| chave do Issabel | `issabel:user:<usuario>` | operador |
| segredo do daemon (CLI `painel-ramais token`) | texto que **não** começa com `issabel:` | operador |

Consequências: token da CLI nunca vira administrador; a chave do Issabel nunca emite `sub` livre; chave igual ao segredo
do daemon é recusada. A chave é vigiada pela verificação de dependências a cada 60 s (item `issabel_key` do health):
trocar ou estragar a chave vale sem reiniciar o serviço.

## Sessão

- Id de 32 bytes aleatórios no cookie `painel_session` (`Secure`, `HttpOnly`, `SameSite=Strict`,
  `Path=/painel-ramais/`). No banco fica só o SHA-256.
- Validade por inatividade (`auth.session_ttl`, padrão 12 h), renovada pelo uso e pelo WebSocket aberto: tela de NOC não
  expira.
- `painel-ramais session purge [--all | --issabel <usuario>]` apaga sessões; o SIGHUP fecha os WebSockets delas com
  `session_expired` e 4001. Atualizar o pacote encerra as sessões antigas.

## Checagem de administrador

Duas camadas, ambas no daemon (a tela só esconde botões, por conveniência):

1. **Papel na sessão**: administrador se, e só se, `kind = token` e `subject` começa com `issabel:admin:`
   (`adminOnly` em `internal/api/authn.go`). Senão `403` antes de ler o corpo.
2. **Conferência a cada escrita**: `StillAdmin` abre `/var/www/db/acl.db` **somente leitura** e confere se o usuário ainda
   está no grupo `administrator` do Issabel (`internal/api/issabel_acl.go`). Arquivo ilegível também dá `403` (falha
   fechada). Vale para as rotas de visão e para renomear fila.

## Acesso de emergência

Única entrada por senha: usuário `emergencia`, em `/painel-ramais/emergencia/` (fora de iframe, `frame-ancestors 'none'`).
Senha aleatória em `/etc/painel-ramais/admin-emergencia` (root 0600), hash bcrypt custo 12. Sessão de operador (nunca
administrador). Limites: 5 falhas por IP do cliente e 20 por usuário em 5 min, no máximo 4 comparações bcrypt ao mesmo
tempo. Outros usuários de senha da fase 1 continuam no banco e não entram.

## AMI somente leitura

- **O que garante a leitura é o código**: `internal/ami/actions.go` tem a lista fixa de 14 actions de consulta (`Login`,
  `Logoff`, `Ping`, `CoreSettings`, `ModuleCheck`, `PJSIPShowEndpoint(s)`, `PJSIPShowContacts`, `PJSIPShowAors`,
  `SIPpeers`, `SIPshowpeer`, `DeviceStateList`, `CoreShowChannels`, `QueueStatus`). Qualquer outra action é recusada
  **antes** de escrever no socket, com log Error. `Command`, `Originate`, `Redirect`, `Hangup`, `Reload`, `GetConfig`,
  `QueueReload`, `QueueReset` e afins nunca são enviadas.
- A conta AMI `painel-ramais` tem `read = system,call,agent` e `write = system,reporting` (a classe `system` de escrita é
  exigida pelas consultas PJSIP). Risco residual aceito pelo usuário (`docs/DECISOES-FASE-1.md`, decisão 1): com o daemon
  comprometido, essa classe permitiria actions que o painel nunca usa. Controles: allowlist, `permit` só 127.0.0.1, senha
  0600 do usuário do serviço, `manager_custom.conf` sem acesso para outros, unit endurecida.
- A conta só recebe os 29 eventos do `eventfilter` do bloco do painel.
- O painel não lê o MySQL do Issabel e não altera `manager.conf`; do Issabel lê só `menu.db` e `acl.db`, em modo `ro`.

## Tela

- CSP estrita: `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self';
  font-src 'self'; ...; frame-ancestors 'self'`. Nada inline, nada de CDN, fontes locais.
- A tela só é entregue dentro de iframe (`Sec-Fetch-Dest`); aberta direto vai para o aviso `/acesso`.
- Texto vindo do Asterisk entra só por `textContent` ou canvas; nomes passam por `model.CleanName` no servidor.
- POST só da própria origem (`Sec-Fetch-Site`), só JSON estrito, sem CORS.
- CSV exportado neutraliza injeção de fórmula (CONTRATOS 4.7.5).

## Processo

- Unit com `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`,
  `CapabilityBoundingSet=` vazio, `MemoryDenyWriteExecute`, `SystemCallFilter=~@debug`, `UMask=0077`.
- Endereço guardado no relatório de registro é só IPv4 (`reg_events.ip` com 7 a 15 caracteres).

## Repositório

- Nenhum segredo, token real, senha ou usuário de remoto em código, doc, commit ou log. O `.gitignore` barra `secret`,
  `ami-secret`, `*.key`, `respostas.ini` e capturas reais.
- `gitleaks` (binário em `.tools/gitleaks/`, fora do git) roda nos commits novos antes de qualquer push. O token de
  **exemplo** do CONTRATOS 3.3 está no `.gitleaksignore` por arquivo e **linha** (`docs/CONTRATOS.md:generic-api-key:328`):
  editar o CONTRATOS acima dessa linha desloca o achado e exige atualizar o ignore.
- Capturas e pacotes de homologação passam pelo `painel-ramais filtrar` (máscara de segredos e anonimização).
