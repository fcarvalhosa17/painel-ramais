## ADR-006: Ramal em N grupos (vínculo manual soma à faixa)

**Data**: 2026-10-19
**Status**: Aceito (decisão do usuário para a rc.7; substitui em parte o ADR-005)
**Porta**: 1 via. Muda o formato do ramal no snapshot e no delta (`group` vira `groups`), o esquema do SQLite (versão 4) e a semântica do CSV. O rollback para a rc.6 só é possível restaurando o `painel.db.bak-v3`.

### Contexto

Até a rc.6 cada ramal tinha um grupo só: atribuição manual > faixa > "Sem grupo" (ADR-005). Na operação real o mesmo ramal precisa aparecer em mais de um grupo (por exemplo "Suporte" e "Plantão"), e as faixas de grupos diferentes se cruzam (um grupo "Andar 2" com 2000-2299 e um grupo "Suporte" com 2100-2199). O trigger `group_ranges_no_overlap`, o `checkRangeConflicts` da tela e o `overlaps` do CSV proíbem exatamente isso.

### Decisão

1. **Relação N:N.** Um ramal pertence a todo grupo cuja faixa o contém **e** a todo grupo onde tem vínculo manual. O vínculo manual **soma** à faixa, nunca substitui (substitui o "manual vence faixa" do ADR-005, item 1). Faixas de grupos diferentes podem se sobrepor; dentro do mesmo grupo continuam sem sobreposição (é erro de entrada, e editar o grupo pela tela dependeria disso).
2. **"Sem grupo" é implícito.** Só o ramal sem nenhum vínculo (nem faixa, nem manual) aparece em "Sem grupo". O "Sem grupo" manual deixa de existir: não há como fixar um ramal fora de um grupo. Não existe lista de exclusões.
3. **Tirar um ramal de um grupo só vale para o vínculo manual.** "Remover deste grupo" apaga o vínculo manual do ramal com o grupo aberto. Se o ramal está no grupo pela faixa, nada é criado para excluí-lo: a tela mostra "Este ramal pertence ao grupo pela faixa X-Y" e o botão fica desabilitado. Para tirar um ramal que está pela faixa, edita-se a faixa.
4. **Adicionar aceita vários grupos e só acrescenta.** `POST /api/groups/assign` ganha `mode`: `add` (N ramais em 1 ou mais grupos; vínculo que já existe é ignorado) e `remove` (N ramais de um grupo; só vínculo manual). Tudo numa transação, com a auditoria, como hoje.
5. **Contrato do ramal.** `group` (texto) e `group_manual` (booleano) saem; entram `groups` (todos os grupos do ramal, ordenados como o catálogo, nunca vazio: `["Sem grupo"]` quando não há nenhum), `range_groups` (os que vêm de faixa) e `manual_groups` (os que vêm de vínculo manual). Os dois campos antigos não ficam por compatibilidade: a tela vai embutida no mesmo binário.
6. **Contagens.** Contadores por grupo, histórico por minuto e relatório de registros contam o ramal em **cada** grupo dele. "Todos os ramais" e a linha de total contam cada ramal uma vez. A soma dos grupos pode passar do total, e isso é esperado.
7. **CSV.** O mesmo ramal pode estar em várias linhas (grupos diferentes). A importação só acrescenta; linha repetida (mesmo grupo e mesmo valor) é ignorada sem erro; `Sem grupo,N` é aceito e ignorado (compatibilidade com arquivos antigos); `Sem grupo,A-B` continua erro. Limites mantidos: 5.000 linhas de dados por importação e 1.000 faixas por grupo (agora contando as que o grupo já tinha). `--replace` continua apagando tudo e importando de novo.
8. **Banco, esquema 4.** `extension_groups` é reconstruída com chave `(number, group_id)` e `group_id NOT NULL`; os vínculos atuais com grupo migram sem perda; as linhas de "Sem grupo" manual (`group_id NULL`) não migram (o daemon as conta e lista no log, nível Warn, antes de apagar). O trigger `group_ranges_no_overlap` sai e entra `group_ranges_no_overlap_same_group`. A mesma migração 3 para 4 traz as tabelas do ADR-007 (uma migração só, um backup `painel.db.bak-v3`).
9. **Permissão.** Qualquer alteração de grupo continua exigindo sessão de administrador e a conferência do grupo `administrator` no `acl.db` a cada escrita (M1).

