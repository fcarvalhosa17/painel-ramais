# Componentes

O que existe, o que cada peça faz e quem importa quem. Grafo oficial de importação (sem ciclo):
[CONTRATOS](../CONTRATOS.md), seção 12. Direção única: `cmd -> api -> state -> model <- ami/{sip,pjsip} -> ami`.

## Daemon Go, por pacote

Módulo único (`go.mod`). Dependências externas: `modernc.org/sqlite`, `gopkg.in/yaml.v3`, `golang.org/x/crypto/bcrypt`,
`github.com/coder/websocket`. O resto é stdlib.

| Pacote | Papel | Arquivos de entrada |
|---|---|---|
| `cmd/painel-ramais` | binário único: `serve` (daemon) e a CLI de administração. Monta tudo em `newDaemon` e sobe as goroutines `estado`, `hub`, `coletor`, `http`, `depcheck`, `registros`, `decisoes`, `historico`, `nomes`, `manutencao` | `main.go`, `serve.go` |
| (mesmo) supervisor AMI | conexão única, login, detecção de tecnologia, snapshot, laço ao vivo, releitura de inventário, leitura de filas | `supervisor.go`, `supervisor_queues.go` |
| (mesmo) CLI | `user`, `group`, `session`, `token`, `capturar`, `filtrar`, `actions`, `version`; manda SIGHUP ao daemon depois de gravar | `cli*.go`, `notify.go`, `capturar.go`, `filtrar.go` |
| (mesmo) suporte | socket activation (`LISTEN_FDS`), `metrics.json` em `/run/painel-ramais`, endpoint de heap só com `-tags debug` | `listen.go`, `metrics.go`, `debug.go`, `debug_off.go` |
| `internal/ami` | cliente AMI: framing, login, backoff de 1 a 30 s, **allowlist fixa de actions** (recusa antes de escrever no socket), detecção de versão e tecnologia | `client.go`, `actions.go`, `detect.go` |
| `internal/ami/pjsip`, `internal/ami/sip` | adaptadores por tecnologia: traduzem mensagens AMI em eventos de `model` (registro, dispositivo, inventário) | `pjsip.go`, `enrich.go`, `sip.go` |
| `internal/ami/calls` | tradução dos eventos de canal e chamada, comum às duas tecnologias; descarta canais auxiliares | `calls.go` |
| `internal/ami/queues` | tradução dos eventos de fila e agente e leitura da `QueueStatus` | `queues.go` |
| `internal/model` | tipos compartilhados: `Extension`, `Call`, `LiveCall`, `Queue`, eventos normalizados, `Batch`, `Reset` | `model.go`, `events.go`, `live.go`, `reg.go` |
| `internal/state` | dono único do estado em memória (uma goroutine): ramais, canais, bridges, chamadas ao vivo, filas, visões de cada ramal, contadores do relatório de registro; emite `Batch` a cada 250 ms e `Reset` a cada epoch | `state.go`, `view.go`, `channels.go`, `livecalls.go`, `queues.go`, `groups.go`, `regreport.go` |
| `internal/api` | HTTP: rotas, envelope, sessão, papel administrador, gzip, tela embutida; **hub** do WebSocket (projeção pública, ring buffer de 40 lotes, cache do snapshot, fan-out) | `server.go`, `authn.go`, `data.go`, `groups.go`, `queues.go`, `registrations.go`, `tela.go`, `hub.go`, `ws.go`, `frames.go`, `issabel_acl.go` |
| `internal/auth` | bcrypt, id de sessão, token HMAC de uso único (cache de `jti`), limitadores de login | `auth.go`, `token.go` |
| `internal/store` | SQLite: migrações, usuários, sessões, visões (grupos), CSV, histórico, nomes, filas, relatório de registro | `store.go`, `groups*.go`, `groupcsv.go`, `history.go`, `regreport.go`, `queues.go`, `names.go`, `users.go` |
| `internal/history` | amostrador por minuto (total e por visão), lacuna sem AMI, retenção | `history.go` |
| `internal/reglog` | gravador em lote do relatório de registro e log das decisões de contagem, fora da goroutine do estado | `reglog.go`, `decisions.go` |
| `internal/depcheck` | verificação de dependências a cada 60 s (login AMI, `manager_custom.conf`, Apache, chave do Issabel, item do menu); fonte única da chave do Issabel para o verificador de tokens | `depcheck.go` |
| `internal/config` | leitura e validação estrita do `config.yaml` e dos segredos | `config.go`, `perm_unix.go` |
| `internal/capture` | formato da captura de AMI e máscara/anonimização (usado por `capturar`, `filtrar` e `homologar`) | `capture.go`, `sanitize.go` |
| `internal/web` | só embute `static/` no binário (`//go:embed`) | `web.go` |

