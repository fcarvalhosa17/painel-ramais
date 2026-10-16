## ADR-004: Tela no menu do Issabel (iframe com token), chave separada do Issabel e acesso de emergência

**Data**: 2026-10-16
**Status**: Aceito em 2026-10-16 (decisão do usuário, depois da leitura da VM em `docs/fase2/leitura-vm.txt`; seção "Leitura da VM"). A verificação no Issabel real com o módulo instalado (seção "Verificação na VM") continua obrigatória antes da release e vai anexada aqui.
**Porta**: 1 via para o formato do `sub`, o caminho da chave e a regra de permissão: o emissor é um módulo PHP instalado nos servidores dos clientes, e mudar depois exige atualizar os dois lados juntos. 2 vias para a tecnologia da tela (HTML, CSS e JS embutidos no binário, sem build), que só tem consumidor dentro do repositório.

### Contexto

A fase 1 entregou o daemon, a API e o token HMAC de entrada (ADR-002), com login direto por usuário e senha do daemon. A tela e o link no menu do Issabel ficaram para depois. O usuário fixou em 2026-10-16:

1. `/painel-ramais/` fica bloqueado fora do Issabel, com o aviso "acesse pelo menu PBX do Issabel". Exceções: a CLI, a homologação e uma rota de emergência separada, cuja senha o instalador gera sozinho, **sem perguntar**, e grava em `/etc/painel-ramais/admin-emergencia` (0600, root). A rota é documentada só no `LEIAME-INSTALACAO.md`. **Isto revoga a regra da fase 1 "o login direto funciona sem nada do Issabel"** (`docs/PROMPT-FASE-1.md`, seção Autenticação; ADR-002, Contexto e Consequências).
2. Item de menu para os grupos `administrator` e `Operator`.
3. Posição logo depois do Operator Panel, sem substituir, esconder ou alterar nada do Issabel.
4. Purge de sessão por comando da CLI; expiração por 12 h sem uso mantida; risco da aba aberta aceito.
5. Chave do Issabel separada do segredo do daemon, modo 0640; o daemon só aceita `sub` com prefixo `issabel:` assinado com ela; sem o arquivo, só a integração desliga.
6. Gestão de grupos pela tela só para administradores do Issabel.
7. Tela: indicadores, gráfico do histórico, mapa por grupo, visão de grupo estilo FOP com rolagem virtual, busca e filtros, ranking, faixa de AMI perdido, tela cheia, texto do Asterisk só como texto, CSP estrita, snapshot mais deltas pelo WebSocket existente, tudo em português e sem travar com 10.000 ramais.

Estudo feito sem VM (clone do IssabelFoundation `framework`, `pbx` e `ai_assistant`): o `paloSantoNavigation` distingue item de menu do tipo módulo (PHP do próprio módulo gera o conteúdo) e do tipo framed (iframe para URL fixa); o `issabel-menumerge` só concede os grupos ao **criar** o item (no update o ACL não é regravado); `modules/<x>/index.php` roda direto pela URL, sem o ACL do `index.php` principal, então o arquivo não pode ter código com efeito fora de `_moduleContent`; o `ai_assistant` (2026) é o precedente oficial de módulo web conversando com um daemon Go por segredo 0640 com grupo. Tudo isso é leitura de código, não execução: a seção "A confirmar pela VM" lista o que falta ver no servidor.

### Decisão

**1. Entrada pelo menu: módulo PHP que emite token e monta um iframe.** O item de menu é do tipo módulo (não framed). O `_moduleContent` do módulo, a cada abertura, lê a chave, monta o `sub` (item 3), assina um token no formato do ADR-002 (TTL 60 s, uso único) e devolve um `<iframe src="/painel-ramais/#t=<token>" allow="fullscreen">`. O token vai no fragmento, que nunca chega ao Apache nem ao log de acesso. A tela lê o fragmento, apaga-o da barra com `history.replaceState`, troca por sessão em `POST /api/token` e segue com cookie, como na fase 1. Módulo PHP, `menu.xml` e registro no menu: plano da fase 2, seção 8 (valores da VM).

