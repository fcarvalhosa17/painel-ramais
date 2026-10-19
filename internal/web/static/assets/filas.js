// Painel de filas da aba Chamadas (CONTRATOS 2.4 e 4.8, desvios D32 a D34): uma linha por fila (mesmo a vazia ou
// sem agente), detalhe da fila (quem aguarda e agentes) e renomear (só administrador). Aguardando, maior espera e
// agentes por estado são derivados no navegador (resumoFila). Texto do Asterisk só por textContent.

import { api } from "./conexao.js";
import { formatarNumero, formatarCronometro, formatarPct, nomeFila, resumoFila, erroNomeFila } from "./estado.js";
import { ListaLinhas } from "./linhas.js";

const $ = (id) => document.getElementById(id);
// Estado do agente: rótulo e a cor de estado que a tela já usa (livre, em conversa, em espera, desconhecido).
const AGENTE = {
  free: { rotulo: "Livre", varios: "livres", cor: "livre" },
  busy: { rotulo: "Em atendimento", varios: "em atendimento", cor: "em_conversa" },
  paused: { rotulo: "Pausado", varios: "pausados", cor: "em_espera" },
  unavailable: { rotulo: "Indisponível", varios: "indisponíveis", cor: "desconhecido" },
};
const ORDEM_AGENTE = ["free", "busy", "paused", "unavailable"];

const el = (tag, classe, pai) => {
  const e = document.createElement(tag);
  if (classe) e.className = classe;
  if (pai) pai.append(e);
  return e;
};

