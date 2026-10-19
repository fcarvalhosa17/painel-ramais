// Regras puras da tela (contrato C9 do plano da fase 2). Sem DOM: testadas com node --test.

export const ESTADOS = [
  { chave: "livre", rotulo: "Livre" },
  { chave: "tocando", rotulo: "Tocando" },
  { chave: "em_conversa", rotulo: "Em conversa" },
  { chave: "em_espera", rotulo: "Em espera" },
  { chave: "nao_registrado", rotulo: "Não registrado" },
  { chave: "inalcancavel", rotulo: "Inalcançável" },
  { chave: "desconhecido", rotulo: "Desconhecido" },
];

export const ROTULO = Object.fromEntries(ESTADOS.map((e) => [e.chave, e.rotulo]));

const PRIORIDADE = { talking: 0, hold: 1, ringing: 2 };

export function estadoDaTela(ramal, amiConectado) {
  if (!amiConectado || ramal.registration === "unknown") return "desconhecido";
  if (ramal.registration === "unreachable") return "inalcancavel";
  if (ramal.registration === "unregistered") return "nao_registrado";
  const calls = ramal.calls || [];
  if (calls.some((c) => c.state === "talking")) return "em_conversa";
  if (calls.some((c) => c.state === "hold")) return "em_espera";
  if (calls.some((c) => c.state === "ringing")) return "tocando";
  switch (ramal.device_state) {
    case "ringing": return "tocando";
    case "inuse": case "busy": case "ringinuse": return "em_conversa";
    case "onhold": return "em_espera";
    default: return "livre";
  }
}

// Chamada principal: talking > hold > ringing; empate fica com a primeira da lista.
export function chamadaPrincipal(calls) {
  let melhor = null;
  for (const c of calls || []) {
    const p = PRIORIDADE[c.state] ?? 3;
    if (!melhor || p < (PRIORIDADE[melhor.state] ?? 3)) melhor = c;
  }
  return melhor;
}

// Seta da direção (legenda do design: ↙ entrada, ↗ saída, ↔ interna).
export const SETA = { inbound: "↙", outbound: "↗", internal: "↔" };
export const DIRECAO = { inbound: "Entrada", outbound: "Saída", internal: "Interna" };

// Outro lado da chamada: número (ramal ou externo) ou, sem número, o nome.
export function outroLado(c) {
  const peer = (c && c.peer) || {};
  return (peer.number || (peer.name || "").trim() || "desconhecido") + "";
}

// Linha da chamada do card e do detalhe: "↙ 11987654321 03:12" (tempo à parte para o tique de 1 s).
// Sem chamada: o rótulo do estado.
export function linhaRamal(item, agoraMs, desvioMs) {
  const calls = item.ramal.calls || [];
  const c = chamadaPrincipal(calls);
  if (!c) return { chamada: false, texto: ROTULO[item.estado], tempo: "" };
  const d = duracaoMs(calls, agoraMs, desvioMs);
  const extra = calls.length > 1 ? " +" + (calls.length - 1) : "";
  return { chamada: true, texto: (SETA[c.direction] || SETA.internal) + " " + outroLado(c) + extra, tempo: d == null ? "" : formatarCronometro(d) };
}

// Ramais em conversa ou em espera, por direção da chamada principal (indicador "Em chamada").
export function direcoes(itens) {
  const out = { inbound: 0, outbound: 0, internal: 0 };
  for (const it of itens) {
    if (it.estado !== "em_conversa" && it.estado !== "em_espera") continue;
    const c = chamadaPrincipal(it.ramal.calls);
    if (c && out[c.direction] !== undefined) out[c.direction]++;
  }
  return out;
}

const dois = (n) => (n < 10 ? "0" : "") + n;
export function formatarCronometro(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return h > 0 ? h + ":" + dois(m) + ":" + dois(s % 60) : dois(m) + ":" + dois(s % 60);
}

// Duração da chamada principal no relógio do servidor (desvio = server_time - Date.now()).
export function duracaoMs(calls, agoraMs, desvioMs) {
  const c = chamadaPrincipal(calls);
  if (!c) return null;
  const inicio = Date.parse(c.answered_at ?? c.started_at);
  return Number.isNaN(inicio) ? null : agoraMs + desvioMs - inicio;
}

// Busca sem diferenciar maiúsculas nem acentos.
export function normalizar(texto) {
  return String(texto ?? "").normalize("NFD").replace(/\p{M}/gu, "").toLowerCase().trim();
}

