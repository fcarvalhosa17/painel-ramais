# Funcionalidades e onde estão no código

Caminhos relativos à raiz do repositório. "Tela" = `internal/web/static/`. Contratos citados: [CONTRATOS](../CONTRATOS.md).

## Aba Ramais

Estado de cada ramal ao vivo, em cartões ou blocos compactos, com busca, filtros de estado e a coluna Visões.

| Parte | Arquivos |
|---|---|
| Entrada, abas, coluna Visões, contadores, filtros, densidade | Tela `assets/app.js`, `index.html` |
| Cartões e rolagem virtual | `assets/lista.js` |
| Regras de estado, contagem e rótulo | `assets/estado.js` |
| "Últimas 24 h" (gráfico) | `assets/grafico.js`; dados de `GET /api/history` (`internal/api/data.go`, `internal/store/history.go`, amostrador `internal/history/history.go`) |
| "Instabilidade de registro (24 h)" | `assets/instabilidade.js`; `GET /api/registrations/summary` (`internal/api/registrations.go`) |
| Painel do ramal (detalhe) | `assets/edicao.js` |
| Origem dos dados | `internal/state/` (`state.go`, `view.go`), adaptadores `internal/ami/pjsip`, `internal/ami/sip`, `internal/ami/calls` |
| Contrato | CONTRATOS 2.1, 2.2, 4.1, 5.2 |

## Visões (o "grupo" do código)

Agrupar ramais por faixa numérica e por vínculo manual; um ramal pode estar em várias visões; ramal sem vínculo fica em
"Sem visão" (valor interno `"Sem grupo"`). Só administrador do Issabel edita.

| Parte | Arquivos |
|---|---|
| Seleção de visões (clique simples escolhe uma; Ctrl ou Cmd + clique soma ou tira; "Todos os ramais" volta a todos) | `assets/estado.js` (`proximaSelecao`, `rotuloVisao`, `rotuloVisoes`, `naVisoes`), `assets/visoes.js` (`ligarItemVisao`, `seletorVisoes`); usada em `app.js`, `registros.js`, `chamadas.js` |
| Agregados com várias visões | `assets/conexao.js` (`serieDasVisoes`: `group` repetido), `assets/app.js`, `assets/registros.js`, `assets/instabilidade.js`; servidor soma em `internal/api/data.go` (`queryGroups`, `seriesMeta`, `meta.overlap`) |
| "Gerenciar visões" (renomear, faixas, excluir, importar CSV) | `assets/grupos.js` |
| "Nova visão", adicionar e remover ramais (vínculo manual), seleção múltipla | `assets/edicao.js` |
| Rotas | `internal/api/groups.go` (`/api/groups/*`), `internal/api/authn.go` (`adminOnly`), `internal/api/issabel_acl.go` |
| Banco, validação, CSV | `internal/store/groups.go`, `groups_edit.go`, `groupcsv.go` (cabeçalho `visao` ou `grupo`, mais `ramal`, em qualquer ordem) |
| Visões de cada ramal no estado | `internal/state/groups.go`; recarga em `cmd/painel-ramais/serve.go` (`loadGroups`, `onSIGHUP`) |
| CLI | `cmd/painel-ramais/cli_group.go` (`group import`, `list`, `audit`, `del`) |
| Exemplo e opção do instalador | `packaging/grupos-exemplo.csv`, `instalar.py --grupos` |
| Decisões e contrato | ADR-005, ADR-006; CONTRATOS 4.2, 4.6, 9; desvios D50 a D52 |

## Aba Registros

Quedas e registros por ramal em 24 h, 7 dias ou 30 dias, "Fora do ar há", "Último registro", gráfico, detalhe com linha do
tempo e exportação CSV.

| Parte | Arquivos |
|---|---|
| Tela (tabela por cursor, filtros, detalhe, gráfico, CSV com coluna "Visões") | `assets/registros.js` |
| Rotas | `internal/api/registrations.go` (`/api/registrations`, `/summary`, `/extension`, `/series`) |
| Contagem das transições e pausa de 120 s | `internal/state/regreport.go` |
| Gravação em lote e log de decisões | `internal/reglog/reglog.go`, `internal/reglog/decisions.go` |
| Consultas e retenção | `internal/store/regreport.go` |
| Decisões e contrato | ADR-007, ADR-010, ADR-011; CONTRATOS 4.7, 10 (v4 e v6), 11.5 |

