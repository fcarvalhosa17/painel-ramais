// Gráfico de linha do /api/history em canvas: quatro faixas pequenas (uma por série, cada uma com a
// própria escala) sobre o mesmo eixo de tempo. Escalas separadas porque registrados (milhares) e
// inalcançáveis (dezenas) num eixo só achatariam as séries pequenas. Ponto gap quebra a linha.

export const SERIES = [
  { campo: "registered", rotulo: "Registrados", cor: "--estado-livre" },
  { campo: "unregistered", rotulo: "Não registrados", cor: "--estado-nao-registrado" },
  { campo: "unreachable", rotulo: "Inalcançáveis", cor: "--estado-inalcancavel" },
  { campo: "in_call", rotulo: "Em chamada", cor: "--estado-em-conversa" },
];

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

export function extremos(pontos, campo) {
  let min = Infinity, max = -Infinity;
  for (const p of pontos) {
    if (p.gap || p[campo] == null) continue;
    if (p[campo] < min) min = p[campo];
    if (p[campo] > max) max = p[campo];
  }
  return min === Infinity ? null : { min, max };
}

const NUM = new Intl.NumberFormat("pt-BR");
const HORA = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" });
const DIA = new Intl.DateTimeFormat("pt-BR", { weekday: "short", day: "2-digit" });
const DIA_HORA = new Intl.DateTimeFormat("pt-BR", { weekday: "short", day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });

const ESQ = 128, DIR = 14, TOPO = 6, BAIXO = 22, ENTRE = 10;
const MARCAS = { "1h": 10 * 60e3, "24h": 3 * 3600e3, "7d": 24 * 3600e3 };

export class Grafico {
  constructor(canvas, dica, tema) {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d");
    this.dica = dica;
    this.tema = tema; // () => { cores: {var: hex}, texto, textoFraco, linha, fonte }
    this.pontos = [];
    this.periodo = "1h";
    this.sobre = -1;
    canvas.addEventListener("pointermove", (e) => this.mover(e));
    canvas.addEventListener("pointerleave", () => this.sair());
  }

  definir(pontos, periodo) {
    this.pontos = pontos || [];
    this.periodo = periodo;
    this.sobre = -1;
    this.dica.hidden = true;
    this.desenhar();
  }

  medidas() {
    const r = this.canvas.getBoundingClientRect();
    return { w: Math.max(10, Math.round(r.width)), h: Math.max(10, Math.round(r.height)) };
  }

  xDe(i, w) {
    const n = this.pontos.length;
    return ESQ + (n <= 1 ? 0 : (i / (n - 1)) * (w - ESQ - DIR));
  }

  desenhar() {
    const { w, h } = this.medidas();
    const dpr = window.devicePixelRatio || 1;
    if (this.canvas.width !== Math.round(w * dpr) || this.canvas.height !== Math.round(h * dpr)) {
      this.canvas.width = Math.round(w * dpr);
      this.canvas.height = Math.round(h * dpr);
    }
    const ctx = this.ctx;
    const t = this.tema();
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    const pontos = this.pontos;
    if (!pontos.length) return;
    const alturaFaixa = (h - TOPO - BAIXO - ENTRE * (SERIES.length - 1)) / SERIES.length;
    this.faixas = [];
    SERIES.forEach((s, k) => {
      const y0 = TOPO + k * (alturaFaixa + ENTRE);
      const ext = extremos(pontos, s.campo);
      const min = ext ? ext.min : 0;
      const max = ext ? Math.max(ext.max, min + 1) : 1;
      const yDe = (v) => y0 + alturaFaixa - ((v - min) / (max - min)) * (alturaFaixa - 4) - 2;
      this.faixas.push({ y0, yDe, serie: s });
      // Base e topo da faixa: linhas finas e recessivas.
      ctx.fillStyle = t.linha;
      ctx.fillRect(ESQ, Math.round(y0 + alturaFaixa) - 0.5, w - ESQ - DIR, 1);
      // Rótulo da faixa: chave na cor, texto na tinta, valor mais recente e escala.
      const cor = t.cores[s.cor];
      ctx.fillStyle = cor;
      ctx.fillRect(0, y0 + 5, 10, 3);
      ctx.textBaseline = "top";
      ctx.font = "500 11px " + t.fonte;
      ctx.fillStyle = t.textoFraco;
      ctx.fillText(s.rotulo, 16, y0);
      const ultimo = ultimoValor(pontos, s.campo);
      ctx.font = "600 16px " + t.fonteNum;
      ctx.fillStyle = t.texto;
      ctx.fillText(ultimo == null ? "sem dados" : NUM.format(ultimo), 16, y0 + 15);
      if (ext) {
        ctx.font = "400 10px " + t.fonteNum;
        ctx.fillStyle = t.textoFraco;
        ctx.textAlign = "right";
        ctx.fillText(NUM.format(max), ESQ - 8, y0 + 16);
        ctx.textBaseline = "bottom";
        ctx.fillText(NUM.format(min), ESQ - 8, y0 + alturaFaixa);
        ctx.textAlign = "left";
      }
      // Linha 2px e lavagem de 10% embaixo, quebradas nos gaps.
      ctx.lineWidth = 2;
      ctx.lineJoin = "round";
      ctx.lineCap = "round";
      ctx.strokeStyle = cor;
      const base = y0 + alturaFaixa;
      for (const [a, b] of segmentos(pontos, s.campo)) {
        ctx.beginPath();
        for (let i = a; i <= b; i++) {
          const x = this.xDe(i, w), y = yDe(pontos[i][s.campo]);
          if (i === a) ctx.moveTo(x, y); else ctx.lineTo(x, y);
        }
        ctx.stroke();
        ctx.lineTo(this.xDe(b, w), base);
        ctx.lineTo(this.xDe(a, w), base);
        ctx.closePath();
        ctx.globalAlpha = 0.1;
        ctx.fillStyle = cor;
        ctx.fill();
        ctx.globalAlpha = 1;
      }
    });
    this.desenharEixo(w, h, t);
    if (this.sobre >= 0) this.desenharCruz(w, h, t);
  }

