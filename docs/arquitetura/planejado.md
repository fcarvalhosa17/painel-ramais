# Planejado (não existe ainda)

**Nada deste arquivo está implementado.** Tudo aqui é plano da Etapa 2 de implantação na CAM, sem ADR, sem código e sem
data. O sistema real está descrito nos outros arquivos de `docs/arquitetura/`. Quando um item virar decisão, ele ganha ADR
próprio e sai daqui; quando virar código, entra nos arquivos de componentes, caminhos e funcionalidades.

Não confundir com a "Etapa 2" citada nos planos e specs da rc.7 e da rc.8 (`docs/plans/`, `docs/specs/`): aquela era a aba
Chamadas e o painel de filas, que **já existem**.

## Etapa 2 (planejado)

| Item | Situação |
|---|---|
| Rodar o painel em Docker no servidor `camboxdb` | planejado |
| Conectar ao AMI pelo IP virtual do keepalived (alta disponibilidade do Asterisk) | planejado |
| Usar MariaDB no lugar do SQLite | planejado |
| Build e publicação pelo CI/CD da CAM (GitLab) | planejado |

## O que o sistema atual assume e que esses itens mexeriam

Fatos de hoje, listados só para quem for desenhar a Etapa 2 (cada um vira pergunta do ADR, não decisão tomada):

- O daemon roda no próprio PBX: `config.yaml` **recusa** `listen` e `ami.address` fora de loopback
  (`internal/config/config.go`, função `loopback`), e o bloco AMI tem `permit` só para 127.0.0.1.
- A entrada pelo menu depende de arquivos locais do Issabel: `/etc/painel-ramais-issabel.key` (lido pelo PHP e pelo
  daemon) e `/var/www/db/menu.db` e `acl.db` (lidos pelo daemon em modo somente leitura).
- O banco é SQLite com um escritor, pragmas e DDL `STRICT` próprios do SQLite, migração por `PRAGMA user_version` e
  backup por `VACUUM INTO` (`internal/store/store.go`).
- A instalação é RPM mais systemd mais `instalar.py`, alvo Rocky 8 com Issabel 5; a release é publicada por `build.ps1`
  com o `gh` (GitHub).
- O critério de pronto continua: compilar e conferir por leitura; teste ao vivo no servidor, feito pelo usuário.