## Aba Chamadas

Chamadas externas entrantes, saintes e internas ao vivo, uma linha por chamada (`Linkedid`), com tronco, fila e duração.

| Parte | Arquivos |
|---|---|
| Tela (contadores, filtros, três listas virtuais) | `assets/chamadas.js`, `assets/linhas.js` |
| Projeção de chamadas | `internal/state/livecalls.go`, `channels.go`, `calls.go`, `infer.go` |
| Tradução de canais (descarta canais auxiliares) | `internal/ami/calls/calls.go` |
| Decisões e contrato | ADR-008, ADR-011; CONTRATOS 2.3, 5.2, 11.6 |

## Filas (painel dentro da aba Chamadas)

Uma linha por fila: quem aguarda, agentes por estado, atendidas, abandonadas, nível de serviço; renomear fila.

| Parte | Arquivos |
|---|---|
| Tela | `assets/filas.js` |
| Projeção de filas | `internal/state/queues.go` |
| Eventos e `QueueStatus` | `internal/ami/queues/queues.go`, `cmd/painel-ramais/supervisor_queues.go` |
| Renomear | `internal/api/queues.go`, `internal/store/queues.go` (tabela `queue_names`) |
| Decisões e contrato | ADR-008, ADR-009, ADR-011; CONTRATOS 2.4, 4.8, 7.2 |

## Entrada pelo Issabel e acesso de emergência

| Parte | Arquivos |
|---|---|
| Item de menu e emissor do token | `packaging/issabel/menu.xml`, `packaging/issabel/painel_ramais/index.php` |
| Troca do token, sessão, login de emergência | `internal/api/authn.go`, `internal/auth/token.go`, `internal/auth/auth.go`, `internal/store/users.go` |
| Páginas e cabeçalhos | `internal/api/tela.go`, Tela `index.html`, `acesso.html`, `assets/app.js` |
| Decisões e contrato | ADR-002, ADR-004; CONTRATOS 1.3, 3 |

## Tema e marca CAM

Tema claro do Manual da Marca CAM: logo, malha de fundo no cabeçalho, Exo 2 nos títulos e Barlow no corpo, vermelho da
marca só no cabeçalho, na aba ativa e no foco.

| Parte | Arquivos |
|---|---|
| Tokens de cor e `@font-face` | `assets/app.css` |
| Imagens | `assets/cam-logo.png`, `assets/malha.svg` (logo original em `docs/design/marca/cam-logo.png`) |
| Fontes e licenças | `assets/fonts/` (`Exo2-*.woff2`, `Barlow-*.woff2`, `OFL-Exo2.txt`, `OFL-Barlow.txt`, `ORIGEM.txt`) |
| Quem serve `.png` e as fontes | `internal/api/tela.go` (`assetTypes`, `fontTexts`) |
| Decisões | desvios D47 a D49; guia `tools/tela/DESIGN.md` |

## Saúde, diagnóstico e suporte

| Parte | Arquivos |
|---|---|
| `/api/health` e `/debug` | `internal/api/data.go` |
| Verificação de dependências (60 s) | `internal/depcheck/depcheck.go` |
| Captura e máscara de AMI | `cmd/painel-ramais/capturar.go`, `filtrar.go`, `internal/capture/` |
| Memória (`metrics.json`) | `cmd/painel-ramais/metrics.go` |

## Instalador e pacote

| Parte | Arquivos |
|---|---|
| `verificar`, `instalar` (`--pre`, `--versao`, `--grupos`, `--respostas`, `--tecnologia`), `status`, `rollback`, `remover`, `menu`, `homologar` | `instalar.py` |
| Integração com o sistema no RPM | `packaging/scripts/preinstall`, `postinstall`, `preremove`, `postremove`, `packaging/nfpm.yaml` |
| Units e Apache | `packaging/painel-ramais.service`, `packaging/painel-ramais.socket`, `packaging/httpd.conf.tmpl` |
| Build, bundle e publicação | `build.ps1`, `tools/bundle.py`, `docs/publicacao.md` |
| Guias | `README.md` (técnico de campo), `packaging/LEIAME-INSTALACAO.md` (opções avançadas) |
| Decisões e contrato | ADR-003; CONTRATOS 8, 15 |
