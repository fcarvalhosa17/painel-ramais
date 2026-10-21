# Direção da tela do Painel de Ramais

Fonte da verdade do visual de `internal/web/static/`. Quem mexer na tela atualiza aqui; não contradiz.

## Especificação

Estrutura, ordem dos blocos, tamanhos, raios, espaçamentos e ícones seguem `docs/design/painel-ramais-design.dc.html`
(desde a rc.5). **Desde 2026-10-21 as cores e as fontes seguem o Manual da Marca CAM** (tema claro, decisão do
cliente/usuário; o tema escuro do arquivo de design deixou de valer: desvios.md D47 a D52). Os estilos viram classes
em `assets/app.css` (a CSP bloqueia style inline). Toda diferença fica em `docs/design/desvios.md`, com o motivo e as
linhas dos dois lados. Mudou a tela, confira e atualize lá.

## Direção

Painel técnico claro da CAM Tecnologia: branco e espaço livre predominam, Grafite nos textos, cinzas de apoio. Página
#f6f7f8 com blocos brancos (borda #e3e5e8, sombra curta, raio 10). A cor saturada da área de trabalho é só o estado
do ramal; seleção e botão principal em Grafite.

- Vermelho da marca (#E22A2E, e o #8C2828 no início do degradê) **só** no cabeçalho (logo e linha de destaque de 3 px),
  no traço da aba ativa e no contorno de foco. Nunca na área de ramais, chamadas ou filas (o "em conversa" usa um
  carmim próprio, #c93450).
- Elemento assinatura: o cabeçalho branco com a logo CAM (PNG oficial, só largura definida, área de proteção igual à
  altura do bloco "CAM"), a linha Vermelho Profundo ao Vermelho CAM e a malha de nós da logo em cinza a 12% atrás dos
  controles. Na área de trabalho continua a grade de ramais com fundo e borda pela cor do estado.

## Tokens (em `assets/app.css`, `:root`)

- Marca: `--marca` #E22A2E, `--marca-profundo` #8C2828, `--grafite` #262626, `--cinza-tec` #888888 (borda de campo e
  grafismo, nunca texto pequeno), `--cinza-claro` #EDEEF0 (trilho e fundo de seleção), `--branco`.
- Texto sobre branco (todos AA): #262626 forte, #333 base, #444, #555, #646464 fraco, #6e6e6e (eixo, placeholder).
- Estados (cor / texto / fundo): livre #1e8a4c / #17703c, tocando #b98a00 / #7a5b00, em conversa #c93450 / #a3263f,
  em espera #7656d6 / #5a3db8, não registrado #79818b / #545b64 (tracejado), inalcançável #d66d1c / #9c4c0e,
  desconhecido #b3b9c1 / #686e76 (tracejado). O estado sempre tem rótulo ou ícone, nunca só a cor.
- Fontes locais (`assets/fonts/`, OFL, latin e latin-ext, origem em `ORIGEM.txt`): Exo 2 600/700 (títulos),
  Barlow 400/500/600/700 (corpo, números de ramal, contadores e cronômetros, com `tabular-nums` no `body`).

## Sistema

- Cabeçalho, seis indicadores, três colunas em flex com quebra (Visões 280, central 560, Chamadas ativas 300; cabem a
  partir de 1.208 px de largura útil, abaixo disso o próprio flex reorganiza). O cabeçalho também quebra em linhas
  (flex com quebra), sem rolagem horizontal em 1366 x 768 no iframe.
- "Grupo" do contrato se chama **visão** na tela; "Sem grupo" aparece como "Sem visão" (o valor interno não muda).
- Visões: lista com barra empilhada por estado, % registrados e "X não registrados", ordem Nome ou Mais problemas, com
  "Todos os ramais" fixo no topo. Clique escolhe só uma; Ctrl ou Cmd + clique soma ou tira; tirar a última volta para
  todos (`proximaSelecao`, estado.js). A tela mostra a união dos ramais, sem repetir. O mesmo vale nos filtros de
  visão das abas Registros e Chamadas (lista suspensa `visoes.js`).
- Chamadas ativas: todas as do escopo, mais longas primeiro, com rolagem virtual (desvios.md D18).
- Central: busca por número, nome ou IP em todas as visões; chips de estado (ligados por padrão); Compacto (72 px,
  grade 108) e Cartões (116 px, grade 220), os dois com rolagem virtual; seleção múltipla só para administrador.
- Direita: detalhe do ramal clicado (também o lugar de adicionar e remover visão, para administrador) e chamadas ativas.
- Texto que vem do Asterisk entra só por `textContent`. Nada inline (CSP do C3). Sem travessão em texto da tela.
