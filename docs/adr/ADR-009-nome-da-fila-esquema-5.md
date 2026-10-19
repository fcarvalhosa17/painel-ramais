## ADR-009: Nome da fila no banco do painel (esquema 5)

**Data**: 2026-10-19
**Status**: Aceito (autorização do usuário para uma migração pequena v4 para v5, só para o nome da fila)
**Porta**: 1 via para o esquema (a rc.6 e a rc.7 não abrem banco 5; voltar exige o backup). A forma da rota e da tela é 2 vias.

### Contexto

O Asterisk do Issabel identifica a fila pelo número (`600`); o nome amigável ("Suporte N1") fica no MySQL do IssabelPBX, que o painel é proibido de ler. A operação quer ver o nome. O ADR-008 decide que o nome é dado do painel, editável pela tela por administrador.

Situação de instalação (decisão do usuário): o servidor está na **rc.6** (esquema 3); a rc.7 (esquema 4) não foi instalada e a rc.8 vai direto por cima da rc.6.

### Decisão

1. **Tabela nova `queue_names`**, na migração 4 para 5 (DDL no CONTRATOS 10, versão 5): `queue` (texto, chave), `name`, `updated_at`, `updated_by`. Só aditiva: nenhuma tabela existente muda.
2. **Migração 3 para 5 numa execução, com um backup só.** O `store.migrate` já faz o `VACUUM INTO painel.db.bak-v<versão inicial>` **uma vez**, antes do laço, e aplica cada migração na sua própria transação (`internal/store/store.go:86-95`, conferido por leitura na tag `v0.1.2-rc.7`). Banco 3 gera `painel.db.bak-v3` e passa por 3, 4 e 5; **nenhum** `.bak-v4` é criado nesse caminho, então nada atrapalha o rollback para a rc.6. Banco 4 (quem instalou a rc.7) gera `painel.db.bak-v4`.
3. **Falha no meio.** Se a 3 para 4 passar e a 4 para 5 falhar, o banco fica no esquema 4 (transação desfeita só da 4 para 5), o daemon não sobe e o `.bak-v3` continua lá: o `rollback` restaura o `.bak-v3` como em qualquer banco mais novo que a versão anterior.
4. **Rollback rc.8 para rc.6.** O `instalar.py rollback` já restaura `painel.db.bak-v<schema anterior>` quando o banco é mais novo (CONTRATOS 15.7). Ajustes: a condição do aviso específico passa de `sch_ant == 3 and sch_db == 4` para `sch_ant == 3 and sch_db >= 4`; o texto troca "voltando da rc.7 (banco no esquema 4)" por "voltando para a rc.6 (esquema 3), banco no esquema N"; a lista de perdas continua (vínculos de ramal em mais de um grupo, relatório de registros, histórico, sessões, usuários e grupos gravados depois da atualização) e ganha "nomes de fila". A rc.6 nunca abre banco 4 ou 5: o `store.migrate` dela recusa (`banco com esquema N, este binario conhece ate 3`).
5. **Escrita.** `POST /api/queues/rename`, só administrador, com a mesma checagem de `acl.db` das rotas de grupo (M1). Nome vazio apaga a linha (a tela volta a "Fila 600"). Sem linha em `group_audit` (o `CHECK` de `action` exigiria reconstruir a tabela, porta de 1 via por um registro de baixo valor): a última alteração fica em `updated_at`/`updated_by` e no log Info com `sub`.
6. **Entrega ao estado.** Depois de gravar, o daemon relê a tabela e entrega ao estado como `QueueNamesLoaded` (mesmo caminho do `GroupsLoaded`: sem epoch novo, a fila renomeada vai no próximo delta).

### Alternativas consideradas

- **A (mais simples possível): sem nome, só o número.** Descartada pelo usuário: "Fila 600" não diz nada ao operador.
- **B: ler o nome do MySQL do IssabelPBX (`queues_config`).** Proibido (restrição do usuário) e acoplaria o painel ao esquema do IssabelPBX, que muda com atualização.
- **C: guardar o nome no navegador (`localStorage`).** Descartada: cada operador veria um nome diferente; não é dado de preferência.
- **D: reaproveitar uma tabela existente (por exemplo `extension_names` com prefixo).** Descartada: mistura dois conceitos e quebraria o expurgo do cache de nomes de ramal.
- **E: auditoria em `group_audit` com ação nova.** Descartada: exige reconstruir a tabela por causa do `CHECK`; `updated_by` mais o log cobrem.

### Suposições feitas

- O identificador da fila que o AMI usa (`Queue: 600`) é estável e é o que a operação conhece. Fila renomeada no IssabelPBX mantém o número; fila apagada e recriada com outro número perde o nome dado no painel (a linha antiga fica sem uso, inofensiva).
- O servidor do usuário está na rc.6 com `painel.db` no esquema 3 (declaração do usuário). Se estiver em outro esquema, o caminho 4 para 5 também vale.

### Consequências

- Positivas: nome legível sem tocar no Issabel; migração aditiva e pequena; um backup só.
- Negativas: voltar da rc.8 para a rc.6 perde tudo o que foi gravado depois do upgrade (já era assim na rc.7), agora também os nomes de fila; a mensagem do rollback diz isso antes da confirmação.

### Critério de validação

- Leitura: `migrations` com 5 itens, `ddlV5` igual ao bloco SQL do CONTRATOS 10; o `backup` continua fora do laço de migrações (uma chamada por `migrate`); `painel-ramais version` diz `schema 5`.
- Ao vivo, depois de instalar a rc.8 sobre a rc.6: `sqlite3 /var/lib/painel-ramais/painel.db "PRAGMA user_version"` = 5; existe `painel.db.bak-v3` e **não** existe `painel.db.bak-v4`; renomear a fila 600 pela tela como administrador muda o nome em outra tela aberta em até 1 s; como operador, o botão não aparece e a rota responde 403.
- Rollback: `./instalar.py rollback` mostra "voltando para a rc.6 (esquema 3)" e a lista de perdas com "nomes de fila"; confirmado, a rc.6 sobe com o banco 3.

### Agentes impactados

backend (`internal/store`, `internal/api`, `cmd/painel-ramais`, `instalar.py`), frontend (edição do nome), segurança (rota nova de escrita), devops (release, README).
