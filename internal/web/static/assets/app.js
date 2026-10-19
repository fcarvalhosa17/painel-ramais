// Painel de Ramais: entrada (C4), ligação dos módulos e gancho de medição (C10).
// Layout e valores visuais: docs/design/painel-ramais-design.dc.html (desvios em docs/design/desvios.md).

import {
  ESTADOS, SETA, DIRECAO, contagens, agregarGrupos, filtrar, formatarNumero, formatarPct, normalizar,
  textoFaixas, direcoes, chamadaPrincipal, duracaoMs, formatarCronometro, outroLado, gruposDe, textoProblemas,
} from "./estado.js";
import { Dados, atrasoReconexao } from "./dados.js";
import { api, CanalAoVivo } from "./conexao.js";
import { Lista } from "./lista.js";
import { ListaLinhas } from "./linhas.js";
import { iniciarChamadas } from "./chamadas.js";
import { desenharHistorico, rotulosEixo } from "./grafico.js";
import { iniciarGrupos } from "./grupos.js";
import { iniciarEdicao } from "./edicao.js";
import { iniciarInstabilidade } from "./instabilidade.js";
import { iniciarRegistros } from "./registros.js";

const $ = (id) => document.getElementById(id);
const EMERGENCIA = location.pathname.startsWith("/painel-ramais/emergencia");
const TOKEN = /^#t=([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$/;
const SESSAO_ENCERRADA = "Sessão encerrada. Abra de novo pelo menu PBX do Issabel.";
const MIN = 60e3;
const DIA = 24 * 3600e3;
const TODOS = "Todos os ramais"; // escopo "" (filtros.grupo vazio): não é grupo, nunca entra no catálogo
const CHAMADA_LONGA_MS = 20 * MIN; // tempo em laranja acima de 20 min
const TRACO = String.fromCharCode(0x2014); // sem leitura: "—" como no design (decorate), nunca 0
const CHAVE_MODO = "painel-ramais.modo";
const CHAVE_ABA = "painel-ramais.aba";
// Abas da tela (desvios D19 e D28), na ordem dos botões da #abas; cada uma tem o painel #aba-<nome>.
const ABAS = ["ramais", "chamadas", "registros"];

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
// desligados: estados que o usuário apagou nos chips (vazio = todos, como o "Todos" do design).
const filtros = { busca: "", desligados: new Set(), grupo: "" };
let grupos = []; // agregado por grupo (só grupos com ramal)
let porNome = new Map();
let nomesGrupos = []; // catálogo (inclui grupos vazios) mais os grupos que têm ramal (o "Sem grupo")
let totais = {};
let filtrados = [];
let ordemGrupos = "problemas";
let ultimaMudancaMs = null;
let cont = medida.contagens;
const sujo = { dados: false, filtro: false, delta: false };
let quadroPendente = false;
let ativo = false;
let timerHistorico = null;
let timerRelogio = null;

const lista = new Lista($("lista"), $("lista-altura"));
const areaGrupos = iniciarGrupos({ dados, aoSessaoEncerrada: () => semSessao() });
const edicao = iniciarEdicao({ dados, lista, grupoAberto: () => filtros.grupo, aoSessaoEncerrada: () => semSessao() });
const registros = iniciarRegistros({ dados, relogio: () => Date.now() + dados.desvioMs, aoSessaoEncerrada: () => semSessao() });
const chamadas = iniciarChamadas({ dados, relogio: () => Date.now() + dados.desvioMs, aoSessaoEncerrada: () => semSessao() });
const instabilidade = iniciarInstabilidade({
  // Clique no ramal do bloco: aba Registros com 24 h, o grupo atual e a linha do tempo dele.
  aoAbrir(numero) {
    registros.abrirRamal(numero, filtros.grupo);
    trocarAba("registros");
    $("rl-fechar").focus();
  },
  relogio: () => Date.now() + dados.desvioMs,
  aoSessaoEncerrada: () => semSessao(),
});

