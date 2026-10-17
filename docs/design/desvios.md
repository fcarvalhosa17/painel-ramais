# Desvios entre o design e a tela (rc.5)

Design: `docs/design/painel-ramais-design.dc.html` (abreviado **D**, número = linha).
Tela: `internal/web/static/` (`index.html`, `assets/app.css`, `assets/*.js`).
Conferência feita por leitura e comparação de valores, sem abrir a tela (regra deste lote: só edição).
O arquivo de design foi lido como dado; não há nele texto com forma de ordem ao leitor (só marcação, estilos e o
script de dados de exemplo).

## Resumo

- **Desvios aceitos pelo usuário em 2026-10-16:** os 14 da tabela abaixo (acessibilidade D2, D3, D4, D16 e ajustes de
  dado real D6 a D15). Nenhum muda cor, fonte, raio, espaçamento ou tamanho de bloco do design.
- **Desfeitos por decisão do usuário (2026-10-16):** o antigo D1 (dado ausente agora usa "—", como em decorate() do
  design, inclusive nos indicadores sem leitura) e o antigo D5 (sem linha "Todos os grupos"; a tela abre no primeiro
  grupo da ordenação ativa, como no design).
- **Layout: sem desvio.** A tela segue o empilhamento do próprio design (3 colunas, depois 2 + 1, depois 1).

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
| D6 | "Mostrando N de M": N = todos os ramais do resultado (a rolagem virtual mostra todos, sem limite de 96 ou 30 cards); M = total antes dos chips de estado (grupo ou busca) | Rolagem virtual (item 3) e "mostre o total real" | D207, D404 | app.js:461 |
| D7 | Contagem do chip "Todos": durante a busca mostra o total da busca (o design mostra o total do grupo selecionado mesmo buscando) | Coerência com os chips de estado, que contam a busca | D160, D394 | app.js:453 |
| D8 | Cronômetro acima de 1 h no formato h:mm:ss (o design faz mm:ss com minutos acima de 59) | Dado real: chamadas longas | D287 | estado.js:76 |
| D9 | Ramal com mais de uma chamada: a linha ganha " +N" depois do outro lado | Dado real (o design só tem uma chamada por ramal) | D416-D417 | estado.js:60 |
| D10 | Cabeçalho central: "faixa ..." só aparece se o grupo tiver faixa; várias faixas separadas por vírgula; "Sem grupo" sem faixa | Dado real (o design calcula uma faixa contínua por grupo) | D384, D150 | app.js:458 |
| D11 | Histórico: rótulos do eixo com a hora real do início da janela e a cada 6 h, terminando em "agora" (o design fixa 00h 06h 12h 18h); escala de não registrados pelo máximo do período x 1,4 (o design usa a média sintética x 1,4) | Dado real do /api/history | D138, D455-D466 | grafico.js:39, grafico.js:57 |
| D12 | Detalhe, linha "Grupo": grupo do próprio ramal mais a origem "(pela faixa)", "(escolhido à mão)" ou "(nenhuma faixa)" (o design mostra o grupo selecionado na coluna) | Busca em todos os grupos abre ramal de outro grupo; a origem é a gestão de grupos (item 3) | D227 | edicao.js:208 |
| D13 | "Atualizado há N s" passa a "N min" a partir de 60 s; o IP do servidor é o endereço com que o navegador abriu a tela (`location.hostname`, pode ser um nome) | O snapshot não traz IP do servidor | D44 | app.js:290-298 |
| D14 | Ordem "Mais problemas": empate desempata pelo nome | Ordem estável com dado real (muitos grupos com 0%) | D365-D366 | app.js:360 |
| D15 | Busca ignora maiúsculas e acentos (o design só ignora maiúsculas) | Regra de busca já existente (C9) | D388 | estado.js:113 |
| D16 | Linhas de "Chamadas ativas" ganham `aria-label` com ramal, nome, direção e tempo (invisível) | Acessibilidade | D242 | app.js:550 |

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
  - controles de administrador no detalhe (seletor "Grupo", "Mover", "Voltar para a faixa");
  - textos longos reais (nome, aparelho, nome de grupo) quebram em vez de vazar (`overflow-wrap: anywhere`);
  - mensagem de erro dos diálogos (`.campo-erro`, cor #ff8a8d, 12 px) com `white-space: pre-line`: o 409 do daemon
    lista até 20 conflitos, um por linha (app.css:140); valor novo, só nesse estado de erro;
  - ajuda do import CSV na gestão de grupos com a regra atual (faixa, ramal avulso fixo acima da faixa,
    `Sem grupo,N` tira do grupo), index.html:244.

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
| Gerenciar grupos | 40 px, gap 8, 0 14, #253040, raio 8, #151c25, #dbe2ea, 13 px 600; ícone 15 px, mesmos paths (D51-D52) | app.css:101-106, index.html:25, 70 | ok |
| Tela cheia | 40 x 44, mesma borda e fundo; ícone 16 px, mesmos 4 paths (D53-D54) | app.css:109, index.html:23 | ok |

### Indicadores

| Item | Design | Tela | |
|---|---|---|---|
| grade | auto-fit minmax(170px,1fr), gap 10 (D58) | app.css:192 | ok |
| cartão | #121820, borda #1f2833, raio 10, padding 14 16, gap 6 (D59) | app.css:193 | ok |
| rótulo | 11 px 600 .08em #8b95a3; quadrado 8 px raio 2 (D60, D65) | app.css:194-195 | ok |
| valor | Barlow 700, 34 px, line-height 1, #f2f5f8 (D61) | app.css:196 | ok |
| % registrados | 18 px #6fdc9f; trilho 6 px raio 3 #232c38; barra #2fb36b (D66-D67) | app.css:198, 200-201 | ok |
| inalcançáveis | valor #ffb27a; texto "registrados sem resposta (qualify)" (D76-D77) | app.css:197, index.html:102 | ok |
| em chamada | linha mono "↙ N  ↗ N  ↔ N" (D82) | app.css:203, app.js | ok |
| tocando / espera | "/ N" 18 px #b9a6f5; "aguardando atendimento" (D86-D87) | app.css:199, index.html:112 | ok |
| rótulos | RAMAIS, REGISTRADOS, NÃO REGISTRADOS, INALCANÇÁVEIS, EM CHAMADA, TOCANDO / ESPERA (D60-D85) | index.html:85-112 | ok |

### Grupos

| Item | Design | Tela | |
|---|---|---|---|
| coluna | flex 1 1 280px, max-width 100%, gap 14 (D93) | app.css:207 | ok |
| bloco | #121820, borda #1f2833, raio 10, padding 14, gap 10 (D94) | app.css:214 | ok |
| título | Barlow 600, 17 px, .04em, #f2f5f8 "GRUPOS" (D96) | app.css:218 | ok |
| ordenar | gap 2, 12 px; botões 30 px, 0 8, raio 5, 600; Nome / Mais problemas; padrão "Mais problemas" (D97-D100, D331) | app.css:222-225, app.js:142 | ok |
| lista | gap 6, max-height 520, padding-right 2, pr-scroll (D102) | app.css:227 | ok |
| item | gap 7, padding 10 12, raio 8, borda #1f2833/#3d6aa8, fundo #0f151c/#18263a (D104, D371-D372) | app.css:228-233 | ok |
| nome / total | 14 px 600 #f2f5f8 / Barlow 600 16 px #c7d0da (D106-D107) | app.css:235-236 | ok |
| barra empilhada | 6 px raio 3 #232c38; livre, chamada, tocando, inalc, não reg (#4b5460) (D109-D115) | app.css:237-243 | ok |
| rodapé | 12 px #8b95a3; "N com problema" #ffb27a acima de 25% (D117-D119, D369) | app.css:244-245 | ok |

### Histórico

| Item | Design | Tela | |
|---|---|---|---|
| bloco | gap 8; título "ÚLTIMAS 24 H"; grupo 12 px #8b95a3 (D126-D129) | app.css:215, 220 | ok |
| svg | viewBox 0 0 260 90, 100% x 96 (D130) | index.html:136 | ok |
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
| Fechar detalhe | 36 x 36, #253040, raio 7, #151c25; ícone 14, path "M6 6l12 12M18 6L6 18" (D220-D221) | app.css:115-118, index.html:29 | ok |
| selo | 3 9, raio 999, rgba(0,0,0,.3), 11 px 600 .04em; ponto 7 px (D223) | app.css:357-361 | ok |
| dados | grid auto 1fr, gap 6 12, 13 px; dt #8b95a3; dd #dbe2ea; mono em Chamada, IP, Latência (D224-D229) | app.css:362-364 | ok (Grupo: D12) |
| aparelho / latência | user_agent; inalcançável "sem resposta"; não registrado traço (D424) | edicao.js:185-212 | ok |

### Chamadas ativas

| Item | Design | Tela | |
|---|---|---|---|
| coluna | flex 1 1 300px (D212) | app.css:212 | ok |
| cabeça | "CHAMADAS ATIVAS"; total Barlow 700 20 px #ff8a8d (D235-D237) | app.css:372 | ok |
| subtítulo | "grupo · mais longas primeiro", 12 px #8b95a3, margin-top -6 (D239) | app.css:373 | ok |
| lista | gap 4, max-height 470, pr-scroll; 14 mais longas (D240, D435) | app.css:374, app.js:22 | ok |
| linha | grid 22px 1fr auto, gap 8, padding 8 10, raio 7, #1f2833, #0f151c (D242) | app.css:375-378 | ok |
| seta / textos / tempo | mono 15 #ff8a8d (espera #b9a6f5); 13 px #f2f5f8 com número 600; 12 px mono #8b95a3 "Entrada/Saída/Interna/Em espera · outro"; tempo mono 13 #c7d0da, #ffb27a acima de 20 min (D243-D248, D440-D442) | app.css:380-387, app.js:23 | ok (D8, D16) |
