# FASE 1: respostas às decisões pendentes e ajustes finais

## Ações feitas sem aprovação
- Go e nfpm portáteis em .tools\: manter. Adicionar .tools/ ao .gitignore e documentar no README as versões e como obter as ferramentas, para o build ser reproduzível em outra máquina.
- git init: manter. Fazer o primeiro commit ao final deste ajuste. Sem remoto e sem push.
- ~/.claude/learnings/: manter. Conferir que nenhuma senha, segredo ou token de teste foi gravado nessas anotações. Se houver, remover.
- instalar.py com 994 linhas: exceção aceita. Arquivo único é requisito de campo.
- Atualizar docs/PROMPT-FASE-1.md registrando que o discovery.sh foi substituído por "instalar.py verificar".

## Decisão 1: permissão da conta AMI
- Manter write = system,reporting. Sem a classe system, as actions PJSIPShow* e ModuleCheck não funcionam, e a maioria dos clientes usa PJSIP.
- Documentar no README a tabela com cada action usada e a classe exigida. Marcar como "a confirmar no Asterisk real com manager show command <Action>".
- Controles compensatórios obrigatórios:
  - Lista fixa de actions permitidas, definida em código, no cliente AMI do daemon. Qualquer action fora dela é recusada antes de chegar ao socket, com log de erro. Teste unitário cobrindo isso.
  - permit somente 127.0.0.1. Segredo com permissão 0600, de propriedade do usuário painel-ramais.
  - Endurecimento da unit systemd: NoNewPrivileges, ProtectSystem=strict, ProtectHome, PrivateTmp, ReadWritePaths só para /var/lib/painel-ramais, CapabilityBoundingSet vazio. Documentar o comando "systemd-analyze security painel-ramais" para eu validar no laboratório.
- Registrar na auditoria o risco residual aceito: se o próprio daemon for comprometido, a classe system dá acesso a GetConfig e ModuleUnload.

## Decisão 2: porta 8790
- Usar socket activation do systemd: painel-ramais.socket escutando em 127.0.0.1:8790. O systemd mantém a porta ocupada mesmo com o daemon parado ou em crash.
- O daemon recebe o socket via LISTEN_FDS. Fora do systemd (desenvolvimento e testes), ele mesmo abre a porta.
- Ajustar RPM, units, instalar.py (verificar e status) e README.

## Decisão 3: RPM de rollback
- Aceito /var/lib/painel-ramais-instalador/, acessível só pelo root.
- Gravar o sha256 do RPM guardado e conferir antes de executar o rollback.
- "remover --purge" apaga também esse diretório.

## Decisão 4: nome do ramal
- Liberar PJSIPShowEndpoint no modo pjsip e SIPshowpeer no modo sip. Incluir as duas na lista fixa de actions.
- Nunca consultar em rajada. O preenchimento roda em segundo plano, depois que o snapshot estiver pronto, com taxa limitada (padrão de 20 consultas/s, configurável). Enquanto isso, o painel funciona com o nome vazio.
- Atualização oportunista: aproveitar o CallerIDName dos eventos de chamada originados pelo ramal.
- Refazer o ciclo completo:
  - quando chegar o evento Reload do Asterisk (incluir no eventfilter);
  - a cada 6 h.
- Guardar os nomes em cache no SQLite, para aparecerem imediatamente após um restart.
- Rodar de novo o teste de carga com o preenchimento ativo. Meta: p99 continua abaixo de 1 s.

## Decisão 5: login e token
- Confirmado:
  - login obrigatório para o painel e para a API;
  - token de entrada válido por 60 s e de uso único;
  - /api/health acessível só via localhost.
- Ajuste: o painel vai ficar aberto 24h em TV de NOC.
  - A sessão se renova sozinha enquanto houver WebSocket ativo. Uma tela em uso nunca expira.
  - Expiração por inatividade de 12 h, configurável.
  - Quando a sessão expirar, o WebSocket envia um aviso explícito antes de fechar, para a tela da fase 2 mostrar isso em vez de congelar.

## Novo: captura de AMI real para a homologação
Motivo: as amostras de eventos foram escritas de memória. Hoje os testes validam o código contra as próprias suposições.
- Subcomando "painel-ramais capturar --minutos N --saida arquivo":
  - usa a conta AMI do painel e respeita a lista fixa de actions;
  - grava os frames brutos com timestamp.
- Mascarar senhas e segredos na gravação.
- Opção --anonimizar: troca números externos por números fictícios, sempre o mesmo fictício para o mesmo número (LGPD).
- O simulador ganha o modo "reproduzir arquivo de captura". Os testes de "com quem fala" passam a rodar também sobre capturas guardadas em testdata/.
- Escrever no README o roteiro de chamadas que vou executar durante a captura:
  - chamada interna;
  - entrada e saída por tronco;
  - transferência cega e transferência assistida;
  - chamada em espera;
  - chamada para fila e para ring group (Local channels);
  - chamada não atendida;
  - ramal desregistrando e voltando;
  - restart do Asterisk no meio de uma chamada.

## Entrega
- Gerar novamente dist/ (RPM e tar.gz) como versão 0.1.1.
- Atualizar o relatório de carga e o relatório de auditoria.
- Commit único ao final, com mensagem que liste as decisões tomadas.
- Não iniciar os testes em Linux. A homologação vem no próximo prompt.