**2. Chave do Issabel separada.** Arquivo `/etc/painel-ramais-issabel.key`, fora de `/etc/painel-ramais/` (o diretório é `root:painel-ramais 0750` e o PHP não o atravessa). Conteúdo: base64 de 32 bytes aleatórios ou mais, como o `secret`. Dono `painel-ramais` (o daemon lê como dono), grupo = o grupo do processo PHP do Issabel (`asterisk`, pela VM), modo 0640. Configuração nova `auth.issabel_key_file` (padrão o caminho acima).
- Regra de permissão no daemon: arquivo regular (não symlink), dono = usuário do serviço, nenhum bit fora de `0640` (aceita 0640, 0600, 0440, 0400; recusa escrita de grupo e qualquer bit de outros). Falhou a regra: a chave não é usada.
- Sem o arquivo, ou com permissão errada, ou com conteúdo inválido: o daemon sobe normalmente, só os tokens `issabel:` são recusados, e a verificação de dependências (60 s) mostra o motivo no item `issabel_key`. Arquivo ausente é `ok` com o detalhe "integração com o menu do Issabel desligada", para uma instalação sem o módulo não ficar `degraded`; permissão ou conteúdo errado é `fail` com a correção.
- A mesma verificação de 60 s entrega a chave lida ao verificador de tokens: criar, trocar, apagar ou estragar a permissão da chave vale em até 60 s, sem reiniciar o serviço, e o que o `/api/health` mostra é exatamente o que o verificador usa.
- Quem cria a chave: o `%post` do RPM (como os outros segredos), com grupo `asterisk` (plano, seção 8d). A regra "o `%post` só faz `chmod` no `manager_custom.conf`" vale para arquivos do Issabel; arquivos do painel (`/etc/painel-ramais*`, `/var/lib/painel-ramais`, a chave) são do RPM.

**3. `sub` com papel no prefixo, assinado com a chave.** Gramática para tokens assinados com a chave do Issabel: `issabel:admin:<usuario>` para quem está no grupo `administrator` do Issabel, `issabel:user:<usuario>` para os demais grupos com acesso ao item; `<usuario>` de 1 a 64 caracteres, sem caractere de controle (pode conter `:`). O payload continua com exatamente `sub`, `exp` e `jti` (ADR-002 item 13 intacto).
- Verificação com duas chaves: a assinatura é conferida contra a chave do Issabel (se carregada) e contra o segredo do daemon, ambas com `hmac.Equal`. Casou com a chave do Issabel: o `sub` tem de seguir a gramática acima. Casou com o segredo do daemon: o `sub` **não** pode começar com `issabel:` (comparação sem diferenciar maiúsculas). Qualquer outra combinação: `401 invalid_token`, a mesma resposta de sempre.
- Papel: a sessão é de administrador se, e só se, `kind = token` e o `subject` começa com `issabel:admin:`. Nada muda no esquema do banco: o papel é derivado do `subject` gravado. Consequência: um token da CLI (segredo do daemon) nunca vira administrador do Issabel, e quem tem só a chave do Issabel nunca emite `sub` livre.
- Rebaixar ou remover um usuário no Issabel não derruba a sessão já aberta (até 12 h sem uso): risco aceito pelo usuário (decisão 4); o corte imediato é `painel-ramais session purge --issabel <usuario>`.