const canal = new CanalAoVivo({
  aoDelta(msg) {
    const r = dados.aplicarDelta(msg);
    if (r === "lacuna") { canal.reiniciar(); return; }
    if (r === "aplicado") { ultimaMudancaMs = Date.now(); sujo.dados = true; sujo.delta = true; agendar(); }
  },
  aoPrecisarSnapshot: () => carregarSnapshot(),
  aoReconectar: () => canal.conectar(dados.epoch, dados.version),
  aoSessaoEncerrada: () => semSessao(),
  aoStatus: (s, atraso) => status(s, atraso),
});

function status(s, atraso) {
  const textos = { conectando: "CONECTANDO", ao_vivo: "AO VIVO", atualizando: "ATUALIZANDO", reconectando: "RECONECTANDO" };
  $("status-conexao").dataset.status = s;
  $("status-texto").textContent = textos[s] + (s === "reconectando" && atraso ? " EM " + Math.ceil(atraso / 1000) + " S" : "");
}

function iniciarPainel(sessao) {
  ativo = true;
  mostrar("painel");
  $("botao-grupos").hidden = !sessao.admin;
  $("botao-sair").hidden = !EMERGENCIA;
  $("botao-cheia").hidden = !document.fullscreenEnabled;
  edicao.definirAdmin(!!sessao.admin);
  chamadas.definirAdmin(!!sessao.admin);
  canal.parado = false;
  status("conectando");
  carregarSnapshot();
  // Aba guardada no navegador (valor externo: só vale se for uma das ABAS). Liga o bloco de instabilidade
  // (aba Ramais) ou a atualização da aba Registros; pararPainel desliga os dois.
  let guardada = "ramais";
  try { guardada = localStorage.getItem(CHAVE_ABA) || "ramais"; } catch { /* sem armazenamento */ }
  trocarAba(guardada);
  clearInterval(timerRelogio);
  timerRelogio = setInterval(tique, 1000);
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
  edicao.parar();
  instabilidade.parar();
  registros.parar();
  chamadas.parar();
  for (const d of document.querySelectorAll("dialog[open]")) d.close();
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
  ultimaMudancaMs = Date.now();
  sujo.dados = true;
  agendar();
  canal.conectar(dados.epoch, dados.version);
  // Grupos podem ter mudado com o epoch: o histórico do grupo selecionado é buscado de novo.
  if (epochMudou && medida.pronto) carregarHistorico(false);
}

// Tique de 1 s: cronômetros dos cards, chamadas ativas, "Atualizado há" e o detalhe.
function tique() {
  lista.tique();
  renderChamadas();
  renderInfo();
  edicao.tique();
  chamadas.tique();
  registros.tique();
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
    porNome = new Map(grupos.map((g) => [g.nome, g]));
    nomesGrupos = [...new Set([...dados.grupos.map((g) => g.name), ...grupos.map((g) => g.nome)])];
    // A tela abre em "Todos os ramais" (filtros.grupo ""); se o grupo selecionado some (excluído, renomeado
    // ou fora do catálogo), volta para ele. Roda antes do filtro deste quadro: lista, chips e histórico já saem certos.
    if (filtros.grupo && !nomesGrupos.includes(filtros.grupo)) selecionarGrupo("", false);
    // Totais da instalação contam cada ramal uma vez (somar os grupos contaria o ramal em N grupos N vezes).
    totais = {};
    for (const e of ESTADOS) totais[e.chave] = 0;
    for (const it of dados.itens) totais[it.estado]++;
    cont = contagens(dados.itens.map((i) => i.ramal));
    renderIndicadores();
    renderGrupos();
    renderChamadas();
    renderInfo();
    $("faixa-ami").hidden = dados.amiConectado;
    if (dados.catalogoMudou) {
      dados.catalogoMudou = false;
      areaGrupos.aoMudarCatalogo();
      edicao.aoMudarCatalogo();
      registros.aoMudarCatalogo();
      chamadas.aoMudarCatalogo();
    }
    areaGrupos.aoMudarDados();
    edicao.aoMudarDados();
    chamadas.aoMudarDados();
  }
  if (sujo.dados || sujo.filtro) renderCentro();
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

