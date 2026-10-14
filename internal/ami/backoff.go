package ami

import "time"

// Backoff dobra a espera a cada falha, de Min até Max. Sem jitter: há uma
// única conexão, não existe rebanho para espalhar.
type Backoff struct {
	Min, Max time.Duration
	cur      time.Duration
}

// DefaultBackoff segue a constante do contrato: 1 s a 30 s.
func DefaultBackoff() *Backoff { return &Backoff{Min: time.Second, Max: 30 * time.Second} }

// Next devolve a próxima espera.
func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.Min
	} else if b.cur *= 2; b.cur > b.Max {
		b.cur = b.Max
	}
	return b.cur
}

// Reset volta ao mínimo (chamar depois de conexão bem-sucedida).
func (b *Backoff) Reset() { b.cur = 0 }
