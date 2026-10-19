// Lista virtual de linhas (técnica do D18): só as linhas da janela visível existem no DOM; dois espaçadores no
// fluxo ocupam o lugar das outras (a caixa precisa de overflow-anchor: none e de altura máxima no CSS).
// Teclado do D34: roving tabindex (uma linha com tabindex 0), setas para cima e para baixo, Home e End rolando a
// janela até a linha; ao repintar, a linha focada é achada pela chave guardada antes e recebe o foco de novo com
// preventScroll; se o item sumiu, o foco vai para a vizinha na mesma posição.
// Usada em "Chamadas ativas" (aba Ramais), nas três listas e no painel de filas da aba Chamadas.

import { janela } from "./lista.js";

// opcoes: { criar() -> { no, foco? }, preencher(ui, item), chave(item) -> string, ativar?(item) }
// no: o elemento da linha (filho da caixa); foco: o elemento focável (padrão: o próprio no).
export class ListaLinhas {
  constructor(caixa, opcoes) {
    this.caixa = caixa;
    this.op = opcoes;
    this.itens = [];
    this.pool = [];
    this.altura = 52; // estimativa até medir a primeira linha desenhada
    this.passo = this.altura;
    this.alturaVisivel = 0;
    this.focoChave = null;
    this.forcarFoco = false;
    this.pendente = false;
    [this.antes, this.depois] = [0, 1].map(() => {
      const d = document.createElement("div");
      d.setAttribute("aria-hidden", "true");
      d.style.flex = "none"; // não encolhe dentro da caixa de altura máxima, como as linhas
      d.hidden = true;
      caixa.append(d);
      return d;
    });
    caixa.addEventListener("scroll", () => this.agendar(), { passive: true });
    caixa.addEventListener("focusin", (e) => {
      const ui = this.uiQueContem(e.target);
      if (ui) { this.focoChave = this.op.chave(ui.item); this.marcarTab(); }
    });
    caixa.addEventListener("keydown", (e) => this.tecla(e));
  }

  foco(ui) { return ui.foco || ui.no; }
  uiQueContem(el) { return this.pool.find((u) => !u.no.hidden && u.item && u.no.contains(el)) || null; }
  uiDe(chave) { return chave === null ? null : this.pool.find((u) => !u.no.hidden && u.item && this.op.chave(u.item) === chave) || null; }
  indiceDe(chave) { return chave === null ? -1 : this.itens.findIndex((it) => this.op.chave(it) === chave); }

  agendar() {
    if (this.pendente) return;
    this.pendente = true;
    requestAnimationFrame(() => { this.pendente = false; this.pintar(); });
  }

  // Espaço de n linhas: o vão do flex já separa o espaçador da linha vizinha, por isso desconta um vão.
  espaco(el, n, passo, vao) {
    el.hidden = n === 0;
    el.style.height = Math.max(0, n * passo - vao) + "px";
  }

  pintar() {
    const caixa = this.caixa;
    const itens = this.itens;
    // Foco dentro da lista antes de repintar: chave e posição, para devolver depois (D34).
    const ativo = document.activeElement;
    const tinhaFoco = !!ativo && ativo !== caixa && caixa.contains(ativo);
    let posicao = -1;
    if (tinhaFoco) {
      const ui = this.uiQueContem(ativo);
      if (ui) {
        if (!this.forcarFoco) this.focoChave = this.op.chave(ui.item);
        posicao = ui.indice;
      }
    }
    // Altura real (fracionária) da linha e vão do CSS: um erro aqui se acumula a cada linha acima da janela.
    const medida = this.pool.find((u) => !u.no.hidden);
    if (medida) this.altura = medida.no.getBoundingClientRect().height || this.altura;
    const css = getComputedStyle(caixa);
    const vao = parseFloat(css.rowGap) || 0;
    const passo = this.altura + vao;
    const alturaVisivel = parseFloat(css.maxHeight) || caixa.clientHeight;
    // Rolagem limitada ao fim da lista nova: se ela encolheu, a janela não cai além do fim (lista em branco).
    const topo = Math.min(caixa.scrollTop, Math.max(0, itens.length * passo - alturaVisivel));
    const j = janela(itens.length, 1, topo, alturaVisivel, passo);
    this.espaco(this.antes, j.inicio, passo, vao);
    this.espaco(this.depois, itens.length - j.fim, passo, vao);
    const n = j.fim - j.inicio;
    while (this.pool.length < n) {
      const ui = this.op.criar();
      this.depois.before(ui.no);
      this.pool.push(ui);
    }
    this.pool.forEach((ui, k) => {
      const item = k < n ? itens[j.inicio + k] : undefined;
      ui.no.hidden = !item;
      ui.item = item || null;
      ui.indice = j.inicio + k;
      if (item) this.op.preencher(ui, item);
    });
    this.passo = passo;
    this.alturaVisivel = alturaVisivel;
    // Item focado que sumiu da lista: a vizinha na mesma posição fica com o foco.
    if (this.focoChave !== null && this.indiceDe(this.focoChave) < 0) {
      this.focoChave = itens.length && posicao >= 0 ? this.op.chave(itens[Math.min(posicao, itens.length - 1)]) : null;
    }
    this.forcarFoco = false;
    this.marcarTab();
    if (!tinhaFoco) return;
    const alvo = this.uiDe(this.focoChave);
    if (alvo) this.foco(alvo).focus({ preventScroll: true });
    else {
      // Item focado fora da janela (rolado com o mouse): o foco fica na linha onde está e passa a ser dela.
      const ui = this.uiQueContem(document.activeElement);
      if (ui) { this.focoChave = this.op.chave(ui.item); this.marcarTab(); }
    }
  }

  // Uma linha com tabindex 0: a do item focado; fora da janela, a primeira visível (a lista segue alcançável pelo Tab).
  marcarTab() {
    const alvo = this.uiDe(this.focoChave) || this.pool.find((u) => !u.no.hidden) || null;
    for (const ui of this.pool) this.foco(ui).tabIndex = ui === alvo ? 0 : -1;
  }

  tecla(e) {
    const ui = this.uiQueContem(e.target);
    if (!ui) return;
    if ((e.key === "Enter" || e.key === " ") && this.op.ativar && this.foco(ui).tagName !== "BUTTON") {
      e.preventDefault();
      this.op.ativar(ui.item);
      return;
    }
    const n = this.itens.length;
    const atual = Math.max(0, this.indiceDe(this.op.chave(ui.item)));
    const alvo = { ArrowDown: atual + 1, ArrowUp: atual - 1, Home: 0, End: n - 1 }[e.key];
    if (alvo === undefined || !n) return;
    e.preventDefault();
    this.irPara(Math.min(n - 1, Math.max(0, alvo)));
  }

  // Leva o foco ao item i, rolando a janela o mínimo para ele aparecer.
  irPara(i) {
    this.focoChave = this.op.chave(this.itens[i]);
    this.forcarFoco = true;
    const caixa = this.caixa;
    const topo = i * this.passo;
    const visivel = this.alturaVisivel || caixa.clientHeight;
    if (topo < caixa.scrollTop) caixa.scrollTop = topo;
    else if (topo + this.passo > caixa.scrollTop + visivel) caixa.scrollTop = topo + this.passo - visivel;
    this.pintar();
  }
}
