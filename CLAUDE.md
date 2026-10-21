# Painel de Ramais (Issabel 5)

**Antes de qualquer tarefa, leia [docs/MAPA.md](docs/MAPA.md).**

Painel somente leitura de ramais, chamadas e filas que substitui o FOP no Issabel 5 (Rocky Linux 8): daemon Go com uma
conexão AMI só de consulta, estado em memória, API REST e WebSocket em `127.0.0.1:8790`, SQLite `painel.db`, tela
embutida no binário, módulo PHP no menu PBX do Issabel, pacote RPM e instalador `instalar.py`.

## Regras

- **Nenhum teste, simulador ou carga no PC do usuário.** Aqui só se compila, empacota e publica. O teste é do usuário,
  ao vivo no servidor. Não escrever teste automatizado (CONTRATOS D12).
- **Alvo**: Rocky Linux 8 com Issabel 5. Nada que dependa de outra distribuição ou de outro PBX.
- **AMI só leitura**: nenhuma action de escrita, **nunca** `Command`. Action nova só de consulta e só pela allowlist de
  `internal/ami/actions.go`, com ADR.
- **Não editar** `manager.conf` nem arquivos do Issabel, e **não ler** o MySQL do Issabel. A única escrita que existe é o
  bloco próprio do painel em `manager_custom.conf`, feito pelo `%post` do RPM (ADR-003); não ampliar.
- **Somente IPv4** em código, exemplo e documentação.
- **Nenhuma credencial** (senha, token, chave, segredo, usuário de remoto) em código, doc, commit ou log.
- **gitleaks** nos commits novos antes de qualquer push (`.tools/gitleaks/`, fora do git; rodar da raiz com caminho
  relativo). Editar `docs/CONTRATOS.md` acima da linha do token de exemplo desloca a linha registrada no `.gitleaksignore`.
- **Push sem force.** Commit sem `Co-Authored-By`, no formato `tipo(escopo): descricao`, em português.
- **Sem travessão longo** em texto nenhum.
- **Repositório oficial: GitLab da CAM** (remoto `cam`). O GitHub (`origin`) guarda as releases até a `v0.1.2-rc.10`.
- **Interface diz "Visões"**, não "grupos". Internamente continuam `groups`, `/api/groups*`, `group`, `"Sem grupo"` (que a
  tela mostra como "Sem visão"). Não renomear identificadores sem decisão do usuário.
- **Documentação junto do código**: toda entrega que mudar comportamento atualiza `docs/MAPA.md` e `docs/arquitetura/` no
  mesmo commit.
