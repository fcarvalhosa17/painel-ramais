# Roteiro de captura de AMI real (homologação)

Objetivo: gravar o tráfego AMI de um Asterisk real durante um conjunto conhecido de chamadas, para que o simulador reproduza o arquivo e os testes de "com quem fala" rodem sobre dados reais (hoje as amostras foram escritas de memória). Texto para o README (seção de homologação); o formato do arquivo está no `docs/CONTRATOS.md`, seção 18.

## Antes de começar

1. O painel instalado e conectado ao Asterisk (`/api/health` com `ami.connected = true`).
2. Anote os ramais que vai usar: dois ramais internos (A e B), um terceiro (C), um ramal com fila ou ring group, um tronco e um número externo seu para ligar e receber.
3. Rode como o usuário do serviço, num diretório onde ele grave:

```
sudo -u painel-ramais painel-ramais capturar --minutos 20 --saida /var/lib/painel-ramais/captura-AAAAMMDD.txt --anonimizar
```

- `--minutos` é o tempo de captura ao vivo (fracionário aceito; Ctrl-C encerra antes e ainda grava o snapshot final).
- A captura usa a conta AMI do painel e só as actions da lista fixa; nada é alterado no Asterisk.
- Senhas e segredos saem mascarados sempre. `--anonimizar` troca números externos, nomes e IPs por valores fictícios (o mesmo fictício para o mesmo original). **Revise o arquivo antes de compartilhar ou versionar** (`grep` pelos seus números, nomes e IPs). A anonimização cobre números longos, nomes, usuários/contas, hosts, IPv4 e IPv6, mas é melhor esforço: texto livre sem chave de nome e números curtos de pessoas não são tratados.
- O arquivo tem modo 0600 e o comando recusa sobrescrever.

## Roteiro (execute na ordem, com 20 a 30 s de intervalo entre os itens)

Anote a hora de cada passo para cruzar com o arquivo.

1. **Chamada interna**: A liga para B, B atende, conversam 20 s, A desliga.
2. **Chamada interna não atendida**: A liga para B e B não atende (deixe tocar até cair no timeout ou no correio de voz). Repita desligando do lado de A antes de tocar muito.
3. **Entrada por tronco**: de um celular, ligue para o número do tronco; A atende, 20 s, desligue.
4. **Saída por tronco**: A liga para o seu celular (prefixo de saída), atenda, 20 s, desligue.
5. **Chamada em espera**: A em chamada com B coloca B em espera, 15 s, retoma, desliga.
6. **Transferência cega**: externo liga para A; A atende e transfere (cega) para B; B atende, 10 s, desliga. Repita com chamada interna (B liga para A e A transfere para C).
7. **Transferência assistida**: externo liga para A; A consulta C (assistida), C atende, A completa a transferência; 10 s, desliga. Repita cancelando a consulta (A volta para o externo).
8. **Fila**: ligue de fora para a fila; um agente (ramal) atende; 15 s; desligue. Faça uma segunda ligação deixando a fila tocar sem atender até o transbordo.
9. **Ring group**: ligue para o ring group; um dos ramais atende; 15 s; desligue. Faça outra em que ninguém atende.
10. **Ramal desregistrando e voltando**: desligue o telefone (ou o softphone) de C da rede por 40 s; ligue de novo e espere registrar. Faça isso também com o ramal em chamada.
11. **Restart do Asterisk no meio de uma chamada**: com uma chamada em conversa (A com B) e outra tocando (externo para a fila), rode `systemctl restart asterisk`. Espere o painel reconectar e mantenha a captura por mais 1 minuto.
12. **Reload**: aplique uma alteração qualquer no IssabelPBX (sem mudar ramais) para gerar `Reload`; observe que os nomes são refeitos.

Ao final espere o prazo de `--minutos`: o comando grava um snapshot final (com as chamadas ainda em andamento) e imprime a quantidade de quadros. Guarde o arquivo e a lista de horários e resultados esperados de cada passo (quem falou com quem, direção, atendida ou não); o QA usa essa lista como oráculo do replay.

## O que conferir no arquivo antes de entregar

- Não aparece nenhum segredo (`grep -i 'secret\|password'` mostra só `***`).
- Com `--anonimizar`, não aparecem seus números externos, nomes, e-mails, domínios nem IPs reais, IPv4 ou IPv6 (`grep` por cada um). Só depois disso o arquivo pode ir para `testdata/`.
- O `#fim` traz `tecnologia`, `asterisk` e `encerrado_por`.