function renderInfo() {
  const partes = [];
  if (ultimaMudancaMs !== null) {
    const s = Math.max(0, Math.round((Date.now() - ultimaMudancaMs) / 1000));
    partes.push("Atualizado há " + (s < 60 ? s + " s" : Math.floor(s / 60) + " min"));
  }
  if (dados.tecnologia) partes.push(String(dados.tecnologia).toUpperCase());
  if (location.hostname) partes.push(location.hostname);
  $("topo-info").textContent = partes.join(" · ");
}

// ---------- indicadores ----------
function renderIndicadores() {
  const ami = dados.amiConectado;
  const leitura = (n) => (ami ? formatarNumero(n) : TRACO);
  const total = dados.itens.length;
  // Como o design: registrado é todo ramal com registro, inclusive o inalcançável (registrado sem resposta).
  const reg = total - totais.nao_registrado - totais.desconhecido;
  $("ind-total").textContent = formatarNumero(total);
  $("ind-grupos").textContent = "em " + formatarNumero(nomesGrupos.length) + " grupos";
  $("ind-reg").textContent = leitura(reg);
  $("ind-reg-pct").textContent = ami ? formatarPct(reg, total) : "";
  $("ind-reg-barra").style.width = ami && total ? ((reg / total) * 100).toFixed(2) + "%" : "0%";
  $("ind-nao-reg").textContent = leitura(totais.nao_registrado);
  let pior = null;
  for (const g of grupos) if (g.porEstado.nao_registrado > 0 && (!pior || g.porEstado.nao_registrado > pior.porEstado.nao_registrado)) pior = g;
  $("ind-pior").textContent = "pior grupo: " + (!ami ? TRACO : pior ? pior.nome + " (" + formatarNumero(pior.porEstado.nao_registrado) + ")" : "nenhum");
  $("ind-inalc").textContent = leitura(totais.inalcancavel);
  $("ind-chamadas").textContent = leitura(totais.em_conversa + totais.em_espera);
  const d = direcoes(dados.itens);
  $("ind-direcoes").textContent = SETA.inbound + " " + leitura(d.inbound) + "  " + SETA.outbound + " " + leitura(d.outbound) + "  " + SETA.internal + " " + leitura(d.internal);
  $("ind-tocando").textContent = leitura(totais.tocando);
  $("ind-espera").textContent = leitura(totais.em_espera);
}

// ---------- coluna Grupos: barra empilhada, % registrados, "X não registrados" e "Y inalcançáveis" (item 11) ----------
const linhasGrupo = new Map(); // nome ("" = Todos os ramais) -> nós da linha
let chaveOrdemGrupos = "";

function criarLinhaGrupo(nome) {
  const span = (classe, pai) => { const s = document.createElement("span"); s.className = classe; pai.append(s); return s; };
  const b = document.createElement("button");
  b.type = "button";
  b.className = "grupo";
  const l1 = span("grupo-linha", b);
  const titulo = span("grupo-nome", l1);
  const n = span("grupo-n", l1);
  const barra = span("grupo-barra", b);
  barra.setAttribute("aria-hidden", "true");
  const segs = ["s-livre", "s-chamada", "s-tocando", "s-inalc", "s-nao-reg"].map((c) => span(c, barra));
  const pe = span("grupo-pe", b);
  const reg = span("", pe);
  const alerta = span("", pe);
  titulo.textContent = nome || TODOS;
  b.addEventListener("click", () => { edicao.fecharDetalhe(); selecionarGrupo(nome); });
  return { b, n, segs, reg, alerta };
}

const ZEROS = Object.fromEntries(ESTADOS.map((e) => [e.chave, 0]));
// g ausente: grupo do catálogo sem nenhum ramal agora.
function estatGrupo(nome, g) {
  const p = g ? g.porEstado : ZEROS;
  const n = g ? g.total : 0;
  const ruins = (p.nao_registrado || 0) + (p.inalcancavel || 0);
  return { nome, n, p, ruins, problema: n ? ruins / n : 0, reg: n - (p.nao_registrado || 0) - (p.desconhecido || 0) };
}

