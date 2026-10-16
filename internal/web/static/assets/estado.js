// Regras puras da tela (contrato C9 do plano da fase 2). Sem DOM: testadas com node --test.

export const ESTADOS = [
  { chave: "livre", rotulo: "livre", cor: "--estado-livre" },
  { chave: "tocando", rotulo: "tocando", cor: "--estado-tocando" },
  { chave: "em_conversa", rotulo: "em conversa", cor: "--estado-em-conversa" },
  { chave: "em_espera", rotulo: "em espera", cor: "--estado-em-espera" },
  { chave: "nao_registrado", rotulo: "não registrado", cor: "--estado-nao-registrado" },
  { chave: "inalcancavel", rotulo: "inalcançável", cor: "--estado-inalcancavel" },
  { chave: "desconhecido", rotulo: "desconhecido", cor: "--estado-desconhecido" },
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

export function textoOutroLado(calls) {
  const c = chamadaPrincipal(calls);
  if (!c) return "";
  const peer = c.peer || {};
  const numero = peer.number || "";
  // Nome só entra se acrescenta algo ao número (nunca "11468 · 11468").
  const nome = peer.name && peer.name.trim() !== numero ? peer.name.trim() : "";
  let texto;
  if (peer.kind === "extension") {
    texto = [numero, nome].filter(Boolean).join(" · ") || "ramal";
  } else if (peer.kind === "external") {
    texto = [numero ? "externo " + numero : "externo", nome].filter(Boolean).join(" · ");
  } else {
    texto = [numero, nome].filter(Boolean).join(" · ") || "desconhecido";
  }
  return calls.length > 1 ? texto + " +" + (calls.length - 1) : texto;
}

const DIRECAO = { internal: "interna", inbound: "entrada", outbound: "saída" };
export function textoDirecao(calls) {
  const c = chamadaPrincipal(calls);
  return c ? DIRECAO[c.direction] || "" : "";
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
// lista: [{ ramal, estado, chaveBusca }] na ordem do snapshot.
export function filtrar(lista, filtros) {
  const termo = normalizar(filtros.busca);
  const estados = filtros.estados;
  const grupo = filtros.grupo;
  const out = [];
  for (const item of lista) {
    if (grupo && item.ramal.group !== grupo) continue;
    if (estados && estados.size && !estados.has(item.estado)) continue;
    if (termo && !item.chaveBusca.includes(termo)) continue;
    out.push(item);
  }
  return out;
}

export function chaveBusca(ramal) {
  return normalizar(ramal.number + " " + (ramal.name || ""));
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

// Por grupo: { nome, total, porEstado: {chave: n} }, na ordem de aparição dos grupos
// com "Sem grupo" por último (igual ao GET /api/groups).
export function agregarGrupos(itens) {
  const mapa = new Map();
  for (const it of itens) {
    const nome = it.ramal.group;
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
  const grupos = [...mapa.values()];
  grupos.sort((a, b) => {
    if (a.nome === "Sem grupo") return 1;
    if (b.nome === "Sem grupo") return -1;
    return a.nome.localeCompare(b.nome, "pt-BR");
  });
  return grupos;
}

// Até 10 grupos com mais "não registrado"; desempate pelo nome; zero não entra.
export function ranking(grupos, limite = 10) {
  return grupos
    .filter((g) => g.porEstado.nao_registrado > 0)
    .map((g) => ({ nome: g.nome, quantidade: g.porEstado.nao_registrado, total: g.total }))
    .sort((a, b) => b.quantidade - a.quantidade || (a.nome < b.nome ? -1 : a.nome > b.nome ? 1 : 0))
    .slice(0, limite);
}

// Variação de registrados na última hora: agora menos o primeiro ponto sem lacuna.
export function variacaoUltimaHora(registradosAgora, pontos) {
  const p = (pontos || []).find((x) => !x.gap && x.registered != null);
  return p ? registradosAgora - p.registered : null;
}

const NUM = new Intl.NumberFormat("pt-BR");
export const formatarNumero = (n) => NUM.format(n);
