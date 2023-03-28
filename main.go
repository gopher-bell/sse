package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

func main() {
	h := newHub()

	mux := new(http.ServeMux)
	mux.HandleFunc("/sse", h.sse)
	mux.HandleFunc("/broadcast", h.broadcast)

	srv := http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Error().Err(err).Msg("http.ListenAndServe failed")
	}
}

type hub struct {
	clients      map[*client]struct{}
	clientAdd    chan *client
	clientDel    chan *client
	broadcastReq chan BroadcastRequest
}

func newHub() *hub {
	h := &hub{
		clients:      make(map[*client]struct{}),
		clientAdd:    make(chan *client),
		clientDel:    make(chan *client),
		broadcastReq: make(chan BroadcastRequest),
	}

	go h.loop()

	return h
}

func (h *hub) broadcast(w http.ResponseWriter, r *http.Request) {
	s := BroadcastRequest{
		"1",
		r.RemoteAddr,
	}

	h.broadcastReq <- s
}

func (h *hub) loop() {
	for {
		select {
		case c := <-h.clientAdd:
			h.clients[c] = struct{}{}
			log.Info().Str("name", c.name).Int("clients", len(h.clients)).Msg("loop: new client")
		case c := <-h.clientDel:
			delete(h.clients, c)
			log.Info().Str("name", c.name).Int("clients", len(h.clients)).Msg("loop: del client")
		case br := <-h.broadcastReq:
			log.Info().Int("count", len(h.clients)).Str("event", br.Event).Str("data", br.Data).Msg("loop: broadcast to clients")
			msg := br.Bytes()
			for client := range h.clients {
				_ = client.Write(msg)
			}
		}
	}
}

func (h *hub) sse(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := &client{
		name: r.RemoteAddr,
		msg:  make(chan []byte, 8),
	}

	log.Info().Str("name", client.name).Msg("client connected")
	h.clientAdd <- client
	defer func() {
		h.clientDel <- client
	}()

	for {
		select {
		case <-r.Context().Done():
			log.Info().Str("name", client.name).Err(r.Context().Err()).Msg("client closed")
			return
		case msg := <-client.msg:
			log.Debug().Str("name", client.name).Int("size", len(msg)).Msg("writing to client")
			if _, err := w.Write(msg); err != nil {
				log.Printf("client %v write failed: %v", client.name, err)
				return
			}
			flusher.Flush()
		}
	}
}

type client struct {
	name string
	msg  chan []byte
}

func (c *client) Write(msg []byte) error {
	if c == nil {
		return errors.New("nil client")
	}

	select {
	case c.msg <- msg:
		return nil
	default:
		return fmt.Errorf("client %v full", c.name)
	}
}

type BroadcastRequest struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

func (r BroadcastRequest) Bytes() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "event: %s\n", r.Event)
	for _, line := range strings.Split(r.Data, "\n") {
		fmt.Fprintf(&buf, "data: %s\n", line)
	}
	buf.WriteString("\n")
	return buf.Bytes()
}
