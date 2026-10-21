# Banco e migrações

SQLite em `<data_dir>/painel.db` (padrão `/var/lib/painel-ramais/painel.db`), driver `modernc.org/sqlite` (Go puro, sem
CGO). Código: `internal/store/store.go` (DDL `ddlV1` a `ddlV6` e `migrate`). DDL exata e notas de cada versão:
[CONTRATOS](../CONTRATOS.md), seção 10. O que fica no banco é pouco: o estado ao vivo (ramais, chamadas, filas) mora só
em memória.

## Conexão

- Pragmas no DSN: `foreign_keys(1)`, `journal_mode(WAL)`, `busy_timeout(5000)`, `_txlock=immediate`.
- Um único escritor no daemon (pool de uma conexão). A CLI escreve no mesmo arquivo; WAL e `busy_timeout` resolvem.
- A CLI recusa rodar como root (o banco e o WAL ficariam com dono root): `sudo -u painel-ramais painel-ramais ...`.
- O sqlite3 de linha de comando do Rocky 8 (3.26) não abre tabela `STRICT`: conferência ao vivo é pela API, pela tela ou
  pelo `journalctl`.

## Tabelas atuais (esquema 6)

| Tabela | Desde | Guarda | Retenção |
|---|---|---|---|
| `users` | v1 | usuário de senha (hoje só `emergencia`), hash bcrypt | sem |
| `sessions` | v1 | SHA-256 do cookie, `kind` (`password` ou `token`), `subject`, validade | vencidas apagadas a cada hora |
| `groups` | v1 | catálogo de **visões** (nome único sem diferenciar maiúsculas, nunca `Sem grupo`) | sem |
| `group_ranges` | v1 | faixas numéricas de cada visão; imutáveis (troca = apagar e inserir); sem sobreposição dentro da mesma visão (trigger da v4) | sem |
| `history` | v1 (+2 colunas na v4) | contagens por minuto: total (`group_name = ''`) e por visão, `gap`, `lost_registration`, `became_unreachable` | `history.retention_days` (padrão 30) |
| `extension_names` | v2 | cache de nomes dos ramais | expurgo dos ramais que saíram do PBX |
| `extension_groups` | v3, refeita na v4 | vínculo manual ramal (texto) para visão, chave `(number, group_id)` | sem (cascade ao apagar a visão) |
| `group_audit` | v3 | auditoria de toda alteração de visão (quem, ação, detalhe JSON); `UPDATE` proibido por trigger | igual ao histórico |
| `reg_meta` | v4 | data de instalação do relatório ("Dados desde") | sem |
| `reg_events` | v4 | ocorrências brutas de registro, até 200 por ramal | 30 dias |
| `reg_hourly` | v4 (+`down_gap` na v6) | agregado por ramal e hora, cursor `rev` | 30 dias |
| `reg_system_events` | v4 | sinais `shutdown`, `fully_booted`, `ami_connected` | 30 dias |
| `queue_names` | v5 | nome dado pelo painel a cada fila | sem |
| `reg_state` | v6 | estado "fora do ar" e último registro de cada ramal já visto | sem |

Os nomes internos `groups`, `group_ranges`, `extension_groups`, `group_audit` e a coluna `history.group_name` são a
**Visão** da interface. O valor `"Sem grupo"` não é linha de `groups`: é o ramal sem nenhum vínculo (a tela mostra "Sem
visão"; o histórico grava uma linha por minuto com `group_name = 'Sem grupo'`).

## Migrações

| Versão | Release | O que mudou | ADR |
|---|---|---|---|
| 1 | fase 1 | `users`, `sessions`, `groups`, `group_ranges`, `history` | ADR-002 |
| 2 | fase 1 | `extension_names` (cache de nomes) | (decisão 4, `docs/DECISOES-FASE-1.md`) |
| 3 | rc.5 | `extension_groups` (manual vence faixa) e `group_audit` | ADR-005 |
| 4 | rc.7 | `extension_groups` refeita para N:N, trigger por visão, colunas novas em `history`, tabelas do relatório | ADR-006, ADR-007 |
| 5 | rc.8 | `queue_names` | ADR-009 |
| 6 | rc.9 | `reg_state` e `reg_hourly.down_gap` | ADR-010 |

A rc.10 não tem migração (continua no esquema 6).

Como roda (`store.migrate`):

1. Lê `PRAGMA user_version`. Maior que o binário conhece: recusa iniciar com erro no log (downgrade não suportado). O
   código de saída 4 (`RestartPreventExitStatus=4` na unit) é só para Asterisk abaixo da versão 13
   (`ami.ErrVersionTooOld`); o comentário da unit que fala em "esquema mais novo" está desatualizado.
2. Se há banco com versão maior que 0 e menor que a atual: **um** backup `VACUUM INTO painel.db.bak-v<versão inicial>`
   (modo 0600), antes de qualquer mudança.
3. Aplica cada versão numa transação própria e grava o `user_version` novo. Falha no meio: o banco fica na última versão
   que passou, o daemon não sobe e o backup continua valendo.

Rollback de versão: o `instalar.py rollback` troca o banco pelo `painel.db.bak-v<esquema da versão anterior>` quando o
banco atual é mais novo (CONTRATOS 15.7). Regra para versão nova: migração só para frente, aditiva sempre que possível,
sempre com o backup acima; o `painel-ramais version` imprime o `schema` que o instalador usa para decidir o rollback.
