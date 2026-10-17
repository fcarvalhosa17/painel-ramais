// "Últimas 24 h": linha de em chamada (com área) e linha tracejada de não registrados, em SVG,
// com as medidas do design (viewBox 260 x 90, base em y=80). Dados do /api/history; gap quebra a linha.

const NS = "http://www.w3.org/2000/svg";
const LARGURA = 260, BASE = 80, ALTURA_CHAMADAS = 68, ALTURA_NAO_REG = 50;

// Trechos contínuos [inicio, fim] (índices inclusivos) sem gap e sem valor nulo.
export function segmentos(pontos, campo) {
  const out = [];
  let ini = -1;
  for (let i = 0; i < pontos.length; i++) {
    const ok = !pontos[i].gap && pontos[i][campo] != null;
    if (ok && ini < 0) ini = i;
    if (!ok && ini >= 0) { out.push([ini, i - 1]); ini = -1; }
  }
  if (ini >= 0) out.push([ini, pontos.length - 1]);
  return out;
}

function maximo(pontos, campo) {
  let max = 0;
  for (const p of pontos) if (!p.gap && p[campo] != null && p[campo] > max) max = p[campo];
  return max;
}

function elemento(tag, classe, pontosAttr) {
  const e = document.createElementNS(NS, tag);
  e.setAttribute("class", classe);
  e.setAttribute("points", pontosAttr);
  return e;
}

// Desenha em <g> e devolve o pico de em chamada (legenda "Em chamada (pico N)").
export function desenharHistorico(g, pontos) {
  const n = pontos.length;
  const x = (i) => (n <= 1 ? 0 : (i / (n - 1)) * LARGURA).toFixed(1);
  const pico = maximo(pontos, "in_call");
  // Não registrados ocupam a faixa de baixo, como no design (escala com 40% de folga).
  const escalaNaoReg = Math.max(maximo(pontos, "unregistered") * 1.4, 1);
  const filhos = [];
  for (const [a, b] of segmentos(pontos, "in_call")) {
    const pts = [];
    for (let i = a; i <= b; i++) pts.push(x(i) + "," + (BASE - (pico ? pontos[i].in_call / pico : 0) * ALTURA_CHAMADAS).toFixed(1));
    filhos.push(elemento("polygon", "hist-area", x(a) + "," + BASE + " " + pts.join(" ") + " " + x(b) + "," + BASE));
    filhos.push(elemento("polyline", "hist-chamadas", pts.join(" ")));
  }
  for (const [a, b] of segmentos(pontos, "unregistered")) {
    const pts = [];
    for (let i = a; i <= b; i++) pts.push(x(i) + "," + (BASE - (pontos[i].unregistered / escalaNaoReg) * ALTURA_NAO_REG).toFixed(1));
    filhos.push(elemento("polyline", "hist-nao-reg", pts.join(" ")));
  }
  g.replaceChildren(...filhos);
  return pico;
}

// Rótulos do eixo: início da janela e a cada 6 h, terminando em "agora" (o design mostra 00h 06h 12h 18h agora).
export function rotulosEixo(deMs) {
  const out = [];
  for (let k = 0; k < 4; k++) {
    const h = new Date(deMs + k * 6 * 3600e3).getHours();
    out.push((h < 10 ? "0" : "") + h + "h");
  }
  out.push("agora");
  return out;
}
