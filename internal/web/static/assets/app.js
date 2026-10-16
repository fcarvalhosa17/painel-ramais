// Painel de Ramais: entrada (C4), ligação dos módulos e gancho de medição (C10).

import {
  ESTADOS, ROTULO, contagens, agregarGrupos, ranking, filtrar, variacaoUltimaHora, formatarNumero,
} from "./estado.js";
import { Dados, atrasoReconexao } from "./dados.js";
import { api, CanalAoVivo } from "./conexao.js";
import { Lista } from "./lista.js";
import { calcularLayout, acharBloco, desenharMapa } from "./mapa.js";
import { Grafico } from "./grafico.js";
import { iniciarGrupos } from "./grupos.js";

const $ = (id) => document.getElementById(id);
const EMERGENCIA = location.pathname.startsWith("/painel-ramais/emergencia");
const TOKEN = /^#t=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$/;
const SESSAO_ENCERRADA = "Sessão encerrada. Abra de novo pelo menu PBX do Issabel.";
const PERIODOS = { "1h": 3600e3, "24h": 24 * 3600e3, "7d": 7 * 24 * 3600e3 };
const MIN = 60e3;

// ---------- gancho de medição (C10) ----------
const medida = {
  pronto: false, primeiraExibicaoMs: null, epoch: null, version: 0, ultimoDeltaMs: null,
  contagens: { total: 0, registered: 0, unregistered: 0, unreachable: 0, in_call: 0 }, tarefasLongas: [],
};
Object.defineProperty(window, "__painel", {
  configurable: false,
  writable: false,
  value: Object.freeze({
    get pronto() { return medida.pronto; },
    get primeiraExibicaoMs() { return medida.primeiraExibicaoMs; },
    get contagens() { return { ...medida.contagens }; },
    get epoch() { return medida.epoch; },
    get version() { return medida.version; },
    get ultimoDeltaMs() { return medida.ultimoDeltaMs; },
    get tarefasLongas() { return medida.tarefasLongas.slice(); },
  }),
});
try {
  new PerformanceObserver((lista) => {
    for (const e of lista.getEntries()) {
      if (medida.tarefasLongas.length < 1000) medida.tarefasLongas.push({ inicio: Math.round(e.startTime), duracao: Math.round(e.duration) });
    }
  }).observe({ type: "longtask", buffered: true });
} catch { /* longtask só existe no Chromium */ }

// ---------- telas ----------
function mostrar(qual) {
  $("tela-mensagem").hidden = qual !== "mensagem";
  $("tela-login").hidden = qual !== "login";
  $("tela-painel").hidden = qual !== "painel";
}

function mensagem(texto) {
  $("mensagem-texto").textContent = texto;
  mostrar("mensagem");
}

function semSessao() {
  pararPainel();
  if (EMERGENCIA) {
    mostrar("login");
    $("login-senha").value = "";
    $("login-senha").focus();
  } else {
    mensagem(SESSAO_ENCERRADA);
  }
}

// ---------- entrada (C4) ----------
let tentativasEntrada = 0;
async function entrar() {
  let sessao = null;
  const veioToken = location.hash.startsWith("#t=");
  const m = location.hash.match(TOKEN);
  if (veioToken) history.replaceState(null, "", location.pathname);
  // Token apresentado e recusado (ou malformado, ou falha de rede) encerra: nunca cai no cookie anterior,
  // senão quem perdeu o acesso no Issabel seguiria entrando com a sessão antiga (segurança, M3a).
  if (veioToken) {
    try {
      if (!m) throw new Error("token malformado");
      sessao = await api.token(m[1]);
    } catch {
      semSessao();
      return;
    }
    iniciarPainel(sessao);
    return;
  }
  try {
    sessao = await api.sessao();
  } catch (e) {
    if (e.status === 401) { semSessao(); return; }
    const atraso = atrasoReconexao(tentativasEntrada++);
    mensagem("Não foi possível falar com o painel. Tentando de novo em " + Math.ceil(atraso / 1000) + " s.");
    setTimeout(entrar, atraso);
    return;
  }
  iniciarPainel(sessao);
}