// Grupos na ordenação ativa (Nome ou Mais problemas). "Todos os ramais" fica fora: não é grupo.
function gruposOrdenados() {
  const linhas = nomesGrupos.map((nome) => estatGrupo(nome, porNome.get(nome)));
  if (ordemGrupos === "nome") linhas.sort((a, b) => a.nome.localeCompare(b.nome, "pt-BR"));
  else linhas.sort((a, b) => b.problema - a.problema || a.nome.localeCompare(b.nome, "pt-BR"));
  return linhas;
}

function renderGrupos() {
  // "Todos os ramais" (chave "") sempre primeiro, fora da ordenação; soma todos os ramais, inclusive os sem grupo.
  const linhas = [estatGrupo("", { total: dados.itens.length, porEstado: totais }), ...gruposOrdenados()];
  const vivos = new Set();
  for (const e of linhas) {
    let ui = linhasGrupo.get(e.nome);
    if (!ui) { ui = criarLinhaGrupo(e.nome); linhasGrupo.set(e.nome, ui); }
    vivos.add(e.nome);
    const w = (v) => (e.n ? ((v / e.n) * 100).toFixed(2) : "0") + "%";
    const larguras = [e.p.livre, e.p.em_conversa + e.p.em_espera, e.p.tocando, e.p.inalcancavel, e.p.nao_registrado + e.p.desconhecido];
    ui.segs.forEach((s, i) => { s.style.width = w(larguras[i] || 0); });
    ui.n.textContent = formatarNumero(e.n);
    ui.reg.textContent = formatarPct(e.reg, e.n) + " registrados";
    ui.alerta.textContent = textoProblemas(e.p); // destaque e ordem continuam pela soma (ruins)
    ui.alerta.classList.toggle("grupo-alerta-forte", e.problema > 0.25);
    ui.b.setAttribute("aria-pressed", String(e.nome === filtros.grupo));
  }
  for (const nome of [...linhasGrupo.keys()]) if (!vivos.has(nome)) { linhasGrupo.get(nome).b.remove(); linhasGrupo.delete(nome); }
  // Só reordena o DOM quando a ordem muda (mover o nó tira o foco do botão).
  const chave = linhas.map((e) => e.nome).join("\n");
  if (chave !== chaveOrdemGrupos) {
    chaveOrdemGrupos = chave;
    $("grupos-lista").replaceChildren(...linhas.map((e) => linhasGrupo.get(e.nome).b));
  }
}

for (const b of document.querySelectorAll(".ordem-btn")) {
  b.addEventListener("click", () => {
    ordemGrupos = b.dataset.ordem;
    for (const o of document.querySelectorAll(".ordem-btn")) o.setAttribute("aria-pressed", String(o === b));
    renderGrupos();
  });
}

