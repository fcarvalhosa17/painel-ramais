# Direção da tela do Painel de Ramais

Fonte da verdade do visual de `internal/web/static/`. Quem mexer na tela atualiza aqui; não contradiz.

## Especificação

Desde a rc.5 a tela segue **exatamente** `docs/design/painel-ramais-design.dc.html` (estrutura, ordem dos blocos,
cores, fontes, tamanhos, raios, espaçamentos, textos e ícones). Os estilos inline do arquivo viraram classes em
`assets/app.css` com os valores copiados (a CSP bloqueia style inline). Toda diferença entre a tela e o arquivo fica
registrada em `docs/design/desvios.md`, com o motivo e as linhas dos dois lados. Mudou a tela, confira e atualize lá.

## Direção

Sala de controle escura: bloco main #0d1218 com cartões #121820, borda #1f2833, raio 10. A única cor saturada é o
estado do ramal; azul (#7fb2ff foco, #3d6aa8 seleção, #2a3a52 pressionado) só para interação.

- Elemento assinatura: a grade de ramais com fundo e borda pela cor do estado (tracejado para não registrado e
  desconhecido, ponto piscando no tocando) e a linha da chamada com seta (↙ entrada, ↗ saída, ↔ interna).

## Tokens (em `assets/app.css`, `:root`)

- Fundos #0d1218 / #121820 / #141b24; controles #151c25; itens de lista #0f151c; trilho #232c38.
- Bordas #1f2833 / #253040; detalhe #2c3a4d; tracejado #2a333f.
- Texto #f2f5f8 (forte), #dbe2ea (base), #c7d0da, #aeb7c2, #8b95a3 (fraco), #7c8794.
- Estados (cor / cor de texto): livre #2fb36b / #6fdc9f, tocando #e8b931 / #f3d36b, em conversa #e5484d / #ff8a8d,
  em espera #9b7bf0 / #b9a6f5, não registrado #6b7480 / #aeb7c2, inalcançável #f08a3c / #ffb27a,
  desconhecido #a3aab3 / #c7d0da. O card sempre escreve o rótulo do estado (compensa pares próximos para daltonismo).
- Fontes locais (`assets/fonts/`, OFL): Barlow Semi Condensed 500/600/700 (títulos e números), IBM Plex Sans
  400/500/600 (texto), IBM Plex Mono 400/500 (IP, tempos, números externos).

## Sistema

- Cabeçalho, seis indicadores, três colunas em flex com quebra (Grupos 280, central 560, Chamadas ativas 300; cabem a
  partir de 1.208 px de largura útil, abaixo disso o próprio flex reorganiza).
- Grupos: lista com barra empilhada por estado, % registrados e "N com problema", ordem Nome ou Mais problemas, com
  "Todos os ramais" fixo no topo, fora da ordenação (não é grupo; a tela abre nele e volta a ele se o grupo aberto
  some); clicar filtra a coluna central, o histórico e as chamadas ativas (desvios.md D17).
- Chamadas ativas: todas as do escopo, mais longas primeiro, com rolagem virtual (desvios.md D18).
- Central: busca por número, nome ou IP em todos os grupos; chips de estado (ligados por padrão); Compacto (72 px,
  grade 108) e Cartões (116 px, grade 220), os dois com rolagem virtual; seleção múltipla só para administrador.
- Direita: detalhe do ramal clicado (também o lugar de mover de grupo, para administrador) e chamadas ativas.
- Texto que vem do Asterisk entra só por `textContent`. Nada inline (CSP do C3). Sem travessão em texto da tela.
