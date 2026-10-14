# Painel de Ramais para Issabel 5: FASE 1, Coletor (backend), empacotamento e isolamento

## Ambiente de desenvolvimento
- Raiz do projeto: D:\CLaude\painel-ramais. Todo o código, scripts, documentação e relatórios ficam nessa pasta. Este arquivo fica em docs/PROMPT-FASE-1.md.
- Iniciar repositório git na raiz, com .gitignore para binários, dist/ e arquivos de teste gerados.
- O desenvolvimento acontece no Windows, mas o alvo é Linux. Build sempre com GOOS=linux GOARCH=amd64 CGO_ENABLED=0.
- Criar .gitattributes forçando fim de linha LF em *.sh, *.service, *.conf, *.yaml, *.yml, Makefile e na config do nfpm. Script com CRLF quebra no Rocky. Conferir os arquivos gerados antes de fechar a fase.
- Garantir que build, testes e geração do RPM rodem a partir do Windows. Se make não estiver disponível, entregar um build.ps1 equivalente ao Makefile.
- Antes de começar os testes que dependem de Linux (rpm, systemd, SELinux, Apache), verificar se WSL2 ou Docker Desktop estão disponíveis na máquina e me dizer qual vai usar.
- Medições de CPU e memória do teste de carga precisam ser feitas em Linux (WSL2 ou container), não no Windows. O relatório informa onde cada medição foi feita.

## Contexto
- Produto interno da minha empresa: painel somente leitura que substitui a visualização do FOP em servidores Issabel 5.
- Cenário de referência: um único Asterisk com cerca de 10.000 ramais. O FOP atual trava com esse volume.
- O painel deve mostrar em tempo real, por ramal:
  - registrado, não registrado ou inalcançável;
  - tocando ou em chamada;
  - com quem está falando (ramal interno ou número externo via tronco, com nome quando houver);
  - direção da chamada e duração.
- Ramais organizados em grupos para monitoramento.
- Nenhuma ação de controle: zero originate, redirect, hangup ou qualquer action de escrita no AMI.
- ESTA FASE entrega só o coletor (daemon), o empacotamento e as ferramentas de validação.
  - Fase 2: tela final.
  - Fase 3: link no menu PBX do Issabel e entrada por token.

## Plataforma
- Alvo exclusivo: Issabel 5 em Rocky Linux 8 ou superior (el8 e el9). Não suportar CentOS nem versões antigas do Issabel.
- Asterisk mínimo 13, porque o painel depende de BridgeEnter e DialBegin. Na subida, ler a versão via AMI (CoreSettings). Abaixo do mínimo, recusar iniciar com mensagem clara.
- Cada instalação usa SIP OU PJSIP, nunca os dois ao mesmo tempo. A maioria dos clientes usa PJSIP; os antigos usam chan_sip. O coletor precisa suportar os dois.

## Decisões técnicas fixas
- Go, binário estático (CGO_ENABLED=0, linux/amd64), rodando como serviço systemd.
- SQLite em Go puro (modernc.org/sqlite), sem CGO.
- Config em /etc/painel-ramais/config.yaml. Dados em /var/lib/painel-ramais/.
- HTTP escutando apenas em 127.0.0.1, porta configurável. O acesso externo passa pelo Apache.

## Tecnologia por instalação
- Campo technology em config.yaml, com os valores auto, sip ou pjsip. Padrão: auto.
- No modo auto, detectar na subida, via action ModuleCheck, qual módulo está carregado: chan_pjsip.so ou chan_sip.so.
  - Se os dois estiverem carregados, usar o que tiver ramais configurados e registrar um aviso no log.
  - Se nenhum estiver carregado, falhar com mensagem clara.
- Dois adaptadores (sip e pjsip) atrás de uma interface comum. Só o da tecnologia ativa é usado.
- A chave do ramal é normalizada pelo número.
- A tecnologia ativa aparece no /api/health e no snapshot.
- Se, ao reconectar, a tecnologia detectada for diferente, não trocar sozinho: registrar erro e exigir restart do serviço.

## AMI
- Uma única conexão AMI, com usuário dedicado, permit somente 127.0.0.1.
- Permissões mínimas: só leitura e as actions de consulta necessárias. Validar na versão real quais classes cada action e evento exigem, e documentar.
- Proibido usar a action Command ou qualquer action de escrita.
- eventfilter montado conforme a tecnologia ativa:
  - Registro e estado: ContactStatus (pjsip) ou PeerStatus (sip), mais DeviceStateChange.
  - Chamadas: Newchannel, Newstate, NewCallerid, NewConnectedLine, DialBegin, DialEnd, BridgeEnter, BridgeLeave, Hangup. Hold e Unhold se existirem na versão.
