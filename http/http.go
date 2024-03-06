// Package http provides an HTTP handler for a goqite.Queue.
// GET receives a message from the queue, if any. If there is no message, it returns a 204 No Content.
// POST sends a message to the queue.
// PUT extends a message's timeout.
// DELETE deletes a message from the queue.
package http

import (
	"encoding/json"
	"net/http"

	"github.com/maragudk/goqite"
)

func GoqiteHandler(q *goqite.Queue) http.HandlerFunc {
	type request struct {
		Message goqite.Message
	}

	type response struct {
		Message *goqite.Message
	}

	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			m, err := q.Receive(r.Context())
			if err != nil {
				http.Error(w, "error receiving message: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if m == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			if err := json.NewEncoder(w).Encode(response{Message: m}); err != nil {
				http.Error(w, "error encoding message: "+err.Error(), http.StatusInternalServerError)
				return
			}

		case http.MethodPost:
			var req request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "error decoding request: "+err.Error(), http.StatusBadRequest)
				return
			}

			if req.Message.Delay < 0 {
				http.Error(w, "delay cannot be negative", http.StatusBadRequest)
				return
			}

			if err := q.Send(r.Context(), req.Message); err != nil {
				http.Error(w, "error sending message: "+err.Error(), http.StatusInternalServerError)
				return
			}

		case http.MethodPut:
			var req request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "error decoding request: "+err.Error(), http.StatusBadRequest)
				return
			}

			if req.Message.ID == "" {
				http.Error(w, "ID cannot be empty", http.StatusBadRequest)
				return
			}
			if req.Message.Delay <= 0 {
				http.Error(w, "delay must larger than zero", http.StatusBadRequest)
				return
			}

			err := q.Extend(r.Context(), req.Message.ID, req.Message.Delay)
			if err != nil {
				http.Error(w, "error extending message: "+err.Error(), http.StatusInternalServerError)
				return
			}

		case http.MethodDelete:
			var req request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "error decoding request: "+err.Error(), http.StatusBadRequest)
				return
			}

			if req.Message.ID == "" {
				http.Error(w, "ID cannot be empty", http.StatusBadRequest)
				return
			}

			if err := q.Delete(r.Context(), req.Message.ID); err != nil {
				http.Error(w, "error deleting message: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}