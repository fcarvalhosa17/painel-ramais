# Publicação do repositório: varredura de dados sensíveis

- Repositório público: https://github.com/fcarvalhosa17/painel-ramais
- Commit varrido: `c1215b828bf4c1feacb770229ae13d53ebd57568` (`c1215b8`), único commit do histórico (`git rev-list --all` = 1; refs: `main`, `origin/main`, `origin/HEAD`; sem stash, sem tag)
- Data da varredura: 2026-10-15
- Commits posteriores: consultar `git log` (este arquivo não guarda hash de commit próprio).
- Nota rc.8: este arquivo é o registro das varreduras feitas até a rc.7. Os achados e arquivos citados com `_test.go`, `testdata` de AMI e `tests/` descrevem o commit `c1215b8`; esses arquivos de teste foram **removidos do repositório na rc.8** e já não existem na árvore. Na rc.9 também saíram as ferramentas de carga e de simulação de AMI e as capturas de teste (recuperáveis pela tag `v0.1.2-rc.8`). O fluxo de publicação atual está na seção 7.

## Registro franco

- (a) O `c1215b8` foi publicado no GitHub **antes** da varredura completa de dados sensíveis, contrariando a regra de varrer primeiro.
- (b) A publicação foi em 2026-10-15, nesta sessão do Claude Code, pelo orquestrador principal, atendendo o pedido do usuário "sobe tudo para o meu git https://github.com/fcarvalhosa17/painel-ramais". Comando: `git remote add origin https://github.com/fcarvalhosa17/painel-ramais.git && git push -u origin main`.
- (c) Antes desse push houve só uma busca leve com `git grep` (padrões de senha, token e chave privada, IPs privados e e-mails), sem gitleaks.
- (d) A varredura posterior (abaixo) cobriu esse commit e não achou dado sensível real. Não há histórico a limpar: sem rotação de segredo, sem force push, sem reescrita.
- (e) Regra daqui em diante: nenhum push acontece antes da varredura do que vai subir, principalmente quando houver captura real no projeto. Rodar o gitleaks (comando abaixo) e esta varredura **antes** do push.

Decisões mantidas: a troca de `200.1.2.3` por `203.0.113.3` (RFC 5737) e os IPs `10.1.1.x` das capturas do simulador (privados, não identificam ninguém).

## 1. gitleaks sobre todo o histórico

Ferramenta: gitleaks 8.30.1 (release oficial `v8.30.1`, de 2026-03-21), binário portátil em `.tools/gitleaks/` (pasta ignorada pelo git, nada instalado globalmente).

Integridade do download: o SHA-256 do `gitleaks_8.30.1_windows_x64.zip` bate com o `gitleaks_8.30.1_checksums.txt` do mesmo release:

```
d29144deff3a68aa93ced33dddf84b7fdc26070add4aa0f4513094c8332afc4e  gitleaks_8.30.1_windows_x64.zip
gitleaks_8.30.1_windows_x64.zip: OK
```

Limitação: o checksum vem do mesmo release. Ele prova que o download chegou íntegro, não a autoria (a assinatura do release não foi verificada).

Comando (histórico inteiro):

```
.tools/gitleaks/gitleaks.exe git --no-color --log-opts="--all" --redact -v .
```

Saída (resumida aos campos de cada achado):

```
RuleID: generic-api-key  File: internal/api/sec_test.go    Line: 40   Commit: c1215b828bf4c1feacb770229ae13d53ebd57568  Secret: REDACTED
RuleID: generic-api-key  File: internal/auth/auth_test.go  Line: 19   Commit: c1215b828bf4c1feacb770229ae13d53ebd57568  Secret: REDACTED
RuleID: generic-api-key  File: internal/auth/auth_test.go  Line: 21   Commit: c1215b828bf4c1feacb770229ae13d53ebd57568  Secret: REDACTED
RuleID: generic-api-key  File: internal/api/api_test.go    Line: 242  Commit: c1215b828bf4c1feacb770229ae13d53ebd57568  Secret: REDACTED
RuleID: generic-api-key  File: docs/CONTRATOS.md           Line: 185  Commit: c1215b828bf4c1feacb770229ae13d53ebd57568  Secret: REDACTED
INF 1 commits scanned.
INF scanned ~1189854 bytes (1.19 MB) in 382ms
WRN leaks found: 5
```

