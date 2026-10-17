// Edição de grupos pela tela principal: criar grupo, painel do ramal, seleção múltipla e mover ramais.
// Controles de escrita só aparecem para administrador (cosmético: o daemon decide e responde 403).
// A seleção é um Set de números fora do DOM (lista.selecao): os cards reciclados da rolagem virtual
// só refletem o Set em preencher(), então 10.000 selecionados não custam nada no DOM.

import { api } from "./conexao.js";
import {
  ROTULO, SEM_GRUPO, compararGrupos, formatarNumero, lerFaixas, conflitoDeFaixa, contarComFaixas, erroNomeGrupo, linhaRamal,
} from "./estado.js";

const $ = (id) => document.getElementById(id);
const MAX_MOVER = 20000; // teto do /api/groups/assign
const ORIGENS_VISIVEIS = 5;
const plural = (n, um, varios) => formatarNumero(n) + " " + (n === 1 ? um : varios);
const TRACO = String.fromCharCode(0x2014); // dado ausente: "—" como no design (decorate)

function opcao(texto, valor, acao) {
  const o = document.createElement("option");
  o.textContent = texto;
  o.value = valor;
  if (acao) o.dataset.acao = acao;
  return o;
}

export function iniciarEdicao({ dados, lista, aoSessaoEncerrada }) {
  const selecao = lista.selecao;
  let admin = false;
  let resultados = [];
  let ancora = null;

  const catalogo = () => dados.grupos.filter((g) => g.name !== SEM_GRUPO);

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

  // Lista de destinos: grupos do catálogo (inclui vazios), "Sem grupo" e ações. Mantém a escolha anterior.
  function preencherDestinos(sel, lote) {
    const antes = sel.selectedOptions[0];
    const chave = antes ? (antes.dataset.acao || "") + "\n" + antes.value : "";
    const ops = [];
    if (lote) ops.push(opcao("Escolha o destino", "", "nada"));
    for (const nome of catalogo().map((g) => g.name).sort(compararGrupos)) ops.push(opcao(nome, nome));
    ops.push(opcao(SEM_GRUPO, SEM_GRUPO));
    ops.push(opcao("+ Novo grupo", "", "novo"));
    if (lote) ops.push(opcao("Voltar para a faixa", "", "faixa"));
    sel.replaceChildren(...ops);
    const igual = ops.find((o) => (o.dataset.acao || "") + "\n" + o.value === chave);
    if (igual) igual.selected = true;
  }

  function selecionarGrupoNaLista(sel, nome) {
    const o = [...sel.options].find((x) => !x.dataset.acao && x.value === nome);
    if (o) o.selected = true;
  }

  // ---------- criar grupo ----------
  const ng = {
    dialogo: $("dialogo-novo-grupo"), form: $("ng-form"), nome: $("ng-nome"), faixas: $("ng-faixas"),
    nomeErro: $("ng-nome-erro"), faixasErro: $("ng-faixas-erro"), previa: $("ng-previa"), erro: $("ng-erro"),
    contexto: $("ng-contexto"), criar: $("ng-criar"),
  };
  let depoisDeCriar = null;

  function abrirCriar(contexto = "", depois = null) {
    depoisDeCriar = depois;
    ng.form.reset();
    for (const el of [ng.nomeErro, ng.faixasErro, ng.erro]) el.textContent = "";
    ng.contexto.textContent = contexto;
    ng.contexto.hidden = !contexto;
    previaCriar();
    ng.dialogo.showModal();
    ng.nome.focus();
  }

  function lerFaixasCriar() {
    const lidas = lerFaixas(ng.faixas.value);
    return { faixas: lidas.faixas, erro: lidas.erro || conflitoDeFaixa(lidas.faixas, catalogo()) };
  }

  function previaCriar() {
    const { faixas, erro } = lerFaixasCriar();
    ng.faixasErro.textContent = erro;
    if (erro) { ng.previa.textContent = ""; return; }
    if (!faixas.length) { ng.previa.textContent = "Sem faixa, o grupo começa vazio e recebe ramais movidos à mão."; return; }
    const c = contarComFaixas(dados.itens, faixas, null);
    let t = plural(c.pelaFaixa, "ramal existente entra", "ramais existentes entram") + " no grupo.";
    if (c.manuaisNaFaixa) t += " " + plural(c.manuaisNaFaixa, "ramal da faixa tem grupo escolhido à mão e fica onde está.", "ramais da faixa têm grupo escolhido à mão e ficam onde estão.");
    ng.previa.textContent = t;
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
    const { faixas, erro: erroFaixas } = lerFaixasCriar();
    ng.nomeErro.textContent = erroNome;
    ng.faixasErro.textContent = erroFaixas;
    ng.erro.textContent = "";
    if (erroNome) { ng.nome.focus(); return; }
    if (erroFaixas) { ng.faixas.focus(); return; }
    ocupar(ng.dialogo, [ng.criar], true);
    ng.criar.textContent = "Criando...";
    try {
      const r = await api.criarGrupo(nome, faixas);
      const criado = (r && r.name) || nome;
      delete ng.dialogo.dataset.ocupado;
      ng.dialogo.close();
      avisar("Grupo " + criado + " criado.");
      if (depoisDeCriar) depoisDeCriar(criado);
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
    estadoTexto: $("dr-estado-texto"), grupo: $("dr-grupo"), origem: $("dr-origem"), chamada: $("dr-chamada"),
    ip: $("dr-ip"), ua: $("dr-ua"), latencia: $("dr-latencia"), admin: $("dr-admin"), destino: $("dr-destino"),
    mover: $("dr-mover"), faixa: $("dr-faixa"), erro: $("dr-erro"), ok: $("dr-ok"), fechar: $("dr-fechar"),
  };
  let numeroAberto = null;
  let ocupadoRamal = false;
  const detalheAberto = () => !dr.painel.hidden;

  function abrirRamal(numero) {
    numeroAberto = numero;
    dr.erro.textContent = "";
    dr.ok.textContent = "";
    preencherDestinos(dr.destino, false);
    dr.painel.hidden = false;
    renderRamal(true);
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

  // escolherAtual: põe o grupo atual no seletor (ao abrir e quando o catálogo muda).
  function renderRamal(escolherAtual) {
    const item = dados.porNumero.get(numeroAberto);
    dr.num.textContent = numeroAberto;
    dr.sumiu.hidden = !!item;
    dr.admin.hidden = !admin || !item;
    if (!item) return;
    const r = item.ramal;
    dr.nome.textContent = r.name || "sem nome";
    dr.estado.dataset.estado = item.estado;
    dr.estadoTexto.textContent = ROTULO[item.estado].toLocaleUpperCase("pt-BR");
    dr.grupo.textContent = r.group;
    dr.origem.textContent = r.group_manual ? "(escolhido à mão)" : r.group === SEM_GRUPO ? "(nenhuma faixa)" : "(pela faixa)";
    renderLinha(item);
    dr.ip.textContent = r.ip || TRACO;
    dr.ua.textContent = item.estado === "nao_registrado" || r.user_agent == null ? TRACO : r.user_agent;
    dr.latencia.textContent = textoLatencia(item);
    dr.faixa.hidden = !r.group_manual;
    if (escolherAtual) selecionarGrupoNaLista(dr.destino, r.group);
    atualizarMoverRamal();
  }

  function atualizarMoverRamal() {
    if (ocupadoRamal) return;
    const item = dados.porNumero.get(numeroAberto);
    const op = dr.destino.selectedOptions[0];
    dr.mover.disabled = !item || !op || (!op.dataset.acao && op.value === item.ramal.group);
  }

  async function moverUm(grupo) {
    const numero = numeroAberto;
    dr.erro.textContent = "";
    dr.ok.textContent = "";
    const botoes = [dr.mover, dr.faixa, dr.destino];
    ocupadoRamal = true;
    for (const b of botoes) b.disabled = true;
    try {
      await api.moverRamais([numero], grupo);
      if (numeroAberto === numero) {
        dr.ok.textContent = grupo === null ? "Ramal " + numero + " voltou para a faixa." : "Ramal " + numero + " movido para " + grupo + ".";
      }
    } catch (e) {
      tratarErro(e, dr.erro);
    } finally {
      ocupadoRamal = false;
      for (const b of botoes) b.disabled = false;
      atualizarMoverRamal();
    }
  }

  dr.destino.addEventListener("change", atualizarMoverRamal);
  dr.mover.addEventListener("click", () => {
    const op = dr.destino.selectedOptions[0];
    if (!op) return;
    if (op.dataset.acao === "novo") {
      const numero = numeroAberto;
      abrirCriar("Depois de criar, o ramal " + numero + " vai para o grupo novo.", (nome) => {
        if (detalheAberto() && numeroAberto === numero) moverUm(nome);
      });
      return;
    }
    moverUm(op.value);
  });
  dr.faixa.addEventListener("click", () => moverUm(null));

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

  // ---------- seleção múltipla ----------
  const bs = {
    barra: $("barra-selecao"), contagem: $("bs-contagem"), destino: $("bs-destino"), mover: $("bs-mover"),
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
    renderBotaoResultados();
    lista.agendar();
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
  bs.destino.addEventListener("change", () => {
    const op = bs.destino.selectedOptions[0];
    bs.mover.disabled = !op || op.dataset.acao === "nada";
  });
  bs.mover.addEventListener("click", () => {
    const op = bs.destino.selectedOptions[0];
    if (!op || op.dataset.acao === "nada") { bs.destino.focus(); return; }
    if (op.dataset.acao === "novo") {
      abrirCriar("Depois de criar, você confirma a mudança dos " + plural(selecao.size, "ramal selecionado.", "ramais selecionados."), (nome) => abrirConfirmar(nome));
      return;
    }
    abrirConfirmar(op.dataset.acao === "faixa" ? null : op.value);
  });

  // ---------- confirmar e aplicar ----------
  const dm = {
    dialogo: $("dialogo-mover"), resumo: $("dm-resumo"), origensTitulo: $("dm-origens-titulo"), origens: $("dm-origens"),
    notas: $("dm-notas"), erro: $("dm-erro"), aplicar: $("dm-aplicar"), cancelar: $("dm-cancelar"),
  };
  let destinoMover = null;

  // Resumo pelos dados locais: quem já está no destino, de onde saem os outros e números que sumiram do PBX.
  function resumir(destino) {
    const origens = new Map();
    let ja = 0, ausentes = 0, pelaFaixa = 0;
    for (const n of selecao) {
      const it = dados.porNumero.get(n);
      if (!it) { ausentes++; continue; }
      const r = it.ramal;
      if (destino === null ? !r.group_manual : r.group === destino && r.group_manual) { ja++; continue; }
      // No grupo de destino só pela faixa: não sai de grupo nenhum, passa a ficar fixo nele.
      if (r.group === destino) { pelaFaixa++; continue; }
      origens.set(r.group, (origens.get(r.group) || 0) + 1);
    }
    return { origens, ja, ausentes, pelaFaixa };
  }

  function abrirConfirmar(destino) {
    destinoMover = destino;
    const total = selecao.size;
    const { origens, ja, ausentes, pelaFaixa } = resumir(destino);
    let saem = 0;
    for (const q of origens.values()) saem += q;
    dm.resumo.textContent = destino === null
      ? "Devolver " + plural(total, "ramal", "ramais") + " para a faixa."
      : "Mover " + plural(total, "ramal", "ramais") + " para o grupo " + destino + ".";
    dm.origensTitulo.hidden = saem === 0;
    dm.origensTitulo.textContent = (destino === null ? "Saem do grupo escolhido à mão: " : "Saem de outro grupo: ") + formatarNumero(saem);
    const linhas = [...origens].sort((a, b) => b[1] - a[1] || compararGrupos(a[0], b[0]));
    const visiveis = linhas.length > ORIGENS_VISIVEIS + 1 ? linhas.slice(0, ORIGENS_VISIVEIS) : linhas;
    const itens = visiveis.map(([nome, q]) => linhaOrigem(nome, q));
    if (visiveis.length < linhas.length) {
      const resto = linhas.slice(visiveis.length);
      itens.push(linhaOrigem("Outros " + resto.length + " grupos", resto.reduce((s, [, q]) => s + q, 0)));
    }
    dm.origens.replaceChildren(...itens);
    const notas = [];
    if (ja) notas.push(destino === null ? plural(ja, "já segue a faixa e não muda.", "já seguem a faixa e não mudam.")
      : plural(ja, "já está", "já estão") + " no grupo " + destino + ".");
    if (pelaFaixa) notas.push(plural(pelaFaixa, "está em " + destino + " pela faixa e passa", "estão em " + destino + " pela faixa e passam") + " a ficar fixo" + (pelaFaixa === 1 ? "" : "s") + " no grupo.");
    if (ausentes) notas.push(plural(ausentes, "número selecionado não está", "números selecionados não estão") + " mais no PBX e vai junto (o vínculo é pelo número).");
    dm.notas.textContent = notas.join(" ");
    dm.notas.hidden = !notas.length;
    dm.erro.textContent = "";
    dm.aplicar.textContent = destino === null ? "Devolver para a faixa" : "Mover " + plural(total, "ramal", "ramais");
    dm.dialogo.showModal();
    dm.cancelar.focus();
  }

  function linhaOrigem(nome, q) {
    const li = document.createElement("li");
    const n = document.createElement("span");
    n.textContent = nome;
    const v = document.createElement("b");
    v.textContent = formatarNumero(q);
    li.append(n, v);
    return li;
  }

  dm.aplicar.addEventListener("click", async () => {
    const numeros = [...selecao];
    if (numeros.length > MAX_MOVER) {
      dm.erro.textContent = "No máximo " + formatarNumero(MAX_MOVER) + " ramais por vez. Refine a seleção.";
      return;
    }
    const botoes = [dm.aplicar, dm.cancelar, bs.mover, bs.limpar, bs.destino, bs.resultados];
    ocupar(dm.dialogo, botoes, true);
    try {
      const r = await api.moverRamais(numeros, destinoMover);
      const mudaram = r && Number.isFinite(r.changed) ? r.changed : numeros.length;
      delete dm.dialogo.dataset.ocupado;
      dm.dialogo.close();
      selecao.clear();
      ancora = null;
      avisar(mudaram === 0 ? "Nenhum ramal mudou de grupo." : plural(mudaram, "ramal mudou", "ramais mudaram") + " de grupo.");
    } catch (e) {
      tratarErro(e, dm.erro); // seleção mantida para tentar de novo
    } finally {
      ocupar(dm.dialogo, botoes, false);
      bs.destino.dispatchEvent(new Event("change")); // devolve o estado certo ao "Mover para grupo..."
      aoMudarSelecao();
    }
  });

  preencherDestinos(bs.destino, true);

  return {
    abrirCriar,
    abrirRamal,
    fecharDetalhe,
    definirAdmin(sim) {
      admin = sim;
      lista.marcavel = sim;
      $("botao-novo-grupo").hidden = !sim;
      if (!sim) { selecao.clear(); ancora = null; }
      aoMudarSelecao();
      if (detalheAberto()) renderRamal(false);
    },
    // Tique de 1 s: cronômetro da linha "Chamada" do detalhe.
    tique() {
      const item = detalheAberto() && dados.porNumero.get(numeroAberto);
      if (item) renderLinha(item);
    },
    // Saída do painel (sessão encerrada): detalhe e seleção não passam para o próximo usuário.
    parar() {
      fecharDetalhe();
      selecao.clear();
      ancora = null;
      aoMudarSelecao();
    },
    aoMudarCatalogo() {
      preencherDestinos(bs.destino, true);
      bs.destino.dispatchEvent(new Event("change"));
      if (detalheAberto()) { preencherDestinos(dr.destino, false); renderRamal(true); }
      if (ng.dialogo.open && ng.nome.value.trim()) { ng.nomeErro.textContent = erroNomeGrupo(ng.nome.value, catalogo()); previaCriar(); }
    },
    aoMudarDados() {
      if (detalheAberto()) renderRamal(false);
    },
    // Resultado completo de filtrar() sobre todos os dados (não só os cards desenhados).
    aoFiltrar(filtrados) {
      resultados = filtrados;
      renderBotaoResultados();
    },
  };
}
