# Desvios entre o design e a tela (rc.5)

Design: `docs/design/painel-ramais-design.dc.html` (abreviado **D**, número = linha).
Tela: `internal/web/static/` (`index.html`, `assets/app.css`, `assets/*.js`).
Conferência feita por leitura e comparação de valores, sem abrir a tela (regra deste lote: só edição).
O arquivo de design foi lido como dado; não há nele texto com forma de ordem ao leitor (só marcação, estilos e o
script de dados de exemplo).

## Resumo

- **Desvios aceitos pelo usuário em 2026-10-16:** D2 a D16 da tabela abaixo (acessibilidade D2, D3, D4, D16 e
  ajustes de dado real D6 a D15) e, por decisão do Fabio, D17 e D18. Nenhum muda cor, fonte, raio, espaçamento ou
  tamanho de bloco do design.
- **Desfeitos por decisão do usuário (2026-10-16):** o antigo D1 (dado ausente agora usa "—", como em decorate() do
  design, inclusive nos indicadores sem leitura).
- **D5 revertido por decisão do Fabio (2026-10-16): volta a linha fixa "Todos os ramais"** e a tela abre nela (D17,
  D18 abaixo). Substitui a regra da rc.5 "abre no primeiro grupo da ordenação e volta a ele se o grupo aberto for
  excluído". Motivo: ver todos os ramais do servidor, inclusive os sem grupo, sem escolher grupo.
