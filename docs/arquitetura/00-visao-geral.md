# Visão geral do sistema

Retrato do Painel de Ramais **como ele é hoje** (versão `v0.1.2-rc.10`). O que ainda não existe fica só em
[planejado.md](planejado.md). Em divergência de formato (JSON, SQL, CLI), vale o [CONTRATOS](../CONTRATOS.md).

## O que é

Painel **somente leitura** de ramais, chamadas e filas para o Issabel 5 (alvo: Rocky Linux 8), que substitui o FOP.
Não liga, não desliga, não transfere e não recarrega nada no Asterisk. Fica no menu **PBX > Painel de Ramais** do
Issabel e usa o login do próprio Issabel.

## Peças, numa frase cada

| Peça | O que faz | Onde |
|---|---|---|
| Daemon Go (`painel-ramais serve`) | uma conexão AMI só de consulta, estado em memória, API REST e WebSocket em `127.0.0.1:8790` | `cmd/painel-ramais/`, `internal/` |
| Cliente AMI | protocolo AMI próprio com lista fixa de actions de consulta | `internal/ami/` |
| Estado | dono único do estado ao vivo (ramais, canais, chamadas, filas), lotes de 250 ms | `internal/state/` |
| API e hub | REST com envelope, WebSocket com replay, tela embutida | `internal/api/` |
| Banco | SQLite `painel.db` (visões, sessões, histórico, relatório de registro, nomes) | `internal/store/` |
| Tela | HTML, CSS e JS sem framework, embutidos no binário | `internal/web/static/` |
| Módulo PHP do Issabel | item de menu que emite token HMAC e monta o iframe | `packaging/issabel/` |
| Pacote RPM | binário, units systemd, conf do Apache, bloco AMI, segredos | `packaging/` |
| Instalador de campo | `instalar.py` (verificar, instalar, status, rollback, remover, menu, homologar) | `instalar.py` |

Detalhe por pacote: [componentes.md](componentes.md).

## Desenho em uma figura

```
Navegador (iframe no Issabel)
   |  HTTPS 443
Apache do Issabel  --(ProxyPass /painel-ramais/ e /painel-ramais/ws)-->  daemon 127.0.0.1:8790
                                                                          |  |        |
                                                     SQLite painel.db  <--+  |        +--> AMI 127.0.0.1:5038 (só consulta)
                                                                             |
                                    leitura somente: /var/www/db/menu.db e acl.db do Issabel
```

Fluxos completos (evento AMI até a tela, tela até a API, gravação no banco): [fluxo-dados.md](fluxo-dados.md).

## "Grupo" no código, "Visão" na tela

Na interface o agrupamento de ramais se chama **Visão** ("Visões", "Gerenciar visões", "Sem visão"). Internamente nada
mudou de nome: tabelas `groups`, `group_ranges`, `extension_groups`, `group_audit`; rotas `/api/groups*`; chaves JSON
`groups`, `range_groups`, `manual_groups`, `group`; subcomando `painel-ramais group`; opção `--grupos` do instalador;
arquivo `packaging/grupos-exemplo.csv`. O valor interno do ramal sem nenhum vínculo continua `"Sem grupo"` (no JSON e no
banco) e a tela mostra **"Sem visão"** (`rotuloVisao` em `internal/web/static/assets/estado.js`). Regra para quem mexe:
texto visível diz "visão"; identificador, rota, coluna e chave continuam "group". Registro da decisão: desvio D50 em
[desvios.md](../design/desvios.md).

## Versão e repositório

- `v0.1.2-rc.10`: pré-release de laboratório (não homologada). É a última publicada no GitHub (remoto `origin`, de onde o
  `instalar.py --pre` baixa as releases).
- O repositório oficial passa a ser o **GitLab da CAM** (remoto `cam`).
- Esquema do banco nesta versão: 6 (o mesmo da rc.9; a rc.10 não tem migração).

## Para continuar

- Arquivos e caminhos no servidor: [servidor-caminhos.md](servidor-caminhos.md)
- Rotas da API e do WebSocket: [api-endpoints.md](api-endpoints.md)
- Banco e migrações: [banco-migracoes.md](banco-migracoes.md)
- Segurança: [seguranca.md](seguranca.md)
- Funcionalidade por arquivo: [funcionalidades.md](funcionalidades.md)
- Decisões já tomadas: [decisoes.md](decisoes.md)
- Índice geral: [MAPA.md](../MAPA.md)
