// Ramais em blocos (Compacto) ou cartões (Cartões), com rolagem virtual nos dois modos.
// Só as linhas visíveis (mais uma folga) existem no DOM; os nós são reaproveitados.
// Medidas do design: Compacto 72 px de altura, grade mínima 108 px, vão 6; Cartões 116 px, 220 px, vão 8.

import { ROTULO, linhaRamal } from "./estado.js";

export const MODOS = {
  compacto: { altura: 72, vao: 6, largura: 108 },
  cartoes: { altura: 116, vao: 8, largura: 220 },
};
const FOLGA_LINHAS = 2;
const TRACO = String.fromCharCode(0x2014); // sem IP: "—" como no design (decorate)

// Janela visível: quais índices renderizar. Pura.
export function janela(total, colunas, scrollTop, alturaVisivel, passoLinha, folga = FOLGA_LINHAS) {
  const linhas = Math.ceil(total / colunas);
  const primeira = Math.max(0, Math.floor(scrollTop / passoLinha) - folga);
  const ultima = Math.min(linhas, Math.ceil((scrollTop + alturaVisivel) / passoLinha) + folga);
  return { inicio: primeira * colunas, fim: Math.min(total, ultima * colunas), alturaTotal: linhas * passoLinha };
}

// Igual a repeat(auto-fill, minmax(min, 1fr)) com o vão do modo.
export function colunasPara(largura, min, vao) {
  return Math.max(1, Math.floor((largura + vao) / (min + vao)));
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
    this.modo = "compacto";
    caixa.dataset.modo = this.modo;
    // Seleção múltipla (só administrador): Set de números fora do DOM, dono é o edicao.js.
    // O card reciclado só reflete o Set em preencher(); nunca guarda seleção própria.
    this.selecao = new Set();
    this.marcavel = false;
    caixa.addEventListener("scroll", () => this.agendar(), { passive: true });
  }

  definirModo(modo) {
    if (!MODOS[modo] || modo === this.modo) return;
    this.modo = modo;
    this.caixa.dataset.modo = modo;
    for (const card of this.pool) card.pos = ""; // altura e largura mudaram: reposiciona todos
    this.irAoTopo();
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
    const m = MODOS[this.modo];
    const passo = m.altura + m.vao;
    const css = getComputedStyle(this.caixa);
    const largura = this.caixa.clientWidth - (parseFloat(css.paddingLeft) || 0) - (parseFloat(css.paddingRight) || 0);
    this.colunas = colunasPara(largura, m.largura, m.vao);
    const larguraCard = (largura - m.vao * (this.colunas - 1)) / this.colunas;
    // Altura primeiro, rolagem depois: se a lista encolheu (ramal removido, filtro), o navegador
    // limita o scrollTop à altura nova e a janela não cai além do fim (lista em branco).
    const linhas = Math.ceil(this.itens.length / this.colunas);
    this.espaco.style.height = Math.max(0, linhas * passo - m.vao) + "px";
    const j = janela(this.itens.length, this.colunas, this.caixa.scrollTop, this.caixa.clientHeight, passo);
    const precisa = Math.max(0, j.fim - j.inicio);
    while (this.pool.length < precisa) this.pool.push(criarCard(this.caixa));
    const agora = Date.now();
    this.visiveis = [];
    for (let k = 0; k < this.pool.length; k++) {
      const card = this.pool[k];
      if (k >= precisa) { if (!card.no.hidden) card.no.hidden = true; continue; }
      const idx = j.inicio + k;
      const linha = Math.floor(idx / this.colunas), coluna = idx % this.colunas;
      const x = Math.round(coluna * (larguraCard + m.vao));
      const y = linha * passo;
      const pos = x + "," + y + "," + Math.round(larguraCard);
      if (card.pos !== pos) {
        card.pos = pos;
        card.no.style.transform = "translate(" + x + "px," + y + "px)";
        card.no.style.width = Math.round(larguraCard) + "px";
      }
      if (card.no.hidden) card.no.hidden = false;
      preencher(card, this.itens[idx], agora, this.desvioMs, this.selecao, this.marcavel);
      this.visiveis.push(card);
    }
  }

  // Um único tique de 1 s atualiza só os cronômetros dos cards visíveis.
  tique() {
    const agora = Date.now();
    for (const card of this.visiveis) {
      if (!card.item || !card.item.ramal.calls.length) continue;
      const t = linhaRamal(card.item, agora, this.desvioMs).tempo;
      definir(card, "tempo", t ? " " + t : "");
    }
  }
}

