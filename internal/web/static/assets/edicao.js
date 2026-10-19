// Edição de grupos pela tela principal: criar grupo, painel do ramal, seleção múltipla e vínculo manual
// (rc.7, ADR-006: ramal em N grupos; "Adicionar" só acrescenta, "Remover deste grupo" só apaga vínculo manual).
// Controles de escrita só aparecem para administrador (cosmético: o daemon decide e responde 403).
// A seleção é um Set de números fora do DOM (lista.selecao): os cards reciclados da rolagem virtual
// só refletem o Set em preencher(), então 10.000 selecionados não custam nada no DOM.

import { api } from "./conexao.js";
import {
  ROTULO, SEM_GRUPO, compararGrupos, formatarNumero, lerFaixas, contarComFaixas, erroNomeGrupo, linhaRamal,
  gruposDe, origensDoRamal, faixaDoNumero, textoFaixa,
} from "./estado.js";

const $ = (id) => document.getElementById(id);
// Tetos do /api/groups/assign (CONTRATOS 4.6).
const MAX_NUMEROS = 20000;
const MAX_GRUPOS = 20;
const MAX_PARES = 100000; // numbers x groups no modo add
const plural = (n, um, varios) => formatarNumero(n) + " " + (n === 1 ? um : varios);
const TRACO = String.fromCharCode(0x2014); // dado ausente: "—" como no design (decorate)

// Caixas de seleção de grupos (modo add). Só refaz quando a lista de nomes muda e mantém as marcadas:
// o detalhe é redesenhado a cada delta e não pode apagar a escolha em andamento.
function listaDeGrupos(caixa) {
  let chave = null;
  const marcados = () => [...caixa.querySelectorAll("input:checked")].map((i) => i.value);
  return {
    marcados,
    preencher(nomes) {
      const k = nomes.join("\n");
      if (k === chave) return;
      chave = k;
      const antes = new Set(marcados());
      caixa.replaceChildren(...nomes.map((nome) => {
        const label = document.createElement("label");
        label.className = "opcao";
        const input = document.createElement("input");
        input.type = "checkbox";
        input.value = nome;
        input.checked = antes.has(nome);
        const span = document.createElement("span");
        span.textContent = nome;
        label.append(input, span);
        return label;
      }));
    },
    limpar() { for (const i of caixa.querySelectorAll("input:checked")) i.checked = false; },
  };
}

