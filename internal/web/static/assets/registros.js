// Aba Registros (CONTRATOS 4.7.2 a 4.7.5, desvios D21, D27 e D38 a D43).
// Tabela por cursor: uma chamada por vez, a próxima 5 s depois que a anterior terminou, só com a aba e o
// documento visíveis; full:true substitui a tabela, full:false substitui ou insere pelo número e tira a linha que
// deixou de passar na regra de lista. Os relógios de "Fora do ar há" andam no tique de 1 s, sem rede.
// Gráfico por hora (24 h) ou por dia (7 e 30 dias), linha do tempo do ramal e CSV montado no navegador.
// Texto do servidor só por textContent.

import { api } from "./conexao.js";
import { SEM_GRUPO, compararGrupos, formatarNumero, normalizar, dataHora, horaMinuto, avisoPausa } from "./estado.js";
import { janela } from "./lista.js";

const $ = (id) => document.getElementById(id);
const INTERVALO = 5e3;
const INTERVALO_SERIE = 60e3;
const ALTURA_LINHA = 36; // px, a mesma altura do .reg-linha no app.css
const TODOS = "Todos os ramais";
const AUSENTE = String.fromCharCode(0x2013); // dado ausente nesta aba (sem travessão longo)
const PERIODOS = ["24h", "7d", "30d"];
const NS = "http://www.w3.org/2000/svg";
const GRAF_L = 720, GRAF_A = 140, GRAF_TOPO = 4; // viewBox do gráfico; a base das barras fica em GRAF_A

export const ROTULO_OCORRENCIA = {
  lost_registration: "Perdeu registro",
  registered: "Registrou",
  became_unreachable: "Ficou inalcançável",
  reachable_again: "Voltou a ser alcançável",
};
export const ROTULO_SINAL = {
  shutdown: "Asterisk desligando",
  fully_booted: "Asterisk iniciado",
  ami_connected: "Painel reconectado ao Asterisk",
};

const dois = (n) => (n < 10 ? "0" : "") + n;
const inteiro = (v) => (Number.isFinite(v) ? Math.trunc(v) : 0);
const plural = (n, um, varios) => formatarNumero(n) + " " + (n === 1 ? um : varios);
const ms = (iso) => (iso ? Date.parse(iso) : NaN);

// Fim dos relógios (CONTRATOS 4.7): agora, ou a queda do AMI (state_unknown_since), quando o estado é desconhecido.
export const fimRelogio = (agoraMs, desconhecidoDesde) => {
  const t = ms(desconhecidoDesde);
  return Number.isNaN(t) ? agoraMs : Math.min(t, agoraMs);
};

// Tempo total fora do ar no período (CONTRATOS 4.7): down_s + (down_count_from ? fim - max(down_count_from, from) : 0).
export function tempoFora(row, fromMs, fimMs) {
  let s = inteiro(row.down_s);
  const aberto = ms(row.down_count_from);
  if (!Number.isNaN(aberto)) {
    const inicio = Number.isFinite(fromMs) ? Math.max(aberto, fromMs) : aberto;
    s += Math.max(0, (fimMs - inicio) / 1000);
  }
  return Math.floor(s);
}

// "Fora do ar há" em segundos: só para down_kind observed (os outros não têm relógio); null se não se aplica.
export function foraHaSegundos(row, fimMs) {
  const desde = ms(row.down_since);
  if (row.down_kind !== "observed" || Number.isNaN(desde)) return null;
  return Math.max(0, Math.floor((fimMs - desde) / 1000));
}

// Duração (D45): "X d HH h" a partir de 1 dia, "H h MM min" a partir de 1 hora, "M min SS s", "S s".
export function formatarDuracao(s) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60), seg = s % 60;
  if (d) return d + " d " + dois(h) + " h";
  if (h) return h + " h " + dois(m) + " min";
  if (m) return m + " min " + dois(seg) + " s";
  return seg + " s";
}

// Data e hora local "19/10/2026 13:42".
export function dataCompleta(iso) {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return "";
  return dois(d.getDate()) + "/" + dois(d.getMonth() + 1) + "/" + d.getFullYear() + " " + dois(d.getHours()) + ":" + dois(d.getMinutes());
}

// Data de "fora desde antes da instalação do painel" (decisão do usuário, rc.9): o "dados desde" do cabeçalho
// (since = reg_meta.since); sem ele, a hora em que o painel viu o ramal fora (down_since).
const dataInstalacao = (row, since) => since || row.down_since;

// Textos de "Fora do ar há" (CONTRATOS 4.7.2): { curto (célula), longo (title, aria-label e detalhe), vivo }.
// since: o "since" do cabeçalho da resposta (data da instalação, para before_install).
export function textoForaHa(row, fimMs, since) {
  const s = foraHaSegundos(row, fimMs);
  if (s !== null) { const t = formatarDuracao(s); return { curto: t, longo: "fora do ar há " + t, vivo: true }; }
  if (row.down_kind === "unknown_start" && row.down_since) {
    return { curto: "antes de " + dataHora(row.down_since), longo: "fora desde antes de " + dataCompleta(row.down_since), vivo: false };
  }
  if (row.down_kind === "before_install" && row.down_since) {
    return { curto: "antes da instalação", longo: "fora desde antes da instalação do painel (" + dataCompleta(dataInstalacao(row, since)) + ")", vivo: false };
  }
  return { curto: "", longo: "", vivo: false };
}