function el(tag, classe, pai) {
  const e = document.createElement(tag);
  e.className = classe;
  if (pai) pai.append(e);
  return e;
}

// Um DOM serve aos dois modos; o CSS mostra as partes de cada um ([data-modo] na lista).
// O botão cobre o card inteiro (clique ou Enter abre o detalhe); o checkbox fica por cima dele.
function criarCard(caixa) {
  const no = el("div", "tile");
  no.setAttribute("role", "listitem");
  const abrir = el("button", "t-abrir", no);
  abrir.type = "button";
  const cima = el("span", "t-cima", no);
  const marca = el("input", "t-marca", cima);
  marca.type = "checkbox";
  marca.hidden = true;
  const id = el("span", "t-id", cima);
  const numero = el("span", "t-num", id);
  const nomeCartao = el("span", "t-nome-c", id);
  el("span", "t-ponto", cima);
  const selo = el("span", "t-selo", cima);
  el("span", "t-ponto", selo);
  const rotulo = el("span", "", selo);
  const nome = el("span", "t-nome", no);
  const baixo = el("span", "t-baixo", no);
  const linhaEl = el("span", "t-linha", baixo);
  const outro = el("span", "", linhaEl);
  const tempoEl = el("span", "t-tempo", linhaEl);
  const ip = el("span", "t-ip", baixo);
  caixa.append(no);
  return {
    no, abrir, marca, item: null, pos: "",
    campos: { numero, nomeCartao, nome, rotulo, outro, tempo: tempoEl, ip }, valores: {},
  };
}

// Escreve no DOM só o que mudou (texto do Asterisk só por textContent).
function definir(card, campo, valor) {
  if (card.valores[campo] === valor) return;
  card.valores[campo] = valor;
  card.campos[campo].textContent = valor;
}

function preencher(card, item, agora, desvio, selecao, marcavel) {
  card.item = item;
  const r = item.ramal;
  const nome = r.name || "sem nome";
  if (card.valores.estadoChave !== item.estado) {
    card.valores.estadoChave = item.estado;
    card.no.dataset.estado = item.estado;
  }
  const rotulo = ROTULO[item.estado];
  const aria = "Ramal " + r.number + ", " + nome + ", " + rotulo;
  if (card.valores.aria !== aria) {
    card.valores.aria = aria;
    card.abrir.setAttribute("aria-label", aria);
  }
  if (card.valores.numero !== r.number) {
    card.no.dataset.numero = r.number;
    card.marca.setAttribute("aria-label", "Selecionar o ramal " + r.number);
  }
  if (card.marca.hidden === marcavel) card.marca.hidden = !marcavel;
  const marcado = marcavel && selecao.has(r.number);
  card.marca.checked = marcado;
  if (card.valores.marcado !== marcado) {
    card.valores.marcado = marcado;
    card.no.classList.toggle("t-selecionado", marcado);
  }
  const linha = linhaRamal(item, agora, desvio);
  if (card.valores.chamada !== linha.chamada) {
    card.valores.chamada = linha.chamada;
    if (linha.chamada) card.no.dataset.chamada = "1"; else delete card.no.dataset.chamada;
  }
  definir(card, "numero", r.number);
  definir(card, "nomeCartao", nome);
  definir(card, "nome", nome);
  definir(card, "rotulo", rotulo.toLocaleUpperCase("pt-BR"));
  definir(card, "outro", linha.texto);
  definir(card, "tempo", linha.tempo ? " " + linha.tempo : "");
  definir(card, "ip", r.ip || TRACO);
}