Também rodei `gitleaks dir` sobre a árvore exportada do commit (`git archive c1215b8`), e ele deu os mesmos 5 achados.

Classificação dos 5 achados: **todos fictícios**.

| Arquivo:linha | Valor | Classificação |
|---|---|---|
| `internal/auth/auth_test.go:19` | segredo HMAC `AAECAw...Hh8=` | Fictício: são os bytes 0x00 a 0x1f, o vetor de teste documentado em `docs/CONTRATOS.md` §3.3 ("nunca usar em produção"). O segredo de produção é gerado no `%post` com `head -c 32 /dev/urandom` (`packaging/scripts/postinstall:22`). |
| `internal/auth/auth_test.go:21`, `internal/api/api_test.go:242`, `docs/CONTRATOS.md:185` | token HMAC `eyJzdWIiOiJpc3NhYmVsOmFkbWluIi...` | Fictício: token assinado com o vetor de teste acima. Não vale em nenhuma instalação real. |
| `internal/api/sec_test.go:40` | senha `senha-correta-123` | Fictício: senha de usuário de teste criado no próprio teste. |

## 2. Varredura manual (git grep + histórico)

Como o histórico tem um commit só, `git log -p` equivale ao conteúdo da árvore em `c1215b8`. A busca foi feita com `git grep` em `c1215b8`.

