# Direção da tela do Painel de Ramais

Fonte da verdade do visual de `internal/web/static/`. Quem mexer na tela atualiza aqui; não contradiz.

## Direção

Sala de controle com sinalização industrial: painel operacional de NOC, sóbrio, lido de longe numa TV.
Fundo grafite azulado em camadas tonais; a única cor saturada da tela é o estado do ramal.

- Referências: wallboards de central de atendimento (Asterisk FOP, painéis de NOC) e sinalização DIN de sala técnica.
- Elemento assinatura: o mosaico. No mapa, cada ramal é um quadrado na cor do estado, agrupado por grupo; o ícone
  (2x2 quadrados), os pinos de estado da legenda, dos cards e da dica repetem o mesmo quadrado.

## Tokens (em `assets/app.css`, `:root`)

- Superfícies: `--fundo #0b1117`, `--sup-1 #111920`, `--sup-2 #16212a`, `--sup-3 #1c2934`; borda translúcida `--borda`.
- Texto por opacidade da mesma tinta: `--tinta-1` 94%, `--tinta-2` 72%, `--tinta-3` 56% (mínimo AA sobre `--sup-1`).
- Acento (só interação: foco, seleção, botão primário): `--acento #58a6ff`. Nunca usado como dado.
- Estados (C9 do plano, famílias mantidas, tons para pelo menos 3:1 sobre o card):
  livre `#2ea043`, tocando `#f2c94c`, em conversa `#f85149`, em espera `#a371f7`, não registrado `#6e7681`,
  inalcançável `#f0883e`, desconhecido `#a5b8cc`.
  Validado com o `validate_palette.js` do dataviz: contraste passa; separação para daltonismo NÃO passa em alguns pares
  (verde x laranja, vermelho x laranja) porque as famílias são fixas pelo plano. Compensação obrigatória: o card sempre
  escreve o rótulo do estado; a legenda tem rótulo e contagem; a dica do mapa lista estado por estado; o gráfico separa
  séries por posição (uma faixa por série) e nome.
- Tipografia (fontes do sistema, a CSP não permite fonte externa): display e números `Bahnschrift` (DIN, com
  alternativas DIN Alternate, Barlow Semi Condensed, Roboto Condensed); texto `Segoe UI`/system-ui; IP em `Cascadia Mono`/Consolas.
  Números sempre `tabular-nums`. Escala única em `rem`, com `html` crescendo com a largura (14 a 18 px) para a TV.
- Forma: raio 4 px em card, botão, campo e diálogo; quadrados sem raio no mapa e nos pinos.

## Sistema (densidade, navegação, convenções)

- Densidade compacta; uma tela só, sem abas: indicadores no topo, coluna "Visão geral" (mapa, ranking, histórico) e
  coluna "Ramais" (cards). Abaixo de 1024 px as colunas empilham.
- Cards de altura fixa (108 px) com rolagem virtual; estado ativo tinge o card inteiro, não registrado e desconhecido
  ficam apagados com borda tracejada. Sem faixa lateral colorida.
- A legenda de estados é também o filtro (botões com `aria-pressed` e contagem). Grupo pelo seletor, pelo ranking ou
  pelo clique no mapa; a seleção vale para cards, legenda e histórico.
- Gráfico: pequenos múltiplos (uma faixa por série, escala própria) sobre o mesmo eixo de tempo; linha 2 px, lavagem
  de 10%, lacuna quebra a linha; cruz e dica no hover.
- Texto que vem do Asterisk entra só por `textContent` ou desenho em canvas. Nada inline (CSP do C3).
