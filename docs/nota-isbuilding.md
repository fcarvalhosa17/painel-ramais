# Nota tecnica: `isBuilding` e o vazamento de `hungup` (internal/state)

## O que e

`view.isBuilding` (internal/state/view.go) marca a view de **montagem**: a copia do estado que o `State` constroi durante uma sincronizacao com o AMI (`SyncStarted` ... `SyncFinished`) enquanto a view publicada (`s.cur`) continua servindo os clientes. So a view de montagem precisa lembrar de Hangups, para que um canal que caiu durante a sincronizacao nao seja ressuscitado por um snapshot atrasado.

## Todos os usos

| Onde | O que faz |
|---|---|
| `state.go`, evento `SyncStarted` | cria a view de montagem e liga `isBuilding = true` |
| `state.go`, `onSyncFinished` | a montagem vira a publicada: **desliga** `isBuilding = false` e zera `hungup = nil` |
| `state.go`, `onDisconnected` | descarta a montagem inteira (`s.building = nil`); nada a desligar |
| `channels.go`, `view.building()` | unico leitor do campo |
| `channels.go`, `onHangup` (canal desconhecido) | se `building()`, grava o Uniqueid em `hungup`; fora da montagem, ignora |
| `channels.go`, `onHangup` (canal conhecido, apos remover) | se `building()`, grava o Uniqueid em `hungup`; fora da montagem, nao grava |
| `channels.go`, `onChannelCreated` (leitura de `hungup`, linha 100) | recusa criar canal cujo Uniqueid esta em `hungup` (nao ressuscita) |

## O bug que existia

A view montada virava a publicada **sem desligar** `isBuilding`. Depois da primeira sincronizacao, todo Hangup gravava o Uniqueid em `hungup` da view publicada, para sempre: um mapa que crescia sem limite. Foi o que o teste de carga mediu como crescimento do heap (o run 38804792199, de 5 h, media esse codigo e foi cancelado por decisao do usuario, e o 38833482013 tambem foi cancelado).

## A correcao e o comportamento agora

`onSyncFinished` desliga `isBuilding` e zera `hungup`. Depois do sync a view publicada trabalha em modo normal: Hangup de canal desconhecido nao grava nada, e canal criado e encerrado some sem rastro. Durante uma **re**sincronizacao a nova montagem tem `isBuilding = true` e lembra de Hangups so ate o `SyncFinished`. Testes: `TestHangupAfterSyncDoesNotAccumulate` (1000 Hangups, `hungup` tem de ficar vazio) e `TestCallEventsFollowViewMode` (modo montagem x publicada).

## Relacao com "em conversa" sem outro lado: nenhuma

O `isBuilding` e a protecao de "em conversa" sem outro lado (`noPeer`/`onNoPeer`, calls.go) sao mecanismos independentes. O "em conversa sem outro lado" era um bug **pre-existente** de publicacao de estado, corrigido em 9c69b1a (nunca publicar "em conversa" sem o outro lado resolvido) e refinado em 8c00cec, 30bed0f e a44c4fc (protecao de 1 s, ultimo peer conhecido, contagem do loadtest). Nao foi causado nem corrigido pelo ajuste do `isBuilding`.