function selecionarGrupo(nome, rolar = true) {
  if (filtros.grupo === nome) return;
  filtros.grupo = nome;
  $("hist-grupo").textContent = nome || TODOS;
  instabilidade.definirGrupo(nome);
  lista.irAoTopo();
  $("chamadas").scrollTop = 0;
  sujo.dados = true; // grupos, chamadas ativas e centro marcam o grupo
  agendar();
  carregarHistorico(true);
  // Colunas empilhadas: leva a lista para a vista se ela ficou abaixo da dobra.
  if (rolar && $("lista").getBoundingClientRect().top > window.innerHeight) {
    $("lista").scrollIntoView({ behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }
}

// ---------- coluna central ----------
const chips = new Map();
for (const e of ESTADOS) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "chip";
  b.dataset.estado = e.chave;
  b.setAttribute("aria-pressed", "true");
  const cor = document.createElement("span");
  cor.className = "chip-cor";
  cor.setAttribute("aria-hidden", "true");
  const n = document.createElement("span");
  n.className = "chip-n";
  n.textContent = "0";
  b.append(cor, e.rotulo + " ", n);
  b.addEventListener("click", () => {
    if (filtros.desligados.has(e.chave)) filtros.desligados.delete(e.chave); else filtros.desligados.add(e.chave);
    aplicarFiltroEstados();
  });
  chips.set(e.chave, { b, n });
  $("legenda").append(b);
}
$("chip-todos").addEventListener("click", () => { filtros.desligados.clear(); aplicarFiltroEstados(); });

function aplicarFiltroEstados() {
  for (const [chave, c] of chips) c.b.setAttribute("aria-pressed", String(!filtros.desligados.has(chave)));
  $("chip-todos").setAttribute("aria-pressed", String(filtros.desligados.size === 0));
  $("legenda").classList.toggle("com-filtro", filtros.desligados.size > 0);
  lista.irAoTopo();
  sujo.filtro = true;
  agendar();
}

function renderCentro() {
  // Com busca, o design procura em todos os grupos e ignora o grupo selecionado.
  const fonte = filtrar(dados.itens, { busca: filtros.busca, grupo: normalizar(filtros.busca) ? "" : filtros.grupo, estados: null });
  const porEstado = {};
  for (const e of ESTADOS) porEstado[e.chave] = 0;
  for (const it of fonte) porEstado[it.estado]++;
  filtrados = filtros.desligados.size ? fonte.filter((it) => !filtros.desligados.has(it.estado)) : fonte;
  lista.itens = filtrados;
  lista.desvioMs = dados.desvioMs;
  lista.render();
  for (const [chave, c] of chips) c.n.textContent = formatarNumero(porEstado[chave]);
  $("chip-todos-n").textContent = formatarNumero(fonte.length);

  const g = filtros.grupo;
  const total = g ? (porNome.get(g) || { total: 0 }).total : dados.itens.length;
  const cat = g ? dados.grupos.find((x) => x.name === g) : null;
  const faixas = cat && cat.ranges && cat.ranges.length ? "faixa " + textoFaixas(cat.ranges, String.fromCharCode(0x2013)) + " · " : "";
  $("centro-nome").textContent = g || TODOS;
  $("centro-info").textContent = faixas + formatarNumero(total) + " ramais";
  $("contagem-lista").textContent = "Mostrando " + formatarNumero(filtrados.length) + " de " + formatarNumero(fonte.length) + " ramais · a lista carrega conforme a rolagem";

  // Vazio por filtro, ou servidor sem ramal nenhum (estado pronto, não "carregando").
  // Antes do primeiro snapshot (epoch nulo) não há o que afirmar.
  const vazio = $("lista-vazia");
  vazio.hidden = dados.epoch === null || filtrados.length > 0;
  vazio.textContent = dados.itens.length > 0 ? "Nenhum ramal com esses filtros."
    : dados.amiConectado ? "Nenhum ramal cadastrado." : "Aguardando a conexão com o Asterisk.";
  edicao.aoFiltrar(filtrados);
}

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

// Densidade: Compacto (72 px) ou Cartões (116 px). A escolha fica no navegador de quem vê.
function aplicarModo(modo) {
  lista.definirModo(modo);
  for (const b of document.querySelectorAll(".seg[data-modo]")) b.setAttribute("aria-pressed", String(b.dataset.modo === modo));
}
for (const b of document.querySelectorAll(".seg[data-modo]")) {
  b.addEventListener("click", () => {
    aplicarModo(b.dataset.modo);
    try { localStorage.setItem(CHAVE_MODO, b.dataset.modo); } catch { /* sem armazenamento: vale só nesta visita */ }
  });
}
try { if (localStorage.getItem(CHAVE_MODO) === "cartoes") aplicarModo("cartoes"); } catch { /* idem */ }

// ---------- abas (desvios D19): Ramais e Registros; Densidade só vale em Ramais ----------
let aba = "ramais";
const botoesAba = [...document.querySelectorAll("#abas [role=\"tab\"]")];
function trocarAba(nova, focar = false) {
  aba = ABAS.includes(nova) ? nova : "ramais";
  for (const b of botoesAba) {
    const sel = b.dataset.aba === aba;
    b.setAttribute("aria-selected", String(sel));
    b.tabIndex = sel ? 0 : -1;
    if (sel && focar) b.focus();
  }
  for (const nome of ABAS) $("aba-" + nome).hidden = aba !== nome;
  $("densidade").hidden = aba !== "ramais";
  try { localStorage.setItem(CHAVE_ABA, aba); } catch { /* sem armazenamento: vale só nesta visita */ }
  // Cada aba só busca dados (e a de Chamadas só pinta e conta o tempo) enquanto está visível.
  instabilidade.mostrar(ativo && aba === "ramais");
  registros.mostrar(ativo && aba === "registros");
  chamadas.mostrar(ativo && aba === "chamadas");
  if (aba === "ramais") { lista.agendar(); pintarChamadas(); }
}
for (const b of botoesAba) {
  b.addEventListener("click", () => trocarAba(b.dataset.aba));
  // Setas trocam de aba (padrão de tablist com ativação automática); Home e End vão às pontas.
  b.addEventListener("keydown", (e) => {
    const i = botoesAba.indexOf(b);
    const alvo = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: botoesAba.length - 1 }[e.key];
    if (alvo === undefined) return;
    e.preventDefault();
    trocarAba(botoesAba[(alvo + botoesAba.length) % botoesAba.length].dataset.aba, true);
  });
}

