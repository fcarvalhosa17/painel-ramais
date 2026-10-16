// Mapa em canvas: um bloco por grupo, um quadrado por ramal na cor do estado.
// calcularLayout e acharBloco são puros (testados); desenharMapa usa o canvas.

const CABECA = 18;       // altura do rótulo do bloco, px CSS
const MARGEM = 10;       // espaço entre blocos
const LARGURA_MIN = 72;  // bloco nunca mais estreito que isso (rótulo legível)

// grupos: [{ nome, total }]. Devolve { passo, lado, altura, blocos: [{ nome, x, y, w, h, colunas }] }.
// Escolhe o maior quadrado que faz tudo caber em alturaMax; se nem o menor cabe, cresce na vertical.
export function calcularLayout(grupos, largura, alturaMax) {
  let melhor = null;
  for (let passo = 16; passo >= 3; passo--) {
    const l = empacotar(grupos, largura, passo);
    melhor = l;
    if (l.altura <= alturaMax) break;
  }
  return melhor || { passo: 3, lado: 2, altura: 0, blocos: [] };
}

function empacotar(grupos, largura, passo) {
  const lado = passo >= 6 ? passo - 2 : passo - 1;
  const maxColunas = Math.max(1, Math.floor(largura / passo));
  const minColunas = Math.min(maxColunas, Math.ceil(LARGURA_MIN / passo));
  const blocos = [];
  let x = 0, y = 0, alturaLinha = 0;
  for (const g of grupos) {
    const n = Math.max(1, g.total);
    const colunas = Math.min(maxColunas, Math.max(minColunas, Math.ceil(Math.sqrt(n * 2.2))));
    const w = colunas * passo;
    const h = CABECA + Math.ceil(n / colunas) * passo;
    if (x > 0 && x + w > largura) { x = 0; y += alturaLinha + MARGEM; alturaLinha = 0; }
    blocos.push({ nome: g.nome, x, y, w, h, colunas });
    x += w + MARGEM;
    if (h > alturaLinha) alturaLinha = h;
  }
  return { passo, lado, altura: blocos.length ? y + alturaLinha : 0, blocos };
}

export function acharBloco(layout, x, y) {
  for (const b of layout.blocos) {
    if (x >= b.x && x < b.x + b.w && y >= b.y && y < b.y + b.h) return b;
  }
  return null;
}

// grupos: saída de agregarGrupos (com itens). cores: { chaveEstado: "#hex" }.
export function desenharMapa(ctx, layout, grupos, cores, opcoes) {
  const { selecionado, sobre, corTexto, corTextoFraco, corSelecao, fonte } = opcoes;
  // Limpa o canvas inteiro sem a translação (a moldura de seleção passa da origem do desenho).
  ctx.save();
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height);
  ctx.restore();
  const porNome = new Map(grupos.map((g) => [g.nome, g]));
  const { passo, lado } = layout;
  ctx.textBaseline = "middle";
  for (const b of layout.blocos) {
    const g = porNome.get(b.nome);
    if (!g) continue;
    // Rótulo: nome (cortado à largura do bloco) e total.
    ctx.font = "600 11px " + fonte;
    const total = g.total.toLocaleString("pt-BR");
    const wTotal = ctx.measureText(total).width;
    ctx.fillStyle = b.nome === selecionado ? corSelecao : corTexto;
    ctx.fillText(cortar(ctx, g.nome, b.w - wTotal - 6), b.x, b.y + 8);
    ctx.fillStyle = corTextoFraco;
    ctx.fillText(total, b.x + b.w - wTotal, b.y + 8);
    // Quadrados: troca o fillStyle só quando o estado muda.
    let ultimo = "";
    const y0 = b.y + CABECA;
    for (let i = 0; i < g.itens.length; i++) {
      const estado = g.itens[i].estado;
      if (estado !== ultimo) { ctx.fillStyle = cores[estado]; ultimo = estado; }
      ctx.fillRect(b.x + (i % b.colunas) * passo, y0 + Math.floor(i / b.colunas) * passo, lado, lado);
    }
    if (b.nome === selecionado || b.nome === sobre) {
      ctx.strokeStyle = b.nome === selecionado ? corSelecao : corTextoFraco;
      ctx.lineWidth = b.nome === selecionado ? 2 : 1;
      ctx.strokeRect(b.x - 4.5, b.y - 3.5, b.w + 8, b.h + 6);
    }
  }
}

function cortar(ctx, texto, max) {
  if (ctx.measureText(texto).width <= max) return texto;
  let s = texto;
  while (s.length > 1 && ctx.measureText(s + "…").width > max) s = s.slice(0, -1);
  return s + "…";
}