- Snapshot inicial, e novamente após cada reconexão:
  - pjsip: PJSIPShowEndpoints e PJSIPShowContacts, com fallback por PJSIPShowAors se PJSIPShowContacts não existir.
  - sip: SIPpeers.
  - Ambos: DeviceStateList e CoreShowChannels, para as chamadas já em andamento.
- A leitura do socket AMI nunca pode bloquear. A goroutine leitora só faz o parse e entrega a um canal com buffer. Se o processamento atrasar, registrar métrica e log, mas nunca parar de ler.
- Reconexão automática com backoff.

## Modelo de estado (em memória)
- Campos por ramal:
  - número, tecnologia, nome (callerid), grupo;
  - estado de registro: registrado, não registrado, inalcançável ou desconhecido;
  - IP, user agent, latência do qualify quando disponível;
  - estado do dispositivo;
  - lista de chamadas ativas.
- Campos de cada chamada ativa:
  - id;
  - direção: entrada, saída ou interna;
  - estado: tocando, em conversa ou em espera;
  - outro lado: ramal interno ou número externo, com nome;
  - timestamp de início.
- Correlacionar canais por Uniqueid, Linkedid e bridges para resolver "com quem fala". Isso inclui chamadas via tronco, transferências e Local channels.
- Depois do Hangup, a lista de chamadas do ramal fica limpa.
- Versão global incremental. Toda mudança de estado gera um delta.

## Comportamento sem AMI
- Ao perder a conexão AMI:
  - todos os ramais vão para o estado "desconhecido", NUNCA "não registrado";
  - snapshot e deltas passam a carregar ami_connected=false.
- Ao reconectar: snapshot completo e incremento do "epoch", para os clientes recarregarem o estado inteiro.
- No histórico, o período sem AMI é gravado como lacuna, não como queda de registros.

## API (127.0.0.1)
- GET /api/snapshot: estado completo, com versão, epoch, tecnologia e ami_connected. Resposta com gzip.
- GET /ws: WebSocket.
  - Envia deltas agrupados a cada 250 ms; várias mudanças do mesmo ramal na janela viram uma só.
  - O cliente informa a versão que tem. Se houver lacuna ou mudança de epoch, o servidor manda a instrução de recarregar o snapshot.
- GET /api/history: série por minuto de registrados, não registrados e em chamada, total e por grupo. Retenção de 30 dias.
- GET /api/groups: grupos e seus ramais.
- GET /api/health, com:
  - eventos por segundo, ramais por estado, chamadas ativas, clientes WebSocket;
  - tamanho da fila interna, lag de processamento, uptime;
  - estado do AMI, tecnologia ativa e resultado da verificação de dependências.

## Autenticação
- Usuários próprios do daemon, com senha em bcrypt no SQLite.
- CLI: "painel-ramais user add", "user del" e "user passwd".
- Login por sessão com cookie seguro e expiração.
- Token HMAC de curta duração, com segredo em /etc/painel-ramais/secret (permissão 0600), como atalho opcional de entrada.
  - Nesta fase, implementar a validação e uma CLI para gerar token de teste.
  - O link do Issabel usará esse token na fase 3.
- O login direto precisa funcionar sem nada do Issabel.

## Grupos
- Tabela no SQLite do daemon.
- CLI de importação de CSV no formato: grupo, ramal ou faixa (ex.: 2000-2999). Também listar e remover grupos pela CLI.
- Ramal fora de qualquer grupo recebe "Sem grupo".
- O grupo vai junto no snapshot e nos deltas.

## Isolamento do Issabel (requisito principal)
Nenhuma atualização do Issabel, do IssabelPBX ou de módulos pode parar o serviço. O núcleo é independente. A integração com o Issabel é uma camada fina e descartável.

### Pacote RPM
- Gerado com nfpm (sem rpmbuild). Nome do pacote: painel-ramais.
- Um único binário x86_64 atende el8 e el9.
- O pacote fica fora de qualquer repositório do Issabel e não declara dependência de nenhum pacote do Issabel.

### Arquivos instalados
- /usr/bin/painel-ramais
- /etc/painel-ramais/config.yaml, marcado como config noreplace.
- /etc/painel-ramais/secret, gerado no %post só se ainda não existir.
- /var/lib/painel-ramais/
- Unit /usr/lib/systemd/system/painel-ramais.service, com Restart=always.
- Usuário de sistema painel-ramais, sem shell.

### Upgrade e remoção
- Upgrade preserva config, segredo, usuários, grupos e histórico.
- Remoção apaga binário, unit e as alterações no Asterisk e no Apache, mas mantém /var/lib/painel-ramais. Documentar o comando de limpeza total.
- Tratar corretamente os argumentos de %post e %postun, para que um upgrade não seja executado como se fosse remoção.

### AMI
- Nunca editar manager.conf, porque o IssabelPBX regera esse arquivo.
- No %post, adicionar o usuário AMI em /etc/asterisk/manager_custom.conf somente se ele não existir.
  - O bloco fica entre os marcadores "; BEGIN painel-ramais" e "; END painel-ramais".
  - Depois, rodar "asterisk -rx 'manager reload'".
