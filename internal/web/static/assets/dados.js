// Núcleo de dados (contrato C8): snapshot, aplicação de delta, lacuna e desvio de relógio.
// Sem DOM e sem rede: testado com node --test.

import { estadoDaTela, chaveBusca } from "./estado.js";

export class Dados {
  constructor() {
    this.epoch = null;
    this.version = 0;
    this.amiConectado = true;
    this.desvioMs = 0;
    this.tecnologia = null;
    // itens: [{ ramal, estado, chaveBusca }] ordenados por number (ordem de string).
    this.itens = [];
    this.porNumero = new Map();
    // Catálogo de grupos [{ name, ranges }], inclui grupos vazios. catalogoMudou: quem desenha zera depois de ler.
    this.grupos = [];
    this.catalogoMudou = false;
  }

  aplicarSnapshot(data, agoraMs = Date.now()) {
    this.epoch = data.epoch;
    this.version = data.version;
    this.amiConectado = data.ami_connected !== false;
    this.tecnologia = data.technology ?? null;
    this.atualizarRelogio(data.server_time, agoraMs);
    // Snapshot novo (queda do AMI, ressincronização) traz o catálogo inteiro: só sinaliza se mudou de fato,
    // senão o redesenho da tela Grupos apagaria um formulário de edição aberto.
    const grupos = Array.isArray(data.groups) ? data.groups : [];
    if (chaveCatalogo(grupos) !== chaveCatalogo(this.grupos)) this.catalogoMudou = true;
    this.grupos = grupos;
    this.itens = [];
    this.porNumero = new Map();
    for (const ramal of data.extensions || []) {
      const item = this.criarItem(ramal);
      this.itens.push(item);
      this.porNumero.set(ramal.number, item);
    }
    // O contrato garante a ordem; ordenar de novo só se vier fora (custo zero quando já ordenado).
    if (!estaOrdenado(this.itens)) this.itens.sort(compararItens);
  }

  // Devolve "aplicado", "ignorado" ou "lacuna" (lacuna: buscar snapshot e reconectar).
  aplicarDelta(msg, agoraMs = Date.now()) {
    if (msg.epoch !== this.epoch) return "lacuna";
    if (msg.to_version <= this.version) return "ignorado";
    if (msg.from_version > this.version) return "lacuna";
    const amiMudou = (msg.ami_connected !== false) !== this.amiConectado;
    this.amiConectado = msg.ami_connected !== false;
    this.atualizarRelogio(msg.server_time, agoraMs);
    // Catálogo completo, só quando mudou (criar, editar, excluir grupo); o epoch não muda.
    if (Array.isArray(msg.groups)) { this.grupos = msg.groups; this.catalogoMudou = true; }
    for (const ramal of msg.extensions || []) {
      const atual = this.porNumero.get(ramal.number);
      if (atual) {
        atual.ramal = ramal;
        atual.estado = estadoDaTela(ramal, this.amiConectado);
        atual.chaveBusca = chaveBusca(ramal);
      } else {
        const item = this.criarItem(ramal);
        this.porNumero.set(ramal.number, item);
        this.itens.splice(posicaoDeInsercao(this.itens, ramal.number), 0, item);
      }
    }
    // Ramal apagado no PBX (contrato 5.2/7.4): sai da lista sem epoch novo; número que não temos é ignorado.
    for (const numero of msg.removed || []) {
      const item = this.porNumero.get(numero);
      if (!item) continue;
      this.porNumero.delete(numero);
      const i = posicaoDeInsercao(this.itens, numero);
      if (this.itens[i] === item) this.itens.splice(i, 1);
      else {
        // ponytail: só acontece se a ordem local divergir; busca linear de garantia.
        const j = this.itens.indexOf(item);
        if (j >= 0) this.itens.splice(j, 1);
      }
    }
    // A conexão com o AMI muda o estado exibido de todos (C9 regra 1).
    if (amiMudou) for (const it of this.itens) it.estado = estadoDaTela(it.ramal, this.amiConectado);
    this.version = msg.to_version;
    return "aplicado";
  }

  criarItem(ramal) {
    return { ramal, estado: estadoDaTela(ramal, this.amiConectado), chaveBusca: chaveBusca(ramal) };
  }

  atualizarRelogio(serverTime, agoraMs) {
    const t = Date.parse(serverTime);
    if (!Number.isNaN(t)) this.desvioMs = t - agoraMs;
  }
}

// Nomes e faixas, na ordem do servidor: igual = mesmo catálogo.
const chaveCatalogo = (grupos) => grupos.map((g) => g.name + ":" + (g.ranges || []).map((r) => r.start + "-" + r.end).join(",")).join("\n");

const compararNumero = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
const compararItens = (a, b) => compararNumero(a.ramal.number, b.ramal.number);

function estaOrdenado(itens) {
  for (let i = 1; i < itens.length; i++) if (compararItens(itens[i - 1], itens[i]) > 0) return false;
  return true;
}

function posicaoDeInsercao(itens, numero) {
  let lo = 0, hi = itens.length;
  while (lo < hi) {
    const m = (lo + hi) >> 1;
    if (itens[m].ramal.number < numero) lo = m + 1; else hi = m;
  }
  return lo;
}

// Reconexão: 1 s dobrando até 30 s, com variação de 20% para cima ou para baixo.
export function atrasoReconexao(tentativa, aleatorio = Math.random()) {
  const base = Math.min(30000, 1000 * 2 ** Math.max(0, tentativa));
  return Math.round(base * (0.8 + 0.4 * aleatorio));
}