// "Último registro": exato "dd/mm/aaaa hh:mm"; inexato "antes de dd/mm/aaaa hh:mm"; nunca: vazio.
export function textoUltimoRegistro(row) {
  const t = dataCompleta(row.last_registered);
  return !t ? "" : row.last_registered_exact ? t : "antes de " + t;
}

// Regra de lista da 4.7: algum dos quatro contadores, down_s > 0 ou down_since não nulo.
export function passaLista(r) {
  return inteiro(r.lost_registration) + inteiro(r.became_unreachable) + inteiro(r.registered) + inteiro(r.reachable_again) > 0
    || inteiro(r.down_s) > 0 || !!r.down_since;
}

// Faixa da ordem padrão (D39): 0 fora com hora (observed, unknown_start), 1 antes da instalação, 2 o resto.
export const faixaFora = (r) => (r.down_since && (r.down_kind === "observed" || r.down_kind === "unknown_start") ? 0
  : r.down_since && r.down_kind === "before_install" ? 1 : 2);

export const ipPorta = (ip, porta) => (ip ? ip + (porta != null ? ":" + porta : "") : "");

// Filtro de grupo da tela (CONTRATOS 4.7.2): "" = todas as linhas, inclusive removidas;
// "Sem grupo" = groups igual a ["Sem grupo"]; outro = groups contém o grupo.
export function passaGrupo(row, grupo) {
  const gs = Array.isArray(row.groups) ? row.groups : [];
  if (!grupo) return true;
  if (grupo === SEM_GRUPO) return gs.length === 1 && gs[0] === SEM_GRUPO;
  return gs.includes(grupo);
}

// ---------- CSV (CONTRATOS 4.7.5) ----------
const BOM = String.fromCharCode(0xfeff);
const CABECALHO_CSV = [
  "Ramal", "Nome", "Grupos", "Perdeu registro", "Ficou inalcançável", "Registrou", "Voltou a ser alcançável",
  "Última queda", "Último IP", "Fora do ar há (s)", "Último registro", "Tempo total fora no período (s)",
  "Lacuna no período", "Removido",
];

