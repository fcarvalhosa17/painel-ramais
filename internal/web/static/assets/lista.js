// Visão de grupo estilo FOP: cards de altura fixa com rolagem virtual.
// Só as linhas visíveis (mais uma folga) existem no DOM; os nós são reaproveitados.

import { ROTULO, textoOutroLado, textoDirecao, formatarCronometro, duracaoMs } from "./estado.js";

const ALTURA_CARD = 108;
const VAO = 8;
const LARGURA_MIN = 212;
const FOLGA_LINHAS = 2;

// Janela visível: quais índices renderizar. Pura (testada).
export function janela(total, colunas, scrollTop, alturaVisivel, passoLinha = ALTURA_CARD + VAO, folga = FOLGA_LINHAS) {
  const linhas = Math.ceil(total / colunas);
  const primeira = Math.max(0, Math.floor(scrollTop / passoLinha) - folga);
  const ultima = Math.min(linhas, Math.ceil((scrollTop + alturaVisivel) / passoLinha) + folga);
  return { inicio: primeira * colunas, fim: Math.min(total, ultima * colunas), alturaTotal: linhas * passoLinha };
}

export function colunasPara(largura) {
  return Math.max(1, Math.floor((largura + VAO) / (LARGURA_MIN + VAO)));
}

export class Lista {
  constructor(caixa, espaco) {
    this.caixa = caixa;
    this.espaco = espaco;
    this.itens = [];
    this.pool = [];
    this.visiveis = [];
    this.desvioMs = 0;
    this.colunas = 1;
    this.larguraCard = LARGURA_MIN;
    this.recuo = parseFloat(getComputedStyle(caixa).paddingLeft) || 0;
    caixa.addEventListener("scroll", () => this.agendar(), { passive: true });
  }

  definir(itens, desvioMs) {
    this.itens = itens;
    this.desvioMs = desvioMs;
    this.agendar();
  }

  irAoTopo() {
    this.caixa.scrollTop = 0;
  }

  agendar() {
    if (this.pendente) return;
    this.pendente = true;
    requestAnimationFrame(() => { this.pendente = false; this.render(); });
  }

  render() {
    const largura = this.caixa.clientWidth - 2 * this.recuo;
    const altura = this.caixa.clientHeight;
    this.colunas = colunasPara(largura);
    this.larguraCard = (largura - VAO * (this.colunas - 1)) / this.colunas;
    const j = janela(this.itens.length, this.colunas, this.caixa.scrollTop, altura);
    this.espaco.style.height = j.alturaTotal + "px";
    const precisa = j.fim - j.inicio;
    while (this.pool.length < precisa) this.pool.push(criarCard(this.caixa));
    const agora = Date.now();
    this.visiveis = [];
    for (let k = 0; k < this.pool.length; k++) {
      const card = this.pool[k];
      if (k >= precisa) { if (!card.no.hidden) card.no.hidden = true; continue; }
      const idx = j.inicio + k;
      const item = this.itens[idx];
      const linha = Math.floor(idx / this.colunas), coluna = idx % this.colunas;
      const x = Math.round(coluna * (this.larguraCard + VAO));
      const y = linha * (ALTURA_CARD + VAO);
      const pos = x + "," + y + "," + Math.round(this.larguraCard);
      if (card.pos !== pos) {
        card.pos = pos;
        card.no.style.transform = "translate(" + x + "px," + y + "px)";
        card.no.style.width = Math.round(this.larguraCard) + "px";
      }
      if (card.no.hidden) card.no.hidden = false;
      preencher(card, item, agora, this.desvioMs);
      this.visiveis.push(card);
    }
  }

  // Um único tique de 1 s atualiza só os cronômetros dos cards visíveis.
  tique() {
    const agora = Date.now();
    for (const card of this.visiveis) {
      if (card.item && card.item.ramal.calls.length) definir(card, "tempo", tempo(card.item.ramal, agora, this.desvioMs));
    }
  }
}

function el(tag, classe, pai) {
  const e = document.createElement(tag);
  e.className = classe;
  if (pai) pai.append(e);
  return e;
}

function criarCard(caixa) {
  const no = el("article", "card");
  no.setAttribute("role", "listitem");
  const topo = el("div", "card-topo", no);
  const numero = el("span", "card-num", topo);
  const estado = el("span", "card-estado", topo);
  el("span", "pino", estado).setAttribute("aria-hidden", "true");
  const rotulo = el("span", "", estado);
  const nome = el("p", "card-nome", no);
  const outro = el("p", "card-outro", no);
  const rodape = el("div", "card-rodape", no);
  const direcao = el("span", "card-dir", rodape);
  const tempoEl = el("span", "card-tempo", rodape);
  const ip = el("span", "card-ip", rodape);
  caixa.append(no);
  return { no, item: null, pos: "", campos: { numero, rotulo, nome, outro, direcao, tempo: tempoEl, ip }, valores: {} };
}

// Escreve no DOM só o que mudou (texto do Asterisk só por textContent).
function definir(card, campo, valor) {
  if (card.valores[campo] === valor) return;
  card.valores[campo] = valor;
  card.campos[campo].textContent = valor;
}

function tempo(ramal, agora, desvio) {
  const d = duracaoMs(ramal.calls, agora, desvio);
  return d == null ? "" : formatarCronometro(d);
}

function preencher(card, item, agora, desvio) {
  card.item = item;
  const r = item.ramal;
  if (card.valores.estadoChave !== item.estado) {
    card.valores.estadoChave = item.estado;
    card.no.dataset.estado = item.estado;
  }
  definir(card, "numero", r.number);
  definir(card, "rotulo", ROTULO[item.estado]);
  definir(card, "nome", r.name || "sem nome");
  definir(card, "outro", textoOutroLado(r.calls));
  definir(card, "direcao", textoDirecao(r.calls));
  definir(card, "tempo", tempo(r, agora, desvio));
  definir(card, "ip", r.ip || "");
  const titulo = [r.user_agent, r.latency_ms != null ? "latência " + r.latency_ms + " ms" : ""].filter(Boolean).join(" · ");
  if (card.valores.titulo !== titulo) {
    card.valores.titulo = titulo;
    if (titulo) card.no.title = titulo; else card.no.removeAttribute("title");
  }
}
