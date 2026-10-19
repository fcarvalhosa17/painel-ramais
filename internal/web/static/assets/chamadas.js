// Aba Chamadas (rc.8, CONTRATOS 2.3 e 2.4, desvios D28 a D34): contadores, filtros, três listas com rolagem
// virtual e o painel de filas (filas.js). Os dados vêm do snapshot e do delta (dados.js: mapas por id e por fila);
// a aba só recalcula, pinta e conta o tempo enquanto está visível. Texto do Asterisk só por textContent.

import {
  SETA, SEM_GRUPO, compararGrupos, formatarNumero, formatarCronometro, nomeFila, textosChamada,
  filtrarChamadas, contarChamadas, listaDaChamada,
} from "./estado.js";
import { ListaLinhas } from "./linhas.js";
import { iniciarFilas } from "./filas.js";

const $ = (id) => document.getElementById(id);
const TODOS = "Todos os ramais";
const LONGA_MS = 20 * 60e3; // tempo em laranja acima de 20 min (como em Chamadas ativas)
const LISTAS = [
  { chave: "entrantes", titulo: "Externas entrantes" },
  { chave: "saintes", titulo: "Externas saintes" },
  { chave: "internas", titulo: "Internas" },
];
const DIRECAO_FALADA = { inbound: "Entrada", outbound: "Saída", internal: "Interna" };

function opcao(texto, valor) {
  const o = document.createElement("option");
  o.textContent = texto;
  o.value = valor;
  return o;
}

// Seletor que só é refeito quando as opções mudam (refazer a cada delta fecharia o seletor aberto).
// A opção escolhida continua na lista mesmo se sumir dos dados, para o filtro não mudar sozinho.
function preencherSeletor(sel, ops, atual) {
  if (atual && !ops.some(([, v]) => v === atual)) ops.push([atual, atual]);
  const chave = ops.map(([t, v]) => t + "\t" + v).join("\n");
  if (sel.dataset.chave !== chave) {
    sel.dataset.chave = chave;
    sel.replaceChildren(...ops.map(([t, v]) => opcao(t, v)));
  }
  sel.value = atual;
}