// ---------- chamadas ativas do escopo selecionado (grupo ou todos os ramais), mais longas primeiro ----------
// Rolagem virtual e teclado pelo ListaLinhas (linhas.js, D18 e D34): só as linhas da janela visível existem no DOM,
// roving tabindex, setas, Home e End; a linha focada é achada pelo número do ramal e não perde o foco no tique de 1 s.
// Cada linha é um item de lista com o botão dentro (o clique abre o detalhe do ramal).
function criarLinhaChamada() {
  const item = document.createElement("div");
  item.className = "chamada-item";
  item.setAttribute("role", "listitem");
  const b = document.createElement("button");
  b.type = "button";
  b.className = "chamada";
  const seta = document.createElement("span");
  seta.className = "chamada-seta";
  seta.setAttribute("aria-hidden", "true");
  const meio = document.createElement("span");
  meio.className = "chamada-meio";
  const l1 = document.createElement("span");
  l1.className = "chamada-l1";
  const num = document.createElement("strong");
  const nome = document.createTextNode("");
  l1.append(num, nome);
  const l2 = document.createElement("span");
  l2.className = "chamada-l2";
  meio.append(l1, l2);
  const tempo = document.createElement("span");
  tempo.className = "chamada-tempo";
  b.append(seta, meio, tempo);
  item.append(b);
  return { no: item, foco: b, b, seta, num, nome, l2, tempo };
}

function preencherLinhaChamada(ui, a) {
  const r = a.it.ramal;
  const c = chamadaPrincipal(r.calls) || {};
  const espera = a.it.estado === "em_espera";
  ui.b.dataset.numero = r.number;
  if (espera) ui.b.dataset.espera = "1"; else delete ui.b.dataset.espera;
  ui.seta.textContent = SETA[c.direction] || SETA.internal;
  ui.num.textContent = r.number;
  ui.nome.data = " " + (r.name || "sem nome");
  ui.l2.textContent = (espera ? "Em espera" : DIRECAO[c.direction] || "Chamada") + " · " + outroLado(c);
  ui.tempo.textContent = formatarCronometro(a.ms);
  ui.tempo.classList.toggle("longa", a.ms > CHAMADA_LONGA_MS);
  ui.b.setAttribute("aria-label", "Ramal " + r.number + ", " + (r.name || "sem nome") + ", " + ui.l2.textContent + ", " + ui.tempo.textContent);
}

