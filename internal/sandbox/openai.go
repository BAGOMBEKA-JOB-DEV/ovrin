package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxRequestBytes bounds what the sandbox will read from one request.
//
// A test server with an unbounded read is a denial-of-service target in the
// test suite itself, and page images make these bodies genuinely large.
const maxRequestBytes = 8 << 20

// chatRequest is as much of OpenAI's chat-completions request as the sandbox
// needs to decide what to answer.
//
// Decoding the response_format shape in full, rather than reaching for the
// schema alone, means a request that names the wrong type or omits the name
// OpenAI requires is visible to a test.
type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`

	ResponseFormat *struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name   string         `json:"name"`
			Strict bool           `json:"strict"`
			Schema map[string]any `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

// promptText returns the text of the last user turn, flattened.
//
// It exists for [FaultEchoPrompt], which needs something from the request to
// quote back.
func (r chatRequest) promptText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role != "user" {
			continue
		}
		switch c := r.Messages[i].Content.(type) {
		case string:
			return c
		case []any:
			var b strings.Builder
			for _, raw := range c {
				block, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if s, ok := block["text"].(string); ok {
					b.WriteString(s)
				}
			}
			return b.String()
		}
	}
	return ""
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, "invalid_request_error", "",
			"Not allowed: "+r.Method)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "",
			"could not read request body")
		return
	}

	// Recorded before authorisation, so a test can assert that a rejected call
	// still reached the wire.
	fault := s.record(body)

	if !s.authorised(r) {
		apiError(w, http.StatusUnauthorized, "invalid_request_error",
			"invalid_api_key", "Incorrect API key provided.")
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "",
			"could not parse request body")
		return
	}

	// A fault named in the model identifier wins, because it is the only
	// channel a test has that does not require the adapter to cooperate.
	if f, ok := faultFromModel(req.Model); ok {
		fault = f
	}

	s.serve(w, r, req, fault)
}

// serve writes the response for one fault.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, req chatRequest, fault Fault) {
	model, reply, usage := s.settings()

	switch fault {
	case FaultNone:
		writeJSON(w, http.StatusOK, completion(model, reply, "stop", usage))

	case FaultInvalidJSONReply:
		// A valid envelope carrying an invalid document. The transport is
		// blameless; the model is not.
		writeJSON(w, http.StatusOK,
			completion(model, `{"amount": 12.5, "currency": `, "stop", usage))

	case FaultEmptyChoices:
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      "chatcmpl-sandbox",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []any{},
			"usage":   usageBody(usage),
		})

	case FaultRefusal:
		// Content is explicitly null rather than absent, which is what the
		// real API sends: an adapter reading it as a string gets a type
		// mismatch here rather than in production. Built directly rather than
		// by editing a completion, so no chain of type assertions stands
		// between this fault and the response.
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      "chatcmpl-sandbox",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": nil},
				"finish_reason": "content_filter",
			}},
			"usage": usageBody(usage),
		})

	case FaultMalformedJSON:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// A short write here would only produce a shorter broken body, which
		// is still the fault this case exists to serve.
		_, _ = io.WriteString(w, `{"id":"chatcmpl-sandbox","choices":[{"message":{"role":`) //nolint:errcheck // see above

	case FaultWrongContentType:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck // a short write leaves a shorter wrong-typed body, still the fault
		_, _ = io.WriteString(w,
			"<html><head><title>502 Bad Gateway</title></head><body>"+
				"<h1>502 Bad Gateway</h1><hr/>nginx</body></html>")

	case FaultTruncatedBody:
		full := mustJSON(completion(model, reply, "stop", usage))
		writeRawAndClose(w,
			"HTTP/1.1 200 OK\r\n"+
				"Content-Type: application/json\r\n"+
				fmt.Sprintf("Content-Length: %d\r\n", len(full))+
				"\r\n",
			string(full[:len(full)/2]))

	case FaultDisconnectMidBody:
		full := mustJSON(completion(model, reply, "stop", usage))
		half := full[:len(full)/2]
		// Chunked, with the first chunk delivered and the terminating chunk
		// never sent. The client has already consumed bytes, so there is no
		// status code left to fail with — which is the point.
		writeRawAndClose(w,
			"HTTP/1.1 200 OK\r\n"+
				"Content-Type: application/json\r\n"+
				"Transfer-Encoding: chunked\r\n"+
				"\r\n",
			fmt.Sprintf("%x\r\n%s\r\n", len(half), half))

	case FaultRateLimitRetryAfter:
		w.Header().Set("Retry-After", "1")
		apiError(w, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded",
			"Rate limit reached for requests.")

	case FaultRateLimitNoRetryAfter:
		apiError(w, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded",
			"Rate limit reached for requests.")

	case FaultServerError:
		apiError(w, http.StatusInternalServerError, "server_error", "",
			"The server had an error while processing your request.")

	case FaultUnavailable:
		apiError(w, http.StatusServiceUnavailable, "server_error", "",
			"The engine is currently overloaded.")

	case FaultUnauthorized:
		apiError(w, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key",
			"Incorrect API key provided.")

	case FaultForbidden:
		apiError(w, http.StatusForbidden, "invalid_request_error", "unsupported_country",
			"Country, region, or territory not supported.")

	case FaultBadRequest:
		apiError(w, http.StatusBadRequest, "invalid_request_error", "",
			"Invalid schema for response_format.")

	case FaultNotFound:
		apiError(w, http.StatusNotFound, "invalid_request_error", "model_not_found",
			fmt.Sprintf("The model `%s` does not exist.", req.Model))

	case FaultEchoPrompt:
		apiError(w, http.StatusBadRequest, "invalid_request_error", "invalid_prompt",
			"Invalid prompt: your request was rejected. Offending content: "+
				req.promptText())

	case FaultHang:
		select {
		case <-r.Context().Done():
		case <-s.closing:
		}

	default:
		apiError(w, http.StatusInternalServerError, "server_error", "",
			"sandbox: unknown fault "+string(fault))
	}
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	s.record(nil)
	if !s.authorised(r) {
		apiError(w, http.StatusUnauthorized, "invalid_request_error",
			"invalid_api_key", "Incorrect API key provided.")
		return
	}
	model, _, _ := s.settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []any{map[string]any{
			"id":       model,
			"object":   "model",
			"created":  1735689600,
			"owned_by": "ovrin-sandbox",
		}},
	})
}