// Célula de texto que começa com =, +, -, @, TAB ou CR ganha ' na frente (injeção de fórmula, antes das aspas);
// célula com ;, aspas, CR ou LF vai entre aspas, com as aspas dobradas. Números entram por inteiro().
export function celulaCsv(valor, texto = true) {
  let s = valor == null ? "" : String(valor);
  if (texto && /^[=+\-@\t\r]/.test(s)) s = "'" + s;
  return /[;"\r\n]/.test(s) ? "\"" + s.replace(/"/g, "\"\"") + "\"" : s;
}

function dataCsv(iso) {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return "";
  return d.getFullYear() + "-" + dois(d.getMonth() + 1) + "-" + dois(d.getDate()) + " "
    + dois(d.getHours()) + ":" + dois(d.getMinutes()) + ":" + dois(d.getSeconds());
}

// "Fora do ar há (s)": segundos (observed, na hora da exportação), texto (unknown_start, before_install) ou vazio.
function foraHaCsv(r, fimMs, since) {
  const s = foraHaSegundos(r, fimMs);
  if (s !== null) return celulaCsv(s, false);
  if (r.down_kind === "unknown_start" && r.down_since) return celulaCsv("antes de " + dataCsv(r.down_since));
  if (r.down_kind === "before_install" && r.down_since) return celulaCsv("antes da instalação do painel (" + dataCsv(dataInstalacao(r, since)) + ")");
  return "";
}

function ultimoRegistroCsv(r) {
  const t = dataCsv(r.last_registered);
  return celulaCsv(!t ? "" : r.last_registered_exact ? t : "antes de " + t);
}

// linhas: [{ r }] já filtradas e ordenadas como na tela; fromMs e fimMs: início do período e fim dos relógios
// (agora, ou state_unknown_since); since: data da instalação (cabeçalho). UTF-8 com BOM, ";" e CRLF.
export function gerarCsv(linhas, fromMs, fimMs, since) {
  const out = [CABECALHO_CSV.map((c) => celulaCsv(c)).join(";")];
  for (const { r } of linhas) {
    out.push([
      celulaCsv(r.number), celulaCsv(r.name || ""), celulaCsv((r.groups || []).join(", ")),
      celulaCsv(inteiro(r.lost_registration), false), celulaCsv(inteiro(r.became_unreachable), false),
      celulaCsv(inteiro(r.registered), false), celulaCsv(inteiro(r.reachable_again), false),
      celulaCsv(dataCsv(r.last_drop_at)), celulaCsv(ipPorta(r.last_ip, r.last_port)),
      foraHaCsv(r, fimMs, since), ultimoRegistroCsv(r), celulaCsv(tempoFora(r, fromMs, fimMs), false),
      celulaCsv(r.down_gap || r.down_kind === "unknown_start" ? "sim" : "não"), celulaCsv(r.removed ? "sim" : "não"),
    ].join(";"));
  }
  return BOM + out.join("\r\n") + "\r\n";
}

// registros-<period>-<AAAAMMDD-HHMM>.csv, hora local do navegador.
export function nomeArquivoCsv(periodo, d) {
  return "registros-" + periodo + "-" + d.getFullYear() + dois(d.getMonth() + 1) + dois(d.getDate())
    + "-" + dois(d.getHours()) + dois(d.getMinutes()) + ".csv";
}

function baixar(texto, nome) {
  const url = URL.createObjectURL(new Blob([texto], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = nome;
  a.hidden = true;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10e3);
}

// ---------- ordem da tabela ----------
const texto = (a, b) => a.localeCompare(b, "pt-BR", { numeric: true });
// desc: direção do primeiro clique (números do maior para o menor, texto de A a Z).
const ORDENS = {
  numero: { desc: false, f: (a, b) => texto(a.r.number, b.r.number) },
  nome: { desc: false, f: (a, b) => texto(a.r.name || "", b.r.name || "") },
  grupos: { desc: false, f: (a, b) => texto(a.grupos, b.grupos) },
  perdeu: { desc: true, f: (a, b) => a.perdeu - b.perdeu },
  inalc: { desc: true, f: (a, b) => a.inalc - b.inalc },
  registrou: { desc: true, f: (a, b) => a.registrou - b.registrou },
  queda: { desc: true, f: (a, b) => a.queda - b.queda },
  ip: { desc: false, f: (a, b) => texto(a.r.last_ip || "", b.r.last_ip || "") },
  ultreg: { desc: true, f: (a, b) => a.ultreg - b.ultreg },
  // Ordem padrão (CONTRATOS 4.7.2, D39): três faixas sempre nesta ordem; a direção vale só na faixa (a).
  foraha: { desc: false, faixas: true },
};
function comparador({ chave, desc }) {
  const numero = ORDENS.numero.f;
  if (ORDENS[chave].faixas) {
    return (a, b) => a.faixa - b.faixa || (a.faixa === 0 ? (desc ? b.desde - a.desde : a.desde - b.desde) : 0) || numero(a, b);
  }
  const f = ORDENS[chave].f;
  return (a, b) => (desc ? f(b, a) : f(a, b)) || numero(a, b);
}

// Rótulos do gráfico: hora ("19/10 13h") ou dia ("19/10"); no eixo, a cada 6 h ou a cada 5 dias, mais o último.
function rotuloPonto(t, dia) {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "";
  const data = dois(d.getDate()) + "/" + dois(d.getMonth() + 1);
  return dia ? data : data + " " + dois(d.getHours()) + "h";
}
function rotuloEixo(t, dia, i, n) {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "";
  if (dia) return n <= 7 || i % 5 === 0 || i === n - 1 ? dois(d.getDate()) + "/" + dois(d.getMonth() + 1) : "";
  return i % 6 === 0 || i === n - 1 ? dois(d.getHours()) + "h" : "";
}

function retangulo(classe, x, y, w, h, titulo) {
  const r = document.createElementNS(NS, "rect");
  r.setAttribute("class", classe);
  r.setAttribute("x", x.toFixed(2));
  r.setAttribute("y", y.toFixed(2));
  r.setAttribute("width", w.toFixed(2));
  r.setAttribute("height", h.toFixed(2));
  const t = document.createElementNS(NS, "title");
  t.textContent = titulo;
  r.append(t);
  return r;
}

function span(classe, pai, papel) {
  const s = document.createElement("span");
  if (classe) s.className = classe;
  if (papel) s.setAttribute("role", papel);
  if (pai) pai.append(s);
  return s;
}

export function iniciarRegistros({ dados, relogio, aoSessaoEncerrada }) {
  const el = {
    grupo: $("reg-grupo"), busca: $("reg-busca"), ocultar: $("reg-ocultar"), exportar: $("reg-exportar"),
    avisoEstado: $("reg-aviso-estado"), avisoPausa: $("reg-aviso-pausa"),
    desde: $("reg-desde"), tabela: $("reg-tabela"), corpo: $("reg-corpo"), altura: $("reg-altura"),
    contagem: $("reg-contagem"), estado: $("reg-estado"), estadoTexto: $("reg-estado-texto"), repetir: $("reg-repetir"),
    grafTitulo: $("reg-graf-titulo"), grafNota: $("reg-graf-nota"), graf: $("reg-graf"), barras: $("reg-graf-barras"),
    grafEstado: $("reg-graf-estado"), grafEstadoTexto: $("reg-graf-estado-texto"), grafRepetir: $("reg-graf-repetir"),
    eixo: $("reg-graf-eixo"), grafMax: $("reg-graf-max"), sistema: $("reg-sistema"),
  };
  const rl = {
    painel: $("reg-linha"), num: $("rl-num"), nome: $("rl-nome"), removido: $("rl-removido"), grupos: $("rl-grupos"),
    perdeu: $("rl-perdeu"), inalc: $("rl-inalc"), registrou: $("rl-registrou"), voltou: $("rl-voltou"),
    sistema: $("rl-sistema"), foraHa: $("rl-foraha"), ultReg: $("rl-ultreg"), fora: $("rl-fora"), estado: $("rl-estado"), estadoTexto: $("rl-estado-texto"),
    repetir: $("rl-repetir"), eventos: $("rl-eventos"), limite: $("rl-limite"), fechar: $("rl-fechar"),
  };
  const cabecalhos = [...el.tabela.querySelectorAll("[role=\"columnheader\"]")];
  // ocultarAntes: filtro "Ocultar fora desde antes da instalação" (D40), desligado ao abrir a aba.
  const filtro = { periodo: "24h", grupo: "", busca: "", ocultarAntes: false };
  let ordem = { chave: "foraha", desc: false };

  // Tabela: mapa número -> { r, chave de busca }; meta da última resposta (from em ms, since e o cabeçalho rc.9:
  // paused_until, pause_reason, state_unknown_since).
  let mapa = new Map();
  let cursor = "";
  let meta = null;
  let carregou = false;
  let falhou = false;
  let linhas = []; // filtradas e ordenadas: [{ r, perdeu, inalc, registrou, queda, ultreg, grupos, faixa, desde }]
  const pool = [];
  let exportando = false;

  // Ciclo de atualização. geracao muda a cada troca de período (ou parada): resposta velha é descartada.
  let visivel = false;
  let timer = null;
  let emVoo = false;
  let geracao = 0;

  // Gráfico e linha do tempo: cada um com o próprio contador de pedido.
  let serieEm = 0;
  let serieCarregada = false;
  let pedidoSerie = 0;
  let linhaAberta = null;
  let pedidoLinha = 0;

  const ligado = () => visivel && document.visibilityState === "visible";

  function proximo(ms) {
    clearTimeout(timer);
    timer = null;
    if (ligado()) timer = setTimeout(ciclo, ms);
  }

  async function ciclo() {
    timer = null;
    if (!ligado() || emVoo) return;
    emVoo = true;
    const gen = geracao;
    try {
      const d = await api.registros(filtro.periodo, cursor);
      if (gen === geracao) aplicar(d || {});
    } catch (e) {
      if (e.status === 401) { aoSessaoEncerrada(); return; }
      if (gen === geracao) { falhou = true; renderEstado(); }
    } finally {
      emVoo = false;
    }
    if (ligado() && Date.now() - serieEm >= INTERVALO_SERIE) carregarSerie();
    // Período trocado durante o voo: busca o novo já; senão, 5 s depois desta resposta.
    proximo(gen === geracao ? INTERVALO : 0);
  }

  function aplicar(d) {
    meta = {
      from: Date.parse(d.from), since: d.since || null,
      pausadaAte: d.paused_until || null, motivoPausa: d.pause_reason || null, desconhecidoDesde: d.state_unknown_since || null,
    };
    cursor = typeof d.cursor === "string" ? d.cursor : "";
    const rows = Array.isArray(d.rows) ? d.rows : [];
    if (d.full) mapa = new Map();
    let linhaMudou = false;
    for (const r of rows) {
      if (!r || typeof r.number !== "string") continue;
      // full:false traz as linhas tocadas sem a regra de lista (4.7.2): a que deixou de passar sai do mapa
      // (o ramal que voltou a registrar e não tem mais nada no período).
      if (r.number === linhaAberta) linhaMudou = true;
      if (!passaLista(r)) { mapa.delete(r.number); continue; }
      mapa.set(r.number, { r, chave: normalizar(r.number + " " + (r.name || "")) });
    }
    carregou = true;
    falhou = false;
    renderDesde();
    renderAvisos();
    renderTabela();
    // CONTRATOS 4.7.3: relê a linha do tempo quando a linha do ramal chega num full:false.
    if (linhaMudou && !d.full) carregarLinha(false);
  }

  function renderDesde() {
    if (!meta || !meta.since) { el.desde.textContent = ""; return; }
    const incompleto = Date.parse(meta.since) > meta.from;
    el.desde.textContent = "Dados desde " + dataHora(meta.since) + (incompleto ? " (período incompleto)" : "");
  }

  // Avisos do cabeçalho (D41, D42) na faixa da nota "Dados desde"; a pausa some sozinha quando a hora passa.
  // Roda a cada tique: os dois têm role="status", então o texto só é escrito quando muda (senão o leitor de tela
  // repetiria o aviso a cada segundo).
  function renderAvisos() {
    const desconhecido = meta && meta.desconhecidoDesde;
    const estado = desconhecido
      ? "Estado de registro desconhecido desde " + horaMinuto(desconhecido) + " (painel sem conexão com o Asterisk)" : "";
    el.avisoEstado.hidden = !estado;
    if (el.avisoEstado.textContent !== estado) el.avisoEstado.textContent = estado;
    const pausa = meta ? avisoPausa(meta.pausadaAte, meta.motivoPausa, relogio()) : "";
    el.avisoPausa.hidden = !pausa;
    if (el.avisoPausa.textContent !== pausa) el.avisoPausa.textContent = pausa;
  }
  const fimAgora = () => fimRelogio(relogio(), meta && meta.desconhecidoDesde);

  // ---------- tabela ----------
  function renderTabela() {
    const termo = normalizar(filtro.busca);
    const out = [];
    for (const { r, chave } of mapa.values()) {
      if (!passaGrupo(r, filtro.grupo) || (termo && !chave.includes(termo))) continue;
      if (filtro.ocultarAntes && r.down_kind === "before_install") continue;
      out.push({
        r, perdeu: inteiro(r.lost_registration), inalc: inteiro(r.became_unreachable), registrou: inteiro(r.registered),
        queda: Date.parse(r.last_drop_at) || 0, ultreg: Date.parse(r.last_registered) || 0, grupos: (r.groups || []).join(", "),
        faixa: faixaFora(r), desde: Date.parse(r.down_since) || 0,
      });
    }
    out.sort(comparador(ordem));
    linhas = out;
    el.tabela.setAttribute("aria-rowcount", String(out.length + 1));
    el.contagem.textContent = carregou ? plural(out.length, "ramal", "ramais") : "";
    el.exportar.disabled = exportando || !out.length;
    renderEstado();
    pintar();
  }

  function renderEstado() {
    let t = "", repetir = false;
    if (!carregou) {
      t = falhou ? "Não foi possível carregar os registros." : "Carregando registros...";
      repetir = falhou;
    } else if (falhou) {
      t = "Falha ao atualizar; os dados abaixo podem estar atrasados. Nova tentativa em 5 s.";
    } else if (!linhas.length) {
      t = mapa.size ? "Nenhum ramal com esses filtros." : "Nenhuma ocorrência de registro no período.";
    }
    el.estado.hidden = !t;
    el.estadoTexto.textContent = t;
    el.repetir.hidden = !repetir;
  }

  // Rolagem virtual: linhas absolutas de altura fixa sobre um espaçador (a mesma técnica da lista de ramais).
  // Altura primeiro, rolagem depois: com a lista encolhendo a janela não cai além do fim.
  function pintar() {
    el.altura.style.height = linhas.length * ALTURA_LINHA + "px";
    const j = janela(linhas.length, 1, el.corpo.scrollTop, el.corpo.clientHeight, ALTURA_LINHA);
    const n = Math.max(0, j.fim - j.inicio);
    while (pool.length < n) pool.push(criarLinha());
    pool.forEach((ui, k) => {
      ui.no.hidden = k >= n;
      if (k < n) preencherLinha(ui, linhas[j.inicio + k], j.inicio + k);
    });
  }

  let pintarPendente = false;
  el.corpo.addEventListener("scroll", () => {
    if (pintarPendente) return;
    pintarPendente = true;
    requestAnimationFrame(() => { pintarPendente = false; pintar(); });
  }, { passive: true });
  new ResizeObserver(() => { if (visivel) pintar(); }).observe(el.corpo);

  function criarLinha() {
    const no = document.createElement("div");
    no.className = "reg-linha";
    no.setAttribute("role", "row");
    const c0 = span("", no, "rowheader");
    const abrir = document.createElement("button");
    abrir.type = "button";
    abrir.className = "reg-abrir";
    c0.append(abrir);
    const celNome = span("", no, "cell");
    const nome = document.createTextNode("");
    const removido = span("reg-removido", null);
    removido.textContent = "removido";
    celNome.append(nome, removido);
    const c = {
      grupos: span("", no, "cell"), perdeu: span("reg-dir", no, "cell"),
      inalc: span("reg-dir", no, "cell"), registrou: span("reg-dir", no, "cell"), queda: span("reg-mono", no, "cell"),
      ip: span("reg-mono", no, "cell"), foraHa: span("reg-dir", no, "cell"), ultReg: span("reg-mono", no, "cell"),
    };
    el.corpo.append(no);
    return { no, abrir, nome, removido, c };
  }

  function preencherLinha(ui, l, idx) {
    const r = l.r;
    ui.r = r;
    ui.no.style.transform = "translateY(" + idx * ALTURA_LINHA + "px)";
    ui.no.setAttribute("aria-rowindex", String(idx + 2));
    ui.no.dataset.numero = r.number;
    if (r.number === linhaAberta) ui.no.dataset.aberta = "1"; else delete ui.no.dataset.aberta;
    ui.abrir.textContent = r.number;
    ui.abrir.setAttribute("aria-label", "Ramal " + r.number + ": abrir a linha do tempo");
    ui.removido.hidden = !r.removed;
    ui.nome.data = r.name || "sem nome";
    ui.c.grupos.textContent = l.grupos || AUSENTE;
    ui.c.perdeu.textContent = formatarNumero(l.perdeu);
    ui.c.inalc.textContent = formatarNumero(l.inalc);
    ui.c.registrou.textContent = formatarNumero(l.registrou);
    ui.c.queda.textContent = dataHora(r.last_drop_at) || AUSENTE;
    ui.c.ip.textContent = ipPorta(r.last_ip, r.last_port) || AUSENTE;
    pintarForaHa(ui.c.foraHa, r);
    const ult = textoUltimoRegistro(r);
    ui.c.ultReg.textContent = ult || AUSENTE;
    if (ult) ui.c.ultReg.setAttribute("aria-label", "último registro " + ult); else ui.c.ultReg.removeAttribute("aria-label");
  }

  // "Fora do ar há" (D38, D41): relógio ao vivo; parado e acinzentado com o estado desconhecido (AMI fora).
  function pintarForaHa(cel, r) {
    const t = textoForaHa(r, fimAgora(), meta && meta.since);
    if (cel.textContent !== (t.curto || AUSENTE)) cel.textContent = t.curto || AUSENTE;
    const parado = t.vivo && !!(meta && meta.desconhecidoDesde);
    cel.classList.toggle("reg-fora-aberto", t.vivo && !parado);
    cel.classList.toggle("reg-fora-parado", parado);
    if (t.longo) {
      const longo = t.longo + (parado ? " (relógio parado: estado desconhecido)" : "");
      cel.title = longo;
      cel.setAttribute("aria-label", longo);
    } else { cel.removeAttribute("title"); cel.removeAttribute("aria-label"); }
  }

  el.corpo.addEventListener("click", (e) => {
    const no = e.target.closest(".reg-linha");
    if (no && no.dataset.numero) abrirLinha(no.dataset.numero, true);
  });

  function marcarOrdem() {
    for (const h of cabecalhos) {
      h.setAttribute("aria-sort", h.dataset.chave !== ordem.chave ? "none" : ordem.desc ? "descending" : "ascending");
    }
  }
  for (const h of cabecalhos) {
    h.querySelector("button").addEventListener("click", () => {
      const chave = h.dataset.chave;
      ordem = ordem.chave === chave ? { chave, desc: !ordem.desc } : { chave, desc: ORDENS[chave].desc };
      marcarOrdem();
      el.corpo.scrollTop = 0;
      renderTabela();
    });
  }

  // ---------- filtros ----------
  function preencherGrupos() {
    const nomes = dados.grupos.map((g) => g.name).filter((n) => n !== SEM_GRUPO).sort(compararGrupos);
    const ops = [[TODOS, ""], ...nomes.map((n) => [n, n]), [SEM_GRUPO, SEM_GRUPO]].map(([t, v]) => {
      const o = document.createElement("option");
      o.textContent = t;
      o.value = v;
      return o;
    });
    el.grupo.replaceChildren(...ops);
    // Grupo escolhido que saiu do catálogo (excluído, renomeado): volta para todos os ramais.
    if (filtro.grupo && filtro.grupo !== SEM_GRUPO && !nomes.includes(filtro.grupo)) definirGrupo("");
    el.grupo.value = filtro.grupo;
  }

  function definirGrupo(g) {
    if (g === filtro.grupo) return;
    filtro.grupo = g;
    el.grupo.value = g;
    el.grafNota.textContent = g || TODOS;
    el.corpo.scrollTop = 0;
    serieCarregada = false;
    renderTabela();
    if (ligado()) carregarSerie();
  }
  el.grupo.addEventListener("change", () => definirGrupo(el.grupo.value));

  function definirPeriodo(p) {
    if (!PERIODOS.includes(p) || p === filtro.periodo) return;
    filtro.periodo = p;
    for (const b of document.querySelectorAll("#reg-periodo .seg")) b.setAttribute("aria-pressed", String(b.dataset.periodo === p));
    // Trocar o período descarta o cursor e a tabela (CONTRATOS 4.7.2).
    geracao++;
    cursor = "";
    mapa = new Map();
    meta = null;
    carregou = falhou = false;
    renderDesde();
    el.corpo.scrollTop = 0;
    renderTabela();
    serieCarregada = false;
    el.grafTitulo.textContent = p === "24h" ? "OCORRÊNCIAS POR HORA" : "OCORRÊNCIAS POR DIA";
    if (ligado()) {
      if (!emVoo) proximo(0);
      carregarSerie();
    }
    if (linhaAberta) carregarLinha(true);
  }
  for (const b of document.querySelectorAll("#reg-periodo .seg")) b.addEventListener("click", () => definirPeriodo(b.dataset.periodo));

  el.ocultar.addEventListener("change", () => {
    filtro.ocultarAntes = el.ocultar.checked;
    el.corpo.scrollTop = 0;
    renderTabela();
  });

  let timerBusca = null;
  el.busca.addEventListener("input", () => {
    clearTimeout(timerBusca);
    timerBusca = setTimeout(() => {
      filtro.busca = el.busca.value;
      el.corpo.scrollTop = 0;
      renderTabela();
    }, 120);
  });

  el.repetir.addEventListener("click", () => {
    falhou = false;
    renderEstado();
    if (!emVoo) proximo(0);
  });

  // ---------- exportar CSV (desvios D27): a tabela filtrada e ordenada que está na tela ----------
  el.exportar.addEventListener("click", () => {
    if (exportando || !linhas.length) return;
    exportando = true;
    el.exportar.disabled = true;
    el.exportar.textContent = "Gerando...";
    const copia = linhas;
    // Deixa o navegador pintar o botão desabilitado antes de montar o arquivo.
    setTimeout(() => {
      try {
        baixar(gerarCsv(copia, meta ? meta.from : NaN, fimAgora(), meta && meta.since), nomeArquivoCsv(filtro.periodo, new Date()));
      } finally {
        exportando = false;
        el.exportar.textContent = "Exportar CSV";
        el.exportar.disabled = !linhas.length;
      }
    }, 0);
  });

  // ---------- gráfico (CONTRATOS 4.7.4) ----------
  function estadoGrafico(t, comRepetir) {
    el.grafEstado.hidden = !t;
    el.grafEstadoTexto.textContent = t;
    el.grafRepetir.hidden = !comRepetir;
  }

  async function carregarSerie() {
    serieEm = Date.now();
    const meu = ++pedidoSerie;
    if (!serieCarregada) {
      el.barras.replaceChildren();
      el.eixo.replaceChildren();
      el.grafMax.textContent = "";
      el.sistema.textContent = "";
      estadoGrafico("Carregando gráfico...", false);
    }
    try {
      const s = await api.serieRegistros(filtro.periodo, filtro.grupo);
      if (meu !== pedidoSerie) return;
      desenharSerie(s || {});
      serieCarregada = true;
    } catch (e) {
      if (meu !== pedidoSerie) return;
      if (e.status === 401) { aoSessaoEncerrada(); return; }
      if (!serieCarregada) estadoGrafico("Não foi possível carregar o gráfico.", true);
    }
  }
  el.grafRepetir.addEventListener("click", () => carregarSerie());

  // Barras empilhadas: perdeu registro embaixo, ficou inalcançável em cima; intervalo sem dado em tracejado.
  function desenharSerie(s) {
    const pts = Array.isArray(s.points) ? s.points : [];
    const dia = s.step === "day";
    const n = Math.max(1, pts.length);
    let max = 0, somaPerdeu = 0, somaInalc = 0, temDado = false;
    for (const p of pts) {
      if (p.lost_registration != null || p.became_unreachable != null) temDado = true;
      const lp = inteiro(p.lost_registration), ui = inteiro(p.became_unreachable);
      somaPerdeu += lp;
      somaInalc += ui;
      max = Math.max(max, lp + ui);
    }
    const escala = Math.max(1, max);
    const slot = GRAF_L / n, larg = slot * 0.68, alt = GRAF_A - GRAF_TOPO;
    const filhos = [];
    pts.forEach((p, i) => {
      const x = i * slot + (slot - larg) / 2;
      const quando = rotuloPonto(p.t, dia);
      if (p.lost_registration == null && p.became_unreachable == null) {
        filhos.push(retangulo("reg-sem-dado", x, GRAF_TOPO, larg, alt, quando + ": sem dado"));
        return;
      }
      const lp = inteiro(p.lost_registration), ui = inteiro(p.became_unreachable);
      const hp = (lp / escala) * alt, hu = (ui / escala) * alt;
      const titulo = quando + ": perdeu registro " + lp + ", ficou inalcançável " + ui;
      if (hp > 0) filhos.push(retangulo("reg-perdeu", x, GRAF_A - hp, larg, hp, titulo));
      if (hu > 0) filhos.push(retangulo("reg-inalc", x, GRAF_A - hp - hu, larg, hu, titulo));
    });
    el.barras.replaceChildren(...filhos);
    el.grafTitulo.textContent = dia ? "OCORRÊNCIAS POR DIA" : "OCORRÊNCIAS POR HORA";
    el.grafNota.textContent = filtro.grupo || TODOS;
    el.graf.setAttribute("aria-label", "Ocorrências por " + (dia ? "dia" : "hora") + " no período: perdeu registro "
      + formatarNumero(somaPerdeu) + ", ficou inalcançável " + formatarNumero(somaInalc));
    el.eixo.style.gridTemplateColumns = "repeat(" + n + ", minmax(0, 1fr))";
    el.eixo.replaceChildren(...pts.map((p, i) => {
      const r = span(i === pts.length - 1 ? "reg-eixo-fim" : "", null);
      r.textContent = rotuloEixo(p.t, dia, i, pts.length);
      return r;
    }));
    el.grafMax.textContent = max ? "escala até " + formatarNumero(max) + " por " + (dia ? "dia" : "hora") : "";
    const ev = Array.isArray(s.system_events) ? s.system_events : [];
    el.sistema.textContent = ev.length
      ? "Eventos do sistema no período: " + (ev.length >= 100 ? "100 ou mais" : formatarNumero(ev.length))
        + "; último: " + (ROTULO_SINAL[ev[0].kind] || "evento do sistema") + " em " + dataHora(ev[0].at) + "."
      : "Nenhum evento do sistema no período.";
    estadoGrafico(!temDado ? "Sem dados no período." : somaPerdeu + somaInalc === 0 ? "Nenhuma ocorrência no período." : "", false);
  }

  // ---------- linha do tempo do ramal (CONTRATOS 4.7.3) ----------
  function estadoLinha(t, comRepetir) {
    rl.estado.hidden = !t;
    rl.estadoTexto.textContent = t;
    rl.repetir.hidden = !comRepetir;
  }

  function abrirLinha(numero, focar) {
    linhaAberta = numero;
    rl.painel.hidden = false;
    rl.num.textContent = numero;
    const conhecida = mapa.get(numero);
    rl.nome.textContent = conhecida ? conhecida.r.name || "sem nome" : "";
    pintar(); // marca a linha aberta na tabela
    carregarLinha(true);
    if (focar) rl.fechar.focus();
  }

  function fecharLinha() {
    linhaTempo = null;
    rl.painel.hidden = true;
    linhaAberta = null;
    pedidoLinha++;
    pintar();
  }
  rl.fechar.addEventListener("click", () => {
    const numero = linhaAberta;
    fecharLinha();
    // Devolve o foco à linha do ramal na tabela, se ela está desenhada.
    const b = pool.find((ui) => !ui.no.hidden && ui.no.dataset.numero === numero);
    (b ? b.abrir : el.corpo).focus();
  });
  rl.repetir.addEventListener("click", () => carregarLinha(true));

  async function carregarLinha(mostrarCarregando) {
    const numero = linhaAberta;
    if (!numero) return;
    const meu = ++pedidoLinha;
    if (mostrarCarregando) {
      rl.eventos.replaceChildren();
      rl.limite.hidden = true;
      estadoLinha("Carregando linha do tempo...", false);
    }
    try {
      const d = await api.registrosRamal(numero, filtro.periodo);
      if (meu !== pedidoLinha || linhaAberta !== numero) return;
      renderLinhaTempo(d || {});
    } catch (e) {
      if (meu !== pedidoLinha) return;
      if (e.status === 401) { aoSessaoEncerrada(); return; }
      if (mostrarCarregando) estadoLinha("Não foi possível carregar a linha do tempo.", true);
    }
  }

  // Linha do tempo aberta: a linha e o cabeçalho da última resposta, para o relógio do tique (D43).
  let linhaTempo = null;

  function renderLinhaTempo(d) {
    const x = d.extension || {};
    linhaTempo = { x, from: Date.parse(d.from), desconhecidoDesde: d.state_unknown_since || null, since: d.since || null };
    rl.nome.textContent = x.name || "sem nome";
    rl.removido.hidden = !x.removed;
    rl.grupos.textContent = (x.groups || []).join(", ") || AUSENTE;
    rl.perdeu.textContent = formatarNumero(inteiro(x.lost_registration));
    rl.inalc.textContent = formatarNumero(inteiro(x.became_unreachable));
    rl.registrou.textContent = formatarNumero(inteiro(x.registered));
    rl.voltou.textContent = formatarNumero(inteiro(x.reachable_again));
    rl.sistema.textContent = formatarNumero(inteiro(x.system_events));
    rl.ultReg.textContent = textoUltimoRegistro(x) || AUSENTE;
    pintarRelogiosLinha();
    const evs = Array.isArray(d.events) ? d.events : [];
    rl.eventos.replaceChildren(...evs.map(evento));
    rl.limite.hidden = evs.length < (inteiro(d.events_limit) || 200);
    estadoLinha(evs.length ? "" : "Nenhuma ocorrência guardada no período.", false);
  }

  // "Fora do ar há" (frase completa) e "Tempo fora do ar no período" com o sufixo de lacuna (CONTRATOS 4.7, D43).
  function pintarRelogiosLinha() {
    if (!linhaTempo) return;
    const { x, from, desconhecidoDesde, since } = linhaTempo;
    const fim = fimRelogio(relogio(), desconhecidoDesde);
    const t = textoForaHa(x, fim, since);
    const parado = t.vivo && !!desconhecidoDesde;
    rl.foraHa.textContent = t.vivo ? t.curto + (parado ? " (parado: estado desconhecido)" : "") : t.longo || AUSENTE;
    rl.foraHa.classList.toggle("reg-fora-aberto", t.vivo && !parado);
    rl.foraHa.classList.toggle("reg-fora-parado", parado);
    const sufixo = x.down_gap || x.down_kind === "unknown_start" ? " (com lacuna: painel parado)"
      : x.down_kind === "before_install" ? " (conta desde a instalação do painel)" : "";
    rl.fora.textContent = formatarDuracao(tempoFora(x, from, fim)) + sufixo;
  }

  function evento(e) {
    const li = document.createElement("li");
    li.className = "rl-evento";
    if (ROTULO_OCORRENCIA[e.kind]) li.dataset.tipo = e.kind;
    if (e.system) li.dataset.sistema = "1";
    const cab = span("rl-cabeca", li);
    span("rl-ponto", cab).setAttribute("aria-hidden", "true");
    span("rl-quando", cab).textContent = dataHora(e.at, true);
    span("rl-oque", cab).textContent = ROTULO_OCORRENCIA[e.kind] || "Ocorrência";
    if (e.system) span("rl-sistema", cab).textContent = "evento do sistema";
    const onde = ipPorta(e.ip, e.port);
    if (onde) span("rl-onde", li).textContent = onde;
    if (e.user_agent) span("rl-ua", li).textContent = e.user_agent;
    return li;
  }

  document.addEventListener("visibilitychange", () => {
    if (ligado()) { if (!emVoo) proximo(0); } else { clearTimeout(timer); timer = null; }
  });

  return {
    // Aba Registros visível (e painel ativo) ou não. O filtro "Ocultar fora desde antes da instalação" volta
    // desligado a cada abertura da aba (D40, não é lembrado).
    mostrar(sim) {
      const abrindo = sim && !visivel;
      visivel = sim;
      if (!sim) { clearTimeout(timer); timer = null; return; }
      if (abrindo) { filtro.ocultarAntes = false; el.ocultar.checked = false; }
      preencherGrupos();
      el.grafNota.textContent = filtro.grupo || TODOS;
      renderTabela();
      if (!emVoo) proximo(0);
      if (!serieCarregada || Date.now() - serieEm >= INTERVALO_SERIE) carregarSerie();
    },
    // Vindo do bloco de instabilidade: 24 h, o grupo da tela de ramais, busca limpa e a linha do tempo aberta.
    abrirRamal(numero, grupo) {
      definirPeriodo("24h");
      preencherGrupos();
      const valido = !grupo || grupo === SEM_GRUPO || dados.grupos.some((g) => g.name === grupo);
      definirGrupo(valido ? grupo : "");
      el.busca.value = "";
      filtro.busca = "";
      renderTabela();
      abrirLinha(numero, false);
    },
    aoMudarCatalogo() {
      if (visivel) preencherGrupos();
    },
    // Tique de 1 s com a aba visível: só os relógios das linhas desenhadas e da linha do tempo, sem reordenar e
    // sem rede (a ordem é pela data, não pela duração); o aviso de pausa some quando a hora passa.
    tique() {
      if (!visivel) return;
      for (const ui of pool) if (!ui.no.hidden && ui.r) pintarForaHa(ui.c.foraHa, ui.r);
      pintarRelogiosLinha();
      renderAvisos();
    },
    // Saída do painel: para o ciclo e apaga o que o próximo usuário não deve ver.
    parar() {
      visivel = false;
      clearTimeout(timer);
      timer = null;
      geracao++;
      pedidoSerie++;
      cursor = "";
      mapa = new Map();
      meta = null;
      carregou = falhou = false;
      serieCarregada = false;
      serieEm = 0;
      el.barras.replaceChildren();
      el.eixo.replaceChildren();
      renderDesde();
      renderAvisos();
      fecharLinha();
      renderTabela();
    },
  };
}