export function iniciarFilas({ dados, relogio, aoSessaoEncerrada }) {
  let admin = false;
  let aberta = null; // queue da fila no detalhe
  const espera = (iso) => formatarCronometro(Math.max(0, relogio() - (Date.parse(iso) || relogio())));
  const ordenadas = () => [...dados.filas.values()].sort((a, b) => a.queue.localeCompare(b.queue, "pt-BR", { numeric: true }));

  // ---------- lista de filas ----------
  function criarLinha() {
    const no = el("div", "fila-linha");
    no.setAttribute("role", "listitem");
    const cabeca = el("span", "fila-cabeca", no);
    const nome = el("span", "fila-nome", cabeca);
    const num = el("span", "fila-num", cabeca);
    const aguardando = el("span", "fila-espera", no);
    const agentes = el("span", "fila-agentes", no);
    const contagens = {};
    for (const k of ORDEM_AGENTE) {
      const s = el("span", "fila-ag", agentes);
      s.dataset.estado = AGENTE[k].cor;
      el("span", "fila-ag-ponto", s).setAttribute("aria-hidden", "true");
      contagens[k] = document.createTextNode("");
      s.append(contagens[k]);
    }
    const stats = el("span", "fila-stats", no);
    return { no, nome, num, aguardando, contagens, stats };
  }

  function preencherLinha(ui, f) {
    const r = resumoFila(f);
    const nome = nomeFila(f.queue, dados.filas);
    ui.no.dataset.fila = f.queue;
    if (f.queue === aberta) ui.no.dataset.aberta = "1"; else delete ui.no.dataset.aberta;
    ui.nome.textContent = nome;
    ui.num.textContent = f.queue;
    ui.aguardando.textContent = r.aguardando
      ? formatarNumero(r.aguardando) + " aguardando · maior espera " + espera(r.desde)
      : "Ninguém aguardando";
    ui.aguardando.classList.toggle("fila-com-espera", r.aguardando > 0);
    const agentes = ORDEM_AGENTE.map((k) => formatarNumero(r.agentes[k]) + " " + AGENTE[k].varios);
    ORDEM_AGENTE.forEach((k, i) => { ui.contagens[k].data = " " + agentes[i]; });
    // Linha de altura fixa (lista virtual): o texto visível é curto; o completo vai no aria-label e no detalhe.
    const pct = formatarPct(Number(f.service_level_perf) || 0, 100);
    const contas = "Atendidas " + formatarNumero(f.completed || 0) + " · abandonadas " + formatarNumero(f.abandoned || 0);
    ui.stats.textContent = contas + " · NS " + pct;
    ui.no.setAttribute("aria-label", nome + " (fila " + f.queue + "): " + ui.aguardando.textContent + "; agentes: "
      + agentes.join(", ") + "; " + contas + "; nível de serviço " + pct + textoNs(f) + ". Enter abre o detalhe");
  }

  const lista = new ListaLinhas($("filas-lista"), {
    criar: criarLinha, preencher: preencherLinha, chave: (f) => f.queue, ativar: (f) => abrir(f.queue),
  });
  $("filas-lista").addEventListener("click", (e) => {
    const no = e.target.closest(".fila-linha");
    if (no && no.dataset.fila) abrir(no.dataset.fila);
  });

  function render() {
    const fs = ordenadas();
    lista.itens = fs;
    lista.pintar();
    $("filas-n").textContent = formatarNumero(fs.length);
    $("filas-lista").setAttribute("aria-label", "Filas, " + formatarNumero(fs.length) + (fs.length === 1 ? " fila" : " filas"));
    const vazio = $("filas-vazio");
    vazio.hidden = fs.length > 0;
    vazio.textContent = dados.epoch === null ? "Carregando filas..."
      : !dados.amiConectado ? "Sem conexão com o Asterisk: as filas voltam quando a conexão voltar."
      : "Nenhuma fila no Asterisk.";
    if (aberta !== null) renderDetalhe();
  }

  // ---------- detalhe da fila ----------
  const fd = {
    painel: $("fila-detalhe"), nome: $("fd-nome"), num: $("fd-num"), sumiu: $("fd-sumiu"), lida: $("fd-lida"),
    renomear: $("fd-renomear"), espera: $("fd-espera"), esperaVazio: $("fd-espera-vazio"), agentes: $("fd-agentes"),
    agentesVazio: $("fd-agentes-vazio"), fechar: $("fd-fechar"),
  };

  function abrir(q) {
    aberta = q;
    filaMostrada = false; // força remontar as listas da fila que abriu
    fd.painel.hidden = false;
    renderDetalhe();
    lista.pintar(); // marca a fila aberta
    fd.fechar.focus({ preventScroll: true });
    const r = fd.painel.getBoundingClientRect();
    if (r.top > window.innerHeight || r.bottom < 0) {
      fd.painel.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    }
  }

  function fechar(focar) {
    const q = aberta;
    aberta = null;
    filaMostrada = false;
    fd.painel.hidden = true;
    lista.pintar();
    if (!focar) return;
    const ui = lista.uiDe(q);
    (ui ? lista.foco(ui) : $("filas-lista")).focus({ preventScroll: true });
  }
  fd.fechar.addEventListener("click", () => fechar(true));

  // Quem aguarda e agentes (D44): listas virtuais (ListaLinhas) remontadas só quando a fila muda (objeto novo no
  // delta) ou quando o detalhe abre; o tique de 1 s só troca o tempo de espera das linhas desenhadas.
  function criarEspera() {
    const no = el("div", "fd-item");
    no.setAttribute("role", "listitem");
    return { no, pos: el("span", "fd-pos", no), quem: el("span", "fd-quem", no), tempo: el("span", "fd-tempo", no) };
  }
  function preencherEspera(ui, x) {
    const quem = (x.number || "sem número") + (x.name ? " " + x.name : "");
    ui.no.dataset.id = x.uniqueid;
    ui.pos.textContent = String(x.position);
    ui.quem.textContent = quem;
    ui.quem.title = quem;
    ui.tempo.textContent = espera(x.since);
    ui.no.setAttribute("aria-label", "Posição " + x.position + ", " + quem + ", esperando há " + ui.tempo.textContent);
  }
  function criarAgente() {
    const no = el("div", "fd-item fd-agente");
    no.setAttribute("role", "listitem");
    el("span", "fila-ag-ponto", no).setAttribute("aria-hidden", "true");
    return { no, quem: el("span", "fd-quem", no), estado: el("span", "fd-estado", no) };
  }
  function preencherAgente(ui, m) {
    const a = AGENTE[m.state] || AGENTE.unavailable;
    const it = m.extension ? dados.porNumero.get(m.extension) : null;
    const quem = m.extension ? m.extension + " " + ((it && it.ramal.name) || m.name || "sem nome") : m.name || m.interface;
    const pausa = m.paused && m.state !== "paused" ? " (pausado)" : "";
    const estado = a.rotulo + pausa + (m.pause_reason ? ": " + m.pause_reason : "") + " · atendidas " + formatarNumero(m.calls_taken || 0);
    ui.no.dataset.estado = a.cor;
    ui.quem.textContent = quem;
    ui.estado.textContent = estado;
    ui.no.title = quem + ", " + estado;
    ui.no.setAttribute("aria-label", quem + ", " + estado);
  }
  const esperaLista = new ListaLinhas(fd.espera, { criar: criarEspera, preencher: preencherEspera, chave: (x) => x.uniqueid });
  const agentesLista = new ListaLinhas(fd.agentes, { criar: criarAgente, preencher: preencherAgente, chave: (m) => m.interface });
  // Objeto da fila desenhado nas listas (o delta troca o objeto inteiro quando ela muda; undefined = fila sumiu;
  // false = nada desenhado ainda).
  let filaMostrada = false;
  let nomesMostrados = ""; // nomes dos ramais dos agentes desenhados (renomear um ramal remonta a lista)
  const nomesDosAgentes = (f) => (f && Array.isArray(f.members) ? f.members : [])
    .map((m) => { const it = m.extension ? dados.porNumero.get(m.extension) : null; return (it && it.ramal.name) || ""; }).join("|");

  function definirListas(f) {
    filaMostrada = f;
    nomesMostrados = nomesDosAgentes(f);
    const entries = f && Array.isArray(f.entries) ? f.entries : [];
    const membros = f && Array.isArray(f.members) ? f.members : [];
    esperaLista.itens = entries;
    agentesLista.itens = membros;
    esperaLista.pintar();
    agentesLista.pintar();
    fd.espera.setAttribute("aria-label", "Quem aguarda na fila, " + formatarNumero(entries.length));
    fd.agentes.setAttribute("aria-label", "Agentes da fila, " + formatarNumero(membros.length));
    fd.esperaVazio.hidden = !f || entries.length > 0;
    fd.agentesVazio.hidden = !f || membros.length > 0;
  }

  function renderDetalhe() {
    const f = dados.filas.get(aberta);
    fd.nome.textContent = nomeFila(aberta, dados.filas);
    fd.num.textContent = "fila " + aberta;
    fd.sumiu.hidden = !!f;
    fd.renomear.hidden = !admin || !f;
    // Remonta só se a fila mudou (objeto novo no delta) ou se o nome de um ramal de agente mudou; no tique, nenhum
    // dos dois muda e as listas ficam como estão (ponytail: compara os nomes a cada chamada, O(agentes)).
    if (f !== filaMostrada || nomesDosAgentes(f) !== nomesMostrados) definirListas(f);
    if (!f) { fd.lida.textContent = ""; return; }
    fd.lida.textContent = (f.stats_at
      ? "Contadores desde o último reinício do Asterisk; lidos há " + idade(f.stats_at)
      : "Contadores aguardando leitura") + ". Atendidas " + formatarNumero(f.completed || 0) + ", abandonadas "
      + formatarNumero(f.abandoned || 0) + ", nível de serviço " + formatarPct(Number(f.service_level_perf) || 0, 100) + textoNs(f) + ".";
  }

  const textoNs = (f) => (f.service_level_s ? " em " + f.service_level_s + " s" : "");

  function idade(iso) {
    const s = Math.max(0, Math.round((relogio() - Date.parse(iso)) / 1000));
    return Number.isNaN(s) ? "?" : s < 60 ? s + " s" : Math.floor(s / 60) + " min";
  }

  // ---------- renomear (D33, só administrador; o daemon decide e responde 403) ----------
  const df = {
    dialogo: $("dialogo-fila"), form: $("df-form"), nome: $("df-nome"), erro: $("df-erro"), salvar: $("df-salvar"),
    contexto: $("df-contexto"),
  };
  let filaRenomear = null;
  fd.renomear.addEventListener("click", () => {
    const f = dados.filas.get(aberta);
    if (!f) return;
    filaRenomear = f.queue;
    df.contexto.textContent = "Fila " + f.queue + " no Asterisk.";
    df.nome.value = f.name || "";
    df.erro.textContent = "";
    df.dialogo.showModal();
    df.nome.focus();
    df.nome.select();
  });
  df.nome.addEventListener("input", () => { df.erro.textContent = erroNomeFila(df.nome.value); });
  df.dialogo.addEventListener("cancel", (e) => { if (df.dialogo.dataset.ocupado) e.preventDefault(); });
  df.form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (df.dialogo.dataset.ocupado || filaRenomear === null) return;
    const nome = df.nome.value.trim();
    const erro = erroNomeFila(nome);
    df.erro.textContent = erro;
    if (erro) { df.nome.focus(); return; }
    df.dialogo.dataset.ocupado = "1";
    df.salvar.disabled = true;
    df.salvar.textContent = "Salvando...";
    try {
      await api.renomearFila(filaRenomear, nome);
      delete df.dialogo.dataset.ocupado;
      df.dialogo.close(); // o nome novo chega pelo delta, nesta e nas outras telas
    } catch (err) {
      if (err.status === 401) { delete df.dialogo.dataset.ocupado; df.dialogo.close(); aoSessaoEncerrada(); return; }
      df.erro.textContent = err.message; // 400, 403, 404, 500: mensagem do daemon
    } finally {
      delete df.dialogo.dataset.ocupado;
      df.salvar.disabled = false;
      df.salvar.textContent = "Salvar";
    }
  });

  return {
    render,
    tique() {
      lista.pintar();
      if (aberta === null) return;
      renderDetalhe(); // remonta as listas só se a fila mudou
      esperaLista.paraVisiveis((ui, x) => {
        const t = espera(x.since);
        if (ui.tempo.textContent !== t) {
          ui.tempo.textContent = t;
          ui.no.setAttribute("aria-label", "Posição " + x.position + ", " + ui.quem.textContent + ", esperando há " + t);
        }
      });
    },
    definirAdmin(sim) {
      admin = sim;
      if (aberta !== null) renderDetalhe();
    },
    parar() {
      if (aberta !== null) fechar(false);
    },
  };
}
