// "Gerenciar grupos" (C6): só aparece com sessão de administrador (o daemon decide e responde 403).
// Lista o catálogo do snapshot/delta (inclui grupos vazios), renomeia, edita faixas, exclui e importa CSV.
// Criar, editar, excluir e importar chegam por delta, sem epoch novo. A lista é redesenhada quando o catálogo
// muda de fato (aoMudarCatalogo), nunca por um GET logo depois do POST.

import { api } from "./conexao.js";
import {
  SEM_GRUPO, formatarNumero, textoFaixas, lerFaixas, contarComFaixas, erroNomeGrupo, gruposDe,
} from "./estado.js";

const $ = (id) => document.getElementById(id);
const LIMITE_CORPO = 1024 * 1024; // o daemon aceita até 1 MiB no corpo JSON do import (C6); acima disso, 413
const VAZIO = "Nenhuma visão cadastrada. Use Nova visão ou importe um CSV.";
const plural = (n, um, varios) => formatarNumero(n) + " " + (n === 1 ? um : varios);

// Tamanho do corpo exatamente como sai no POST: o JSON escapa aspas, barras e quebras de linha do CSV.
export const tamanhoCorpo = (csv, replace) => new TextEncoder().encode(JSON.stringify({ csv, replace })).length;

function botaoTexto(rotulo, classe) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = classe;
  b.textContent = rotulo;
  return b;
}

function campoTexto(rotulo, valor, extra) {
  const label = document.createElement("label");
  label.className = "campo";
  const span = document.createElement("span");
  span.className = "campo-rotulo";
  span.textContent = rotulo;
  const input = document.createElement("input");
  input.type = "text";
  input.value = valor;
  input.autocomplete = "off";
  input.spellcheck = false;
  Object.assign(input, extra);
  label.append(span, input);
  return { label, input };
}

function paragrafo(classe, viva) {
  const p = document.createElement("p");
  p.className = classe;
  if (viva) p.setAttribute("aria-live", "polite");
  return p;
}