**4. Bloqueio fora do Issabel, no daemon.** A tela é servida pelo próprio daemon em `/` (navegador: `/painel-ramais/`).
- `Sec-Fetch-Dest: iframe` ou cabeçalho ausente (navegador antigo): entrega a tela, com `Content-Security-Policy` estrita e `frame-ancestors 'self'` (mais `X-Frame-Options: SAMEORIGIN`). Só uma página da mesma origem, isto é, o próprio Issabel, consegue emoldurar a tela.
- Qualquer outro valor (`document` ao abrir o endereço direto, `empty`): `303` para `/painel-ramais/acesso#`, página sem script com o aviso "Acesse pelo menu PBX do Issabel". O `#` vazio no `Location` impede que um token colado na barra seja herdado pelo redirecionamento.
- Segunda barreira, no script da tela: fora da rota de emergência, se `window.top === window.self` a tela mostra o mesmo aviso e não consome token nem chama a API. Cobre o navegador que não manda `Sec-Fetch-Dest`.
- A barreira de dados continua sendo a sessão: `/api/*` e `/ws` exigem cookie como na fase 1. O bloqueio da tela é regra de uso, não substitui a sessão.
- CLI e homologação seguem como na fase 1: `painel-ramais token --sub <texto>` com o segredo do daemon, direto em `127.0.0.1:<porta>`, sem passar pela tela.

**5. Rota de emergência.** `/painel-ramais/emergencia/` entrega a mesma tela, abrível na aba principal (`frame-ancestors 'none'`, `X-Frame-Options: DENY`), com formulário de login.
- `POST /api/login` passa a aceitar **só** o usuário reservado `emergencia`; qualquer outro nome recebe `401 invalid_credentials`, com a mesma comparação bcrypt falsa de hoje (o tempo não revela nada). Usuários criados na fase 1 continuam no banco e deixam de entrar (o `LEIAME` manda removê-los com `user del`).
- Senha: o `instalar.py instalar` gera (24 caracteres de `secrets.token_urlsafe`), grava `/etc/painel-ramais/admin-emergencia` (root 0600, criado com `O_EXCL`) e entrega à CLI existente pelo stdin (`user add emergencia --password-stdin`; código 3 com arquivo recém-criado: `user passwd`). Arquivo existente nunca é reescrito. O daemon nunca lê esse arquivo: valida pelo bcrypt no SQLite, como qualquer usuário.
- Sessão de emergência: `kind = password`, `subject = emergencia`, **não** é administradora (decisão 6: gestão de grupos só para administradores do Issabel; o root tem a CLI).
- Limite de tentativas: por IP (5 erros em 5 min) e por nome (20 erros em 5 min), detalhe no plano (C5). Como só existe um usuário aceito, alguém na rede pode travar a rota de emergência por 5 min de cada vez; aceito, porque a CLI do root continua disponível.

**6. Gestão de grupos pela tela.** `POST /api/groups/import` (CSV dentro do JSON, com ou sem substituir tudo) e `POST /api/groups/delete`, só para sessão administradora (senão `403 forbidden`). Mesma validação e mesma transação da CLI (`store.ParseGroupsCSV`, `ImportGroups`, `DeleteGroup`); a CLI `group import|list|del` fica como está. Depois de gravar, o daemon relê os grupos pelo mesmo caminho do SIGHUP (epoch novo; as telas abertas recebem `reload`). Corpo dessas duas rotas até 1 MiB (as demais seguem com 4 KiB).

**7. Purge de sessão.** `painel-ramais session purge --issabel <usuario>` apaga as sessões `issabel:admin:<usuario>` e `issabel:user:<usuario>` e manda o SIGHUP (os WebSockets dessas sessões fecham com `session_expired`/`revoked` e 4001). `session purge` e `--all` continuam como na fase 1.

**8. Tela sem framework e sem build, embutida no binário.** HTML, CSS e JavaScript (módulos ES, sem dependência externa, sem CDN, sem fonte remota) em `internal/web/static/`, embutidos com `go:embed`. O RPM continua com um binário só, e tela e daemon nunca ficam em versões diferentes. Rolagem virtual, mapa e gráfico em canvas são escritos no próprio código (poucas centenas de linhas), sem biblioteca.

### Alternativas consideradas