$("form-login").addEventListener("submit", async (e) => {
  e.preventDefault();
  const senha = $("login-senha").value;
  const erro = $("login-erro");
  const botao = $("login-entrar");
  erro.textContent = "";
  if (!senha) { erro.textContent = "Digite a senha."; $("login-senha").focus(); return; }
  botao.disabled = true;
  botao.textContent = "Entrando...";
  try {
    iniciarPainel(await api.login(senha));
  } catch (err) {
    erro.textContent = err.status === 401 ? "Senha incorreta." : err.message;
    $("login-senha").select();
  } finally {
    botao.disabled = false;
    botao.textContent = "Entrar";
  }
});

$("botao-sair").addEventListener("click", async () => {
  try { await api.logout(); } catch { /* sai da tela de qualquer jeito */ }
  semSessao();
});

// ---------- estado da tela ----------
const dados = new Dados();
const filtros = { busca: "", estados: new Set(), grupo: "" };
let grupos = [];
let filtrados = [];
let cont = medida.contagens;
const sujo = { dados: false, filtro: false, delta: false };
let quadroPendente = false;
let ativo = false;
let timerHistorico = null;
let timerRelogio = null;

const lista = new Lista($("lista"), $("lista-altura"));
const tema = lerTema();
const grafico = new Grafico($("grafico"), $("grafico-dica"), () => tema);
const areaGrupos = iniciarGrupos(() => semSessao());

const canal = new CanalAoVivo({
  aoDelta(msg) {
    const r = dados.aplicarDelta(msg);
    if (r === "lacuna") { canal.reiniciar(); return; }
    if (r === "aplicado") { sujo.dados = true; sujo.delta = true; agendar(); }
  },
  aoPrecisarSnapshot: () => carregarSnapshot(),
  aoReconectar: () => canal.conectar(dados.epoch, dados.version),
  aoSessaoEncerrada: () => semSessao(),
  aoStatus: (s, atraso) => status(s, atraso),
});

function status(s, atraso) {
  const textos = { conectando: "Conectando", ao_vivo: "Ao vivo", atualizando: "Atualizando", reconectando: "Reconectando" };
  $("status-conexao").dataset.status = s;
  $("status-texto").textContent = textos[s] + (s === "reconectando" && atraso ? " em " + Math.ceil(atraso / 1000) + " s" : "");
}

function iniciarPainel(sessao) {
  ativo = true;
  mostrar("painel");
  $("botao-grupos").hidden = !sessao.admin;
  $("botao-sair").hidden = !EMERGENCIA;
  $("botao-cheia").hidden = !document.fullscreenEnabled;
  canal.parado = false;
  status("conectando");
  carregarSnapshot();
  clearInterval(timerRelogio);
  timerRelogio = setInterval(() => lista.tique(), 1000);
  // pararPainel desliga o histórico; numa nova entrada (sair e entrar na emergência) ele precisa voltar.
  // A primeira entrada liga o histórico pelo gancho da primeira exibição.
  if (medida.pronto) depoisDaPrimeiraExibicao();
}

function pararPainel() {
  ativo = false;
  canal.parar();
  clearInterval(timerRelogio);
  clearInterval(timerHistorico);
  timerHistorico = null;
}

let tentativasSnapshot = 0;
async function carregarSnapshot() {
  if (!ativo) return;
  let data;
  try {
    data = await api.snapshot();
  } catch (e) {
    if (e.status === 401) { semSessao(); return; }
    const atraso = atrasoReconexao(tentativasSnapshot++);
    status("reconectando", atraso);
    setTimeout(carregarSnapshot, atraso);
    return;
  }
  tentativasSnapshot = 0;
  const epochMudou = data.epoch !== dados.epoch;
  dados.aplicarSnapshot(data);
  sujo.dados = true;
  agendar();
  canal.conectar(dados.epoch, dados.version);
  // Grupos podem ter mudado com o epoch: o histórico do grupo selecionado é buscado de novo.
  if (epochMudou && medida.pronto) {
    carregarHistorico(false);
    areaGrupos.aoMudarEpoch();
  }
}

// ---------- quadro: tudo que depende dos dados, num requestAnimationFrame ----------
function agendar() {
  if (quadroPendente) return;
  quadroPendente = true;
  requestAnimationFrame(quadro);
}