const listaChamadas = new ListaLinhas($("chamadas"), {
  criar: criarLinhaChamada,
  preencher: preencherLinhaChamada,
  chave: (a) => a.it.ramal.number,
});

function renderChamadas() {
  const agora = Date.now();
  const ativos = [];
  for (const it of dados.itens) {
    if (it.estado !== "em_conversa" && it.estado !== "em_espera") continue;
    if (filtros.grupo && !gruposDe(it.ramal).includes(filtros.grupo)) continue;
    ativos.push({ it, ms: duracaoMs(it.ramal.calls, agora, dados.desvioMs) ?? 0 });
  }
  ativos.sort((a, b) => b.ms - a.ms);
  $("chamadas-n").textContent = formatarNumero(ativos.length);
  $("chamadas-sub").textContent = (filtros.grupo || TODOS) + " · mais longas primeiro";
  $("chamadas").setAttribute("aria-label", "Chamadas ativas, " + formatarNumero(ativos.length) + (ativos.length === 1 ? " chamada" : " chamadas"));
  listaChamadas.itens = ativos;
  pintarChamadas();
  $("chamadas-vazio").hidden = ativos.length > 0;
}

function pintarChamadas() {
  listaChamadas.pintar();
}

$("chamadas").addEventListener("click", (e) => {
  const b = e.target.closest(".chamada");
  if (b && b.dataset.numero) edicao.abrirRamal(b.dataset.numero);
});

// ---------- últimas 24 h ----------
let pedidoHistorico = 0;
async function carregarHistorico(mostrarCarregando) {
  if (!ativo) return;
  const meu = ++pedidoHistorico;
  const estado = $("hist-estado");
  const texto = $("hist-estado-texto");
  const repetir = $("hist-repetir");
  if (mostrarCarregando) {
    estado.hidden = false;
    texto.textContent = "Carregando histórico...";
    repetir.hidden = true;
  }
  const ate = new Date();
  const de = new Date(ate.getTime() - DIA);
  try {
    const h = await api.historico(de, ate, filtros.grupo);
    if (meu !== pedidoHistorico) return;
    const pontos = h.points || [];
    const temDado = pontos.some((p) => !p.gap);
    estado.hidden = temDado;
    repetir.hidden = true;
    if (!temDado) texto.textContent = "Sem dados nas últimas 24 h. O histórico grava um ponto por minuto.";
    const pico = desenharHistorico($("hist-series"), pontos);
    $("hist-pico").textContent = "Em chamada (pico " + formatarNumero(pico) + ")";
    const rotulos = rotulosEixo(de.getTime());
    [...$("hist-eixo").children].forEach((s, i) => { s.textContent = rotulos[i]; });
  } catch (e) {
    if (meu !== pedidoHistorico) return;
    if (e.status === 401) { semSessao(); return; }
    estado.hidden = false;
    texto.textContent = "Não foi possível carregar o histórico.";
    repetir.hidden = false;
  }
}
$("hist-repetir").addEventListener("click", () => carregarHistorico(true));

// ---------- tela cheia e grupos ----------
$("botao-cheia").addEventListener("click", () => {
  if (document.fullscreenElement) document.exitFullscreen();
  else document.documentElement.requestFullscreen().catch(() => { $("botao-cheia").hidden = true; });
});
document.addEventListener("fullscreenchange", () => {
  const cheia = !!document.fullscreenElement;
  $("icone-cheia").setAttribute("href", cheia ? "#i-sair-cheia" : "#i-cheia");
  $("botao-cheia").setAttribute("aria-label", cheia ? "Sair da tela cheia" : "Tela cheia");
});
$("botao-grupos").addEventListener("click", () => areaGrupos.abrir());

new ResizeObserver(() => { if (ativo) lista.agendar(); }).observe($("lista"));

// ---------- início ----------
if (!EMERGENCIA && window.top === window.self) {
  mensagem("Acesse pelo menu PBX do Issabel.");
} else {
  entrar();
}
