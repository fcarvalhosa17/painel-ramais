package ami

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// allowedActions é a allowlist fixa (CONTRATOS.md 7.1). Qualquer outra action
// é erro de programação. Proibidas em qualquer hipótese: Command, Originate,
// Redirect, Hangup, Atxfer, Setvar, Reload, ModuleLoad, UpdateConfig,
// GetConfig, Filter, QueueReload, QueueReset, QueueAdd, QueueRemove, QueuePause,
// QueuePenalty, QueueLog, QueueSummary, MixMonitor, DBPut, PJSIPNotify e qualquer outra.
// Sem teste automatizado (CONTRATOS D12): a lista é conferida por leitura contra a tabela da seção 7.1.
var allowedActions = map[string]struct{}{
	"Login":              {},
	"Logoff":             {},
	"Ping":               {},
	"CoreSettings":       {},
	"ModuleCheck":        {},
	"PJSIPShowEndpoint":  {},
	"PJSIPShowEndpoints": {},
	"PJSIPShowContacts":  {},
	"PJSIPShowAors":      {},
	"SIPpeers":           {},
	"SIPshowpeer":        {},
	"DeviceStateList":    {},
	"CoreShowChannels":   {},
	"QueueStatus":        {}, // rc.8 (ADR-008): filas, membros e quem aguarda; só leitura
}

// ErrActionNotAllowed é devolvido para action fora da allowlist.
var ErrActionNotAllowed = errors.New("ami: action fora da allowlist")

// AllowedActions devolve a allowlist ordenada.
func AllowedActions() []string {
	out := make([]string, 0, len(allowedActions))
	for a := range allowedActions {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// validate confere allowlist e injeção de linha (CR/LF) nos parâmetros.
func validate(action string, params map[string]string) error {
	if _, ok := allowedActions[action]; !ok {
		return fmt.Errorf("%w: %q", ErrActionNotAllowed, action)
	}
	for k, v := range params {
		// Action/ActionID vindos dos parametros duplicariam o cabecalho que o cliente escreve.
		if strings.EqualFold(k, "Action") || strings.EqualFold(k, "ActionID") {
			return fmt.Errorf("ami: parâmetro %q reservado em %s", k, action)
		}
		if k == "" || strings.ContainsAny(k, ":\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("ami: parâmetro inválido em %s", action)
		}
	}
	return nil
}