function quadro() {
  quadroPendente = false;
  const veioDelta = sujo.delta;
  if (sujo.dados) {
    grupos = agregarGrupos(dados.itens);
    cont = contagens(dados.itens.map((i) => i.ramal));
    renderIndicadores();
    renderLegenda();
    renderRanking();
    renderOpcoesGrupo();
    $("faixa-ami").hidden = dados.amiConectado;
    agendarMapa();
  }
  if (sujo.dados || sujo.filtro) {
    filtrados = filtrar(dados.itens, filtros);
    lista.itens = filtrados;
    lista.desvioMs = dados.desvioMs;
    lista.render();
    $("contagem-lista").textContent = formatarNumero(filtrados.length) + " de " + formatarNumero(dados.itens.length) + " ramais";
    $("lista-vazia").hidden = filtrados.length > 0 || dados.itens.length === 0;
  }
  sujo.dados = sujo.filtro = sujo.delta = false;
  // Publica no gancho no quadro seguinte: o anterior já foi pintado.
  const c = cont;
  requestAnimationFrame(() => {
    const agora = performance.now();
    medida.contagens = c;
    medida.epoch = dados.epoch;
    medida.version = dados.version;
    if (veioDelta) medida.ultimoDeltaMs = agora;
    if (!medida.pronto) {
      medida.primeiraExibicaoMs = agora;
      medida.pronto = true;
      depoisDaPrimeiraExibicao();
    }
  });
}

function depoisDaPrimeiraExibicao() {
  carregarHistorico(true);
  clearInterval(timerHistorico);
  timerHistorico = setInterval(() => carregarHistorico(false), MIN);
}

// ---------- indicadores ----------
// Com o Asterisk fora não há leitura: traço, nunca 0 (0 diria "ninguém registrado").
const SEM_LEITURA = String.fromCharCode(0x2013);
function renderIndicadores() {
  const leitura = (n) => (dados.amiConectado ? formatarNumero(n) : SEM_LEITURA);
  $("ind-total").textContent = formatarNumero(cont.total);
  $("ind-registrados").textContent = leitura(cont.registered);
  $("ind-de-total").textContent = formatarNumero(cont.total);
  $("ind-nao-registrados").textContent = leitura(cont.unregistered);
  $("ind-inalcancaveis").textContent = leitura(cont.unreachable);
  $("ind-em-chamada").textContent = leitura(cont.in_call);
  renderVariacao();
}

let pontosUltimaHora = null;
function renderVariacao() {
  const el = $("ind-variacao");
  // Com o Asterisk fora todos ficam desconhecidos: a conta daria uma queda falsa.
  if (!dados.amiConectado) { el.dataset.sinal = "zero"; el.textContent = "sem leitura com o Asterisk fora"; return; }
  const v = variacaoUltimaHora(cont.registered, pontosUltimaHora);
  if (v == null) { el.dataset.sinal = "zero"; el.textContent = "sem dados da última hora"; return; }
  el.dataset.sinal = v > 0 ? "mais" : v < 0 ? "menos" : "zero";
  el.textContent = v === 0 ? "sem variação na última hora"
    : (v > 0 ? "+" : "−") + formatarNumero(Math.abs(v)) + " na última hora";
}

// ---------- legenda, que também é o filtro por estado ----------
const chips = new Map();
for (const e of ESTADOS) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "chip";
  b.dataset.estado = e.chave;
  b.setAttribute("aria-pressed", "false");
  const pino = document.createElement("span");
  pino.className = "pino";
  pino.setAttribute("aria-hidden", "true");
  const nome = document.createElement("span");
  nome.textContent = e.rotulo;
  const n = document.createElement("span");
  n.className = "chip-n";
  n.textContent = "0";
  b.append(pino, nome, n);
  b.addEventListener("click", () => {
    if (filtros.estados.has(e.chave)) filtros.estados.delete(e.chave); else filtros.estados.add(e.chave);
    b.setAttribute("aria-pressed", String(filtros.estados.has(e.chave)));
    lista.irAoTopo();
    sujo.filtro = true;
    agendar();
  });
  chips.set(e.chave, n);
  $("legenda").append(b);
}

