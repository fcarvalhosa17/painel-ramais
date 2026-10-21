// Seleção de visões (o "grupo" do contrato se chama visão na tela). A regra fica em proximaSelecao (estado.js) e vale
// nos três lugares: coluna Visões da aba Ramais e os filtros de visão das abas Registros e Chamadas.

import { MAX_VISOES, SEM_GRUPO, compararGrupos, proximaSelecao, rotuloVisao, rotuloVisoes, TODOS_RAMAIS } from "./estado.js";

// Aviso de teto no rodapé (#aviso-status, o mesmo dos avisos de gravação), em laranja, por 5 s.
let timerAviso = null;
function avisarLimite() {
  const el = document.getElementById("aviso-status");
  el.dataset.tipo = "limite";
  el.textContent = "Máximo de " + MAX_VISOES + " visões selecionadas.";
  clearTimeout(timerAviso);
  timerAviso = setTimeout(() => { el.textContent = ""; delete el.dataset.tipo; }, 5000);
}

// Liga um botão de visão: clique simples escolhe só ela; Ctrl ou Cmd (clique, Enter ou Espaço) soma ou tira.
// O clique gerado pelo teclado nem sempre traz a tecla modificadora: ela é lida no keydown.
// atual(): seleção de agora; aoMudar(novas, somar) só roda quando a seleção muda (o teto de visões avisa e para).
export function ligarItemVisao(b, nome, atual, aoMudar) {
  let somarTecla = false;
  b.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") somarTecla = e.ctrlKey || e.metaKey; });
  b.addEventListener("click", (e) => {
    const somar = e.ctrlKey || e.metaKey || (e.detail === 0 && somarTecla);
    somarTecla = false;
    const novas = proximaSelecao(atual(), nome, somar);
    if (!novas) { avisarLimite(); return; }
    aoMudar(novas, somar);
  });
}

// Filtro de visões em lista suspensa (<details>): raiz tem o <summary> com .visoes-rotulo e a caixa .visoes-lista.
// aoMudar(visoes) só roda por ação do usuário; preencher() devolve true quando tirou visão que saiu do catálogo.
export function seletorVisoes(raiz, aoMudar) {
  const rotulo = raiz.querySelector(".visoes-rotulo");
  const resumo = raiz.querySelector("summary");
  const caixa = raiz.querySelector(".visoes-lista");
  const botoes = new Map(); // nome ("" = todos os ramais) -> botão
  let visoes = [];
  let chave = null;

  function marcar() {
    for (const [nome, b] of botoes) b.setAttribute("aria-pressed", String(nome ? visoes.includes(nome) : !visoes.length));
    rotulo.textContent = rotuloVisoes(visoes);
    resumo.title = visoes.map(rotuloVisao).join(", ");
  }

  function criar(nome) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "visoes-item";
    b.textContent = nome ? rotuloVisao(nome) : TODOS_RAMAIS;
    ligarItemVisao(b, nome, () => visoes, (novas, somar) => {
      visoes = novas;
      marcar();
      // Clique simples fecha a lista; com Ctrl ou Cmd ela fica aberta para somar mais.
      if (!somar) { raiz.open = false; resumo.focus(); }
      aoMudar(visoes);
    });
    botoes.set(nome, b);
    return b;
  }

  document.addEventListener("click", (e) => { if (raiz.open && !raiz.contains(e.target)) raiz.open = false; });
  raiz.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && raiz.open) { raiz.open = false; resumo.focus(); }
  });

  return {
    get visoes() { return visoes; },
    definir(v) { visoes = v; marcar(); },
    // nomes: catálogo sem "Sem grupo". Os botões só são refeitos quando a lista muda.
    preencher(nomes) {
      const lista = ["", ...nomes.filter((n) => n !== SEM_GRUPO).sort(compararGrupos), SEM_GRUPO];
      const k = lista.join("\n");
      if (k !== chave) {
        chave = k;
        botoes.clear();
        caixa.replaceChildren(...lista.map(criar));
      }
      const validas = visoes.filter((v) => lista.includes(v));
      const mudou = validas.length !== visoes.length;
      visoes = validas;
      marcar();
      return mudou;
    },
  };
}