| Categoria | Achados (arquivo:linha, commit c1215b8) | Classificação |
|---|---|---|
| Senhas e segredos em código | ferramentas de teste de AMI e de carga (`segredo-de-teste` e uma senha de exemplo), `cmd/painel-ramais/capturar_test.go:17`, `daemon_test.go:92`, `supervisor_test.go:115`, `internal/ami/client_test.go:159`, `internal/api/api_test.go` (`uma-senha-longa`, `errada-errada`), `tests/test_instalar.py:326` (`senha-bem-longa`) | Fictício. São o simulador AMI e o teste de carga, que nunca entraram no binário de produção. Os segredos reais (`secret`, `ami-secret`) são gerados no `%post`. |
| Chaves privadas, tokens de nuvem/GitHub, hashes bcrypt reais | nenhum (`BEGIN ... PRIVATE KEY`, `ghp_`, `github_pat_`, `AKIA`, `xox?-`, `sk-`, `AIza`, `$2a$NN$...`) | n/a |
| IPs privados de exemplo | `10.0.5.23/24/30/31/50` e `10.0.0.1/2/5/9` em `docs/CONTRATOS.md`, `README.md`, testes Go, `internal/ami/testdata`, comentários de `internal/ami/calls/util.go:21` e `internal/ami/pjsip/pjsip.go:215`, `tests/test_instalar.py:335` | Fictício (RFC 1918). Trocados por 192.0.2.0/24 (item 3), menos o `README.md`. |
| IPs das capturas sintéticas | `10.1.1.x` nas capturas de teste da época | Fictício: gerados por simulador. Capturas e simulador foram removidos na rc.9 (recuperáveis pela tag `v0.1.2-rc.8`). |
| IP público | `200.1.2.3` (tronco `trunk-acme`) em `internal/ami/testdata/sip/peers.txt:45` e `internal/ami/sip/sip_test.go:70` | Fictício, mas cai num bloco roteável real (LACNIC). Trocado por `203.0.113.3` (TEST-NET-3). |
| IPs de documentação | `203.0.113.x`, `198.51.100.7`, `2001:db8::/32`, `fe80::...` | Fictício (faixas reservadas para documentação). |
| Falsos positivos de IP | `108.86.0.20`, `1.0.11.23` (firmware no User-Agent de Yealink/Grandstream), `1.8.32.3` (versão do Asterisk), `10.200.x.y` / `fd00:200::` (destino da anonimização, é lógica), `127.0.0.1`, `0.0.0.0`, `255.255.255.255` | Não são endereços de ninguém. |
| Hostnames e domínios | `pbx.empresa.com.br`, `pbx-matriz.empresa.com.br`, `maria.souza@empresa.com.br` (`internal/capture/anonymize_test.go:17-31`), `host.dominio.com` (`internal/capture/sanitize.go:254`), `usuario@host.dominio` (`docs/CONTRATOS.md:1080`), `evil.example` (`internal/api/api_test.go:484`), `SERVIDOR` / `<ip-do-servidor>` em docs | Fictício (placeholders). |
| E-mails | `maria.souza@empresa.com.br` (fixture de anonimização), `usuario@host.dominio`. Autor e committer do commit: `274128241+fcarvalhosa17@users.noreply.github.com` (noreply do GitHub) | Fictício. Não há e-mail pessoal no histórico. |
| Telefones | `11987654321`, `01133334444`, `1133334444`, `21999887766`, `91900000123`, `11980000NNN`, `119NNNNNNNN` gerados no teste de carga | Fictício (sequências e padrões de exemplo). Os outros números longos são timestamps unix (`1791902000`...) e epochs (`1791933971602-9`). |
| Pessoas, clientes e instituições | `Maria Souza`, `Joao Pereira`, `Joao Lima`, `Ana Paula`, `Carlos Eduardo Lima`, `Pessoa NNN`, `ACME LTDA`, `cliente-acme-sa`, `Padaria Estrela`, `Transportes Rio`; grupos `Vendas`, `Suporte`, `Diretoria`, `Recepcao` (`packaging/grupos-exemplo.csv`) | Fictício (nomes genéricos). Nenhum cliente ou instituição real. |
| Nome do autor | `README.md:1` (linha de copyright com o nome completo); usuário de teste `fabio` em `internal/api/*_test.go`, `internal/store/store_test.go`, `internal/auth/auth_test.go`, `cmd/painel-ramais/cli_test.go` e no exemplo de login de `docs/CONTRATOS.md:154,162` | Real, mas não é dado sensível: o copyright é intencional, e `fabio` é um nome de usuário de teste sem credencial real. |
| Caminhos locais do Windows | `docs/PROMPT-FASE-1.md:4` (`D:\CLaude\painel-ramais`); nenhum `C:\Users\...` | Real, mas não sensível: mostra a pasta de trabalho, sem nome de usuário do Windows. |
| `manager_custom.conf` | bloco gerado com `deny 0.0.0.0/0.0.0.0` e `permit 127.0.0.1/255.255.255.255` (`docs/adr/ADR-003-pacote-rpm-e-integracao-issabel.md:60-61`, `packaging/scripts/postinstall`) | É configuração real do produto, sem IP de exemplo. Não foi alterado. |

Conclusão: o `c1215b8` não tem nenhum dado real sensível. Nada exige rotação.

## 3. O que foi ajustado

Troquei os IPs de exemplo `10.0.5.N` e `10.0.0.N` por `192.0.2.N` (TEST-NET-1, RFC 5737), mantendo o último octeto. Assim nenhum par de IPs que era distinto passa a coincidir. Troquei também `200.1.2.3` por `203.0.113.3`. Só mexi em exemplos e fixtures. A lógica não mudou: o bloco AMI continua `permit 127.0.0.1`, e o destino da anonimização continua `10.200.x.y`.

Arquivos:

- `docs/CONTRATOS.md` (4 linhas de exemplo JSON)
- `internal/ami/calls/util.go`, `internal/ami/pjsip/pjsip.go` (só comentários)
- arquivos de teste e fixtures de AMI da época (removidos na rc.8)

Fora da troca: o `README.md` (3 linhas com `10.0.5.23` e `10.0.5.24`, nas linhas 177, 198 e 242) é do @devops nesta rodada e ficou para ele trocar para `192.0.2.23` e `192.0.2.24`, igual ao `docs/CONTRATOS.md`. `config.yaml.example`, `grupos-exemplo.csv` e o bloco do `manager_custom.conf` não tinham IP 10.0.x.