function renderLegenda() {
  const base = filtros.grupo ? grupos.find((g) => g.nome === filtros.grupo) : null;
  const porEstado = {};
  for (const e of ESTADOS) porEstado[e.chave] = 0;
  if (base) Object.assign(porEstado, base.porEstado);
  else for (const g of grupos) for (const e of ESTADOS) porEstado[e.chave] += g.porEstado[e.chave];
  for (const [chave, el] of chips) el.textContent = formatarNumero(porEstado[chave]);
}

// ---------- ranking ----------
let chaveRanking = "";
function renderRanking() {
  const top = ranking(grupos);
  const chave = JSON.stringify(top) + "|" + filtros.grupo;
  if (chave === chaveRanking) return;
  chaveRanking = chave;
  const ol = $("ranking");
  ol.replaceChildren();
  const maior = top.length ? top[0].quantidade : 1;
  for (const g of top) {
    const li = document.createElement("li");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "rank";
    if (g.nome === filtros.grupo) b.setAttribute("aria-current", "true");
    const nome = document.createElement("span");
    nome.className = "rank-nome";
    nome.textContent = g.nome;
    const barra = document.createElement("span");
    barra.className = "rank-barra";
    barra.setAttribute("aria-hidden", "true");
    const fill = document.createElement("span");
    fill.style.transform = "scaleX(" + (g.quantidade / maior).toFixed(3) + ")";
    barra.append(fill);
    const n = document.createElement("span");
    n.className = "rank-n";
    const q = document.createElement("b");
    q.textContent = formatarNumero(g.quantidade);
    n.append(q, " de " + formatarNumero(g.total));
    b.append(nome, barra, n);
    b.addEventListener("click", () => selecionarGrupo(g.nome));
    li.append(b);
    ol.append(li);
  }
  $("ranking-vazio").hidden = top.length > 0;
}

// ---------- seleção de grupo ----------
let chaveOpcoes = "";
function renderOpcoesGrupo() {
  const nomes = grupos.map((g) => g.nome);
  const chave = nomes.join("\n");
  if (chave === chaveOpcoes) return;
  chaveOpcoes = chave;
  const sel = $("filtro-grupo");
  const todos = sel.options[0];
  sel.replaceChildren(todos);
  for (const nome of nomes) {
    const o = document.createElement("option");
    o.value = nome;
    o.textContent = nome;
    sel.append(o);
  }
  if (filtros.grupo && !nomes.includes(filtros.grupo)) selecionarGrupo("");
  else sel.value = filtros.grupo;
}

function selecionarGrupo(nome) {
  if (filtros.grupo === nome) return;
  filtros.grupo = nome;
  $("filtro-grupo").value = nome;
  $("grafico-grupo").textContent = nome ? "do grupo " + nome : "de todos os grupos";
  lista.irAoTopo();
  sujo.dados = true; // legenda e ranking marcam o grupo
  agendar();
  carregarHistorico(true);
  if (matchMedia("(max-width: 1023px)").matches) {
    $("lista").scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }
}

$("filtro-grupo").addEventListener("change", (e) => selecionarGrupo(e.target.value));

let timerBusca = null;
$("filtro-busca").addEventListener("input", (e) => {
  clearTimeout(timerBusca);
  timerBusca = setTimeout(() => {
    filtros.busca = e.target.value;
    lista.irAoTopo();
    sujo.filtro = true;
    agendar();
  }, 120);
});

// ---------- mapa (no máximo 4 redesenhos por segundo) ----------
const mapa = $("mapa");
const mapaCtx = mapa.getContext("2d");
let layoutMapa = { blocos: [], passo: 3, lado: 2, altura: 0 };
let ultimoMapa = -1e9;
let timerMapa = null;
let sobreMapa = null;

function agendarMapa() {
  if (timerMapa) return;
  const espera = Math.max(0, 250 - (performance.now() - ultimoMapa));
  timerMapa = setTimeout(() => {
    requestAnimationFrame(() => { timerMapa = null; desenharMapaAgora(); });
  }, espera);
}