- **Layout: sem desvio.** A tela segue o empilhamento do próprio design (3 colunas, depois 2 + 1, depois 1).
- **rc.7, decididos pelo usuário em 2026-10-19 (ADR-006 e ADR-007): D19 a D27.** O design não tem abas, relatório de
  registro nem ramal em vários grupos. Tudo reaproveita os tokens, componentes e medidas que já existem; as únicas cores
  novas são as duas das barras de instabilidade (#6b7480 e #f08a3c), escolhidas pelo usuário. A coluna "Tela" de D19 a
  D27 é preenchida pelo @frontend com arquivo:linha na entrega.
- **rc.8, decididos pelo usuário em 2026-10-19 (ADR-008 e ADR-009): D28 a D37.** O design não tem aba de chamadas, filas
  nem edição de nome de fila. Tudo reaproveita componentes, tokens e medidas existentes (barra de abas do D19, indicadores
  do topo, linha `.chamada` de "Chamadas ativas", `.bloco`, `.detalhe`, `.segmentado`, busca central, diálogo estreito do
  "Novo grupo"); **nenhuma cor nova**. Contrato dos dados: CONTRATOS 2.3, 2.4, 4.8 e 5.2. A coluna "Tela" de D28 a D37 é
  preenchida pelo @frontend com arquivo:linha na entrega; ajustes de coluna e de medida que a montagem exigir entram como
  linha nova (D38 em diante), nunca editando a decisão.
- **rc.9, decididos pelo usuário em 2026-10-19 (ADR-010 e ADR-011): D38 a D46** (D46: texto da coluna Grupos, item 11). Colunas novas da aba Registros, ordem
  em três faixas, filtro, relógio parado e acinzentado, aviso de pausa (aba Registros e bloco da aba Ramais), detalhe
  do ramal com lacuna e detalhe da fila com rolagem virtual. **Nenhuma cor nova**: usam a cor de desconhecido e a nota
  já existentes. Contrato: CONTRATOS 4.7, 4.7.2, 4.7.3 e 4.7.5. A coluna "Tela" é preenchida pelo @frontend na entrega;
  ajuste de medida que a montagem exigir entra como linha nova (D47 em diante).

- **2026-10-21, decididos pelo cliente/usuário: D47 a D52.** Tema escuro substituído pelo tema claro do Manual da
  Marca CAM (cores, fontes Exo 2 e Barlow, logo, cabeçalho), "grupo" passa a se chamar "visão" na tela e a seleção
  aceita várias visões. As seções "Conferência por seção" e "Itens 3 e 4" abaixo continuam valendo para estrutura,
  medidas e textos; as cores, as fontes e as refs `app.css:linha` delas são da versão escura da rc.9.

## Layout das colunas (o mesmo do design)

Conta com os valores do design (D37, D91, D93, D146, D212):

| Parcela | px |
|---|---|
| coluna Grupos (base flex) | 280 |
| coluna central (base flex) | 560 |
| coluna Chamadas ativas (base flex) | 300 |
| 2 vãos de 14 | 28 |
| padding horizontal do main (20 + 20) | 40 |
| **largura útil mínima para 3 colunas** | **1.208** |

- Issabel com menu lateral aberto (~1.250 px úteis): cabe, com folga de ~42 px. Se a barra de rolagem vertical do
  iframe ocupar ~17 px, sobram ~1.233 px: ainda cabe. Se o iframe tiver margem interna maior que ~40 px, cai para o
  arranjo de 2 colunas abaixo.
- O design não empilha tudo abaixo do ponto de quebra: com `flex-wrap`, entre ~894 px e 1.208 px fica **Grupos +
  central na primeira linha e "Chamadas ativas" sozinha e em largura total na segunda**; abaixo de ~894 px fica
  uma coluna por linha. A tela reproduz exatamente isso (app.css:206-212), sem media query nova.
- Decisão do usuário (2026-10-16): manter o 2 + 1 do design; não é desvio.

## Desvios (fora dos itens 3 e 4)

| # | O que muda | Por quê | Design | Tela |
|---|---|---|---|---|
| D2 | Busca: o design tira o contorno do campo (`outline: none`) e não mostra foco; a tela desenha o anel de foco (2 px #7fb2ff, offset 2 px, o mesmo do design) no rótulo inteiro | Acessibilidade: foco visível | D155 | app.css:269-271 |
| D3 | Só em tela de toque (`pointer: coarse`): botões, chips e campos com 44 px de altura mínima; checkbox 22 px | Acessibilidade: alvo de toque | D47-D53, D97-D99, D160-D163 | app.css:458-461 |
| D4 | `prefers-reduced-motion: reduce` desliga pr-pulse e pr-ring (e a entrada da barra de seleção) | Acessibilidade | D16-D19 | app.css:448-450 |
| D6 | "Mostrando N de M": N = todos os ramais do resultado (a rolagem virtual mostra todos, sem limite de 96 ou 30 cards); M = total antes dos chips de estado (grupo ou busca) | Rolagem virtual (item 3) e "mostre o total real" | D207, D404 | app.js:500 |
| D7 | Contagem do chip "Todos": durante a busca mostra o total da busca (o design mostra o total do grupo selecionado mesmo buscando) | Coerência com os chips de estado, que contam a busca | D160, D394 | app.js:492 |
| D8 | Cronômetro acima de 1 h no formato h:mm:ss (o design faz mm:ss com minutos acima de 59) | Dado real: chamadas longas | D287 | estado.js:76 |
| D9 | Ramal com mais de uma chamada: a linha ganha " +N" depois do outro lado | Dado real (o design só tem uma chamada por ramal) | D416-D417 | estado.js:60 |
| D10 | Cabeçalho central: "faixa ..." só aparece se o grupo tiver faixa; várias faixas separadas por vírgula; "Sem grupo" sem faixa | Dado real (o design calcula uma faixa contínua por grupo) | D384, D150 | app.js:497 |
| D11 | Histórico: rótulos do eixo com a hora real do início da janela e a cada 6 h, terminando em "agora" (o design fixa 00h 06h 12h 18h); escala de não registrados pelo máximo do período x 1,4 (o design usa a média sintética x 1,4) | Dado real do /api/history | D138, D455-D466 | grafico.js:39, grafico.js:57 |
| D12 | Detalhe, linha "Grupo": grupo do próprio ramal mais a origem "(pela faixa)", "(escolhido à mão)" ou "(nenhuma faixa)" (o design mostra o grupo selecionado na coluna) | Busca em todos os grupos abre ramal de outro grupo; a origem é a gestão de grupos (item 3) | D227 | substituído pelo D22 (edicao.js:232-243) |
| D13 | "Atualizado há N s" passa a "N min" a partir de 60 s; o IP do servidor é o endereço com que o navegador abriu a tela (`location.hostname`, pode ser um nome) | O snapshot não traz IP do servidor | D44 | app.js:323-332 |
| D14 | Ordem "Mais problemas": empate desempata pelo nome | Ordem estável com dado real (muitos grupos com 0%) | D365-D366 | app.js:394 |
| D15 | Busca ignora maiúsculas e acentos (o design só ignora maiúsculas) | Regra de busca já existente (C9) | D388 | estado.js:113 |
| D16 | Linhas de "Chamadas ativas" ganham `aria-label` com ramal, nome, direção e tempo (invisível) | Acessibilidade | D242 | app.js:610 |
| D17 | Linha fixa **"Todos os ramais"** no topo da coluna Grupos, sempre primeira, fora da ordenação Nome / Mais problemas. É o mesmo componente dos itens de grupo (nome, total, barra empilhada, % registrados, "N com problema", mesmas cores, fontes, raio e espaçamento; sem estilo novo), calculado sobre todos os ramais, inclusive os sem grupo. A tela abre com ela selecionada e volta para ela se o grupo aberto some (excluído, renomeado). Com ela: título central "Todos os ramais" e "N ramais" sem faixa; "ÚLTIMAS 24 H" e "Chamadas ativas" com o subtítulo "Todos os ramais"; histórico do total da instalação (`/api/history` sem `group`); "Selecionar todos os resultados (N)" aparece para administrador mesmo sem busca nem chip (N = todos os ramais do resultado). Não é grupo: fica fora do catálogo, da gestão de grupos, dos seletores de destino e da contagem "em N grupos". O nome "Todos os ramais" é reservado na criação e renomeação pela tela (estado.js:228-230); **dívida:** o daemon (internal/api/groups.go) e o import CSV ainda aceitam esse nome. A coluna central passa a ter `aria-label="Ramais"` (era "Ramais do grupo"), sem efeito visual. O design não tem essa linha (abre no primeiro grupo) | Decisão do Fabio (2026-10-16), reverte o antigo D5 | D102-D121, D128, D149-D150, D239, D331, D384 | app.js:25, app.js:277, app.js:376, app.js:398-400, app.js:435, app.js:498-499, app.js:629; edicao.js:375-376; estado.js:228-230; index.html:143, 183, 186, 251 |
| D18 | "Chamadas ativas" lista **todas** as chamadas do escopo (grupo ou todos os ramais), mais longas primeiro, com rolagem virtual na mesma caixa de 470 px (só as linhas da janela visível existem no DOM, dois espaçadores ocupam o lugar das outras). O design mostra as 14 mais longas. Linha, cores e vão iguais ao design. A caixa ganha `overflow-anchor: none` (valor novo, sem efeito visual): com os espaçadores no fluxo, a ancoragem de rolagem do Chrome/Firefox faria a lista pular até o fim | Servidor inteiro com centenas de chamadas: rolar até qualquer uma sem desenhar todas | D240, D435 | app.js:567-643, app.css:374 |
| D19 | **Barra de abas** no topo: primeiro item da `.topo-dir`, um controle segmentado com o mesmo componente de "Densidade" (`.segmentado`/`.seg`, 34 px, raio 6, ligado #2a3a52/#f2f5f8), com `role="tablist"`, botões `role="tab"` e `aria-selected`, setas esquerda e direita trocam a aba. Abas: "Ramais" (a tela atual, aberta ao entrar) e "Registros". Na aba Registros o segmentado "Densidade" fica escondido (não vale lá); os demais botões do cabeçalho continuam. A aba "Chamadas" (Etapa 2) só tem o encaixe no código (lista de abas e um ponto de montagem comentado); **nenhum** botão visível, nem desabilitado | Decisão do usuário (rc.7): relatório de registro em tela própria sem tirar espaço da tela de ramais | D46-D49 | index.html:71-75 (abas), 76 (Densidade), 126 e 260 (painéis); app.js:29-31 (ABAS), app.js:202-206 (aba guardada), app.js:535-565 (troca, teclado, Densidade escondida); app.css:182 |
| D20 | Bloco **"INSTABILIDADE DE REGISTRO (24 H)"** na `.col-esq`, logo abaixo de "ÚLTIMAS 24 H": mesmo `.bloco` (#121820, borda #1f2833, raio 10, padding 14, gap 10), título no estilo `.bloco-titulo` e nota com o grupo selecionado no estilo `.bloco-nota`. Até 10 linhas (ramal em Barlow 600 e nome 12 px #aeb7c2; barra horizontal empilhada de 6 px, raio 3, trilho #232c38, "perdeu registro" #6b7480 e "ficou inalcançável" #f08a3c, escala pelo maior total do bloco; números à direita em mono 12 px). Legenda das duas cores no estilo `.hist-legenda`. Vazio: "Nenhuma instabilidade nas últimas 24 h". Se o período começa antes do `since`: "Dados desde DD/MM HH:MM". Acompanha o grupo selecionado (inclusive "Todos os ramais"). Clique ou Enter na linha abre a aba Registros com período 24 h, o grupo atual e a linha do tempo do ramal aberta. A coluna esquerda fica mais alta; a central não estica (`.colunas` já tem `align-items: flex-start`) e a caixa da lista de ramais continua com 600 px e rolagem virtual | Decisão do usuário (rc.7): ranking de instabilidade sem sair da tela principal | D93-D94, D126-D129 | index.html:164-180; instabilidade.js:66-126 (lista, barras, vazio, "Dados desde"), instabilidade.js:130-138 (30 s, só aba visível); app.js:162-171 (clique abre Registros), app.js:436 (grupo); app.css:468-489 |
| D21 | **Aba Registros** (tela nova, sem design): faixa de controles (segmentado de período 24 h / 7 d / 30 d no componente `.segmentado`; seletor de grupo com "Todos os ramais", o catálogo e "Sem grupo"; campo de busca no componente da busca central; botão "Exportar CSV" no estilo de "Gerenciar grupos"; nota "Dados desde ..."), bloco do gráfico (barras empilhadas por hora ou por dia nas duas cores do D20, intervalo sem dado em tracejado #2a333f, eixo no estilo `.hist-eixo`, linha "Eventos do sistema no período" em 12 px #8b95a3), tabela ordenável (cabeçalhos com `aria-sort`, clique alterna; ordem inicial perdeu registro + inalcançável, maior primeiro) com corpo de linhas de altura fixa e rolagem virtual (mesma técnica da lista de ramais e do D18), linha de ramal removido com selo "removido" nas cores de desconhecido, e painel da linha do tempo do ramal no estilo do detalhe (`.detalhe`: #121820, borda #2c3a4d, raio 10). Atualiza sozinha a cada 5 s (CONTRATOS 4.7.2) | Decisão do usuário (rc.7) | sem correspondente (usa D37-D54, D146-D168, D214-D229) | index.html:260-373; registros.js:303-352 (ciclo de 5 s por cursor), registros.js:376-511 (tabela, rolagem virtual, ordem), registros.js:540-561 (período), registros.js:604-681 (gráfico), registros.js:683-788 (linha do tempo), registros.js:790-792 (documento escondido); app.css:491-560 |
| D22 | Detalhe do ramal: a linha "Grupo" vira **"Grupos"**, uma linha por grupo com a origem "(pela faixa A-B)", "(pela faixa A-B e manual)" ou "(manual)"; ramal sem nenhum vínculo mostra "Sem grupo (nenhum vínculo)". Atualiza o D12 | ADR-006: ramal em N grupos | D227 | index.html:224; edicao.js:232-243; estado.js:180-204 (faixa com BigInt e origem); app.css:365, 367 |
| D23 | Controles de administrador no detalhe: saem "Mover" e "Voltar para a faixa"; entram **"Adicionar ao grupo"** (lista de caixas de seleção com os grupos em que o ramal ainda não está, um ou mais, botão primário) e **"Remover deste grupo"** (só com um grupo aberto na coluna Grupos, botão de perigo). Quando o ramal está no grupo aberto pela faixa, o botão fica desabilitado e aparece abaixo dele, em 12 px #8b95a3: "Este ramal pertence ao grupo pela faixa X-Y" | ADR-006: vínculo manual soma à faixa; tirar da faixa é editar a faixa | D224-D229 | index.html:230-243; edicao.js:265-322; app.css:368, 428, 463-466 |
| D24 | Seleção múltipla: a barra fixa troca "Mover para" por **"Adicionar aos grupos"** (escolha de um ou mais) e **"Remover deste grupo"** (só com grupo aberto). A confirmação conta à parte os ramais que estão no grupo pela faixa ("N continuam no grupo pela faixa; para tirá-los, edite a faixa") | ADR-006 | sem correspondente (estado do item 3) | index.html:541-560 (diálogo, `dialogo-vinculo` desde o D36), index.html:582-587 (barra); edicao.js:368-373 (botão só com grupo aberto), edicao.js:412-529 (confirmação, contagem pela faixa, tetos do assign) |
| D25 | Contagens com ramal em vários grupos: o total de cada item da coluna Grupos conta todo ramal do grupo, então a soma dos grupos pode passar do total; "Todos os ramais", os indicadores e "Chamadas ativas" de "Todos os ramais" contam cada ramal uma vez. Filtro por grupo = `groups` contém o grupo | ADR-006 | D104-D121 | estado.js:104, 132, 237-253; app.js:278-281 (totais uma vez), app.js:624; grupos.js:65-69 |
| D26 | Gestão de grupos: a ajuda do CSV passa a dizer "o mesmo ramal pode estar em vários grupos; faixas de grupos diferentes podem se cruzar; a importação só acrescenta; `Sem grupo,N` é ignorado"; o diálogo de exclusão diz "os ramais saem só deste grupo; quem ficar sem nenhum vai para Sem grupo"; o 409 de faixa sobreposta entre grupos deixa de aparecer | ADR-006 | sem correspondente (estado do item 3, index.html:498) | index.html:498; grupos.js:65-69 (contagem N:N), grupos.js:121-124 (exclusão), grupos.js:175 (sem conflito de faixa entre grupos), grupos.js:245-248 (`ignored`); estado.js:167 |
| D27 | Botão "Exportar CSV" gera o arquivo no navegador (CONTRATOS 4.7.5); durante a geração fica desabilitado; tabela vazia deixa o botão desabilitado | ADR-007 | sem correspondente | index.html:277; registros.js:126-194 (célula, CSV com as colunas da rc.9, nome, download), registros.js:585-602 (botão) |
| D28 | **Aba "Chamadas"** na barra de abas do D19, terceiro botão (`data-aba="chamadas"`, painel `#aba-chamadas`, `role="tabpanel"`), mesmo componente e mesmo teclado (setas, Home, End). Ordem: Ramais, Chamadas, Registros. Densidade escondida nela (como em Registros). A aba só pinta e só conta o tempo enquanto está visível (os dados do delta continuam sendo aplicados com ela escondida). Substitui a última frase do D19 ("nenhum botão visível") | ADR-008 | D46-D49 | index.html:73 (botão), 376-481 (painel); app.js:31 (ABAS), app.js:538-554 (três painéis, Densidade, liga e desliga), app.js:257 (tique); chamadas.js:166-191 (só pinta e conta o tempo visível; snapshot e delta aplicados em dados.js:35-36, 85-86 e 103-113 com a aba escondida) |
| D29 | **Topo da aba**: quatro contadores ao vivo no componente dos indicadores do topo da aba Ramais (mesmo tamanho de número e rótulo): "Externas entrantes", "Externas saintes", "Internas", "Aguardando em fila". Contam o resultado dos filtros de grupo, busca, tronco e fila (não o de tipo). "Aguardando em fila" = chamadas em `queued` de qualquer direção | ADR-008, item 7 | D58-D87 (indicadores) | index.html:377-394; estado.js:339-346; chamadas.js:139-144; app.css:574-575 |
| D30 | **Faixa de filtros** abaixo dos contadores: grupo (seletor com "Todos os ramais", o catálogo e "Sem grupo"; a chamada aparece se **qualquer** ramal dela estiver no grupo; "Sem grupo" = algum ramal sem vínculo), busca (componente da busca central; procura em número externo, números e nomes dos ramais, sem maiúsculas nem acentos, como o D15), tipo (segmentado "Todas / Entrantes / Saintes / Internas", que esconde as listas não escolhidas), tronco (seletor com "Todos" e os troncos presentes nas chamadas) e fila (seletor com "Todas" e as filas da projeção, pelo nome ou "Fila 600"). Filtros guardados só na visita (sem `localStorage` novo) | ADR-008 | D146-D168 (busca e segmentado) | index.html:396-420; estado.js:313-337 (grupo, tronco, fila, busca); chamadas.js:24-33 (seletor refeito só quando muda), chamadas.js:103-131; app.css:576-577 |
| D31 | **Três listas**: "Externas entrantes", "Externas saintes", "Internas", cada uma num `.bloco` com título no estilo `.bloco-titulo` e o total à direita, caixa com rolagem virtual (técnica do D18: só as linhas visíveis no DOM, dois espaçadores, `overflow-anchor: none`), **sem teto** de linhas, mais longas primeiro. Linha no componente `.chamada` (grid 22px 1fr auto, mesmas cores e fontes): seta da direção; linha 1 = ramal (número em 600 e nome lido da lista de ramais; vários ramais = o primeiro e " +N" como o D9; nenhum = "Fila X" quando `queue`, senão "outro lado desconhecido"); na interna, "origem → destino" com os dois ramais (destino = segundo ramal, ou "Fila X", ou "outro lado desconhecido"); linha 2 = número externo (ou "sem número") · tronco · estado ("Tocando", "Em conversa", "Em espera", "Em fila X", "Em conversa via fila X"; X = nome da fila ou "Fila 600"); tempo ao vivo à direita, contado no navegador desde `started_at` (laranja acima de 20 min, h:mm:ss acima de 1 h como o D8). "Em fila" e "Em espera" usam a cor da seta de espera já existente (#b9a6f5). Vazio: "Nenhuma chamada". Linha sem ação de clique na rc.8 | ADR-008, itens 1 a 7 | D240-D248, D435-D442 | index.html:423-448; estado.js:268-311 (lista pela direção, nome da fila, estado, textos da linha); chamadas.js:56-101 (linha, tempo ao vivo), chamadas.js:134-164 (ordem, vazio, AMI fora); app.css:569, 578-580 |
| D32 | **Painel de filas** na mesma aba (coluna à direita das listas; embaixo delas na largura de uma coluna, como o empilhamento do layout): um `.bloco` "FILAS" com uma linha por fila: nome (ou "Fila 600") e número em mono, aguardando, maior espera ao vivo (contada desde `entries[0].since`), agentes livres, em atendimento, pausados e indisponíveis (números pequenos com as cores de estado que a tela já usa: livre = registrado, em atendimento = em conversa, pausado = espera, indisponível = desconhecido), atendidas, abandonadas e nível de serviço (%). Nota no estilo `.bloco-nota`: "Contadores desde o último reinício do Asterisk" e "atualizado há N s" pelo `stats_at` (sem `stats_at`: "aguardando leitura"). Fila vazia ou sem agente aparece com zeros. Clique ou Enter abre o **detalhe da fila** no componente `.detalhe`: lista de quem aguarda (posição, número, nome, tempo de espera ao vivo) e lista de agentes (ramal e nome lidos da lista de ramais, ou o nome do membro quando não é ramal; estado; motivo da pausa; atendidas); fechar como o detalhe do ramal | ADR-008, item 8 | D214-D229 (detalhe), D93-D94 (bloco) | index.html:450-479; estado.js:349-356 (derivados); filas.js:33-94 (linhas), filas.js:96-207 (detalhe, quem aguarda, agentes; listas virtuais desde o D44); app.css:583-611 |
| D33 | **Nome da fila editável**: no detalhe da fila, botão "Renomear" (estilo de "Gerenciar grupos"), **só para administrador** (mesmo critério dos controles de grupo). Abre um diálogo no componente do "Novo grupo" (`dialogo-estreito`): campo "Nome" (até 64 caracteres; vazio = tirar o nome e voltar a "Fila 600"), "Salvar" e "Cancelar"; erro da rota (400, 403, 404) no `campo-erro` do diálogo. O nome novo chega pelo delta nas outras telas | ADR-009 | D224-D229 | index.html:461 (botão), 562-580 (diálogo); filas.js:209-250; conexao.js:56; estado.js:358-364 |
| D34 | **Acessibilidade das listas de chamadas e de filas** (e de "Chamadas ativas" da aba Ramais, item 6 da rc.8): cada caixa com `role="list"` e `aria-label` ("Externas entrantes, 12 chamadas"); linha com `role="listitem"` (ou o botão já existente em "Chamadas ativas") e `aria-label` com o texto completo; **roving tabindex** (uma linha com `tabindex=0`, as outras -1); setas para cima e para baixo movem o foco, Home e End vão às pontas, rolando a janela virtual até a linha; ao repintar, a linha focada é achada pelo `data-id` (chamada), `data-fila` (fila) ou `data-numero` (Chamadas ativas) guardado **antes** de pintar e recebe o foco de novo com `preventScroll: true`; se a chamada sumiu, o foco vai para a vizinha na mesma posição | Acessibilidade (D2, D16) | D240-D248 | linhas.js:1-152 (roving tabindex, setas, Home, End, foco pela chave); app.js:567-639 (Chamadas ativas pelo número), index.html:252; chamadas.js:94-101 (pelo `data-id`), filas.js:74-80 (pelo `data-fila`, Enter abre); app.css:566-570 |
| D35 | **"+ Novo grupo" volta** dentro do seletor de grupos de destino: na confirmação de "Adicionar aos grupos" (seleção múltipla, D24) e em "Adicionar ao grupo" do detalhe do ramal (D23), como última opção da lista, **só para administrador**. Abre o diálogo "Novo grupo"; criado o grupo, o seletor volta com ele marcado e o resto da seleção intacto | Decisão do usuário (rc.8) | sem correspondente (estado do item 3) | edicao.js:21-62 (volta marcado), edicao.js:113-117 e 158 (`abrirCriar` com retorno), edicao.js:169-182 (botão), 193, 408, 538 (só administrador); app.css:614 |
| D36 | **Fim dos restos do "Mover"**: o diálogo de confirmação de adicionar e remover deixa de se chamar `dialogo-mover` (ids `dm-*`) e passa a ter nome do que faz (`dialogo-vinculo`, ids `dv-*`); regras de CSS sem uso do antigo "Mover" saem do `app.css`. Sem mudança visual. Atualiza a referência de D24 | Decisão do usuário (rc.8) | sem correspondente | index.html:541-560; edicao.js:403-529 (`dv`); app.css:414-417 (`.dv-*`) |
| D37 | Nome **"Todos os ramais"** passa a ser recusado também pelo daemon (criar, renomear, CSV pela tela e pela CLI; CONTRATOS 9). A tela continua bloqueando antes de enviar e mostra o `400` do daemon se ele vier. Quita a dívida anotada no D17. Grupo antigo com esse nome continua aparecendo e pode ser renomeado ou excluído | Decisão do usuário (rc.8) | D102-D121 | estado.js:223 (só o nome novo é conferido), estado.js:228-230; edicao.js:79-82 e grupos.js:73-76 (mensagem do 400 do daemon) |
| D38 | **Aba Registros, colunas "Fora do ar há" e "Último registro"** no lugar da coluna "Fora do ar" (tempo total, que vai para o detalhe do ramal e para o CSV). Mesma célula de altura fixa e alinhamento das demais; "Fora do ar há" alinhada à direita em mono (como a antiga), "Último registro" no estilo de "Última queda". Texto: duração ao vivo, "antes de dd/mm hh:mm" ou "antes da instalação" (frase completa no `title` e no `aria-label`, com a data do "Dados desde"); traço de ausente quando vazio (CONTRATOS 4.7.2). A grade passa de 9 para 10 colunas, na mesma largura total da tabela (a montagem acerta as larguras e registra aqui) | Decisão do usuário (rc.9, ADR-010) | sem correspondente (usa D21) | index.html:328-329 (cabeçalhos); registros.js:79-101 (textos da 4.7.2), registros.js:456-477 (células), registros.js:480-491 (relógio, `title` e `aria-label` da frase completa); app.css:513-517 (10 colunas: 72, 1,2fr mín. 100, 1fr mín. 100, 72, 84, 72, 92, 132, 112 e 132 px; a grade mínima foi de 980 para 1.064 px e a linha do tempo desce para baixo da tabela abaixo de cerca de 1.430 px de largura) |
| D39 | **Ordem padrão em três faixas** pela coluna "Fora do ar há": fora com hora conhecida (mais antigo no topo), depois "antes da instalação", depois os registrados; empate pelo número. O cabeçalho "Fora do ar há" abre com `aria-sort="ascending"`; o segundo clique inverte só a primeira faixa. Substitui a ordem inicial "perdeu registro + inalcançável" do D21 | Decisão do usuário (rc.9) | sem correspondente (usa D21) | index.html:328 (`aria-sort` inicial); registros.js:110-111 (faixa), registros.js:199-219 (três faixas; o segundo clique inverte só a faixa a), registros.js:275 (ordem padrão) |
| D40 | **Filtro "Ocultar fora desde antes da instalação"** na faixa de controles da aba Registros, depois da busca: caixa de marcar nativa com rótulo, no tamanho e na cor do texto dos controles (`.bloco-nota`), foco visível como os demais; desligada ao abrir a aba. Afeta a tabela, a contagem e o CSV | Decisão do usuário (rc.9) | sem correspondente (usa D21) | index.html:276; registros.js:274, 381 (tabela, contagem e CSV), registros.js:563-567, registros.js:801 (desligado a cada abertura); app.css:621 |
| D41 | **Relógio parado e acinzentado** com o AMI fora: o texto de "Fora do ar há" para no instante da queda do AMI e usa a cor de desconhecido já existente; aviso "Estado de registro desconhecido desde HH:MM (painel sem conexão com o Asterisk)" na faixa da nota "Dados desde" (mesmo estilo de nota), com `role="status"` | Decisão do usuário (rc.9) | sem correspondente (cor de desconhecido do D21) | index.html:279; registros.js:40-44 (relógio até `state_unknown_since`), registros.js:360-372 (aviso, escrito só quando muda), registros.js:480-491 (parado e acinzentado); app.css:622 |
| D42 | **Aviso de pausa** "Contagem de registros pausada até HH:MM (Asterisk reiniciado)" na aba Registros (mesma faixa do D41) e no bloco "INSTABILIDADE DE REGISTRO (24 H)" da aba Ramais (linha de nota abaixo do título, 12 px #8b95a3 como a nota do D20), com `role="status"`; some sozinho quando a hora passa | Decisão do usuário (rc.9, ADR-011) | sem correspondente (usa D20, D21) | index.html:280 (aba Registros), index.html:169 (bloco da aba Ramais); estado.js:369-380; registros.js:360-372 e 824-829 (some no tique); instabilidade.js:56-65 (some na hora em que acaba); app.js:169 |
| D43 | **Detalhe do ramal (linha do tempo)**: linhas "Fora do ar há" e "Último registro" (texto da tabela, com a frase completa de "antes da instalação do painel") e "Tempo fora do ar no período" (era "Fora do ar") com o sufixo "(com lacuna: painel parado)" ou "(conta desde a instalação do painel)"; mesmo `dt`/`dd` do detalhe | Decisão do usuário (rc.9) | sem correspondente (usa D21) | index.html:360-362; registros.js:738-772 (relógios e sufixos de lacuna) |
| D44 | **Detalhe da fila com rolagem virtual**: "Quem aguarda" e "Agentes" passam a usar a mesma lista virtual da lista de filas (linhas de altura fixa, só as visíveis no DOM, caixa com altura máxima); o tique de 1 s só troca o texto do tempo de espera das linhas visíveis; a lista é remontada só quando a fila muda. Sem mudança visual além da rolagem | Decisão do usuário (rc.9, ADR-011) | sem correspondente (usa D32) | index.html:463, 466; filas.js:128-200 (listas remontadas só com a fila nova ou com o nome de um ramal de agente mudado), filas.js:254-265 (tique só no tempo das linhas visíveis); linhas.js:136-139; app.css:624-628 |
| D45 | **Duração com dias** em "Fora do ar há": `X d HH h` a partir de 1 dia (a regra antiga mostrava "720 h 00 min") | Dado real (ramal fora há semanas) | sem correspondente | registros.js:63-70 |
| D46 | **Coluna Grupos da aba Ramais: "X não registrados" e "Y inalcançáveis"** no lugar de "N com problema" (item 11, opção (b) do usuário). Em cada linha de grupo e em "Todos os ramais": "1 não registrado" / "N não registrados" (sempre, inclusive "0 não registrados") e, só quando houver, " · 1 inalcançável" / " · N inalcançáveis" no mesmo `span` e estilo de hoje; o nome acessível do botão leva o mesmo texto. Não muda: a ordem "Mais problemas" (soma dos dois), o destaque acima de 25% (mesma soma) e o "pior grupo" do indicador (só não registrados) | Decisão do usuário (rc.9) | D117-D119, D369 | app.js:411 (texto), app.js:386-387 (soma para a ordem e o destaque, sem mudança); estado.js:382-388; app.css:244 (o rodapé quebra a linha quando o texto não cabe nos 280 px da coluna) |
| D47 | **Tema escuro substituído pelo tema claro do Manual da Marca CAM** (2026-10-21). Branco e espaço livre predominam: página #f6f7f8, blocos e cards brancos com borda #e3e5e8 e sombra curta; Grafite #262626 nos textos, cinzas de apoio (#646464 no texto fraco, Cinza Tecnologia #888888 só em borda de campo e grafismo, Cinza Claro #EDEEF0 em trilho e seleção). Vermelho CAM #E22A2E e Vermelho Profundo #8C2828 **só** no cabeçalho (linha de destaque), no traço da aba ativa e no contorno de foco; seleção e botão principal em Grafite (era azul). Estados ajustados ao fundo claro com texto acima de 4,5:1: livre #1e8a4c, tocando #b98a00, em conversa #c93450 (carmim, distinto do vermelho da marca), em espera #7656d6, não registrado #79818b, inalcançável #d66d1c, desconhecido #b3b9c1; estado sempre com rótulo ou ícone. Raios, medidas e espaçamentos do design não mudam. As cores citadas nas seções abaixo (e as refs `app.css:linha` anteriores a esta linha) descrevem a versão escura da rc.9 | Decisão do cliente/usuário (2026-10-21) | todas as cores do arquivo | app.css:17-84 (tokens), 191-203, 223 |
| D48 | **Fontes**: Exo 2 600/700 nos títulos e Barlow 400 a 700 no corpo, nos números de ramal, contadores e cronômetros (`font-variant-numeric: tabular-nums` no `body`), no lugar de Barlow Semi Condensed, IBM Plex Sans e IBM Plex Mono (arquivos apagados). woff2 do Google Fonts (latin e latin-ext), licenças OFL junto, sem CDN; as setas ↙ ↗ ↔ ficam fora do subconjunto e usam a fonte do sistema | Manual da Marca CAM (decisão do cliente/usuário, 2026-10-21) | Barlow Semi Condensed / IBM Plex | app.css:6-15, 76-78, 90; fonts/ORIGEM.txt |
| D49 | **Logo CAM** (PNG oficial `docs/design/marca/cam-logo.png`, copiado sem alteração para `assets/cam-logo.png`) no cabeçalho, à esquerda do título, com 140 px de largura (mínimo 120), altura automática e área de proteção igual à altura do bloco "CAM"; também nas telas de mensagem e de acesso de emergência (160 px, no lugar do ícone). Cabeçalho branco de ponta a ponta com a linha de destaque de 3 px (Vermelho Profundo ao Vermelho CAM) e a malha de nós da logo em cinza a 12% (`assets/malha.svg`) atrás dos controles | Manual da Marca CAM | cabeçalho sem logo | index.html:43, 51, 67; app.css:183, 191-203 |
| D50 | **"Grupo" vira "Visão"** em todo texto da tela (títulos, botões, mensagens, ajuda, aria-label, placeholder, cabeçalho do CSV exportado "Visões"); o valor interno "Sem grupo" aparece como "Sem visão" (o JSON, o banco, os ids e as classes não mudam). "Sem visão" também é recusado como nome novo pela tela | Decisão do cliente/usuário (2026-10-21) | "Grupos", "Gerenciar grupos", "Novo grupo" | estado.js:129-154; index.html; registros.js:122 |
| D51 | **Várias visões ao mesmo tempo**: clique simples escolhe só aquela visão (desmarca as outras e "Todos os ramais"); Ctrl ou Cmd + clique (também com Enter ou Espaço) soma ou tira sem mexer nas outras; tirar a última volta para "Todos os ramais"; clique em "Todos os ramais" volta para todos. A tela mostra a união dos ramais, sem repetir. Vale na coluna Visões da aba Ramais e nos filtros de visão das abas Registros e Chamadas, que trocam o seletor nativo por uma lista suspensa (`details`) com as visões marcáveis; dica "Clique escolhe uma visão; Ctrl ou Cmd + clique soma ou tira." na coluna e na lista. No máximo 20 visões (teto do daemon por pedido): somar a 21ª não muda nada e mostra "Máximo de 20 visões selecionadas." no aviso do rodapé. Título com mais de duas visões: "N visões" (lista completa no title) | Decisão do cliente/usuário (2026-10-21) | seleção de um grupo só | estado.js:147-154; visoes.js:8-74; app.js:383; registros.js:510; chamadas.js:105; index.html:139-140, 270, 403; app.css:672-713 |
| D52 | **Agregados com várias visões**: "Últimas 24 h" e o gráfico da aba Registros pedem todas as visões escolhidas (`group` repetido) e o daemon soma por visão; com `meta.overlap` verdadeiro aparece a nota "Ramais em mais de uma visão podem ser contados duas vezes neste gráfico." abaixo da legenda. "Instabilidade de registro" junta as respostas por número (exato: os 10 primeiros da união estão entre os 10 primeiros de alguma visão) | O histórico guarda contagens por visão, não os ramais (CONTRATOS 4.3 e 4.7.4) | sem correspondente | instabilidade.js:18; conexao.js (serieDasVisoes); app.js (carregarHistorico); registros.js (carregarSerie); index.html (#hist-overlap, #reg-graf-overlap) |

## Itens 3 e 4 (permitidos, registrados)

- **Item 4:** sem a barra "PBX / Painel de Ramais" (D30-D35) e sem o fundo externo #eef1f4 com padding 12/14/14
  (D14, D29). A tela começa no main escuro (raio 8, padding 18/20/20, app.css:159). O `body` fica #0d1218, então o
  raio do main não aparece contra outra cor (app.css:65).
- **Fontes locais** (item 3): mesmas famílias e pesos via `@font-face` em `assets/fonts/` (app.css:5-12), no lugar do
  Google Fonts (D11-D12). Fallback `system-ui, sans-serif` e `ui-monospace, monospace`.
- **Rolagem virtual** (item 3): cards absolutos sobre um espaçador dentro da caixa de 600 px (app.css:288, lista.js);
  grade calculada como `repeat(auto-fill, minmax(108|220 px, 1fr))` com vão 6|8 (lista.js:7-10, 23-25).
- **Estados que o design não mostra** (item 3), com as cores, fontes, raios e espaçamentos do arquivo:
  - selo de conexão "CONECTANDO", "ATUALIZANDO", "RECONECTANDO EM N S" (cores de desconhecido e tocando, app.css:172-177);
  - AMI desconectado: faixa de alerta nas cores de inalcançável (app.css:184-190), indicadores "—", "pior grupo: —";
  - "pior grupo: nenhum" quando não há não registrado;
  - lista vazia, histórico carregando, sem dados e erro com "Tentar de novo";
  - detalhe de ramal que sumiu do PBX; latência `null` mostra "—";
  - hover em botões, segmentado, ordem, grupos, chamadas e cards (bordas #253040/#3d6aa8 e véu branco de 3%);
  - seleção de texto (#3d6aa8);
  - botões extras no cabeçalho, no estilo de "Gerenciar grupos": "Novo grupo" (só administrador) e "Sair" (só na
    rota de emergência); o ícone de tela cheia troca para "sair da tela cheia" quando ativo;
  - gestão de grupos: diálogos "Grupos", "Novo grupo" e "Confirmar mudança" (fundo #121820, borda #253040, raio 10,
    fundo do modal rgba(13,18,24,.72)); botão primário #2a3a52/#3d6aa8, perigo nas cores de em conversa;
  - seleção múltipla: checkbox no card, anel interno de 2 px #3d6aa8 no card marcado, barra fixa (#121820, borda
    #3d6aa8, raio 10, sombra para descolar do conteúdo) e aviso de status;
  - controles de administrador no detalhe (na rc.7: "Adicionar ao grupo" e "Remover deste grupo", D23);
  - textos longos reais (nome, aparelho, nome de grupo) quebram em vez de vazar (`overflow-wrap: anywhere`);
  - mensagem de erro dos diálogos (`.campo-erro`, cor #ff8a8d, 12 px) com `white-space: pre-line`: o 409 do daemon
    lista até 20 conflitos, um por linha (app.css:140); valor novo, só nesse estado de erro;
  - ajuda do import CSV na gestão de grupos com a regra atual (na rc.7: a do D26), index.html:498.

## Conferência por seção (valor do design x valor na tela)

### Cabeçalho

| Item | Design | Tela | |
|---|---|---|---|
| main | #0d1218, raio 8, padding 18 20 20, gap 16 (D37) | app.css:159 | ok |
| header | flex wrap, space-between, gap 12 (D39) | app.css:161 | ok |
| grupo esquerdo / direito | gap 14 wrap / gap 10 wrap (D40, D46) | app.css:162-163 | ok |
| h1 | Barlow 700, 26 px, .02em, #f2f5f8 (D41) | app.css:164 | ok |
| selo AO VIVO | padding 4 10, raio 999, rgba(47,179,107,.12), #6fdc9f, 12 px 600 .06em; pino 8 px #2fb36b com pr-pulse 1.8s (D42-D43) | app.css:167-171 | ok |
| texto de atualização | 12 px #8b95a3 (D44) | app.css:165 | ok (dado: D13) |
| segmentado Densidade | #151c25, borda #253040, raio 8, padding 3; botões 34 px, 0 12, raio 6, 13 px 600; ligado #2a3a52/#f2f5f8, desligado transparente/#8b95a3 (D47-D49) | app.css:179-182 | ok |
| Gerenciar grupos | 40 px, gap 8, 0 14, #253040, raio 8, #151c25, #dbe2ea, 13 px 600; ícone 15 px, mesmos paths (D51-D52) | app.css:101-106, index.html:30, 80 | ok |
| Tela cheia | 40 x 44, mesma borda e fundo; ícone 16 px, mesmos 4 paths (D53-D54) | app.css:109, index.html:28 | ok |

### Indicadores

| Item | Design | Tela | |
|---|---|---|---|
| grade | auto-fit minmax(170px,1fr), gap 10 (D58) | app.css:192 | ok |
| cartão | #121820, borda #1f2833, raio 10, padding 14 16, gap 6 (D59) | app.css:193 | ok |
| rótulo | 11 px 600 .08em #8b95a3; quadrado 8 px raio 2 (D60, D65) | app.css:194-195 | ok |
| valor | Barlow 700, 34 px, line-height 1, #f2f5f8 (D61) | app.css:196 | ok |
| % registrados | 18 px #6fdc9f; trilho 6 px raio 3 #232c38; barra #2fb36b (D66-D67) | app.css:198, 200-201 | ok |
| inalcançáveis | valor #ffb27a; texto "registrados sem resposta (qualify)" (D76-D77) | app.css:197, index.html:112 | ok |
| em chamada | linha mono "↙ N  ↗ N  ↔ N" (D82) | app.css:203, app.js | ok |
| tocando / espera | "/ N" 18 px #b9a6f5; "aguardando atendimento" (D86-D87) | app.css:199, index.html:122 | ok |
| rótulos | RAMAIS, REGISTRADOS, NÃO REGISTRADOS, INALCANÇÁVEIS, EM CHAMADA, TOCANDO / ESPERA (D60-D85) | index.html:95-122 | ok |

### Grupos

| Item | Design | Tela | |
|---|---|---|---|
| coluna | flex 1 1 280px, max-width 100%, gap 14 (D93) | app.css:207 | ok |
| bloco | #121820, borda #1f2833, raio 10, padding 14, gap 10 (D94) | app.css:214 | ok |
| título | Barlow 600, 17 px, .04em, #f2f5f8 "GRUPOS" (D96) | app.css:218 | ok |
| ordenar | gap 2, 12 px; botões 30 px, 0 8, raio 5, 600; Nome / Mais problemas; padrão "Mais problemas" (D97-D100, D331) | app.css:222-225, app.js:148 | ok ("Todos os ramais" fora da ordenação: D17) |
| lista | gap 6, max-height 520, padding-right 2, pr-scroll (D102) | app.css:227 | ok |
| item | gap 7, padding 10 12, raio 8, borda #1f2833/#3d6aa8, fundo #0f151c/#18263a (D104, D371-D372) | app.css:228-233 | ok |
| nome / total | 14 px 600 #f2f5f8 / Barlow 600 16 px #c7d0da (D106-D107) | app.css:235-236 | ok |
| barra empilhada | 6 px raio 3 #232c38; livre, chamada, tocando, inalc, não reg (#4b5460) (D109-D115) | app.css:237-243 | ok |
| rodapé | 12 px #8b95a3; "N com problema" #ffb27a acima de 25% (D117-D119, D369) | app.css:244-245 | ok (texto: D46) |

### Histórico

| Item | Design | Tela | |
|---|---|---|---|
| bloco | gap 8; título "ÚLTIMAS 24 H"; grupo 12 px #8b95a3 (D126-D129) | app.css:215, 220 | ok |
| svg | viewBox 0 0 260 90, 100% x 96 (D130) | index.html:146 | ok |
| grades | y 80 #232c38; y 45 e 10 #1a222c (D131-D133) | app.css:250-251 | ok |
| área / linhas | rgba(229,72,77,.16); #e5484d 2 px; #8b95a3 1.5 px tracejado 3 3, junções arredondadas (D134-D136) | app.css:252-254 | ok |
| eixo | 11 px #7c8794 mono (D138) | app.css:256 | ok (rótulos: D11) |
| legenda | gap 14, 12 px #aeb7c2; traço 12 x 2 #e5484d; tracejado 2 px #8b95a3 (D139-D142) | app.css:257-260 | ok |

### Central

| Item | Design | Tela | |
|---|---|---|---|
| coluna | flex 999 1 560px, #121820, borda #1f2833, raio 10, padding 14, gap 12 (D146) | app.css:208-211 | ok |
| título / faixa | Barlow 700 22 px #f2f5f8 / 13 px #8b95a3 (D149-D150) | app.css:265-266 | ok (texto: D10) |
| busca | gap 8, #0d1218, #253040, raio 8, 0 12, 40 px, flex 0 1 300px; ícone 15 #8b95a3; texto 13 px #f2f5f8; placeholder #7c8794 e texto igual (D152-D156, D25) | app.css:268-269 | ok (foco: D2) |
| chips | gap 6; 34 px, gap 7, 0 12, raio 999, 13 px 500; quadrado 9 px raio 2, opacidade 1/.35; cores de fundo e borda pela regra do design (D159-D166, D395-D399) | app.css:273-282 | ok |
| Todos | 34 px, 0 12, raio 999, #f2f5f8 600; número #aeb7c2 500; ligado #18263a/#3d6aa8, desligado transparente/#253040 (D160, D481) | app.css:283-285 | ok (contagem: D7) |
| caixa da lista | max-height 600, padding-right 4, pr-scroll (D168, D185) | app.css:288 | ok |
| Compacto | 72 px, padding 7 9, raio 7, grade 108 vão 6; número Barlow 700 20 px; ponto 8 px; nome 11 px #aeb7c2; linha 11 px mono (D169-D180) | app.css:322-329 | ok |
| Cartões | 116 px, padding 12 14, raio 9, grade 220 vão 8; número 26 px; nome 13 px #c7d0da; selo 3 8, raio 999, rgba(0,0,0,.25), 11 px 600 .04em; linha 12 px mono; IP #7c8794 (D186-D200) | app.css:332-345 | ok |
| cores por estado | decorate(): fundos, bordas, tracejado #2a333f, número #8b95a3 (D408-D413) | app.css:293-301, 313 | ok |
| linha | "↙/↗/↔ outro tempo" na cor do estado; "Livre" #6fdc9f; inalcançável #ffb27a; resto #8b95a3 (D414-D420) | app.css:319, estado.js:55 | ok (D8, D9) |
| pr-ring | ponto do tocando, 1 s (D18, D426) | app.css:315 | ok |
| rodapé | borda #1f2833, padding-top 10, 12 px #8b95a3; legenda mono "↙ entrada  ↗ saída  ↔ interna" (D206-D208) | app.css:347-348 | ok (contagem: D6) |

### Detalhe

| Item | Design | Tela | |
|---|---|---|---|
| bloco | #121820, borda #2c3a4d, raio 10, padding 14, gap 10 (D214) | app.css:352 | ok |
| número / nome | Barlow 700 28 px lh 1 / 13 px #c7d0da (D217-D218) | app.css:355-356 | ok |
| Fechar detalhe | 36 x 36, #253040, raio 7, #151c25; ícone 14, path "M6 6l12 12M18 6L6 18" (D220-D221) | app.css:115-118, index.html:34 | ok |
| selo | 3 9, raio 999, rgba(0,0,0,.3), 11 px 600 .04em; ponto 7 px (D223) | app.css:357-361 | ok |
| dados | grid auto 1fr, gap 6 12, 13 px; dt #8b95a3; dd #dbe2ea; mono em Chamada, IP, Latência (D224-D229) | app.css:362-364 | ok (Grupos: D22) |
| aparelho / latência | user_agent; inalcançável "sem resposta"; não registrado traço (D424) | edicao.js:220-261 | ok |

### Chamadas ativas

| Item | Design | Tela | |
|---|---|---|---|
| coluna | flex 1 1 300px (D212) | app.css:212 | ok |
| cabeça | "CHAMADAS ATIVAS"; total Barlow 700 20 px #ff8a8d (D235-D237) | app.css:372 | ok |
| subtítulo | "grupo · mais longas primeiro", 12 px #8b95a3, margin-top -6 (D239) | app.css:373 | ok |
| lista | gap 4, max-height 470, pr-scroll; 14 mais longas (D240, D435) | app.css:374, app.js:567-643 | ok (todas, com rolagem virtual: D18) |
| linha | grid 22px 1fr auto, gap 8, padding 8 10, raio 7, #1f2833, #0f151c (D242) | app.css:375-378 | ok |
| seta / textos / tempo | mono 15 #ff8a8d (espera #b9a6f5); 13 px #f2f5f8 com número 600; 12 px mono #8b95a3 "Entrada/Saída/Interna/Em espera · outro"; tempo mono 13 #c7d0da, #ffb27a acima de 20 min (D243-D248, D440-D442) | app.css:380-387, app.js:26 | ok (D8, D16) |
