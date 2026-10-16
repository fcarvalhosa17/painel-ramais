package ami

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MinAsteriskMajor: o painel depende de BridgeEnter e DialBegin (Asterisk 13+).
const MinAsteriskMajor = 13

// Erros de detecção.
var (
	ErrVersionTooOld     = errors.New("ami: Asterisk abaixo da versão mínima")
	ErrNoTechnology      = errors.New("ami: nem chan_pjsip.so nem chan_sip.so está carregado")
	ErrTechnologyChanged = errors.New("ami: a tecnologia detectada mudou; reinicie o serviço (systemctl restart painel-ramais)")
)

var versionRe = regexp.MustCompile(`\d+`)

// ParseMajor extrai a versão maior de textos como "18.20.0", "13.38.3",
// "certified/13.21-cert5" ou "1.8.32.3".
func ParseMajor(version string) (int, error) {
	s := versionRe.FindString(version)
	if s == "" {
		return 0, fmt.Errorf("ami: versão do Asterisk ilegível: %q", version)
	}
	return strconv.Atoi(s)
}

// CheckVersion recusa versões abaixo de MinAsteriskMajor.
func CheckVersion(version string) error {
	major, err := ParseMajor(version)
	if err != nil {
		return err
	}
	if major < MinAsteriskMajor {
		return fmt.Errorf("%w: encontrada %s, mínima %d (o painel depende de BridgeEnter e DialBegin)", ErrVersionTooOld, version, MinAsteriskMajor)
	}
	return nil
}

// Detection é o resultado da escolha de tecnologia.
type Detection struct {
	Technology string // "sip" ou "pjsip"
	// Notice é não vazio quando os dois módulos estão carregados (o padrão do Issabel 5):
	// registrar em Info, não é anomalia.
	Notice string
}

// ResolveTechnology decide a tecnologia. configured: "auto", "sip" ou "pjsip".
// count só é chamado no modo auto com os dois módulos carregados.
func ResolveTechnology(configured string, pjsipLoaded, sipLoaded bool, count func(tech string) (int, error)) (Detection, error) {
	loaded := map[string]bool{"pjsip": pjsipLoaded, "sip": sipLoaded}
	switch configured {
	case "sip", "pjsip":
		if !loaded[configured] {
			return Detection{}, fmt.Errorf("ami: technology: %s configurada, mas chan_%s.so não está carregado", configured, configured)
		}
		return Detection{Technology: configured}, nil
	case "auto", "":
	default:
		return Detection{}, fmt.Errorf("ami: technology inválida: %q", configured)
	}
	switch {
	case pjsipLoaded && !sipLoaded:
		return Detection{Technology: "pjsip"}, nil
	case sipLoaded && !pjsipLoaded:
		return Detection{Technology: "sip"}, nil
	case !pjsipLoaded && !sipLoaded:
		return Detection{}, ErrNoTechnology
	}
	np, err := count("pjsip")
	if err != nil {
		return Detection{}, err
	}
	ns, err := count("sip")
	if err != nil {
		return Detection{}, err
	}
	tech := "pjsip"
	if ns > np {
		tech = "sip"
	}
	return Detection{Technology: tech, Notice: fmt.Sprintf(
		"chan_pjsip.so e chan_sip.so carregados (padrão do Issabel 5); modo auto usa %s (ramais: pjsip=%d, sip=%d)", tech, np, ns)}, nil
}

// EnsureSameTechnology falha se a tecnologia mudou depois de uma reconexão.
// previous vazio = primeira detecção.
func EnsureSameTechnology(previous, current string) error {
	if previous != "" && previous != current {
		return fmt.Errorf("%w (era %s, agora %s)", ErrTechnologyChanged, previous, current)
	}
	return nil
}

// CoreVersion lê a versão do Asterisk (CoreSettings).
func (c *Client) CoreVersion(ctx context.Context) (string, error) {
	r, err := c.Do(ctx, "CoreSettings", nil)
	if err != nil {
		return "", err
	}
	v := r.Head.Get("AsteriskVersion")
	if v == "" {
		return "", errors.New("ami: CoreSettings sem AsteriskVersion")
	}
	return v, nil
}

// ModuleLoaded pergunta ao ModuleCheck se o módulo (ex.: "chan_pjsip.so") está carregado.
func (c *Client) ModuleLoaded(ctx context.Context, module string) (bool, error) {
	_, err := c.Do(ctx, "ModuleCheck", map[string]string{"Module": module})
	var ae *ActionError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Message), "not loaded"):
		return false, nil
	}
	return false, err
}

// CountExtensions conta nomes (ObjectName) que isExt aceita em PJSIPShowEndpoints ou SIPpeers.
// isExt e a regra unica de ramal (model.IsExtension: extension_pattern + endpoints internos);
// lista vazia ("No endpoints found") conta zero.
func (c *Client) CountExtensions(ctx context.Context, tech string, isExt func(name string) bool) (int, error) {
	action := "SIPpeers"
	if tech == "pjsip" {
		action = "PJSIPShowEndpoints"
	}
	r, err := c.DoList(ctx, action, nil)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range r.Items {
		if isExt(it.Get("ObjectName")) {
			n++
		}
	}
	return n, nil
}

// DetectTechnology faz ModuleCheck dos dois módulos e aplica ResolveTechnology.
func (c *Client) DetectTechnology(ctx context.Context, configured string, isExt func(name string) bool) (Detection, error) {
	pj, err := c.ModuleLoaded(ctx, "chan_pjsip.so")
	if err != nil {
		return Detection{}, err
	}
	sp, err := c.ModuleLoaded(ctx, "chan_sip.so")
	if err != nil {
		return Detection{}, err
	}
	return ResolveTechnology(configured, pj, sp, func(tech string) (int, error) {
		return c.CountExtensions(ctx, tech, isExt)
	})
}