function desenharMapaAgora() {
  ultimoMapa = performance.now();
  const caixa = $("mapa-caixa");
  const largura = Math.max(50, caixa.clientWidth);
  // Alvo: perto de 45% da altura da janela; com muitos grupos o mapa cresce e a coluna rola.
  // Janela baixa (área do Issabel, ~700 px): mapa menor para o ranking e o histórico subirem.
  const alvo = Math.max(180, Math.round(window.innerHeight * (window.innerHeight < 800 ? 0.36 : 0.45)));
  layoutMapa = calcularLayout(grupos, largura - 8, alvo - 10);
  const altura = layoutMapa.altura + 10;
  const dpr = window.devicePixelRatio || 1;
  if (mapa.width !== Math.round(largura * dpr) || mapa.height !== Math.round(altura * dpr)) {
    mapa.width = Math.round(largura * dpr);
    mapa.height = Math.round(altura * dpr);
    mapa.style.height = altura + "px";
  }
  mapaCtx.setTransform(dpr, 0, 0, dpr, 4 * dpr, 5 * dpr);
  desenharMapa(mapaCtx, layoutMapa, grupos, tema.coresEstado, {
    selecionado: filtros.grupo, sobre: sobreMapa,
    corTexto: tema.texto, corTextoFraco: tema.textoFraco, corSelecao: tema.acento, fonte: tema.fonte,
  });
  mapa.setAttribute("aria-label", "Mapa de " + formatarNumero(cont.total) + " ramais em " + grupos.length + " grupos");
}

function pontoMapa(e) {
  const r = mapa.getBoundingClientRect();
  return { x: e.clientX - r.left - 4, y: e.clientY - r.top - 5 };
}

mapa.addEventListener("pointermove", (e) => {
  const p = pontoMapa(e);
  const b = acharBloco(layoutMapa, p.x, p.y);
  const nome = b ? b.nome : null;
  mapa.classList.toggle("clicavel", !!b);
  if (nome !== sobreMapa) { sobreMapa = nome; agendarMapa(); }
  const dica = $("mapa-dica");
  if (!b) { dica.hidden = true; return; }
  const g = grupos.find((x) => x.nome === nome);
  if (!g) { dica.hidden = true; return; }
  if (dica.dataset.grupo !== nome || dica.dataset.versao !== String(dados.version)) {
    dica.dataset.grupo = nome;
    dica.dataset.versao = String(dados.version);
    dica.replaceChildren();
    const t = document.createElement("strong");
    t.textContent = g.nome;
    const total = document.createElement("span");
    total.className = "dica-sub";
    total.textContent = formatarNumero(g.total) + " ramais";
    dica.append(t, total);
    for (const est of ESTADOS) {
      const n = g.porEstado[est.chave];
      if (!n) continue;
      const linha = document.createElement("span");
      linha.className = "dica-linha";
      linha.dataset.estado = est.chave;
      const pino = document.createElement("i");
      pino.className = "pino";
      const rot = document.createElement("span");
      rot.textContent = ROTULO[est.chave];
      const v = document.createElement("b");
      v.textContent = formatarNumero(n);
      linha.append(pino, rot, v);
      dica.append(linha);
    }
  }
  dica.hidden = false;
  const caixa = $("mapa-caixa");
  const larg = dica.offsetWidth, alt = dica.offsetHeight;
  const r = caixa.getBoundingClientRect();
  const cx = e.clientX - r.left, cy = e.clientY - r.top;
  const x = cx + 16 + larg > caixa.clientWidth ? cx - 16 - larg : cx + 16;
  const y = Math.min(cy + 12, caixa.clientHeight - alt - 4);
  dica.style.transform = "translate(" + Math.round(Math.max(0, x)) + "px," + Math.round(Math.max(0, y)) + "px)";
});
mapa.addEventListener("pointerleave", () => {
  $("mapa-dica").hidden = true;
  mapa.classList.remove("clicavel");
  if (sobreMapa) { sobreMapa = null; agendarMapa(); }
});
mapa.addEventListener("click", (e) => {
  const p = pontoMapa(e);
  const b = acharBloco(layoutMapa, p.x, p.y);
  if (b) selecionarGrupo(b.nome === filtros.grupo ? "" : b.nome);
});