- **A (mais simples possível): item de menu do tipo framed apontando para `/painel-ramais/`, com login direto.** Descartada: o framed carrega uma URL fixa, sem lugar para emitir token novo por abertura, e o login direto foi revogado pelo usuário.
- **B: usar o mesmo segredo do daemon com leitura de grupo para o PHP.** Descartada pela decisão 5. Com segredo único, quem tem acesso ao arquivo pelo grupo do PHP emitiria qualquer `sub`, inclusive os da CLI, e não daria para desligar a integração sem desligar a CLI.
- **C: PHP chama a CLI por `sudo -u painel-ramais painel-ramais token`.** Descartada: exige editar o sudoers de um sistema do Issabel, cria um processo por abertura de página e amplia o que o usuário do PHP pode executar. É a alternativa que o ADR-002 deixou em aberto.
- **D: claim extra `adm: true` no payload.** Descartada: quebra o payload estrito de três campos e o vetor de teste do ADR-002; o prefixo no `sub` carrega a mesma informação sem mudar o formato.
- **E: bloquear fora do Issabel no Apache (Referer ou `mod_rewrite` sobre `Sec-Fetch-Dest`).** Descartada: mais regra na conf gerada pelo `%post` e no `httpd` do cliente, sem teste automatizado; no daemon a regra é testável em Go e a conf continua trivial. O Referer some com `Referrer-Policy` e proxies.
- **F: React com Vite, como no padrão da casa.** Descartada para este produto: traria Node e npm para o build do Windows e do RPM e uma cadeia de pacotes de terceiros para dentro de um PBX, e a rolagem virtual de 10.000 cards e o canvas teriam de ser escritos à mão de qualquer jeito. Volta à mesa se a tela crescer para várias páginas com estado complexo.
- **G: assets servidos pelo Apache a partir de `/usr/share/painel-ramais/`.** Descartada: mais um diretório no pacote, mudança na conf do Apache e risco de tela e daemon em versões diferentes; o embed custa uns centenas de KiB no binário.
- **H: senha de emergência perguntada na instalação (como a do admin da fase 1).** Descartada pela decisão 1 (gerada sem perguntar).

### Suposições feitas

- O Issabel é servido por HTTPS na mesma origem do `/painel-ramais/` (o cookie é `Secure` e o iframe precisa ser da mesma origem para `frame-ancestors 'self'`). A confirmar pela VM.
- Navegadores atuais (Chrome, Edge, Firefox, Safari 16.4 ou mais novo) mandam `Sec-Fetch-Dest`; o mais antigo cai na segunda barreira (script).
- O `issabel-menumerge` só concede grupos ao criar o item (lido no código, não executado): o subcomando `instalar.py menu` precisa tratar o caso do item já existente.
- O processo PHP do Issabel roda com um grupo único e conhecido (no código estudado, `asterisk`); qualquer processo com esse grupo lê a chave e emite token de administrador do painel. Aceitável porque esse mesmo grupo já lê `manager.conf`, que dá muito mais poder; a confirmar pela VM.
- "Documentada só no LEIAME" vale para a documentação de uso (README, roteiros); o contrato técnico (`docs/CONTRATOS.md`, este ADR e o plano da fase 2) cita a rota porque o código a implementa. A rota não é segredo: a proteção é a senha.

### Leitura da VM (`docs/fase2/leitura-vm.txt`, 2026-10-16)

Issabel 5 (issabel-framework 5.0.0-1, issabelPBX 2.12.0) em Rocky Linux 8.8, PHP 7.4.33 (php-fpm), Apache 2.4.37. O arquivo é dado colado da VM; os itens abaixo são o que ele mostra. Código do `issabel-menumerge`, do `paloSantoInstaller` e da `paloSantoACL` relido no clone do IssabelFoundation/framework.