export function iniciarChamadas({ dados, relogio, aoSessaoEncerrada }) {
  const filtro = { grupo: "", busca: "", tipo: "", tronco: "", fila: "" };
  let visivel = false;
  let sujo = true;
  const inicio = new WeakMap(); // chamada -> started_at em ms (o objeto é trocado inteiro pelo delta)
  const inicioDe = (c) => {
    let v = inicio.get(c);
    if (v === undefined) { v = Date.parse(c.started_at) || 0; inicio.set(c, v); }
    return v;
  };
  const nomeRamal = (n) => { const it = dados.porNumero.get(n); return (it && it.ramal.name) || "sem nome"; };

  const filas = iniciarFilas({ dados, relogio, aoSessaoEncerrada });

  // ---------- linhas ----------
  function criarLinha() {
    const no = document.createElement("div");
    no.className = "chamada chamada-fixa";
    no.setAttribute("role", "listitem");
    const seta = document.createElement("span");
    seta.className = "chamada-seta";
    seta.setAttribute("aria-hidden", "true");
    const meio = document.createElement("span");
    meio.className = "chamada-meio";
    const l1 = document.createElement("span");
    l1.className = "chamada-l1";
    const num = document.createElement("strong");
    const resto = document.createTextNode("");
    l1.append(num, resto);
    const l2 = document.createElement("span");
    l2.className = "chamada-l2";
    meio.append(l1, l2);
    const tempo = document.createElement("span");
    tempo.className = "chamada-tempo";
    no.append(seta, meio, tempo);
    return { no, seta, num, resto, l2, tempo };
  }

  function preencherLinha(ui, c) {
    const t = textosChamada(c, dados.filas, nomeRamal);
    const ms = Math.max(0, relogio() - inicioDe(c));
    ui.no.dataset.id = c.id;
    if (c.state === "queued" || c.state === "hold") ui.no.dataset.espera = "1"; else delete ui.no.dataset.espera;
    ui.seta.textContent = SETA[c.direction] || SETA.internal;
    ui.num.textContent = t.numero;
    ui.resto.data = t.resto;
    ui.l2.textContent = t.l2;
    ui.tempo.textContent = formatarCronometro(ms);
    ui.tempo.classList.toggle("longa", ms > LONGA_MS);
    ui.no.setAttribute("aria-label", (DIRECAO_FALADA[c.direction] || "Chamada") + ", " + (t.numero + t.resto).trim()
      + ", " + t.l2 + ", " + ui.tempo.textContent);
  }

  const listas = {};
  for (const l of LISTAS) {
    listas[l.chave] = {
      ...l,
      bloco: $("ch-bloco-" + l.chave), caixa: $("ch-" + l.chave), total: $("ch-total-" + l.chave), vazio: $("ch-vazio-" + l.chave),
      lista: new ListaLinhas($("ch-" + l.chave), { criar: criarLinha, preencher: preencherLinha, chave: (c) => c.id }),
    };
  }

  // ---------- filtros (D30): guardados só na visita ----------
  function preencherFiltros(todas) {
    const grupos = dados.grupos.map((g) => g.name).filter((n) => n !== SEM_GRUPO).sort(compararGrupos);
    // Grupo escolhido que saiu do catálogo (excluído, renomeado): volta para todos os ramais.
    if (filtro.grupo && filtro.grupo !== SEM_GRUPO && !grupos.includes(filtro.grupo)) filtro.grupo = "";
    preencherSeletor($("ch-grupo"), [[TODOS, ""], ...grupos.map((n) => [n, n]), [SEM_GRUPO, SEM_GRUPO]], filtro.grupo);
    const troncos = [...new Set(todas.map((c) => c.trunk).filter((t) => typeof t === "string" && t))].sort((a, b) => a.localeCompare(b, "pt-BR", { numeric: true }));
    preencherSeletor($("ch-tronco"), [["Todos os troncos", ""], ...troncos.map((t) => [t, t])], filtro.tronco);
    const nomes = [...dados.filas.keys()].sort((a, b) => a.localeCompare(b, "pt-BR", { numeric: true }));
    preencherSeletor($("ch-fila"), [["Todas as filas", ""], ...nomes.map((q) => [nomeFila(q, dados.filas), q])], filtro.fila);
  }

  for (const [id, campo] of [["ch-grupo", "grupo"], ["ch-tronco", "tronco"], ["ch-fila", "fila"]]) {
    $(id).addEventListener("change", (e) => { filtro[campo] = e.target.value; irAoTopo(); render(); });
  }
  let timerBusca = null;
  $("ch-busca").addEventListener("input", (e) => {
    clearTimeout(timerBusca);
    timerBusca = setTimeout(() => { filtro.busca = e.target.value; irAoTopo(); render(); }, 120);
  });
  // Tipo: esconde as listas não escolhidas; não muda os contadores (D29).
  for (const b of document.querySelectorAll("#ch-tipo .seg")) {
    b.addEventListener("click", () => {
      filtro.tipo = b.dataset.tipo;
      for (const o of document.querySelectorAll("#ch-tipo .seg")) o.setAttribute("aria-pressed", String(o === b));
      render();
    });
  }
  function irAoTopo() { for (const l of Object.values(listas)) l.caixa.scrollTop = 0; }

  // ---------- render ----------
  function render() {
    if (!visivel) { sujo = true; return; }
    sujo = false;
    const todas = [...dados.chamadas.values()];
    preencherFiltros(todas);
    const passam = filtrarChamadas(todas, filtro, dados.porNumero);
    const n = contarChamadas(passam);
    $("ch-n-entrantes").textContent = formatarNumero(n.entrantes);
    $("ch-n-saintes").textContent = formatarNumero(n.saintes);
    $("ch-n-internas").textContent = formatarNumero(n.internas);
    $("ch-n-fila").textContent = formatarNumero(n.fila);
    const porLista = { entrantes: [], saintes: [], internas: [] };
    for (const c of passam) porLista[listaDaChamada(c)].push(c);
    // Mais longas primeiro: início mais antigo em cima; empate pelo id (ordem estável).
    const ordem = (a, b) => inicioDe(a) - inicioDe(b) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
    // Estados sem dado: antes do primeiro snapshot, AMI fora (listas vazias com aviso), vazio e vazio por filtro.
    const vazio = dados.epoch === null ? "Carregando chamadas..."
      : !dados.amiConectado ? "Sem conexão com o Asterisk: as chamadas voltam quando a conexão voltar."
      : todas.length ? "Nenhuma chamada com esses filtros." : "Nenhuma chamada.";
    for (const l of Object.values(listas)) {
      const itens = porLista[l.chave].sort(ordem);
      l.bloco.hidden = !!filtro.tipo && filtro.tipo !== l.chave;
      l.total.textContent = formatarNumero(itens.length);
      l.caixa.setAttribute("aria-label", l.titulo + ", " + formatarNumero(itens.length) + (itens.length === 1 ? " chamada" : " chamadas"));
      l.lista.itens = itens;
      l.lista.pintar();
      l.vazio.hidden = itens.length > 0;
      l.vazio.textContent = vazio;
    }
    filas.render();
  }

  return {
    mostrar(sim) {
      visivel = sim;
      if (!sim) return;
      if (sujo) render(); else filas.render();
    },
    aoMudarDados() {
      sujo = true;
      if (visivel) render();
    },
    aoMudarCatalogo() {
      sujo = true;
      if (visivel) render();
    },
    // Tique de 1 s, só com a aba visível: tempos das linhas desenhadas e esperas das filas.
    tique() {
      if (!visivel) return;
      for (const l of Object.values(listas)) l.lista.pintar();
      filas.tique();
    },
    definirAdmin(sim) { filas.definirAdmin(sim); },
    parar() {
      sujo = true;
      filas.parar();
    },
  };
}