// ---------- histórico ----------
let periodo = "1h";
let pedidoHistorico = 0;
async function carregarHistorico(mostrarCarregando) {
  if (!ativo) return;
  const meu = ++pedidoHistorico;
  const estado = $("grafico-estado");
  const texto = $("grafico-estado-texto");
  const repetir = $("grafico-repetir");
  if (mostrarCarregando) {
    estado.hidden = false;
    texto.textContent = "Carregando histórico...";
    repetir.hidden = true;
  }
  const ate = new Date();
  const de = new Date(ate.getTime() - PERIODOS[periodo]);
  const pedidos = [api.historico(de, ate, filtros.grupo)];
  // A variação da última hora é sempre do total da instalação (C9).
  const precisaHora = periodo !== "1h" || filtros.grupo;
  if (precisaHora) pedidos.push(api.historico(new Date(ate.getTime() - PERIODOS["1h"]), ate, ""));
  try {
    const [h, hora] = await Promise.all(pedidos);
    if (meu !== pedidoHistorico) return;
    pontosUltimaHora = (precisaHora ? hora : h).points;
    renderVariacao();
    const temDado = h.points.some((p) => !p.gap);
    estado.hidden = temDado;
    if (!temDado) texto.textContent = "Sem dados no período. O histórico grava um ponto por minuto.";
    grafico.definir(h.points, periodo);
  } catch (e) {
    if (meu !== pedidoHistorico) return;
    if (e.status === 401) { semSessao(); return; }
    estado.hidden = false;
    texto.textContent = "Não foi possível carregar o histórico.";
    repetir.hidden = false;
  }
}

$("grafico-repetir").addEventListener("click", () => carregarHistorico(true));
for (const b of document.querySelectorAll(".seg[data-periodo]")) {
  b.addEventListener("click", () => {
    periodo = b.dataset.periodo;
    for (const o of document.querySelectorAll(".seg[data-periodo]")) o.setAttribute("aria-pressed", String(o === b));
    carregarHistorico(true);
  });
}

// ---------- tela cheia e grupos ----------
$("botao-cheia").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen();
  else document.documentElement.requestFullscreen().catch(() => { $("botao-cheia").hidden = true; });
});
document.addEventListener("fullscreenchange", () => {
  const cheia = !!document.fullscreenElement;
  $("icone-cheia").setAttribute("href", cheia ? "#i-sair-cheia" : "#i-cheia");
  $("texto-cheia").textContent = cheia ? "Sair da tela cheia" : "Tela cheia";
});
$("botao-grupos").addEventListener("click", () => areaGrupos.abrir());

// ---------- tamanho ----------
new ResizeObserver(() => { if (ativo) agendarMapa(); }).observe($("mapa-caixa"));
new ResizeObserver(() => { if (ativo) grafico.desenhar(); }).observe($("grafico-caixa"));
new ResizeObserver(() => { if (ativo) lista.agendar(); }).observe($("lista"));

// Histórico fora da área visível (janela baixa, coluna rolável ou telas empilhadas): aviso fixo no pé da coluna.
// Raiz nula: o observador já desconta o recorte da coluna que rola.
const avisoHistorico = $("aviso-historico");
new IntersectionObserver((entradas) => {
  for (const e of entradas) avisoHistorico.hidden = e.intersectionRatio >= 0.25;
}, { threshold: [0, 0.25] }).observe($("bloco-historico"));
avisoHistorico.addEventListener("click", () => {
  const suave = !matchMedia("(prefers-reduced-motion: reduce)").matches;
  $("bloco-historico").scrollIntoView({ behavior: suave ? "smooth" : "auto", block: "nearest" });
});

function lerTema() {
  const css = getComputedStyle(document.documentElement);
  const v = (n) => css.getPropertyValue(n).trim();
  const coresEstado = {};
  const cores = {};
  for (const e of ESTADOS) { coresEstado[e.chave] = v(e.cor); cores[e.cor] = v(e.cor); }
  return {
    coresEstado, cores, texto: v("--tinta-1"), textoFraco: v("--tinta-3"), linha: v("--linha"),
    fundo: v("--sup-1"), acento: v("--acento"), fonte: v("--fonte-texto"), fonteNum: v("--fonte-display"),
  };
}

// ---------- início ----------
if (!EMERGENCIA && window.top === window.self) {
  mensagem("Acesse pelo menu PBX do Issabel.");
} else {
  entrar();
}