| # | Item | Valor real | Efeito no desenho |
|---|---|---|---|
| 1 | Operator Panel e posição | `fop2`, `IdParent` `pbxconfig`, `Type` module, `order_no` 9 (é o FOP2 2.31.37, não tocar); próximo item `endpoint_configurator`, `order_no` 51 | o painel entra como `painel_ramais`, `pbxconfig`, module, `order_no` 10, sem renumerar nada |
| 2 | Usuário e grupo do PHP | `httpd` e `php-fpm` rodam como `asterisk:asterisk` (uid 992, gid 989) | chave `painel-ramais:asterisk` 0640 |
| 3 | `open_basedir` | não aparece na leitura (`disable_functions` vazio) | sem mudança de caminho; o módulo trata chave ilegível com mensagem clara e a Etapa 16b confere a leitura real |
| 4 | SELinux | `Disabled` | nada a fazer; o `%post` continua tratando `Enforcing` como hoje |
| 5 | Cabeçalhos globais | nenhum `Content-Security-Policy`, `X-Frame-Options` ou `frame-ancestors` no Apache (`headers_module` carregado, sem regra) | os cabeçalhos do daemon valem sozinhos |
| 6 | ACL | `administrator` id 1, `Operator` id 2, `extension` id 3; ação `access` id 1; tabelas `acl_resource`, `acl_group_permission`; usuário logado em `$_SESSION['issabel_user']` (código do framework) | item liberado para os grupos 1 e 2; administrador = membro do grupo id 1 |
| 7 | PHP | 7.4.33 com `hash`, `json`, `sqlite3`, `pdo_sqlite`; `random_bytes` e `hash_equals` são do núcleo | emissor viável sem extensão nova |
| 8 | HTTPS | Apache com OpenSSL responde em HTTPS | cookie `Secure` funciona |
| 9 | Tema e altura do iframe | não aparece na leitura | conferido na Etapa 16b (área de cerca de 1250 x 700 px) |
| 10 | `/etc/asterisk` | não aparece na leitura | o risco do `manager_custom_perm` em `unknown` continua (plano, Riscos) |
| 11 | Bancos do Issabel | `/var/www/db/menu.db` e `acl.db` `asterisk:asterisk` 0644 (pasta 0777); `menu(id, IdParent, Link, Name, Type, order_no)` | legíveis pelo usuário `painel-ramais` e pelo root em modo somente leitura |
| 12 | Ferramentas | `/usr/bin/issabel-menumerge` e `/usr/bin/issabel-menuremove` presentes | registro e remoção pelas ferramentas do próprio Issabel |
| 13 | Sessão do PHP | `session.gc_maxlifetime = 1440` (24 min) | a sessão do Issabel expira bem antes da do painel (12 h sem uso): Etapa 16b cobre |

Divergências entre a VM e o estudo sem VM:

| Estudo | VM | Consequência |
|---|---|---|
| PHP como módulo do httpd (suposição) | php-fpm, também `asterisk:asterisk` | nenhuma: o grupo da chave é o mesmo |
| SELinux talvez `Enforcing` | `Disabled` | nada a fazer nesta VM; o RPM mantém o tratamento |
| Operator Panel como item genérico | é o FOP2 comercial (`fop2`), `order_no` 9 | o painel fica em 10; nenhum item do Issabel é alterado |
| `isUserAdministratorGroup` da `paloSantoACL` devolve booleano | devolve o nome do grupo (membro do id 1), `false` (outros grupos) ou `NULL` (sem grupo) | o módulo só usa o valor como booleano (`(bool)`), nunca compara com `!== false` (plano, seção 8) |
| `issabel-menumerge` só concede grupos na criação | confirmado no código: no update só atualiza o menu e o recurso, nunca as permissões | `instalar.py menu --recriar` remove e registra de novo quando faltar grupo |

### Consequências