- Na remoção, retirar apenas esse bloco.

### Apache
- Arquivo próprio /etc/httpd/conf.d/painel-ramais.conf, fazendo proxy de /painel-ramais/ (HTTP e WebSocket via mod_proxy_wstunnel) para o daemon.
- Não editar nenhum arquivo do Issabel ou do httpd.
- Validar com "httpd -t" antes do reload. Se falhar, remover o arquivo, não recarregar e registrar o erro.

### SELinux e rede
- Se o SELinux estiver enforcing, ativar httpd_can_network_connect com "setsebool -P" no %post e registrar no log.
- Nenhuma porta nova exposta. Todo acesso passa pela 443 do Apache, porque o firewall do Issabel regrava o iptables.

### Verificação de dependências
- Na subida e a cada 60 s, verificar:
  - se o usuário AMI existe e autentica;
  - se o include de manager_custom.conf continua ativo;
  - se o arquivo de proxy do Apache continua presente.
- Expor o resultado no /api/health e registrar no log com mensagem clara.

## Ferramentas de validação
1. **instalar.py verificar** (substitui o antigo discovery.sh; ver `docs/ADENDO-FASE-1-INSTALADOR.md`), para eu rodar no servidor. Não altera nada. Gera um único arquivo txt com:
   - versões do Rocky, do Issabel, do Asterisk e do httpd;
   - tecnologia ativa e quantidade de ramais;
   - existência das actions usadas (manager show command);
   - se o manager.conf inclui o manager_custom.conf;
   - estado do SELinux;
   - presença de mod_proxy_wstunnel;
   - regras de firewall relevantes para a porta 443.
2. **Simulador de AMI**: servidor falso que imita o Asterisk.
   - Parâmetro de modo: sip ou pjsip.
   - 10.000 ramais.
   - Centenas de chamadas simultâneas: internas, entrada e saída por tronco, transferências.
   - Rajada de 3.000 ramais desregistrando de uma vez.
   - Desconexão do AMI no meio da carga.
3. **Teste de carga contra o simulador**, rodado nos dois modos, com relatório em markdown e os resultados lado a lado:
   - latência evento até delta no WebSocket, p50 e p99;
   - tamanho do snapshot, cru e com gzip;
   - tamanho médio dos deltas;
   - CPU e memória do daemon;
   - comportamento na reconexão.
4. **Página de debug** mínima em /debug, acessível só via localhost. Mostra contagens por estado, as chamadas ativas e os últimos deltas. Não é a tela final.

## Entregáveis
- Código-fonte e Makefile (ou build.ps1), com build, testes e geração do RPM.
- Configuração do nfpm.
- Unit systemd.
- Exemplo de config.yaml e de CSV de grupos.
- instalar.py (com o subcomando verificar, que substitui o discovery.sh), o simulador e o relatório de carga.
- README em português com:
  - instalação via RPM, configuração e gestão de usuários e grupos;
  - formato do snapshot e dos deltas, que a fase 2 vai consumir;
  - troubleshooting.

## Não fazer
- Nenhuma action de escrita no AMI, nem a action Command.
- Não editar manager.conf nem qualquer arquivo gerado pelo IssabelPBX.
- Não instalar nada em servidor de produção. Eu instalo o RPM.
- Não criar a tela final nem a integração com o menu do Issabel nesta fase.

## Critérios de aceite
- Com o simulador em 10.000 ramais e rajada de 3.000 desregistros, nos modos sip e pjsip:
  - p99 da latência evento até delta abaixo de 1 s;
  - nenhuma perda de estado;
  - memória sem crescimento contínuo.
- Modo auto detecta corretamente a tecnologia no simulador SIP e no simulador PJSIP.
- Ao derrubar o AMI no meio da carga:
  - nenhum ramal aparece como "não registrado";
  - os ramais aparecem como "desconhecido";
  - ao reconectar, o daemon refaz o snapshot e os clientes recebem o novo epoch.
- Para cada chamada simulada, "com quem fala" está correto: interna, externa e após transferência.
- "rpm -i", "rpm -U" e "rpm -e" testados em Rocky 8 e Rocky 9, usando o simulador no lugar do Asterisk. Use container ou VM, o que estiver disponível. Se nenhum estiver disponível, gerar um roteiro de teste passo a passo para eu executar.
- Simular uma atualização do Issabel que reescreve o manager.conf e reinicia o httpd e o Asterisk:
  - o serviço continua rodando;
  - reconecta sozinho;
  - a verificação de dependências acusa qualquer item que tenha sumido.
- instalar.py verificar roda em Rocky 8 ou superior sem dependências extras (só Python 3.6 da biblioteca padrão).
- Todos os arquivos .sh, .service, .conf e .yaml do repositório estão com fim de linha LF.