Verificação:

- A compilação (`go build ./...`) passou antes e depois da troca.
- `gofmt -l cmd internal`: vazio.
- `git grep -nE '\b10\.0\.'` agora só retorna as 3 linhas do `README.md`.
- Os finais de linha não mudaram: `git ls-files --eol` dá `i/lf w/lf` em todos os arquivos tocados.

## 4. Recomendações (não bloqueiam)

1. Pôr o gitleaks no pre-commit local, com um `.gitleaks.toml` que libere só os 5 vetores de teste acima (por fingerprint), para que um segredo novo quebre o build.
2. Fazer o daemon recusar subir se `/etc/painel-ramais/secret` for o vetor publicado `AAECAw...Hh8=` (bytes 0x00 a 0x1f). Hoje ele só checa o tamanho, e quem copiar o exemplo do `CONTRATOS.md` aceita tokens que qualquer pessoa consegue assinar.
3. Opcional: trocar `empresa.com.br` (domínio que pode existir de verdade) por um nome em `.invalid` ou `example.com` em `internal/capture/anonymize_test.go`, e o usuário de teste `fabio` por um nome genérico.

## 5. Nova varredura antes da pré-release 0.1.2-rc.1 (2026-10-15)

Ferramenta: gitleaks 8.30.1 (`.tools/gitleaks/gitleaks.exe`), regras padrão, `--redact`.

| Escopo | Comando | Achados |
|---|---|---|
| Histórico inteiro | `gitleaks git . --redact --no-banner` | 5 |
| Árvore de trabalho (rastreados e não rastreados, respeitando `.gitignore`, inclui `.github/workflows/`) | `gitleaks dir <cópia de git ls-files -co --exclude-standard> --redact --no-banner` | 5 |

Os 5 são os mesmos já conhecidos (regra `generic-api-key`, todos fictícios, de teste): `internal/api/sec_test.go:40`, `internal/auth/auth_test.go:19`, `internal/auth/auth_test.go:21`, `internal/api/api_test.go:242` e `docs/CONTRATOS.md:185` (segredo HMAC de teste 0x00 a 0x1f, tokens assinados com ele e a senha `senha-correta-123`). Nenhum achado novo nos arquivos acrescentados nesta rodada (máscara/filtro de capturas, `homologar`, analisador, workflows, scripts de CI).

## 6. Varredura antes da pré-release 0.1.2-rc.2 (2026-10-15)

Ferramenta e comando iguais aos da seção 5 (`gitleaks detect --source .`, histórico inteiro): 5 achados, todos no commit `c1215b8` e nos mesmos cinco vetores fictícios de teste da seção 5. Nenhum achado novo.

## 7. Fluxo de publicação (a partir da rc.8)

O repositório não tem testes automatizados: publicar é **compilar e empacotar**. Ordem:

1. `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` sem erro.
2. gitleaks (`.tools/gitleaks/gitleaks.exe dir .` e `git ... --log-opts` nos commits novos) antes do push; ignorar só achados em `.tools\` e `dist\`. Segredo real: parar.
3. `git push origin main` (sem force).
4. `.\build.ps1 release -Version 0.1.2 -Pre rc.N` (antes, `-DryRun`): gera o binário (versão por `-ldflags -X main.version`), o RPM, o tar.gz e o `SHA256SUMS`, cria a tag `v0.1.2-rc.N` e publica a Release como **pré-release** (`--latest=false`) com a nota `NÃO HOMOLOGADA. Uso exclusivo em laboratório.`. O `build.ps1` não executa o binário nem testes.

O teste real é feito no servidor, seguindo o `docs/homologacao/ROTEIRO-MANUAL.md`.

rc.9: `.\build.ps1 release -Version 0.1.2 -Pre rc.9`; a atualizacao parte da rc.8 (esquema 5 para 6, backup `painel.db.bak-v5`).