// filtros: { busca: "", estados: Set de chaves (vazio = todos), grupo: "" (vazio = todos) }
// lista: [{ ramal, estado, chaveBusca }] na ordem do snapshot. Grupo: o ramal está nele (groups contém, N:N).
export function filtrar(lista, filtros) {
  const termo = normalizar(filtros.busca);
  const estados = filtros.estados;
  const grupo = filtros.grupo;
  const out = [];
  for (const item of lista) {
    if (grupo && !gruposDe(item.ramal).includes(grupo)) continue;
    if (estados && estados.size && !estados.has(item.estado)) continue;
    if (termo && !item.chaveBusca.includes(termo)) continue;
    out.push(item);
  }
  return out;
}

// Busca por número, nome ou IP.
export function chaveBusca(ramal) {
  return normalizar(ramal.number + " " + (ramal.name || "") + " " + (ramal.ip || ""));
}

export function contagens(ramais) {
  const c = { total: 0, registered: 0, unregistered: 0, unreachable: 0, in_call: 0 };
  for (const r of ramais) {
    c.total++;
    if (r.registration === "registered") c.registered++;
    else if (r.registration === "unregistered") c.unregistered++;
    else if (r.registration === "unreachable") c.unreachable++;
    if (r.calls && r.calls.length > 0) c.in_call++;
  }
  return c;
}

export const SEM_GRUPO = "Sem grupo";

// Grupos do ramal (CONTRATOS 2.1: nunca vazio; sem vínculo vem ["Sem grupo"]). A lista vazia só por garantia.
export const gruposDe = (ramal) => (ramal.groups && ramal.groups.length ? ramal.groups : [SEM_GRUPO]);

// Ordem dos grupos na tela: alfabética pt-BR com "Sem grupo" por último.
export function compararGrupos(a, b) {
  if (a === b) return 0;
  if (a === SEM_GRUPO) return 1;
  if (b === SEM_GRUPO) return -1;
  return a.localeCompare(b, "pt-BR");
}

// hifen: "-" no campo editável (o parser lê), "–" na exibição (como no design: 4000–4479).
export const textoFaixa = (f, hifen = "-") => (f.start === f.end ? String(f.start) : f.start + hifen + f.end);
export const textoFaixas = (faixas, hifen = "-") => (faixas || []).map((f) => textoFaixa(f, hifen)).join(", ");

// Faixas digitadas pelo administrador: "2000-2099, 3001" (N vira N-N). Devolve { faixas, erro }.
// Separador: vírgula, ponto e vírgula ou espaço; espaço em volta do hífen é aceito.
export function lerFaixas(texto) {
  const partes = String(texto ?? "").replace(/\s*-\s*/g, "-").split(/[\s,;]+/).filter(Boolean);
  const faixas = [];
  for (const p of partes) {
    const m = p.match(/^(\d+)(?:-(\d+))?$/);
    if (!m) return { faixas: [], erro: "\"" + p.slice(0, 24) + "\" não é ramal nem faixa. Use 2000-2099 ou 3001." };
    const start = Number(m[1]);
    const end = Number(m[2] ?? m[1]);
    if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end)) return { faixas: [], erro: "Número grande demais em \"" + p.slice(0, 24) + "\"." };
    if (start > end) return { faixas: [], erro: "Na faixa " + p + " o início é maior que o fim." };
    faixas.push({ start, end });
  }
  const ord = faixas.slice().sort((a, b) => a.start - b.start);
  for (let i = 1; i < ord.length; i++) {
    if (ord[i].start <= ord[i - 1].end) return { faixas: [], erro: "As faixas " + textoFaixa(ord[i - 1]) + " e " + textoFaixa(ord[i]) + " se sobrepõem." };
  }
  return { faixas, erro: "" };
}

// Desde a rc.7 (ADR-006) faixas de grupos diferentes podem se cruzar: só as do mesmo grupo são conferidas (lerFaixas).

// Mesma regra do daemon (CONTRATOS seção 9): número com zero à esquerda ou não numérico nunca cai em faixa.
const NUMERICO = /^(0|[1-9]\d*)$/;
export function numeroNaFaixa(numero, faixas) {
  if (!faixas.length || !NUMERICO.test(numero)) return false;
  const n = Number(numero);
  return faixas.some((f) => n >= f.start && n <= f.end);
}