  desenharEixo(w, h, t) {
    const ctx = this.ctx;
    const pontos = this.pontos;
    const t0 = Date.parse(pontos[0].t);
    const t1 = Date.parse(pontos[pontos.length - 1].t);
    const passo = MARCAS[this.periodo] || MARCAS["1h"];
    const fmt = this.periodo === "7d" ? DIA : HORA;
    ctx.font = "400 10px " + t.fonteNum;
    ctx.fillStyle = t.textoFraco;
    ctx.textBaseline = "bottom";
    ctx.textAlign = "center";
    for (let m = Math.ceil(t0 / passo) * passo; m <= t1; m += passo) {
      const x = ESQ + ((m - t0) / Math.max(1, t1 - t0)) * (w - ESQ - DIR);
      ctx.fillRect(Math.round(x) - 0.5, h - BAIXO, 1, 4);
      ctx.fillText(fmt.format(m), Math.min(w - 24, Math.max(ESQ + 14, x)), h - 2);
    }
    ctx.textAlign = "left";
  }

  desenharCruz(w, h, t) {
    const ctx = this.ctx;
    const i = this.sobre;
    const x = this.xDe(i, w);
    ctx.fillStyle = t.textoFraco;
    ctx.fillRect(Math.round(x) - 0.5, TOPO, 1, h - TOPO - BAIXO);
    const p = this.pontos[i];
    if (p.gap) return;
    for (const f of this.faixas) {
      const v = p[f.serie.campo];
      if (v == null) continue;
      const y = f.yDe(v);
      ctx.beginPath();
      ctx.arc(x, y, 6, 0, Math.PI * 2);
      ctx.fillStyle = t.fundo;
      ctx.fill();
      ctx.beginPath();
      ctx.arc(x, y, 4, 0, Math.PI * 2);
      ctx.fillStyle = t.cores[f.serie.cor];
      ctx.fill();
    }
  }

  mover(e) {
    if (!this.pontos.length) return;
    const r = this.canvas.getBoundingClientRect();
    const x = e.clientX - r.left;
    const n = this.pontos.length;
    const i = Math.round(((x - ESQ) / Math.max(1, r.width - ESQ - DIR)) * (n - 1));
    if (i < 0 || i >= n) { this.sair(); return; }
    if (i === this.sobre) return;
    this.sobre = i;
    this.desenhar();
    const p = this.pontos[i];
    const dica = this.dica;
    dica.replaceChildren();
    const titulo = document.createElement("strong");
    titulo.textContent = (this.periodo === "1h" ? HORA : DIA_HORA).format(Date.parse(p.t));
    dica.append(titulo);
    if (p.gap) {
      const s = document.createElement("span");
      s.textContent = "sem dados neste minuto";
      dica.append(s);
    } else {
      for (const s of SERIES) {
        const linha = document.createElement("span");
        linha.className = "dica-linha";
        linha.dataset.serie = s.campo;
        const nome = document.createElement("span");
        nome.textContent = s.rotulo;
        const val = document.createElement("b");
        val.textContent = NUM.format(p[s.campo]);
        linha.append(nome, val);
        dica.append(linha);
      }
    }
    dica.hidden = false;
    const xDica = this.xDe(i, r.width);
    const largura = dica.offsetWidth;
    const esquerda = xDica + 14 + largura > r.width ? xDica - 14 - largura : xDica + 14;
    dica.style.transform = "translate(" + Math.round(esquerda) + "px, 8px)";
  }

  sair() {
    if (this.sobre < 0) return;
    this.sobre = -1;
    this.dica.hidden = true;
    this.desenhar();
  }
}

function ultimoValor(pontos, campo) {
  for (let i = pontos.length - 1; i >= 0; i--) if (!pontos[i].gap && pontos[i][campo] != null) return pontos[i][campo];
  return null;
}
