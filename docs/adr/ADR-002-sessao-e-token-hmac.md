## ADR-002: Sessão por cookie e token HMAC de entrada

**Data**: 2026-10-13
**Status**: ACEITO em 2026-10-13 (`docs/DECISOES-FASE-1.md`, decisão 5), com o ajuste de NOC abaixo. Respostas às perguntas do fim: 1 (a) login obrigatório; 2 (a) token de 60 s e uso único; 4 (a) `/api/health` só localhost; 3 substituída pelo ajuste de NOC.

**Ajuste de NOC (decisão 5, substitui o item 6 e a pergunta 3)**: o painel fica aberto 24 h em TV. A sessão deixa de ser absoluta: expira por **inatividade** de 12 h (`auth.session_ttl`, configurável de 15 min a 24 h) e se renova sozinha enquanto houver uso. Uma conexão `/ws` ativa renova a sessão a cada `session_ttl/6`; requisição REST passada a metade do prazo também renova. Tela em uso nunca expira. Quando a sessão acaba (inatividade ou revogação por `user del`/`user passwd`/`session purge --all`), o WebSocket envia a mensagem tipada `session_expired` (`reason`: `expired` ou `revoked`) **antes** de fechar com 4001, para a tela da fase 2 avisar em vez de congelar. O cookie passa a `Max-Age=604800` (7 dias) só para sobreviver à sessão, pois uma tela só com WebSocket não recebe resposta REST que o renove; a validade real é a do banco (`sessions.expires_at`). Consequência aceita: um cookie roubado vale enquanto a sessão for usada; sem uso por 12 h ele morre.
**Revisão 2026-10-16 (ADR-004, Proposto)**: o login direto sem Issabel foi revogado pelo usuário (só a rota de emergência e a CLI); o menu do Issabel entra na fase 2, com chave própria e `sub` `issabel:admin:`/`issabel:user:`.
**Porta**: 1 via (irreversível na prática). O formato do token é contrato com um emissor externo ao daemon (o link do Issabel na fase 3); depois que o Issabel emitir tokens, mudar o formato exige mexer nos dois lados.

### Contexto

Pedido da especificação (`docs/PROMPT-FASE-1.md`, seção Autenticação): usuários próprios do daemon com senha em bcrypt no SQLite; CLI `user add`, `user del`, `user passwd`; login por sessão com cookie seguro e expiração; token HMAC de curta duração com segredo em `/etc/painel-ramais/secret` (0600) como atalho opcional de entrada, validado nesta fase e com CLI para gerar token de teste; o login direto funciona sem nada do Issabel. O painel é somente leitura: não há ação a autorizar, só "pode ver" ou "não pode ver".

### Decisão (proposta)

**Escopo**
1. `/api/*` e `/ws` exigem sessão. Exceções: `POST /api/login`, `POST /api/logout` (idempotente), `POST /api/token` e `GET /api/health`.
2. `/api/health` e `/debug` só por localhost direto: fora do proxy do Apache (`ProxyPass "!"`) e recusados pelo daemon quando chegam com `X-Forwarded-For` ou `X-Forwarded-Host` (`404`).
3. Sem papéis nem permissões: todo usuário autenticado vê o painel inteiro.