// completion builds a well-formed chat-completions response.
func completion(model, content, finish string, u Usage) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-sandbox",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": finish,
		}},
		"usage": usageBody(u),
	}
}

// usageBody reports tokens the way OpenAI does, with the cached count nested
// inside the prompt count rather than beside it.
func usageBody(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":         u.Input,
		"completion_tokens":     u.Output,
		"total_tokens":          u.Input + u.Output,
		"prompt_tokens_details": map[string]any{"cached_tokens": u.Cached},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A write that fails means the client has already hung up, which is the
	// client's business and not something this server can report to anyone.
	_, _ = w.Write(mustJSON(v)) //nolint:errcheck // see above
}

// apiError writes an OpenAI-shaped error envelope.
func apiError(w http.ResponseWriter, status int, kind, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": kind, "code": code},
	})
}

// mustJSON encodes v, returning an error envelope rather than panicking.
//
// Nothing in this package builds a value encoding/json cannot encode, so the
// error branch is unreachable — but a panic inside a test server surfaces as a
// hung request rather than a failed one, which is a bad way to learn about a
// typo.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":{"message":"sandbox: could not encode response"}}`)
	}
	return b
}

// writeRawAndClose hijacks the connection, writes head and body verbatim, and
// drops it.
//
// Hijacking is the only way to lie about a body: net/http computes a correct
// Content-Length and a correct terminating chunk for anything written through
// a ResponseWriter, which are exactly the two things a truncated and a
// disconnected response get wrong.
func writeRawAndClose(w http.ResponseWriter, head, body string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		// Not reachable through httptest, but a caller mounting this handler
		// behind something else deserves an answer rather than silence.
		apiError(w, http.StatusInternalServerError, "server_error", "",
			"sandbox: connection cannot be hijacked")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close() //nolint:errcheck // the whole point is an abrupt close

	// Every write here is best-effort by construction: the connection is about
	// to be dropped on purpose, so a failed write and a successful one produce
	// the same fault.
	_, _ = buf.WriteString(head) //nolint:errcheck // see above
	_, _ = buf.WriteString(body) //nolint:errcheck // see above
	_ = buf.Flush()              //nolint:errcheck // see above
}