- Positivas: o operador entra pelo Issabel sem senha nova; a CLI e a homologação não mudam; a integração com o menu é descartável (apagar a chave desliga, sem parar o painel); administrador do Issabel gere grupos sem shell; tela e daemon saem no mesmo binário; nenhum arquivo do Issabel é alterado pelo daemon.
- Negativas / trade-offs:
  - Login direto revogado: sem Issabel funcionando, só a rota de emergência (senha no arquivo do root) ou a CLI.
  - O papel de administrador vale enquanto a sessão viver (12 h sem uso), mesmo que o usuário seja rebaixado no Issabel; o corte é o `session purge --issabel`.
  - Quem roda com o grupo do PHP do Issabel emite token de administrador do painel (ver Suposições).
  - Os vetores de teste do token mudam (o `sub` `issabel:admin` assinado com o segredo do daemon passa a ser recusado); o baseline do gitleaks (`.claude/agent-memory/seguranca`) precisa ser atualizado com os vetores novos.
  - Tela sem framework: manutenção depende de disciplina de código (módulos pequenos, funções puras testadas com `node --test`).

### Riscos aceitos (aceitos pelo usuário em 2026-10-16)

- Qualquer processo com o grupo do PHP do Issabel lê a chave e emite token de administrador do painel. Aceito: esse usuário já lê o `manager.conf`, e o administrador do painel só gerencia grupos.
- Sessões abertas sobrevivem à remoção da chave (até 12 h sem uso); o remédio é `painel-ramais session purge --all` (ou `--issabel <usuario>`). Aceito.
- O `jti` de uso único fica só em memória: depois de um restart do daemon, um token interceptado pode ser reaproveitado por até 65 s. Aceito.

Os três riscos continuam valendo com o ADR Aceito.

### Critério de validação

1. Testes Go: os vetores do plano (`docs/plans/2026-10-16-fase2-menu-issabel.md`, contrato C1) dão o resultado indicado; `GET /` com `Sec-Fetch-Dest: document` dá 303 para `/painel-ramais/acesso#`; com `iframe` dá 200 com a CSP exata; `/emergencia/` dá 200 com `frame-ancestors 'none'`; `POST /api/login` com outro usuário dá 401; `POST /api/groups/import` com sessão `issabel:user:` dá 403 e com `issabel:admin:` grava e gera epoch novo.
2. Chave: sem arquivo, `issabel_key` sai `ok` e o token `issabel:` dá 401; com 0644, `fail` com a correção; corrigida para 0640, passa a valer em até 60 s sem reiniciar.
3. Medição local (simulador de 10.000 ramais, plano seção 6): primeira exibição abaixo de 2 s e a rajada de 3.000 desregistros refletida na tela em menos de 1 s, sem tarefa longa acima de 200 ms depois da primeira exibição.
4. No Issabel real (spike): o item aparece logo depois do Operator Panel para `administrator` e `Operator` e não aparece para outros grupos; abrir o endereço direto mostra o aviso; o iframe abre a tela; `session purge --issabel` derruba a aba aberta em até 1 s.

### Verificação na VM (antes da release; Etapa 16b do roteiro)

Precisa do módulo PHP instalado. Saída de cada item anexada aqui:
1. `curl -skI https://127.0.0.1/painel-ramais/ -H 'Sec-Fetch-Dest: document'` e `-H 'Sec-Fetch-Dest: iframe'` pelo Apache do Issabel: 303 e 200, com os cabeçalhos do daemon e sem cabeçalho global conflitante.
2. Navegador logado como usuário do grupo `Operator` e como `administrator`: item na posição certa, tela no iframe, botão de grupos só para o administrador.
3. Usuário do grupo `extension`: sem item no menu e sem acesso ao `index.php?menu=painel_ramais`.
4. `ls -l /etc/painel-ramais-issabel.key` depois do `%post`: `painel-ramais asterisk` 0640; leitura pelo PHP comprovada pela tela abrindo.

### Agentes impactados

backend (verificador com duas chaves, rotas da tela, emergência, grupos pela API, purge, depcheck, build tag do debug, ferramenta local de medição, CONTRATOS), frontend (tela em `internal/web/static/` e medição em `tools/tela/`), devops (instalar.py sem senha, pendências 4 e 5, lado do instalador da 6, LEIAME, README; depois da VM: módulo PHP, `menu.xml`, grupo da chave, `instalar.py menu`), seguranca (auditoria do fluxo de token, da CSP e da chave), qa (medição e roteiro na VM).
