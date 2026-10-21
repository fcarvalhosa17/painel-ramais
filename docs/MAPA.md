# Mapa do Painel de Ramais

Ponto de partida para qualquer tarefa. Versão descrita: `v0.1.2-rc.10`. Caminhos relativos a este arquivo (`docs/`).
Na tela o "grupo" do código se chama **Visão** ("Sem grupo" aparece como "Sem visão"): veja
[00-visao-geral.md](arquitetura/00-visao-geral.md), seção "Grupo no código, Visão na tela".

## 1. Por onde começar

- O que é, peças e desenho: [arquitetura/00-visao-geral.md](arquitetura/00-visao-geral.md).
- Regras de trabalho no repositório: [../CLAUDE.md](../CLAUDE.md).
- Formato exato de tudo (JSON, SQL, CLI, instalador): [CONTRATOS.md](CONTRATOS.md). Em divergência, vale ele.
- Guia do técnico de campo (instalar, atualizar, voltar, conferir): [../README.md](../README.md).

## 2. Estrutura do repositório

```
cmd/painel-ramais/       binário único: daemon (serve), supervisor AMI, CLI
internal/ami/            cliente AMI e allowlist; pjsip/, sip/, calls/, queues/ traduzem eventos
internal/state/          estado em memória (ramais, chamadas, filas, visões, relatório de registro)
internal/api/            REST, WebSocket (hub), sessão, tela embutida
internal/store/          SQLite: migrações, visões, histórico, relatório, filas, sessões
internal/{auth,config,depcheck,history,reglog,model,capture,web}/
internal/web/static/     tela (index.html, assets/*.js, app.css, fonts/, imagens)
packaging/               RPM (nfpm), units systemd, Apache, módulo do Issabel, scripts do RPM
instalar.py              instalador de campo
build.ps1, tools/        build, bundle, smoke, análise de homologação, guia visual da tela
docs/                    contratos, ADRs, arquitetura, design, planos, specs, homologação
```

Pacote por pacote: [arquitetura/componentes.md](arquitetura/componentes.md).

## 3. Funcionalidades

Tabela completa de arquivos por funcionalidade: [arquitetura/funcionalidades.md](arquitetura/funcionalidades.md).

- **Aba Ramais** (cartões, filtros, "Últimas 24 h", "Instabilidade de registro"): leia
  [funcionalidades.md](arquitetura/funcionalidades.md), seção "Aba Ramais", e [CONTRATOS.md](CONTRATOS.md), seções 2.1,
  4.1 e 5.2. Código em `internal/web/static/assets/` (`app.js`, `lista.js`, `estado.js`, `grafico.js`,
  `instabilidade.js`) e `internal/state/`.
- **Visões** (faixas, vínculo manual, N visões por ramal, seleção com Ctrl ou Cmd, CSV `visao,ramal` ou `grupo,ramal`):
  leia [funcionalidades.md](arquitetura/funcionalidades.md), seção "Visões", [CONTRATOS.md](CONTRATOS.md), seções 4.6 e 9,
  e [adr/ADR-006-ramal-em-n-grupos.md](adr/ADR-006-ramal-em-n-grupos.md). Código em `internal/api/groups.go`,
  `internal/store/` (`groups*.go`, `groupcsv.go`), `internal/state/groups.go` e na tela `visoes.js`, `grupos.js`,
  `edicao.js`, `estado.js`.
- **Aba Registros** (quedas, "Fora do ar há", gráfico, CSV): leia [funcionalidades.md](arquitetura/funcionalidades.md),
  seção "Aba Registros", e [CONTRATOS.md](CONTRATOS.md), seção 4.7. Código em `internal/api/registrations.go`,
  `internal/state/regreport.go`, `internal/reglog/`, `internal/store/regreport.go` e na tela `registros.js`.
- **Aba Chamadas** (uma linha por chamada): leia [funcionalidades.md](arquitetura/funcionalidades.md), seção "Aba
  Chamadas", e [CONTRATOS.md](CONTRATOS.md), seção 2.3. Código em `internal/state/livecalls.go`, `internal/ami/calls/` e
  na tela `chamadas.js`, `linhas.js`.
- **Filas** (painel de filas, renomear): leia [funcionalidades.md](arquitetura/funcionalidades.md), seção "Filas", e
  [CONTRATOS.md](CONTRATOS.md), seções 2.4 e 4.8. Código em `internal/state/queues.go`, `internal/ami/queues/`,
  `cmd/painel-ramais/supervisor_queues.go`, `internal/api/queues.go` e na tela `filas.js`.
- **Entrada pelo menu do Issabel e emergência**: leia [arquitetura/seguranca.md](arquitetura/seguranca.md), seções
  "Entrada pelo menu" e "Acesso de emergência", e [CONTRATOS.md](CONTRATOS.md), seções 1.3 e 3. Código em
  `packaging/issabel/`, `internal/api/authn.go`, `internal/api/tela.go`, `internal/auth/`.