// grupoAberto(): grupo selecionado na coluna Grupos ("" = Todos os ramais).
export function iniciarEdicao({ dados, lista, grupoAberto, aoSessaoEncerrada }) {
  const selecao = lista.selecao;
  let admin = false;
  let resultados = [];
  let ancora = null;

  const catalogo = () => dados.grupos.filter((g) => g.name !== SEM_GRUPO);
  const nomesCatalogo = () => catalogo().map((g) => g.name).sort(compararGrupos);
  // Grupo aberto em que dá para remover vínculo: um grupo do catálogo (não "Todos os ramais" nem "Sem grupo").
  const grupoRemovivel = () => {
    const g = grupoAberto();
    return g && g !== SEM_GRUPO ? g : "";
  };

  function tratarErro(e, alvo) {
    if (e.status === 401) { aoSessaoEncerrada(); return; }
    alvo.textContent = e.message;
  }

  // Botões "Cancelar" e "x" de todos os diálogos desta área.
  for (const b of document.querySelectorAll("dialog [data-fechar]")) {
    const dialogo = b.closest("dialog");
    b.addEventListener("click", () => { if (!dialogo.dataset.ocupado) dialogo.close(); });
  }
  // Escape não fecha um diálogo com gravação em andamento.
  for (const id of ["dialogo-novo-grupo", "dialogo-mover"]) {
    $(id).addEventListener("cancel", (e) => { if ($(id).dataset.ocupado) e.preventDefault(); });
  }
  function ocupar(dialogo, botoes, sim) {
    if (sim) dialogo.dataset.ocupado = "1"; else delete dialogo.dataset.ocupado;
    for (const b of botoes) b.disabled = sim;
  }

  let timerAviso = null;
  function avisar(texto) {
    const el = $("aviso-status");
    el.textContent = texto;
    clearTimeout(timerAviso);
    timerAviso = setTimeout(() => { el.textContent = ""; }, 5000);
  }

  // ---------- criar grupo ----------
  const ng = {
    dialogo: $("dialogo-novo-grupo"), form: $("ng-form"), nome: $("ng-nome"), faixas: $("ng-faixas"),
    nomeErro: $("ng-nome-erro"), faixasErro: $("ng-faixas-erro"), previa: $("ng-previa"), erro: $("ng-erro"),
    criar: $("ng-criar"),
  };

  function abrirCriar() {
    ng.form.reset();
    for (const el of [ng.nomeErro, ng.faixasErro, ng.erro]) el.textContent = "";
    previaCriar();
    ng.dialogo.showModal();
    ng.nome.focus();
  }

  function previaCriar() {
    const { faixas, erro } = lerFaixas(ng.faixas.value);
    ng.faixasErro.textContent = erro;
    if (erro) { ng.previa.textContent = ""; return; }
    if (!faixas.length) { ng.previa.textContent = "Sem faixa, o grupo começa vazio e recebe ramais adicionados à mão."; return; }
    const c = contarComFaixas(dados.itens, faixas, null);
    ng.previa.textContent = plural(c.pelaFaixa, "ramal existente entra", "ramais existentes entram") + " no grupo (sem sair dos grupos em que já está).";
  }

  ng.faixas.addEventListener("input", previaCriar);
  // Nome vazio só é apontado no envio; duplicado e reservado aparecem enquanto digita.
  ng.nome.addEventListener("input", () => {
    ng.nomeErro.textContent = ng.nome.value.trim() ? erroNomeGrupo(ng.nome.value, catalogo()) : "";
  });

  ng.form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (ng.dialogo.dataset.ocupado) return;
    const nome = ng.nome.value.trim();
    const erroNome = erroNomeGrupo(nome, catalogo());
    const { faixas, erro: erroFaixas } = lerFaixas(ng.faixas.value);
    ng.nomeErro.textContent = erroNome;
    ng.faixasErro.textContent = erroFaixas;
    ng.erro.textContent = "";
    if (erroNome) { ng.nome.focus(); return; }
    if (erroFaixas) { ng.faixas.focus(); return; }
    ocupar(ng.dialogo, [ng.criar], true);
    ng.criar.textContent = "Criando...";
    try {
      const r = await api.criarGrupo(nome, faixas);
      delete ng.dialogo.dataset.ocupado;
      ng.dialogo.close();
      avisar("Grupo " + ((r && r.name) || nome) + " criado.");
    } catch (err) {
      tratarErro(err, ng.erro);
    } finally {
      ocupar(ng.dialogo, [ng.criar], false);
      ng.criar.textContent = "Criar grupo";
    }
  });

  $("botao-novo-grupo").addEventListener("click", () => abrirCriar());

  // ---------- detalhe do ramal (coluna da direita; o mesmo painel para Operator e administrador) ----------
  const dr = {
    painel: $("detalhe"), num: $("dr-num"), sumiu: $("dr-sumiu"), nome: $("dr-nome"), estado: $("dr-estado"),
    estadoTexto: $("dr-estado-texto"), grupos: $("dr-grupos"), chamada: $("dr-chamada"),
    ip: $("dr-ip"), ua: $("dr-ua"), latencia: $("dr-latencia"), admin: $("dr-admin"),
    addVazio: $("dr-add-vazio"), adicionar: $("dr-adicionar"), removerCaixa: $("dr-remover-caixa"),
    remover: $("dr-remover"), removerNota: $("dr-remover-nota"), erro: $("dr-erro"), ok: $("dr-ok"), fechar: $("dr-fechar"),
  };
  const drLista = listaDeGrupos($("dr-add-lista"));
  let numeroAberto = null;
  let ocupadoRamal = false;
  const detalheAberto = () => !dr.painel.hidden;

  function abrirRamal(numero) {
    if (numero !== numeroAberto) drLista.limpar();
    numeroAberto = numero;
    dr.erro.textContent = "";
    dr.ok.textContent = "";
    dr.painel.hidden = false;
    renderRamal();
    // Foco no painel sem rolar; com as colunas empilhadas, rola até ele se ficou fora da vista.
    dr.fechar.focus({ preventScroll: true });
    const r = dr.painel.getBoundingClientRect();
    if (r.top > window.innerHeight || r.bottom < 0) {
      dr.painel.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    }
  }

  function fecharDetalhe() {
    dr.painel.hidden = true;
    numeroAberto = null;
  }
  dr.fechar.addEventListener("click", fecharDetalhe);

  // Latência do qualify: inalcançável "sem resposta"; não registrado ou sem qualify (null) é traço.
  function textoLatencia(item) {
    if (item.estado === "inalcancavel") return "sem resposta";
    if (item.estado === "nao_registrado" || item.ramal.latency_ms == null) return TRACO;
    return item.ramal.latency_ms + " ms";
  }

  function renderLinha(item) {
    const l = linhaRamal(item, Date.now(), dados.desvioMs);
    dr.chamada.textContent = l.tempo ? l.texto + " " + l.tempo : l.texto;
  }

  // Grupos com a origem de cada um (desvios D22).
  function renderGruposDoRamal(r) {
    dr.grupos.replaceChildren(...origensDoRamal(r, dados.grupos).map(({ nome, origem }) => {
      const li = document.createElement("li");
      const n = document.createElement("span");
      n.textContent = nome;
      const o = document.createElement("span");
      o.className = "dr-origem";
      o.textContent = " (" + origem + ")";
      li.append(n, o);
      return li;
    }));
  }

  function renderRamal() {
    const item = dados.porNumero.get(numeroAberto);
    dr.num.textContent = numeroAberto;
    dr.sumiu.hidden = !!item;
    dr.admin.hidden = !admin || !item;
    if (!item) return;
    const r = item.ramal;
    dr.nome.textContent = r.name || "sem nome";
    dr.estado.dataset.estado = item.estado;
    dr.estadoTexto.textContent = ROTULO[item.estado].toLocaleUpperCase("pt-BR");
    renderGruposDoRamal(r);
    renderLinha(item);
    dr.ip.textContent = r.ip || TRACO;
    dr.ua.textContent = item.estado === "nao_registrado" || r.user_agent == null ? TRACO : r.user_agent;
    dr.latencia.textContent = textoLatencia(item);
    if (admin) renderAdmin(r);
  }

  // Controles de administrador (desvios D23). "Remover deste grupo" só com um grupo do catálogo aberto e o ramal
  // nele; habilitado só quando o grupo está em manual_groups e não em range_groups (CONTRATOS 2.1).
  function renderAdmin(r) {
    const ja = new Set(gruposDe(r));
    const candidatos = nomesCatalogo().filter((n) => !ja.has(n));
    drLista.preencher(candidatos);
    dr.addVazio.hidden = candidatos.length > 0;
    dr.addVazio.textContent = catalogo().length ? "O ramal já está em todos os grupos." : "Nenhum grupo cadastrado.";
    atualizarAdicionar();
    const g = grupoRemovivel();
    const noGrupo = !!g && ja.has(g);
    dr.removerCaixa.hidden = !noGrupo;
    if (!noGrupo) return;
    const pelaFaixa = (r.range_groups || []).includes(g);
    const manual = (r.manual_groups || []).includes(g);
    dr.remover.disabled = ocupadoRamal || pelaFaixa || !manual;
    dr.remover.setAttribute("aria-label", "Remover o ramal " + r.number + " do grupo " + g);
    const f = pelaFaixa ? faixaDoNumero(r.number, dados.grupos, g) : null;
    dr.removerNota.hidden = !pelaFaixa;
    dr.removerNota.textContent = pelaFaixa ? "Este ramal pertence ao grupo pela faixa" + (f ? " " + textoFaixa(f) : "") + "." : "";
  }

  function atualizarAdicionar() {
    dr.adicionar.disabled = ocupadoRamal || drLista.marcados().length === 0;
  }
  $("dr-add-lista").addEventListener("change", atualizarAdicionar);

  // Uma gravação por vez no detalhe; o resultado chega por delta e o renderRamal redesenha os grupos.
  async function gravarRamal(chamada, sucesso) {
    const numero = numeroAberto;
    dr.erro.textContent = "";
    dr.ok.textContent = "";
    ocupadoRamal = true;
    dr.adicionar.disabled = dr.remover.disabled = true;
    try {
      const r = await chamada(numero);
      if (numeroAberto === numero) dr.ok.textContent = sucesso(numero, r || {});
    } catch (e) {
      tratarErro(e, dr.erro);
    } finally {
      ocupadoRamal = false;
      if (detalheAberto()) renderRamal();
    }
  }

  dr.adicionar.addEventListener("click", () => {
    const grupos = drLista.marcados();
    if (!grupos.length) return;
    if (grupos.length > MAX_GRUPOS) { dr.erro.textContent = "Escolha no máximo " + MAX_GRUPOS + " grupos por vez."; return; }
    gravarRamal((n) => api.adicionarAGrupos([n], grupos), (n) => {
      drLista.limpar();
      return "Ramal " + n + " adicionado a " + grupos.join(", ") + ".";
    });
  });
  dr.remover.addEventListener("click", () => {
    const g = grupoRemovivel();
    if (!g) return;
    gravarRamal((n) => api.removerDoGrupo([n], g), (n, r) => (r.changed
      ? "Ramal " + n + " removido do grupo " + g + "."
      : "O ramal " + n + " não tinha vínculo manual com " + g + "; nada mudou."));
  });

  // Clique no card abre o detalhe; clique no checkbox só marca (Shift: intervalo).
  lista.caixa.addEventListener("mousedown", (e) => {
    if (e.shiftKey && e.target.classList.contains("t-marca")) e.preventDefault(); // sem seleção de texto
  });
  lista.caixa.addEventListener("click", (e) => {
    const no = e.target.closest(".tile");
    if (!no || !no.dataset.numero) return;
    if (e.target.classList.contains("t-marca")) { marcar(no.dataset.numero, e.target.checked, e.shiftKey); return; }
    abrirRamal(no.dataset.numero);
  });

  // ---------- seleção múltipla (desvios D24) ----------
  const bs = {
    barra: $("barra-selecao"), contagem: $("bs-contagem"), adicionar: $("bs-adicionar"), remover: $("bs-remover"),
    limpar: $("bs-limpar"), resultados: $("selecionar-resultados"),
  };

  function marcar(numero, marcado, intervalo) {
    if (!admin) return;
    let numeros = [numero];
    if (intervalo && ancora !== null && ancora !== numero) {
      // Intervalo sobre o resultado filtrado inteiro (lista.itens), não só sobre os cards desenhados.
      const itens = lista.itens;
      const a = itens.findIndex((it) => it.ramal.number === ancora);
      const b = itens.findIndex((it) => it.ramal.number === numero);
      if (a >= 0 && b >= 0) numeros = itens.slice(Math.min(a, b), Math.max(a, b) + 1).map((it) => it.ramal.number);
    }
    for (const n of numeros) if (marcado) selecao.add(n); else selecao.delete(n);
    ancora = numero;
    aoMudarSelecao();
  }

  function aoMudarSelecao() {
    const n = selecao.size;
    bs.barra.hidden = !admin || n === 0;
    document.body.classList.toggle("com-selecao", !bs.barra.hidden);
    bs.contagem.textContent = plural(n, "ramal selecionado", "ramais selecionados");
    renderBarra();
    renderBotaoResultados();
    lista.agendar();
  }

  // "Remover deste grupo" só com um grupo do catálogo aberto na coluna Grupos.
  function renderBarra() {
    const g = grupoRemovivel();
    bs.remover.hidden = !g;
    if (g) bs.remover.setAttribute("aria-label", "Remover os ramais selecionados do grupo " + g);
  }

  function renderBotaoResultados() {
    // Vale em qualquer escopo, inclusive "Todos os ramais" sem busca (aí são todos os ramais do servidor).
    const mostrar = admin && resultados.length > 0;
    bs.resultados.hidden = !mostrar;
    if (!mostrar) return;
    let faltam = 0;
    for (const it of resultados) if (!selecao.has(it.ramal.number)) faltam++;
    bs.resultados.disabled = faltam === 0;
    bs.resultados.textContent = faltam === 0 ? "Resultados selecionados"
      : "Selecionar todos os resultados (" + formatarNumero(resultados.length) + ")";
  }

  bs.resultados.addEventListener("click", () => {
    for (const it of resultados) selecao.add(it.ramal.number);
    aoMudarSelecao();
  });
  bs.limpar.addEventListener("click", () => {
    selecao.clear();
    ancora = null;
    aoMudarSelecao();
    lista.caixa.focus(); // a barra some: o foco não fica perdido no body
  });
  bs.adicionar.addEventListener("click", () => abrirConfirmar("add", ""));
  bs.remover.addEventListener("click", () => {
    const g = grupoRemovivel();
    if (g) abrirConfirmar("remove", g);
  });

  // ---------- confirmar e aplicar ----------
  const dm = {
    dialogo: $("dialogo-mover"), resumo: $("dm-resumo"), gruposCaixa: $("dm-grupos-caixa"), origens: $("dm-origens"),
    notas: $("dm-notas"), erro: $("dm-erro"), aplicar: $("dm-aplicar"), cancelar: $("dm-cancelar"),
  };
  const dmLista = listaDeGrupos($("dm-grupos"));
  let modo = "add";
  let grupoRemover = "";

  // Remover de um grupo, pelos dados locais: quem sai (só vínculo manual), quem continua pela faixa,
  // quem nem está no grupo e números que não estão mais no PBX (o daemon decide por eles).
  function resumirRemocao(g) {
    let saem = 0, pelaFaixa = 0, fora = 0, ausentes = 0;
    for (const n of selecao) {
      const it = dados.porNumero.get(n);
      if (!it) { ausentes++; continue; }
      const r = it.ramal;
      if ((r.range_groups || []).includes(g)) pelaFaixa++;
      else if ((r.manual_groups || []).includes(g)) saem++;
      else fora++;
    }
    return { saem, pelaFaixa, fora, ausentes };
  }

  function contarAusentes() {
    let n = 0;
    for (const x of selecao) if (!dados.porNumero.has(x)) n++;
    return n;
  }

  function abrirConfirmar(qual, g) {
    modo = qual;
    grupoRemover = g;
    const total = selecao.size;
    const notas = [];
    dm.erro.textContent = "";
    if (modo === "add") {
      dm.resumo.textContent = "Adicionar " + plural(total, "ramal", "ramais") + " aos grupos marcados.";
      dm.gruposCaixa.hidden = false;
      dmLista.preencher(nomesCatalogo());
      dm.origens.replaceChildren();
      if (!catalogo().length) notas.push("Nenhum grupo cadastrado. Crie um em Novo grupo.");
      notas.push("Os ramais continuam nos grupos em que já estão.");
      const ausentes = contarAusentes();
      if (ausentes) notas.push(plural(ausentes, "número selecionado não está", "números selecionados não estão") + " mais no PBX e vai junto (o vínculo é pelo número).");
      atualizarAplicar();
    } else {
      dm.resumo.textContent = "Remover " + plural(total, "ramal", "ramais") + " do grupo " + g + ".";
      dm.gruposCaixa.hidden = true;
      const c = resumirRemocao(g);
      dm.origens.replaceChildren(
        linhaOrigem("Saem do grupo (vínculo manual)", c.saem),
        linhaOrigem("Continuam pela faixa", c.pelaFaixa),
        linhaOrigem("Não estão no grupo", c.fora),
      );
      if (c.pelaFaixa) notas.push(plural(c.pelaFaixa, "continua", "continuam") + " no grupo pela faixa; para tirá-" + (c.pelaFaixa === 1 ? "lo" : "los") + ", edite a faixa.");
      if (c.ausentes) notas.push(plural(c.ausentes, "número selecionado não está", "números selecionados não estão") + " mais no PBX; o vínculo manual dele" + (c.ausentes === 1 ? "" : "s") + " também sai.");
      dm.aplicar.disabled = false;
    }
    dm.notas.textContent = notas.join(" ");
    dm.notas.hidden = !notas.length;
    dm.aplicar.textContent = modo === "add" ? "Adicionar" : "Remover do grupo";
    dm.aplicar.className = "botao " + (modo === "add" ? "botao-primario" : "botao-perigo");
    dm.dialogo.showModal();
    (modo === "add" && catalogo().length ? $("dm-grupos").querySelector("input") : dm.cancelar).focus();
  }

  function atualizarAplicar() {
    if (modo === "add" && !dm.dialogo.dataset.ocupado) dm.aplicar.disabled = dmLista.marcados().length === 0;
  }
  $("dm-grupos").addEventListener("change", atualizarAplicar);

  function linhaOrigem(nome, q) {
    const li = document.createElement("li");
    const n = document.createElement("span");
    n.textContent = nome;
    const v = document.createElement("b");
    v.textContent = formatarNumero(q);
    li.append(n, v);
    return li;
  }

  // Confere os tetos do assign antes de mandar; devolve a mensagem de erro ou "".
  function erroDosTetos(numeros, grupos) {
    if (numeros.length > MAX_NUMEROS) return "No máximo " + formatarNumero(MAX_NUMEROS) + " ramais por vez. Refine a seleção.";
    if (!grupos) return "";
    if (!grupos.length) return "Marque pelo menos um grupo.";
    if (grupos.length > MAX_GRUPOS) return "Escolha no máximo " + MAX_GRUPOS + " grupos por vez.";
    if (numeros.length * grupos.length > MAX_PARES) return "Ramais vezes grupos passa de " + formatarNumero(MAX_PARES) + ". Divida em partes.";
    return "";
  }

  dm.aplicar.addEventListener("click", async () => {
    const numeros = [...selecao];
    const grupos = modo === "add" ? dmLista.marcados() : null;
    const erro = erroDosTetos(numeros, grupos);
    if (erro) { dm.erro.textContent = erro; return; }
    const botoes = [dm.aplicar, dm.cancelar, bs.adicionar, bs.remover, bs.limpar, bs.resultados];
    ocupar(dm.dialogo, botoes, true);
    try {
      let texto;
      if (modo === "add") {
        const r = (await api.adicionarAGrupos(numeros, grupos)) || {};
        const n = Number.isFinite(r.changed) ? r.changed : 0;
        texto = n === 0 ? "Nenhum vínculo novo: os ramais já estavam nesses grupos." : plural(n, "vínculo novo gravado", "vínculos novos gravados") + ".";
      } else {
        const g = grupoRemover;
        const r = (await api.removerDoGrupo(numeros, g)) || {};
        const n = Number.isFinite(r.changed) ? r.changed : 0;
        const sem = Number.isFinite(r.not_manual) ? r.not_manual : 0;
        texto = (n === 0 ? "Nenhum vínculo manual removido de " + g : plural(n, "vínculo manual removido", "vínculos manuais removidos") + " de " + g)
          + (sem ? "; " + plural(sem, "ramal sem vínculo manual", "ramais sem vínculo manual") + " (pela faixa ou fora do grupo)." : ".");
      }
      delete dm.dialogo.dataset.ocupado;
      dm.dialogo.close();
      selecao.clear();
      ancora = null;
      dmLista.limpar();
      avisar(texto);
    } catch (e) {
      tratarErro(e, dm.erro); // seleção mantida para tentar de novo
    } finally {
      ocupar(dm.dialogo, botoes, false);
      atualizarAplicar();
      aoMudarSelecao();
    }
  });

  return {
    abrirRamal,
    fecharDetalhe,
    definirAdmin(sim) {
      admin = sim;
      lista.marcavel = sim;
      $("botao-novo-grupo").hidden = !sim;
      if (!sim) { selecao.clear(); ancora = null; }
      aoMudarSelecao();
      if (detalheAberto()) renderRamal();
    },
    // Tique de 1 s: cronômetro da linha "Chamada" do detalhe.
    tique() {
      const item = detalheAberto() && dados.porNumero.get(numeroAberto);
      if (item) renderLinha(item);
    },
    // Saída do painel (sessão encerrada): detalhe e seleção não passam para o próximo usuário.
    parar() {
      fecharDetalhe();
      drLista.limpar();
      selecao.clear();
      ancora = null;
      aoMudarSelecao();
    },
    aoMudarCatalogo() {
      if (dm.dialogo.open && modo === "add") { dmLista.preencher(nomesCatalogo()); atualizarAplicar(); }
      if (detalheAberto()) renderRamal();
      if (ng.dialogo.open && ng.nome.value.trim()) { ng.nomeErro.textContent = erroNomeGrupo(ng.nome.value, catalogo()); previaCriar(); }
    },
    // Dados ou grupo aberto mudaram: detalhe e o botão "Remover deste grupo" da barra.
    aoMudarDados() {
      if (detalheAberto()) renderRamal();
      renderBarra();
    },
    // Resultado completo de filtrar() sobre todos os dados (não só os cards desenhados).
    aoFiltrar(filtrados) {
      resultados = filtrados;
      renderBotaoResultados();
    },
  };
}
