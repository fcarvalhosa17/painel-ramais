// Bloco "Instabilidade de registro (24 h)" da coluna Visões (CONTRATOS 4.7.1, desvios D20).
// Busca ao ligar, ao trocar as visões e a cada 30 s, só com a aba Ramais e o documento visíveis.
// Texto do servidor (número, nome) só por textContent.

import { api } from "./conexao.js";
import { formatarNumero, dataHora, avisoPausa, rotuloVisoes } from "./estado.js";

const $ = (id) => document.getElementById(id);
const INTERVALO = 30e3;
const MAX_ITENS = 10;
const somaItem = (i) => (i.lost_registration || 0) + (i.became_unreachable || 0);
const ordemItens = (a, b) => somaItem(b) - somaItem(a) || (b.lost_registration || 0) - (a.lost_registration || 0)
  || (a.number < b.number ? -1 : a.number > b.number ? 1 : 0);

// Summary de uma ou várias visões ([] = todos os ramais). O daemon filtra uma visão por pedido; com várias, um pedido
// por visão, juntando pelo número: os contadores do ramal não dependem da visão, e os 10 primeiros da união estão
// sempre entre os 10 primeiros de alguma visão (mesma ordem do CONTRATOS 4.7.1).
async function resumoDasVisoes(visoes) {
  if (visoes.length <= 1) return api.resumoRegistros(visoes[0] || "");
  const respostas = await Promise.all(visoes.map((v) => api.resumoRegistros(v)));
  const porNumero = new Map();
  for (const r of respostas) for (const i of (r && Array.isArray(r.items) ? r.items : [])) porNumero.set(i.number, i);
  return { ...(respostas[0] || {}), items: [...porNumero.values()].sort(ordemItens).slice(0, MAX_ITENS) };
}

// relogio(): hora do servidor no navegador (Date.now() corrigido pelo server_time), para o aviso de pausa (D42).
export function iniciarInstabilidade({ aoAbrir, relogio, aoSessaoEncerrada }) {
  const lista = $("inst-lista");
  const estado = $("inst-estado");
  const texto = $("inst-estado-texto");
  const repetir = $("inst-repetir");
  const desde = $("inst-desde");
  const pausa = $("inst-pausa");
  let timerPausa = null;
  let ultimo = null; // último summary, para tirar o aviso de pausa na hora em que ela acaba
  let visoes = [];
  let ligado = false;
  let timer = null;
  let pedido = 0; // resposta de pedido antigo (outras visões, bloco desligado) é descartada
  let temDados = false;

  function mostrarEstado(t, comRepetir) {
    estado.hidden = !t;
    texto.textContent = t;
    repetir.hidden = !comRepetir;
  }

  async function carregar() {
    if (!ligado || document.visibilityState !== "visible") return;
    const meu = ++pedido;
    if (!temDados) mostrarEstado("Carregando...", false);
    try {
      const d = await resumoDasVisoes(visoes);
      if (meu !== pedido) return;
      render(d || {});
    } catch (e) {
      if (meu !== pedido) return;
      if (e.status === 401) { aoSessaoEncerrada(); return; }
      lista.replaceChildren();
      temDados = false;
      desde.hidden = true;
      ultimo = null;
      renderPausa();
      mostrarEstado("Não foi possível carregar a instabilidade de registro.", true);
    }
  }

  // "Contagem de registros pausada até HH:MM (Asterisk reiniciado)": lido do summary a cada 30 s (pode chegar com
  // até 30 s de atraso) e tirado sozinho quando paused_until passa.
  function renderPausa() {
    clearTimeout(timerPausa);
    timerPausa = null;
    const agora = relogio();
    const t = ultimo ? avisoPausa(ultimo.paused_until, ultimo.pause_reason, agora) : "";
    pausa.hidden = !t;
    if (pausa.textContent !== t) pausa.textContent = t;
    if (t) timerPausa = setTimeout(renderPausa, Math.max(1000, Date.parse(ultimo.paused_until) - agora + 500));
  }

  function render(d) {
    ultimo = d;
    renderPausa();
    const itens = Array.isArray(d.items) ? d.items : [];
    let max = 1;
    for (const i of itens) max = Math.max(max, (i.lost_registration || 0) + (i.became_unreachable || 0));
    lista.replaceChildren(...itens.map((i) => linha(i, max)));
    temDados = true;
    mostrarEstado(itens.length ? "" : "Nenhuma instabilidade nas últimas 24 h", false);
    // Período começou antes da instalação do relatório: o ranking está incompleto.
    const incompleto = d.since && d.from && Date.parse(d.since) > Date.parse(d.from);
    desde.hidden = !incompleto;
    desde.textContent = incompleto ? "Dados desde " + dataHora(d.since) : "";
  }

  function linha(i, max) {
    const perdeu = i.lost_registration || 0;
    const inalc = i.became_unreachable || 0;
    const nome = (i.name || "sem nome") + (i.removed ? " (removido)" : "");
    const li = document.createElement("li");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "inst-linha";
    b.setAttribute("aria-label", "Ramal " + i.number + ", " + nome + ": perdeu registro " + formatarNumero(perdeu)
      + ", ficou inalcançável " + formatarNumero(inalc) + ". Abrir em Registros");
    const topo = document.createElement("span");
    topo.className = "inst-topo";
    const num = document.createElement("span");
    num.className = "inst-num";
    num.textContent = i.number;
    const nm = document.createElement("span");
    nm.className = "inst-nome";
    nm.textContent = nome;
    const n = document.createElement("span");
    n.className = "inst-n";
    const np = document.createElement("span");
    np.className = "inst-n-perdeu";
    np.textContent = formatarNumero(perdeu);
    const ni = document.createElement("span");
    ni.className = "inst-n-inalc";
    ni.textContent = formatarNumero(inalc);
    n.append(np, " · ", ni);
    topo.append(num, nm, n);
    // Barra empilhada na escala do maior total do bloco.
    const barra = document.createElement("span");
    barra.className = "inst-barra";
    barra.setAttribute("aria-hidden", "true");
    const sp = document.createElement("span");
    sp.className = "inst-perdeu";
    sp.style.width = ((perdeu / max) * 100).toFixed(2) + "%";
    const si = document.createElement("span");
    si.className = "inst-inalc";
    si.style.width = ((inalc / max) * 100).toFixed(2) + "%";
    barra.append(sp, si);
    b.append(topo, barra);
    b.addEventListener("click", () => aoAbrir(i.number));
    li.append(b);
    return li;
  }

  repetir.addEventListener("click", () => carregar());
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") carregar(); });

  // Aba Ramais visível (e painel ativo) ou não.
  function mostrar(sim) {
    if (sim === ligado) return;
    ligado = sim;
    clearInterval(timer);
    timer = null;
    if (!sim) { pedido++; return; }
    carregar();
    timer = setInterval(carregar, INTERVALO);
  }

  return {
    mostrar,
    definirVisoes(novas) {
      if (novas.join("\n") === visoes.join("\n")) return;
      visoes = novas;
      $("inst-grupo").textContent = rotuloVisoes(novas);
      // Não mostra o ranking das visões anteriores enquanto o novo carrega.
      pedido++;
      temDados = false;
      lista.replaceChildren();
      desde.hidden = true;
      if (ligado) carregar(); else mostrarEstado("", false);
    },
    // Saída do painel: desliga e apaga o que o próximo usuário não deve ver.
    parar() {
      mostrar(false);
      temDados = false;
      lista.replaceChildren();
      desde.hidden = true;
      ultimo = null;
      renderPausa();
      mostrarEstado("", false);
    },
  };
}