- **Tema e marca CAM** (tema claro, Exo 2 e Barlow, logo): leia [funcionalidades.md](arquitetura/funcionalidades.md),
  seção "Tema e marca CAM", e [design/desvios.md](design/desvios.md), linhas D47 a D49. Código em
  `internal/web/static/assets/app.css`, `assets/fonts/`, `assets/cam-logo.png`, `assets/malha.svg`.
- **Saúde e diagnóstico** (`/api/health`, dependências, captura): leia [arquitetura/api-endpoints.md](arquitetura/api-endpoints.md),
  seção "Dados ao vivo", e [CONTRATOS.md](CONTRATOS.md), seção 4.4. Código em `internal/api/data.go`,
  `internal/depcheck/`, `internal/capture/`.
- **Instalador** (verificar, instalar, status, rollback, remover, menu, homologar): leia [../README.md](../README.md),
  seções 4 a 8, [../packaging/LEIAME-INSTALACAO.md](../packaging/LEIAME-INSTALACAO.md) e [CONTRATOS.md](CONTRATOS.md),
  seção 15. Código em `instalar.py` e `packaging/scripts/`.
- **Pacote, build e publicação**: leia [arquitetura/componentes.md](arquitetura/componentes.md), seção "Empacotamento e
  instalador", e [publicacao.md](publicacao.md). Código em `packaging/nfpm.yaml`, `build.ps1`, `tools/bundle.py`.

## 4. Assuntos transversais

- **Fluxo de dados** (evento AMI até a tela, tela até a API, gravação no banco): [arquitetura/fluxo-dados.md](arquitetura/fluxo-dados.md).
- **Todas as rotas REST e o WebSocket**: [arquitetura/api-endpoints.md](arquitetura/api-endpoints.md); formato exato em
  [CONTRATOS.md](CONTRATOS.md), seções 1 a 5. Código em `internal/api/server.go` (função `Handler`).
- **Banco e migrações** (esquema 6): [arquitetura/banco-migracoes.md](arquitetura/banco-migracoes.md) e
  [CONTRATOS.md](CONTRATOS.md), seção 10. Código em `internal/store/store.go`.
- **Arquivos, chaves, logs e serviço no servidor**: [arquitetura/servidor-caminhos.md](arquitetura/servidor-caminhos.md).
- **Segurança** (token HMAC, administrador, AMI só leitura): [arquitetura/seguranca.md](arquitetura/seguranca.md).
- **Cliente AMI** (actions permitidas, eventos, inventário): [CONTRATOS.md](CONTRATOS.md), seção 7. Código em
  `internal/ami/actions.go` e `cmd/painel-ramais/supervisor.go`.
- **Configuração** (`config.yaml`): [CONTRATOS.md](CONTRATOS.md), seção 6. Código em `internal/config/config.go`.
- **CLI do daemon**: [CONTRATOS.md](CONTRATOS.md), seção 8. Código em `cmd/painel-ramais/cli*.go`.
- **Decisões tomadas e por quê**: [arquitetura/decisoes.md](arquitetura/decisoes.md) e [adr/](adr/).
- **O que está planejado e não existe** (Etapa 2: Docker, keepalived, MariaDB, CI/CD da CAM):
  [arquitetura/planejado.md](arquitetura/planejado.md).

## 5. Referência e histórico

| Documento | Uso |
|---|---|
| [referencia-tecnica.md](referencia-tecnica.md) | texto técnico antigo do README (config, CLI, API, problemas comuns) |
| [design/desvios.md](design/desvios.md), [../tools/tela/DESIGN.md](../tools/tela/DESIGN.md) | regras e desvios da tela |
| [homologacao/](homologacao/) | roteiro manual e pendências de homologação (histórico das rc) |
| [plans/](plans/), [specs/](specs/) | planos e specs de cada release (histórico) |
| [PROMPT-FASE-1.md](PROMPT-FASE-1.md), [ADENDO-FASE-1-INSTALADOR.md](ADENDO-FASE-1-INSTALADOR.md), [DECISOES-FASE-1.md](DECISOES-FASE-1.md) | especificação e decisões da fase 1 |
| [relatorio-carga.md](relatorio-carga.md), [relatorio-auditoria.md](relatorio-auditoria.md), [fase2/](fase2/) | medições e auditorias antigas |

## 6. Manutenção deste mapa

Toda entrega que mudar comportamento atualiza este arquivo e `docs/arquitetura/` **no mesmo commit**. Funcionalidade nova
ganha um item na seção 3; arquivo novo entra em `arquitetura/funcionalidades.md`. Limite deste arquivo: cerca de 150
linhas.