**Sessão**
4. Id de sessão: 32 bytes de `crypto/rand`, em base64url no cookie. No banco grava-se só o SHA-256 do valor bruto (`sessions.id_hash`); vazamento do banco não entrega sessão válida.
5. Cookie `painel_session` com `Secure; HttpOnly; SameSite=Strict; Path=/painel-ramais/; Max-Age=28800`.
6. ~~Validade de 8 h absoluta~~ Substituído pelo ajuste de NOC (cabeçalho): inatividade de 12 h, renovada pelo uso e pelo WebSocket. `auth.session_ttl` entre 15 min e 24 h.
7. Verificação por requisição: um `SELECT` pela chave primária com `expires_at > agora`. WebSocket: verificado no upgrade, fechado com 4001 no vencimento e no SIGHUP se a sessão sumiu (ADR-001).
8. `user del` apaga as sessões por `ON DELETE CASCADE`; `user passwd` apaga as sessões do usuário. A CLI manda SIGHUP para o daemon fechar os WebSockets abertos.
9. Login: bcrypt custo 12; senha de 10 caracteres a 72 bytes na criação. Mesma resposta (`401 invalid_credentials`) para usuário inexistente e senha errada; quando o usuário não existe, o daemon compara contra um hash fixo para o tempo de resposta não revelar a diferença.
10. Limite de tentativas em memória: 5 falhas do mesmo usuário em 5 min geram `429 too_many_attempts` até a janela passar. Reinício zera o contador (aceito).
11. CSRF: `SameSite=Strict`, POST só com `Content-Type: application/json` (formulário cross-site não consegue mandar sem preflight) e o daemon nunca responde CORS.

**Token HMAC**
12. Formato: `base64url(payload) "." base64url(HMAC-SHA256(segredo, base64url(payload)))`, sem padding. O HMAC assina o texto base64url do payload, como em JWS, para não depender de serialização canônica.
13. Payload JSON com exatamente três campos: `sub` (texto livre, 1 a 128 caracteres; não precisa ser usuário do daemon), `exp` (unix segundos) e `jti` (16 a 64 caracteres base64url). Campo a mais ou a menos: inválido.
14. Segredo: `/etc/painel-ramais/secret`, 0600, dono `painel-ramais`, base64 de 32 bytes aleatórios (gerado no `%post`, ADR-003). O daemon recusa iniciar se decodificar em menos de 32 bytes.
15. Validação, nesta ordem: formato (duas partes, base64url válido, tamanho máximo 1 KiB); HMAC com `hmac.Equal` (tempo constante); JSON estrito; `agora < exp <= agora + 65 s` (TTL de 60 s mais 5 s de folga de relógio; o daemon impõe o teto mesmo que o emissor gere token longo); `jti` não visto. Qualquer falha: `401 invalid_token`, mesma mensagem.
16. Uso único: mapa `jti -> exp` em memória, com limpeza dos vencidos a cada verificação. Só cresce com tokens válidos, que exigem o segredo.
17. Token aceito cria sessão `kind = 'token'`, `user_id = NULL`, `subject = sub`, com o mesmo cookie e a mesma validade do login.
18. CLI `painel-ramais token --sub <texto> [--ttl 60s]` gera token de teste (máximo 60 s).
19. Vetor de teste fixo em `docs/CONTRATOS.md`, seção 3.3, para o backend e para o emissor da fase 3.

### Alternativas consideradas

- **A (mais simples possível): Basic Auth do Apache (htpasswd) na frente de `/painel-ramais/`.** Descartada. A especificação pede usuários do daemon com bcrypt no SQLite e CLI própria, sessão com expiração e token de entrada; Basic Auth não tem logout nem expiração, e mexer em autenticação do Apache aumenta o acoplamento com o httpd do Issabel.
- **B: JWT como sessão, sem estado no servidor.** Descartada. Revogar no `user del` e no `user passwd` exigiria lista de revogação, que é uma tabela de sessões com outro nome. Sessão no SQLite custa um `SELECT` por chave primária.
- **C: sessões só em memória.** Descartada. Todo upgrade do RPM reinicia o daemon e deslogaria todos os painéis abertos, incluindo TVs de NOC. O SQLite já existe.
- **D: token reutilizável até o `exp`.** Descartada. Link vazado (histórico do navegador, print, log de proxy) daria acesso durante todo o TTL. Uso único custa um mapa.
- **E: token com TTL de minutos.** Descartada na proposta: o emissor (Issabel) está na mesma máquina, então não há atraso de rede nem diferença de relógio que justifique mais de 60 s.
- **F: deixar `/api/*` aberto (painel só de leitura) e proteger só com rede interna.** Descartada na proposta: o painel mostra quem fala com quem e números externos, que é dado pessoal. Está nas perguntas, porque é decisão do dono do dado.