export function iniciarGrupos({ dados, aoSessaoEncerrada }) {
  const dialogo = $("dialogo-grupos");
  const lista = $("dg-lista");
  const estado = $("dg-lista-estado");
  const okLista = $("dg-ok-lista");
  const erro = $("dg-erro");
  const ok = $("dg-ok");
  const texto = $("dg-texto");
  const botao = $("dg-importar");
  const contadores = new Map(); // nome -> { det, g }

  const catalogo = () => dados.grupos.filter((g) => g.name !== SEM_GRUPO)
    .sort((a, b) => a.name.localeCompare(b.name, "pt-BR"));

  // Ramal em vários grupos conta em cada um (N:N).
  function contarPorGrupo() {
    const m = new Map();
    for (const it of dados.itens) for (const nome of gruposDe(it.ramal)) m.set(nome, (m.get(nome) || 0) + 1);
    return m;
  }

  const detalhe = (g, n) => (textoFaixas(g.ranges) || "sem faixa") + " · " + plural(n, "ramal", "ramais");

  const tratar = (e, alvo) => {
    if (e.status === 401) { dialogo.close(); aoSessaoEncerrada(); return; }
    alvo.textContent = e.message;
  };

  // ponytail: redesenha a lista inteira quando o catálogo muda; uma edição em andamento noutra linha se perde
  // (só acontece com dois administradores ao mesmo tempo). Chavear por nome se isso incomodar.
  function render() {
    const cat = catalogo();
    const porGrupo = contarPorGrupo();
    contadores.clear();
    lista.replaceChildren(...cat.map((g) => linhaGrupo(g, porGrupo.get(g.name) || 0)));
    estado.hidden = cat.length > 0;
    estado.textContent = VAZIO;
  }

  function atualizarContagens() {
    if (!dialogo.open || !contadores.size) return;
    const porGrupo = contarPorGrupo();
    for (const [nome, c] of contadores) c.det.textContent = detalhe(c.g, porGrupo.get(nome) || 0);
  }

  function linhaGrupo(g, n) {
    const li = document.createElement("li");
    li.className = "dg-item";
    const info = document.createElement("div");
    info.className = "dg-info";
    const nome = document.createElement("strong");
    nome.textContent = g.name;
    const det = document.createElement("span");
    det.className = "dg-det";
    det.textContent = detalhe(g, n);
    info.append(nome, det);
    contadores.set(g.name, { det, g });
    const acoes = document.createElement("div");
    acoes.className = "dg-acoes";
    const editar = botaoTexto("Editar", "botao");
    editar.setAttribute("aria-label", "Editar a visão " + g.name);
    const remover = botaoTexto("Excluir", "botao");
    remover.setAttribute("aria-label", "Excluir a visão " + g.name);
    acoes.append(editar, remover);
    li.append(info, acoes);

    editar.addEventListener("click", () => abrirEdicao(li, g, () => { li.replaceChildren(info, acoes); editar.focus(); }));

    remover.addEventListener("click", () => {
      const qtd = contarPorGrupo().get(g.name) || 0;
      const aviso = paragrafo("dg-aviso");
      // CONTRATOS 4.6 (delete): cada ramal sai só deste grupo; quem fica sem nenhum vai para "Sem grupo".
      aviso.textContent = qtd
        ? plural(qtd, "ramal sai", "ramais saem") + " só desta visão; quem ficar sem nenhuma visão vai para \"Sem visão\"."
        : "A visão está vazia.";
      const confirmar = botaoTexto("Confirmar exclusão", "botao botao-perigo");
      const cancelar = botaoTexto("Cancelar", "botao");
      const restaurar = () => { aviso.remove(); acoes.replaceChildren(editar, remover); };
      cancelar.addEventListener("click", () => { restaurar(); remover.focus(); });
      confirmar.addEventListener("click", async () => {
        confirmar.disabled = cancelar.disabled = true;
        confirmar.textContent = "Excluindo...";
        try {
          await api.removerGrupo(g.name);
          okLista.textContent = "Visão " + g.name + " excluída.";
          // Tira da lista já, pelo resultado do POST; o catálogo novo chega pelo delta (ou snapshot) e redesenha.
          li.remove();
          contadores.delete(g.name);
          if (!lista.children.length) { estado.hidden = false; estado.textContent = VAZIO; }
        } catch (e) {
          tratar(e, erro);
          restaurar();
        }
      });
      info.append(aviso);
      acoes.replaceChildren(confirmar, cancelar);
      cancelar.focus();
    });
    return li;
  }

  // Renomear e editar faixas no mesmo formulário, com a contagem prévia pelos dados carregados.
  function abrirEdicao(li, g, cancelarEdicao) {
    const form = document.createElement("form");
    form.className = "dg-editar";
    form.noValidate = true;
    const nome = campoTexto("Nome", g.name, { maxLength: 64, required: true });
    const nomeErro = paragrafo("campo-erro", true);
    const faixas = campoTexto("Faixas e ramais avulsos", textoFaixas(g.ranges), { placeholder: "2000-2099, 3001" });
    const faixasErro = paragrafo("campo-erro", true);
    const previa = paragrafo("previa", true);
    const erroForm = paragrafo("campo-erro");
    erroForm.setAttribute("role", "alert");
    const botoes = document.createElement("div");
    botoes.className = "dialogo-botoes";
    const salvar = document.createElement("button");
    salvar.type = "submit";
    salvar.className = "botao botao-primario";
    salvar.textContent = "Salvar";
    const cancelar = botaoTexto("Cancelar", "botao");
    botoes.append(salvar, cancelar);
    form.append(nome.label, nomeErro, faixas.label, faixasErro, previa, erroForm, botoes);

    const ler = () => {
      const lidas = lerFaixas(faixas.input.value);
      return { faixas: lidas.faixas, erro: lidas.erro };
    };
    const atualizarPrevia = () => {
      const { faixas: fx, erro: e } = ler();
      faixasErro.textContent = e;
      if (e) { previa.textContent = ""; return; }
      const c = contarComFaixas(dados.itens, fx, g.name);
      const hoje = contarPorGrupo().get(g.name) || 0;
      previa.textContent = "Com essas faixas a visão fica com " + plural(c.total, "ramal", "ramais") + " (hoje " + formatarNumero(hoje) + ").";
    };
    faixas.input.addEventListener("input", atualizarPrevia);
    nome.input.addEventListener("input", () => {
      nomeErro.textContent = nome.input.value.trim() ? erroNomeGrupo(nome.input.value, catalogo(), g.name) : "";
    });
    cancelar.addEventListener("click", cancelarEdicao);

    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const novoNome = nome.input.value.trim();
      const eNome = erroNomeGrupo(novoNome, catalogo(), g.name);
      const { faixas: fx, erro: eFaixas } = ler();
      nomeErro.textContent = eNome;
      faixasErro.textContent = eFaixas;
      erroForm.textContent = "";
      if (eNome) { nome.input.focus(); return; }
      if (eFaixas) { faixas.input.focus(); return; }
      const mudancas = { ranges: fx };
      if (novoNome !== g.name) mudancas.new_name = novoNome;
      salvar.disabled = cancelar.disabled = true;
      salvar.textContent = "Salvando...";
      try {
        const r = await api.editarGrupo(g.name, mudancas);
        const final = (r && r.name) || novoNome;
        okLista.textContent = "Visão " + final + " atualizada.";
        // Mostra o resultado do POST já; o delta com o catálogo novo redesenha a lista logo depois.
        const c = contarComFaixas(dados.itens, fx, g.name);
        li.replaceWith(linhaGrupo({ name: final, ranges: fx }, c.total));
        if (final !== g.name) contadores.delete(g.name);
      } catch (err) {
        tratar(err, erroForm);
        salvar.disabled = cancelar.disabled = false;
        salvar.textContent = "Salvar";
      }
    });

    li.replaceChildren(form);
    atualizarPrevia();
    nome.input.focus();
  }

  $("dg-arquivo").addEventListener("change", async (e) => {
    const arq = e.target.files && e.target.files[0];
    if (!arq) return;
    if (arq.size > LIMITE_CORPO) { erro.textContent = "Arquivo grande demais: o limite é 1 MiB."; return; }
    texto.value = await arq.text();
    erro.textContent = "";
  });

  $("dg-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    erro.textContent = "";
    ok.textContent = "";
    const csv = texto.value;
    if (!csv.trim()) { erro.textContent = "Escolha um arquivo ou cole o conteúdo do CSV."; texto.focus(); return; }
    const substituir = $("dg-substituir").checked;
    if (tamanhoCorpo(csv, substituir) > LIMITE_CORPO) { erro.textContent = "Conteúdo grande demais: o limite é 1 MiB."; return; }
    botao.disabled = true;
    botao.textContent = "Importando...";
    try {
      const r = await api.importarGrupos(csv, substituir);
      // rows = linhas lidas; ignored = linhas que não mudaram nada (repetidas, já gravadas, "Sem grupo,N").
      const ignoradas = Number.isFinite(r.ignored) ? r.ignored : 0;
      ok.textContent = plural(r.rows, "linha lida", "linhas lidas")
        + (ignoradas ? ", " + plural(ignoradas, "sem mudança", "sem mudança") + " (repetida, já gravada ou de ramal sem visão)." : ".");
      texto.value = "";
      $("dg-arquivo").value = "";
    } catch (err) {
      tratar(err, erro);
    } finally {
      botao.disabled = false;
      botao.textContent = "Importar visões";
    }
  });

  $("dg-fechar").addEventListener("click", () => dialogo.close());

  return {
    abrir() {
      erro.textContent = "";
      ok.textContent = "";
      okLista.textContent = "";
      render();
      dialogo.showModal();
    },
    aoMudarCatalogo() {
      if (dialogo.open) render();
    },
    aoMudarDados() {
      atualizarContagens();
    },
  };
}