## Tela (`internal/web/static`)

Sem framework e sem nada externo (CSP `default-src 'none'`). Módulos ES, todos em `assets/`:

| Arquivo | Papel |
|---|---|
| `index.html` | estrutura das três abas (Ramais, Chamadas, Registros), cabeçalho com a logo, diálogos |
| `acesso.html` | aviso "abra pelo menu PBX do Issabel" (sem script) |
| `app.js` | entrada: sessão, abas, aba Ramais, coluna Visões, "Últimas 24 h", ligação dos módulos |
| `conexao.js` | única camada de rede: REST com envelope e ciclo do `/ws` |
| `dados.js` | snapshot, aplicação de delta, lacuna, desvio de relógio (sem DOM) |
| `estado.js` | regras puras da tela: estados, contagens, rótulos de visão (`rotuloVisao`, `rotuloVisoes`), seleção (`proximaSelecao`) |
| `visoes.js` | botão de visão (clique e Ctrl/Cmd+clique) e seletor suspenso de visões |
| `lista.js`, `linhas.js` | rolagem virtual (cartões de ramal e linhas de lista) |
| `edicao.js` | painel do ramal, seleção múltipla, vínculo manual, "Nova visão" |
| `grupos.js` | "Gerenciar visões": renomear, faixas, excluir, importar CSV |
| `grafico.js` | gráfico SVG "Últimas 24 h" |
| `instabilidade.js` | bloco "Instabilidade de registro (24 h)" |
| `registros.js` | aba Registros: tabela por cursor, detalhe do ramal, gráfico, exportação CSV |
| `chamadas.js`, `filas.js` | aba Chamadas: listas de chamadas e painel de filas |
| `app.css` | tema claro do Manual da Marca CAM (tokens em `:root`) e `@font-face` das fontes locais |
| `cam-logo.png`, `malha.svg`, `icone.svg`, `grao.svg` | imagens da tela |
| `fonts/` | Exo 2 e Barlow em woff2 (latin e latin-ext), `OFL-Exo2.txt`, `OFL-Barlow.txt`, `ORIGEM.txt` (URL e SHA-256 de cada arquivo) |

Tema e marca: vermelho CAM só no cabeçalho (linha de destaque), na aba ativa e no contorno de foco; resto em grafite e
cinzas. Decisões D47 a D52 em [desvios.md](../design/desvios.md); guia visual em [DESIGN.md](../../tools/tela/DESIGN.md);
logo original em `docs/design/marca/cam-logo.png`.

## Integração com o Issabel

| Peça | Papel |
|---|---|
| `packaging/issabel/menu.xml` | item `painel_ramais` no menu `pbxconfig`, ordem 10, grupos `administrator` e `Operator` |
| `packaging/issabel/painel_ramais/index.php` | só define `_moduleContent`: lê `/etc/painel-ramais-issabel.key`, decide papel pelo `acl` do Issabel, assina token de 60 s e devolve o `<iframe src="/painel-ramais/#t=<token>">` |
| `packaging/httpd.conf.tmpl` | vira `/etc/httpd/conf.d/painel-ramais.conf`: proxy de `/painel-ramais/` e do `/ws`; `/debug` e `/api/health` fora do proxy |

## Empacotamento e instalador

| Peça | Papel |
|---|---|
| `packaging/nfpm.yaml` | lista de arquivos do RPM (binário, config `noreplace`, units, módulo PHP, menu.xml, template do Apache, CSV de exemplo) |
| `packaging/scripts/preinstall` | cria o usuário de sistema `painel-ramais` |
| `packaging/scripts/postinstall` | segredos, chave do Issabel, bloco AMI em `manager_custom.conf` (29 `eventfilter`), conf do Apache, SELinux, menu, systemd |
| `packaging/scripts/preremove`, `postremove` | desfazem a integração (menu, bloco AMI, conf do Apache, units) |
| `packaging/painel-ramais.service`, `.socket` | unit endurecida e socket `127.0.0.1:8790` (socket activation) |
| `instalar.py` | instalador de campo (Python 3.6, só stdlib); contrato em [CONTRATOS](../CONTRATOS.md), seção 15 |
| `build.ps1` | build no Windows: binário Linux, RPM (nfpm), bundle, `SHA256SUMS`, tag e release |
| `tools/bundle.py` | monta `dist/painel-ramais-<versao>.tar.gz` com modo 0755 e LF |
| `tools/ci-build.sh`, `tools/rpm-smoke.sh`, `tools/check-lf.ps1` | build manual em Linux, smoke do RPM em container, checagem de CRLF |
| `tools/analisar_homologacao.py` | lê o pacote do `homologar` e lista divergências |