### Suposições feitas

- TLS terminado no Apache do Issabel (porta 443). Acesso por `http://` puro não guarda o cookie `Secure` e cai em laço de login: o README avisa.
- Emissor do token (fase 3) roda na mesma máquina, então o relógio é o mesmo.
- Dezenas de usuários; nada de SSO, LDAP ou integração com usuários do Issabel nesta fase.
- Na fase 3, o link entrega o token no fragmento da URL (`/painel-ramais/#t=<token>`), que não vai para log de acesso, e a tela faz `POST /api/token`. Só registrado aqui; decisão da fase 3.

### Consequências

- Positivas: login funciona sem Issabel; revogação imediata por usuário; banco vazado não entrega sessão; token de link não serve duas vezes; o emissor da fase 3 tem vetor de teste para validar a implementação dele.
- Negativas / trade-offs:
  - Reinício do daemon esquece os `jti` usados: um token interceptado pode ser reaproveitado se o daemon reiniciar dentro dos 65 s de vida dele. Janela aceita; fechar exigiria gravar `jti` no SQLite.
  - (Resolvido pelo ajuste de NOC.) O custo novo é uma escrita no banco a cada `session_ttl/6` por tela aberta.
  - **Pendência para a fase 3**: o segredo é 0600 do usuário `painel-ramais`; o PHP do Issabel (usuário `asterisk`) não consegue ler. A fase 3 precisa decidir como o emissor assina: grupo de leitura no segredo (0640 com grupo dedicado) ou emissão via `sudo -u painel-ramais painel-ramais token` com regra de sudoers restrita. Não decidido aqui.
  - Limite de tentativas por usuário permite que alguém trave o login de outro por 5 min digitando senha errada. Aceito para ferramenta interna.

### Criterio de validação

Testes automatizados no pacote `auth` e testes de integração do `api`:

1. O vetor de teste da seção 3.3 do contrato valida; o mesmo token com um caractere trocado na assinatura dá `401 invalid_token`.
2. Token com `exp` no passado, com `exp > agora + 65 s`, com campo extra no payload ou sem `jti`: `401 invalid_token`.
3. Mesmo token enviado duas vezes: a primeira cria sessão, a segunda dá `401 invalid_token`.
4. `curl -i` no login mostra o cookie com `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/painel-ramais/` e `Max-Age=28800`; o banco guarda 32 bytes de hash, não o valor do cookie.
5. `/api/snapshot` e o upgrade de `/ws` sem cookie: HTTP 401 com envelope `unauthorized`.
6. `user del` com um WebSocket aberto desse usuário: conexão fechada com 4001 em até 1 s depois do SIGHUP.
7. Sexta tentativa errada do mesmo usuário em 5 min: `429`.
8. `GET /api/health` com `X-Forwarded-For`: `404`; sem o cabeçalho, em 127.0.0.1: `200` ou `503`.

### Perguntas ao usuário (fecham o ADR)

1. **Exigir login para ver o painel?** (a) Sim, `/api/*` e `/ws` exigem sessão (proposta); (b) leitura aberta para quem alcança o Issabel, login só para nada (não há ação de escrita).
2. **Token de entrada**: (a) 60 s e uso único (proposta); (b) 5 min e uso único; (c) 5 min reutilizável.
3. **Validade da sessão** (pensando em TV de NOC ligada o dia todo): (a) 8 h absoluta (proposta); (b) 8 h que renova com o uso, com teto de 7 dias; (c) 8 h para pessoas e um usuário "de parede" sem expiração.
4. **`/api/health`**: (a) só localhost (proposta; monitor roda no próprio servidor); (b) também via Apache, sem sessão, para um Zabbix ou Nagios externo.

### Agentes impactados

backend (auth, store, api, CLI), seguranca (auditoria do fluxo), frontend (fase 2: tela de login e tratamento de 401 e 4001), gerente (levar as 4 perguntas ao usuário).
