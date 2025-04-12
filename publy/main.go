package main

import (
	"log/slog"
	"net/http"
	"strings"
	"errors"
	"time"
	"context"

	"github.com/coder/websocket"
)

type channel struct {
	Name string
	Listeners []chan string
}

func (c channel) Send(message string) error {
	for i := range c.Listeners {
		c.Listeners[i]<-message
	}

	return nil
}

var CHANNELS = map[string]*channel{}

func getOrCreateChannel(path string) (*channel, error) {
	name := strings.Trim(path, "/")

	if strings.Contains(name, "/") {
		return nil, errors.New("Channel name cannot contain /")
	}
	if len(name) < 16 {
		return nil, errors.New("Channel name must be at least 16 chars")
	}
	_, ok := CHANNELS[name]
	if !ok {
		CHANNELS[name] = &channel{Name: name}
	}

	return CHANNELS[name], nil
}

func isWebsocketRequest(r *http.Request) bool {
	if r.Method != "GET" {
		return false
	}

	_, ok := r.Header["Upgrade"]
	if !ok {
		return false
	}

	_, ok = r.Header["Connection"]
	if !ok {
		return false
	}

	return true
}

func handleWebsocket(w http.ResponseWriter, r *http.Request, channel *channel) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.CloseNow()

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute * 10)
	defer cancel()

	ctx = c.CloseRead(ctx)

	queue := make(chan string)
	channel.Listeners = append(channel.Listeners, queue)

	for {
		message := <-queue
		err = c.Write(ctx, websocket.MessageText, []byte(message))
		if err != nil {
			return
		}
	}
}

func handleDispatch(r *http.Request, channel *channel) error {
	var payload string

	if r.Method == "GET" {
		payload = r.URL.RawQuery
		if payload == ""  {
			return errors.New("Missing querystring")
		}
	} else {
		buffer := make([]byte, 1024)
		n, err := r.Body.Read(buffer)
		if err != nil {
			return err
		}
		payload = string(buffer[:n])
	}

	channel.Send(payload)
	return nil
}

func main() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
		// Parse channel from path.
		channel, err := getOrCreateChannel(r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		slog.Debug("Received request", "channel", channel.Name)

		// Check if websocket (has Upgrade and Connection headers)
		if isWebsocketRequest(r) {
			go handleWebsocket(w, r, channel)
			return
		}

		err = handleDispatch(r, channel)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	slog.SetLogLoggerLevel(slog.LevelDebug)

	err := http.ListenAndServe("0.0.0.0:8000", handler)
	if err != nil {
		slog.Error("Could not start server", "err", err)
	}
}