### Alternativas consideradas

- **A (mais simples possível): manter um grupo por ramal e criar "grupos virtuais" só na tela.** Descartada: o filtro, os contadores e o histórico por grupo são do servidor; um grupo que só existe no navegador não tem histórico nem aparece em outra tela.
- **B: vínculo manual como exceção (incluir/excluir) sobre a faixa.** Daria para tirar um ramal de uma faixa sem editar a faixa, mas cria uma lista de exclusões que o usuário recusou: duas fontes de verdade para "está no grupo?" e uma tela a mais para explicar por que um ramal na faixa não aparece.
- **C: guardar a pertença calculada (faixa + manual) numa tabela.** Descartada: precisaria recalcular a cada ramal criado, faixa editada ou grupo renomeado; a resolução em memória (busca nas faixas + mapa manual) já existe e custa microssegundos por ramal.
- **D (escolhida): N:N com faixa e manual somando, "Sem grupo" implícito.** Menor mudança que cumpre o pedido: o modelo de resolução continua em memória, só passa a devolver uma lista.

### Suposições feitas

- Nenhuma instalação depende de "Sem grupo" manual para esconder ramal de faixa. Se depender, após a rc.7 o ramal volta ao grupo da faixa; a lista sai no log da migração para o administrador refazer a faixa.
- Até 20 grupos por pedido de `add` e até 100.000 pares ramal x grupo por pedido bastam (10.000 ramais em 3 grupos dá 30.000).
- A tela compara número de ramal com faixa usando `BigInt` (faixas de até 18 dígitos passam do inteiro seguro do JavaScript).

### Consequências

- Positivas: um ramal aparece em todos os grupos onde faz sentido; faixas de áreas e de funções convivem; acaba o 409 de faixa sobreposta entre grupos.
- Negativas: a soma dos grupos deixa de bater com o total (a tela explica com "Todos os ramais"); um vínculo manual redundante com a faixa fica guardado e só pode ser removido depois que a faixa deixar de cobrir o ramal; o snapshot cresce cerca de 30 bytes por ramal (três listas no lugar de dois campos).
- Rollback para a rc.6 restaura o `painel.db.bak-v3` e perde tudo o que foi gravado depois da atualização (vínculos novos, auditoria, histórico e o relatório do ADR-007). O `instalar.py rollback` avisa na tela antes de confirmar. A rc.6 recusa abrir banco 4 (`store.migrate`: "banco com esquema 4, este binario conhece ate 3"), então não há caminho de abrir o banco novo por engano.

### Critério de validação

Verificado por compilação, leitura e ao vivo no servidor (sem teste automatizado nesta rodada, regra do usuário):
- `go build ./...` sem erro; nenhuma referência a `Group`/`GroupManual` de `model.Extension`, a `group_ranges_no_overlap` ou a `checkRangeConflicts` no código (`git grep`).
- No servidor, depois do upgrade: `sqlite3 painel.db "PRAGMA user_version"` dá 4; `painel.db.bak-v3` existe; um ramal com vínculo manual antes do upgrade continua no mesmo grupo; criar duas faixas que se cruzam em grupos diferentes responde 200; o ramal da interseção aparece nos dois grupos e uma vez em "Todos os ramais".
- `./instalar.py rollback` para a rc.6 mostra o aviso de perda e, confirmado, a rc.6 sobe com o banco 3.

### Agentes impactados

backend (store, state, model, api, cmd), frontend (tela de ramais, gestão de grupos, seleção múltipla, detalhe), devops (README, LEIAME, `grupos-exemplo.csv`, mensagem do rollback), revisor.