// Primeira faixa do grupo (catálogo do snapshot) que contém o número, comparando com BigInt:
// faixas de até 18 dígitos passam do inteiro seguro do JavaScript (CONTRATOS 2.1).
const grande = (v) => (Number.isInteger(v) ? BigInt(v) : null);
export function faixaDoNumero(numero, catalogo, nome) {
  if (!NUMERICO.test(String(numero))) return null;
  const n = BigInt(numero);
  const g = (catalogo || []).find((x) => x.name === nome);
  for (const f of (g && g.ranges) || []) {
    const a = grande(f.start), b = grande(f.end);
    if (a !== null && b !== null && a <= n && n <= b) return f;
  }
  return null;
}

// Grupos do ramal com a origem (detalhe, desvios D22): "pela faixa A-B", "pela faixa A-B e manual", "manual";
// sem vínculo nenhum, "Sem grupo" com "nenhum vínculo".
export function origensDoRamal(ramal, catalogo) {
  const faixa = new Set(ramal.range_groups || []);
  const manual = new Set(ramal.manual_groups || []);
  return gruposDe(ramal).map((nome) => {
    if (faixa.has(nome)) {
      const f = faixaDoNumero(ramal.number, catalogo, nome);
      const t = "pela faixa" + (f ? " " + textoFaixa(f) : "");
      return { nome, origem: manual.has(nome) ? t + " e manual" : t };
    }
    return { nome, origem: manual.has(nome) ? "manual" : "nenhum vínculo" };
  });
}

// Prévia de um grupo com estas faixas, pelos dados carregados: quantos ramais caem nas faixas e quantos ficam no
// grupo ao todo (faixa ou vínculo manual com ele; desde a rc.7 o vínculo manual soma à faixa, não a exclui).
export function contarComFaixas(itens, faixas, nome) {
  let pelaFaixa = 0, total = 0;
  for (const it of itens) {
    const naFaixa = numeroNaFaixa(it.ramal.number, faixas);
    if (naFaixa) pelaFaixa++;
    if (naFaixa || (nome !== null && (it.ramal.manual_groups || []).includes(nome))) total++;
  }
  return { pelaFaixa, total };
}

// Nome de grupo novo ou renomeado. Duplicado é comparado sem maiúsculas nem acentos; o daemon revalida.
export function erroNomeGrupo(nome, catalogo, atual = null) {
  const n = String(nome ?? "").trim();
  if (!n) return "Digite o nome do grupo.";
  if ([...n].length > 64) return "O nome pode ter no máximo 64 caracteres.";
  if (/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(n)) return "O nome tem caractere invisível ou de controle.";
  const chave = normalizar(n);
  if (chave === normalizar(SEM_GRUPO)) return "\"Sem grupo\" é reservado. Escolha outro nome.";
  // "Todos os ramais" é o escopo de todos os ramais na tela, não grupo. ponytail: só a tela recusa; o daemon ainda aceita (dívida).
  if (chave === normalizar("Todos os ramais")) return "\"Todos os ramais\" é reservado. Escolha outro nome.";
  const igual = (catalogo || []).find((g) => g.name !== atual && normalizar(g.name) === chave);
  return igual ? "Já existe o grupo " + igual.name + "." : "";
}

// Por grupo: { nome, total, porEstado: {chave: n} }, em ordem alfabética com "Sem grupo" por último
// (igual ao GET /api/groups). Ramal em vários grupos conta em cada um (a soma dos grupos pode passar do total).
export function agregarGrupos(itens) {
  const mapa = new Map();
  for (const it of itens) {
    for (const nome of gruposDe(it.ramal)) {
      let g = mapa.get(nome);
      if (!g) {
        g = { nome, total: 0, porEstado: {}, itens: [] };
        for (const e of ESTADOS) g.porEstado[e.chave] = 0;
        mapa.set(nome, g);
      }
      g.total++;
      g.porEstado[it.estado]++;
      g.itens.push(it);
    }
  }
  return [...mapa.values()].sort((a, b) => compararGrupos(a.nome, b.nome));
}

// Data e hora local "19/10 13:42" (com segundos "19/10 13:42:10"); vazio se ausente ou inválida.
export function dataHora(iso, comSegundos = false) {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return "";
  return dois(d.getDate()) + "/" + dois(d.getMonth() + 1) + " " + dois(d.getHours()) + ":" + dois(d.getMinutes()) + (comSegundos ? ":" + dois(d.getSeconds()) : "");
}

const NUM = new Intl.NumberFormat("pt-BR");
export const formatarNumero = (n) => NUM.format(n);

// Percentual com uma casa, como o design: "61,3%".
export const formatarPct = (a, b) => (b ? (Math.round((a / b) * 1000) / 10).toLocaleString("pt-BR") : "0") + "%";
