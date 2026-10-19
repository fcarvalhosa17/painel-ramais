// Rede: REST com envelope (C2, C5, C6) e o ciclo do /ws (C8). Única camada que fala com o daemon.

import { atrasoReconexao } from "./dados.js";

export const BASE = "/painel-ramais/";

export class ErroApi extends Error {
  constructor(status, codigo, mensagem) {
    super(mensagem || "Falha ao falar com o painel");
    this.status = status;
    this.codigo = codigo;
  }
}

async function chamar(metodo, caminho, corpo) {
  const opcoes = { method: metodo, credentials: "same-origin", headers: {}, cache: "no-store" };
  if (corpo !== undefined) {
    opcoes.headers["Content-Type"] = "application/json";
    opcoes.body = JSON.stringify(corpo);
  }
  let resp;
  try {
    resp = await fetch(BASE + caminho, opcoes);
  } catch {
    throw new ErroApi(0, "rede", "Sem conexão com o servidor do painel");
  }
  let env = null;
  try { env = await resp.json(); } catch { /* corpo sem JSON: tratado abaixo */ }
  if (resp.ok && env && env.success) return env.data;
  const erro = env && env.error ? env.error : {};
  throw new ErroApi(resp.status, erro.code || "http_" + resp.status, erro.message);
}

export const api = {
  sessao: () => chamar("GET", "api/session"),
  token: (token) => chamar("POST", "api/token", { token }),
  login: (password) => chamar("POST", "api/login", { username: "emergencia", password }),
  logout: () => chamar("POST", "api/logout", {}),
  snapshot: () => chamar("GET", "api/snapshot"),
  historico: (de, ate, grupo) => {
    const q = new URLSearchParams({ from: de.toISOString(), to: ate.toISOString() });
    if (grupo) q.set("group", grupo);
    return chamar("GET", "api/history?" + q.toString());
  },
  grupos: () => chamar("GET", "api/groups"),
  importarGrupos: (csv, replace) => chamar("POST", "api/groups/import", { csv, replace }),
  removerGrupo: (name) => chamar("POST", "api/groups/delete", { name }),
  criarGrupo: (name, ranges) => chamar("POST", "api/groups/create", ranges && ranges.length ? { name, ranges } : { name }),
  // mudancas: { new_name?, ranges? } (ranges substitui todas as faixas do grupo).
  editarGrupo: (name, mudancas) => chamar("POST", "api/groups/update", { name, ...mudancas }),
  // Vínculo manual (CONTRATOS 4.6, rc.7), atômico: add só acrescenta (1 a 20 grupos); remove tira de um grupo
  // e só apaga vínculo manual (não existe exclusão de faixa). Respostas { changed } e { changed, not_manual }.
  adicionarAGrupos: (numbers, groups) => chamar("POST", "api/groups/assign", { mode: "add", numbers, groups }),
  removerDoGrupo: (numbers, group) => chamar("POST", "api/groups/assign", { mode: "remove", numbers, group }),
  // Relatório de registro (CONTRATOS 4.7). grupo "" = todos os ramais (sem o parâmetro).
  resumoRegistros: (grupo) => chamar("GET", "api/registrations/summary" + (grupo ? "?" + new URLSearchParams({ group: grupo }) : "")),
  registros: (period, cursor) => {
    const q = new URLSearchParams({ period });
    if (cursor) q.set("cursor", cursor);
    return chamar("GET", "api/registrations?" + q.toString());
  },
  registrosRamal: (number, period) => chamar("GET", "api/registrations/extension?" + new URLSearchParams({ number, period }).toString()),
  serieRegistros: (period, grupo) => {
    const q = new URLSearchParams({ period });
    if (grupo) q.set("group", grupo);
    return chamar("GET", "api/registrations/series?" + q.toString());
  },
};

// Ciclo de vida do WebSocket. eventos: { aoDelta(msg), aoPrecisarSnapshot(), aoReconectar(), aoSessaoEncerrada(), aoStatus(status, detalhe) }
// O snapshot é buscado por quem usa a classe; depois chama conectar(epoch, version).
export class CanalAoVivo {
  constructor(eventos) {
    this.ev = eventos;
    this.ws = null;
    this.tentativa = 0;
    this.timer = null;
    this.parado = false;
    this.pedirSnapshot = false;
  }

  conectar(epoch, version) {
    this.limpar();
    if (this.parado) return;
    const url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + BASE + "ws";
    const ws = new WebSocket(url);
    this.ws = ws;
    this.pedirSnapshot = false;
    ws.onopen = () => {
      ws.send(JSON.stringify({ type: "hello", epoch, version }));
      this.tentativa = 0;
      this.ev.aoStatus("ao_vivo");
    };
    ws.onmessage = (e) => {
      let msg;
      try { msg = JSON.parse(e.data); } catch { return; }
      if (msg.type === "delta") this.ev.aoDelta(msg);
      else if (msg.type === "reload") this.pedirSnapshot = true;
      else if (msg.type === "session_expired") { this.parado = true; this.ev.aoSessaoEncerrada(); }
    };
    ws.onclose = (e) => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.parado || e.code === 4001) { this.parado = true; this.ev.aoSessaoEncerrada(); return; }
      if (e.code === 4000 || this.pedirSnapshot) { this.ev.aoStatus("atualizando"); this.ev.aoPrecisarSnapshot(); return; }
      const atraso = e.code === 4003 ? 5000 : atrasoReconexao(this.tentativa++);
      this.ev.aoStatus("reconectando", atraso);
      // Reconecta com hello da versão local: o servidor manda o replay ou um reload.
      this.timer = setTimeout(() => this.ev.aoReconectar(), atraso);
    };
  }

  // Lacuna detectada no cliente: fecha e pede snapshot novo.
  reiniciar() {
    this.limpar();
    this.ev.aoStatus("atualizando");
    this.ev.aoPrecisarSnapshot();
  }

  limpar() {
    clearTimeout(this.timer);
    if (this.ws) {
      const ws = this.ws;
      this.ws = null;
      ws.onclose = null;
      ws.onmessage = null;
      try { ws.close(1000); } catch { /* já fechado */ }
    }
  }

  parar() {
    this.parado = true;
    this.limpar();
  }
}
