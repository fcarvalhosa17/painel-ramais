// Área "Grupos" (C6): só aparece com sessão de administrador. Listar, importar CSV e remover.
// Depois de gravar, o daemon troca o epoch e a tela recarrega sozinha pelo reload do /ws.

import { api } from "./conexao.js";

const $ = (id) => document.getElementById(id);
const LIMITE_CORPO = 1024 * 1024; // o daemon aceita até 1 MiB no corpo JSON (C6); acima disso, 413

// Tamanho do corpo exatamente como sai no POST: o JSON escapa aspas, barras e quebras de linha do CSV.
export const tamanhoCorpo = (csv, replace) => new TextEncoder().encode(JSON.stringify({ csv, replace })).length;

function faixasTexto(ranges) {
  if (!ranges || !ranges.length) return "sem faixa";
  return ranges.map((r) => (r.start === r.end ? String(r.start) : r.start + "-" + r.end)).join(", ");
}

export function iniciarGrupos(aoSessaoEncerrada) {
  const dialogo = $("dialogo-grupos");
  const lista = $("dg-lista");
  const estado = $("dg-lista-estado");
  const erro = $("dg-erro");
  const ok = $("dg-ok");
  const texto = $("dg-texto");
  const botao = $("dg-importar");

  const tratar = (e, alvo) => {
    if (e.status === 401) { dialogo.close(); aoSessaoEncerrada(); return; }
    alvo.textContent = e.message;
  };

  async function carregar() {
    estado.hidden = false;
    estado.textContent = "Carregando grupos...";
    lista.replaceChildren();
    try {
      const grupos = await api.grupos();
      const cadastrados = grupos.filter((g) => g.name !== "Sem grupo");
      estado.hidden = cadastrados.length > 0;
      estado.textContent = "Nenhum grupo cadastrado. Importe um CSV para criar.";
      for (const g of cadastrados) lista.append(linhaGrupo(g));
    } catch (e) {
      tratar(e, estado);
    }
  }

  function linhaGrupo(g) {
    const li = document.createElement("li");
    li.className = "dg-item";
    const info = document.createElement("div");
    const nome = document.createElement("strong");
    nome.textContent = g.name;
    const det = document.createElement("span");
    det.className = "dg-det";
    det.textContent = faixasTexto(g.ranges) + " · " + g.extensions.length + " ramais";
    info.append(nome, det);
    const acoes = document.createElement("div");
    acoes.className = "dg-acoes";
    const remover = botaoTexto("Remover", "botao botao-fantasma");
    remover.setAttribute("aria-label", "Remover o grupo " + g.name);
    remover.addEventListener("click", () => {
      const confirmar = botaoTexto("Confirmar remoção", "botao botao-perigo");
      const cancelar = botaoTexto("Cancelar", "botao botao-fantasma");
      cancelar.addEventListener("click", () => { acoes.replaceChildren(remover); remover.focus(); });
      confirmar.addEventListener("click", async () => {
        confirmar.disabled = cancelar.disabled = true;
        confirmar.textContent = "Removendo...";
        try {
          await api.removerGrupo(g.name);
          ok.textContent = "Grupo " + g.name + " removido.";
          // Tira da lista já: o GET /api/groups só reflete a remoção depois do epoch novo (aoMudarEpoch relê).
          li.remove();
          if (!lista.children.length) { estado.hidden = false; estado.textContent = "Nenhum grupo cadastrado. Importe um CSV para criar."; }
        } catch (e) {
          tratar(e, erro);
          acoes.replaceChildren(remover);
        }
      });
      acoes.replaceChildren(confirmar, cancelar);
      cancelar.focus();
    });
    acoes.append(remover);
    li.append(info, acoes);
    return li;
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
      ok.textContent = r.rows + (r.rows === 1 ? " linha importada." : " linhas importadas.") + " A tela recarrega com os grupos novos.";
      texto.value = "";
      $("dg-arquivo").value = "";
      await carregar();
    } catch (err) {
      tratar(err, erro);
    } finally {
      botao.disabled = false;
      botao.textContent = "Importar grupos";
    }
  });

  $("dg-fechar").addEventListener("click", () => dialogo.close());

  return {
    abrir() {
      erro.textContent = "";
      ok.textContent = "";
      dialogo.showModal();
      carregar();
    },
    // A gravação troca o epoch logo depois de responder: a lista é relida quando o snapshot novo chega.
    aoMudarEpoch() {
      if (dialogo.open) carregar();
    },
  };
}

function botaoTexto(rotulo, classe) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = classe;
  b.textContent = rotulo;
  return b;
}
