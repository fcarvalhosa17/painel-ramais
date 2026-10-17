## ADR-005: Grupos editáveis pela tela, atribuição manual sobre faixa e delta sem epoch

**Data**: 2026-10-16
**Status**: Aceito (decisão do usuário na rc.5; ainda sem verificação na VM).
**Porta**: 1 via para o formato do snapshot/delta (`groups`, `group_manual`) e para o esquema 3 do SQLite: o frontend e o rollback do instalador dependem deles.

### Contexto

Até a rc.4 o grupo de um ramal vinha só de faixas numéricas importadas por CSV, e qualquer mudança de grupo gerava epoch novo (todas as telas recarregavam). A tela precisa criar, renomear, editar faixas e apagar grupos, atribuir ramais a mão (um ramal, um lote ou 10.000 de uma vez) e manter auditoria de quem mudou o quê.

### Decisão

1. **Modelo.** O grupo efetivo de um ramal segue a precedência **atribuição manual > faixa > "Sem grupo"**. "Sem grupo" manual também vence a faixa (serve para tirar um ramal de uma faixa sem mexer na faixa). A atribuição é por **número do ramal em texto** (`extension_groups.number`), não por ramal existente: sobrevive a ramal apagado e recriado. Ramal novo dentro de uma faixa entra no grupo da faixa (sem atribuição manual, vale a faixa).
2. **Banco.** Esquema 3 (expand, só tabelas novas): `extension_groups(number PK, group_id NULL = "Sem grupo" manual, ON DELETE CASCADE)` e `group_audit`. Apagar um grupo apaga as atribuições dele; os ramais voltam para a faixa ou para "Sem grupo". Toda escrita é uma transação única que inclui a linha de auditoria. Editar faixas é apagar e inserir (o trigger de imutabilidade proíbe `UPDATE`; o de sobreposição só enxerga os outros grupos). Renomear leva `history.group_name` junto na mesma transação, somando minutos que colidam com histórico de um grupo antigo de mesmo nome.
3. **Mudança de grupo vai por delta, sem `bumpEpoch`.** O estado recalcula o grupo de cada ramal ao receber `GroupsLoaded` (catálogo completo + mapa de atribuições manuais), marca como sujos só os ramais cujo `group` ou `group_manual` mudou e, se o catálogo mudou, liga uma bandeira que força o próximo lote a levar o catálogo inteiro em `groups`. A versão avança pelo menos 1 mesmo sem ramal afetado (quem reconecta recebe o lote no replay do ring). O snapshot ganha `groups` e o `Extension` ganha `group_manual`; ambos aditivos.
4. **Quem escreve.** Rotas `POST /api/groups/create|update|assign|import|delete`, só administrador (sessão + `adminOnly`, validado no daemon). A CLI e o CSV usam a mesma precedência e as mesmas regras de validação (`store.ValidGroupName`, 64 caracteres, sem controle/formatação invisível). Atribuição em massa é atômica (tudo ou nada).
5. **CSV.** Linha de **um ramal** (`Suporte,2150`) é atribuição manual; `N-M` continua faixa; `Sem grupo,2150` (só um ramal) é "Sem grupo" manual. `--replace` apaga também as atribuições. Sem `--replace`, uma atribuição manual do arquivo sobrescreve a existente.

### Alternativas descartadas

- **Manter epoch novo a cada mudança.** Com a tela editando em massa, todos os clientes recarregariam o snapshot inteiro (10.000 ramais) a cada clique. O delta leva só os ramais afetados.
- **Guardar o vínculo pelo ramal existente (FK).** Perderia a atribuição quando o ramal é apagado e recriado no PBX (comum ao trocar telefone), e acoplaria o banco ao inventário do Asterisk.
- **Faixa de um número como forma de atribuir.** Não vence faixa de outro grupo (o trigger de sobreposição barra) e não permite "Sem grupo" manual.

### Consequências

- Lote grande: atribuir 10.000 ramais gera um delta de ~3 MB uma vez. Cliente lento sai pelo `closeSlow` (4002) e refaz o snapshot, como já ocorria.
- Rollback para a rc.4 (schema 2) restaura `painel.db.bak-v2` e perde atribuições manuais e a auditoria de grupos; o rollback do instalador (CONTRATOS 15.7) avisa.
- A tela deve tratar `groups` do delta como substituição do catálogo local inteiro e não pode contar com `reload` para mudanças de grupo.
- Renomear um grupo para o nome de um grupo que já foi apagado **soma** o histórico antigo desse nome ao do grupo renomeado (minutos que existem nos dois lados são somados onde ambos têm dado; nada é perdido). O histórico é gravado por nome em texto e sobrevive à remoção do grupo, então o nome reaproveitado herda a série antiga.
