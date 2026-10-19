package api

import (
	"net/http"
	"strings"

	"painel-ramais/internal/store"
)

// queuesRename: POST /api/queues/rename {queue, name} (so administrador; a checagem e do adminOnlyMsg, antes do
// corpo; CONTRATOS 4.8). Da ou tira o nome de uma fila: name "" apaga. A fila precisa existir na projecao do hub
// agora. Uma transacao no banco; depois de gravar, o daemon rele queue_names e entrega ao estado
// (QueueNamesLoaded): a fila vai no proximo delta, sem epoch novo. Sem linha em group_audit (ADR-009).
func (s *Server) queuesRename(w http.ResponseWriter, r *http.Request, sess store.Session) {
	var in struct {
		Queue *string `json:"queue"`
		Name  *string `json:"name"`
	}
	if !decodeBodyMax(w, r, &in, maxBodyBytes) {
		return
	}
	if in.Queue == nil || in.Name == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Campos queue e name obrigatorios (name pode ser vazio para apagar o nome)")
		return
	}
	queue, name := *in.Queue, strings.TrimSpace(*in.Name)
	if err := store.ValidNumber(queue); err != nil { // mesma regra do identificador de ramal: 1 a 64 sem controle
		writeError(w, http.StatusBadRequest, "bad_request", strings.Replace(err.Error(), "ramal", "fila", 1))
		return
	}
	if name != "" {
		if err := store.ValidQueueName(name); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	has, ok := s.hub.HasQueue(queue)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "degraded", "Servico parando")
		return
	}
	if !has {
		writeError(w, http.StatusNotFound, "not_found", "Fila inexistente")
		return
	}
	changed, err := s.Store.SetQueueName(r.Context(), sess.Subject, queue, name, s.Now())
	if err != nil {
		s.groupsWriteError(w, r, err) // ErrInvalid vira 400; o resto, 500 com o detalhe so no log
		return
	}
	var result any
	shown := "removido"
	if name != "" {
		result, shown = name, name
	}
	if changed {
		s.log.Info("nome de fila alterado", requestIDKey, requestID(r), "sub", sess.Subject, "queue", queue, "name", shown)
		if s.ReloadQueueNames != nil {
			if err := s.ReloadQueueNames(r.Context()); err != nil { // o nome ja esta gravado: repetir e seguro
				s.internalError(w, r, err)
				return
			}
		}
	}
	writeOK(w, map[string]any{"queue": queue, "name": result}, nil)
}
