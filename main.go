package main

import (
	"os"
	"io"
	"fmt"
	"strconv"
	"log/slog"
	"net/http"
	"strings"
	"errors"
	"context"

	"github.com/coder/websocket"
)

func remove[S ~[]E, E comparable](items S, item E) S {
	new := []E{}

	for _, i := range items {
		if i != item {
			new = append(new, i)
		}
	}

	return new
}

type channel struct {
	Name string
	Listeners []chan string
}

func (c channel) Send(message string) error {
	slog.Debug("Sending message", "message", message, "clients", len(c.Listeners))

	for i := range c.Listeners {
		select {
		case c.Listeners[i]<-message:
			slog.Info("Message dispatched to listener")
		default:
			slog.Warn("Could not dispatch message to listener")
		}
	}

	return nil
}

func (c *channel) AddListener(queue chan string) {
	c.Listeners = append(c.Listeners, queue)

	CHANNELS[c.Name] = c
}

func (c *channel) RemoveListener(queue chan string) {
	c.Listeners = remove(c.Listeners, queue)

	if len(c.Listeners) == 0 {
		slog.Info("Removing channel", "name", c.Name)
		delete(CHANNELS, c.Name)
	}
}

var CHANNELS = map[string]*channel{}

func newChannel(name string) *channel {
	slog.Info("Creating channel", "name", name)
	return &channel{Name: name}
}

func getChannel(name string)  *channel {
	channel, ok := CHANNELS[name]

	if !ok {
		return nil
	}

	return channel
}

func parseChannelName(path string) (string, error) {
	name := strings.Trim(path, "/")

	if len(name) < 16 {
		return "", errors.New("Channel name must be at least 16 chars")
	}

	return name, nil
}

func getOrCreateChannel(name string) (*channel, bool) {
	created := false
	channel := getChannel(name)

	if channel == nil {
		channel = newChannel(name)
		created = true
	}

	return channel, created
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

	return ok
}

func handleWebsocket(w http.ResponseWriter, r *http.Request, channel *channel) {
	slog.Debug("Handling websocket", "channel", channel.Name)

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		err := c.CloseNow()
		if err != nil {
			slog.Error("Error closing websocket", "err", err)
		}
	}()

	ctx := context.Background()
	ctx = c.CloseRead(ctx)

	queue := make(chan string)
	channel.AddListener(queue)
	defer channel.RemoveListener(queue)

	for {
		message := <-queue
		slog.Debug("Message to be sent via websocket", "message", message)
		err = c.Write(ctx, websocket.MessageText, []byte(message))
		if err != nil {
			slog.Error("Error sending message", "err", err)
			return
		}
	}
}

func handleDispatch(r *http.Request, channel *channel) error {
	slog.Debug("Handling dispatch", "channel", channel.Name)

	payload := "__empty__"

	if r.Method == "GET" {
		payload = r.URL.RawQuery

	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("Error reading body", "err", err)
		} else {
			payload = string(body)
		}
	}

	return channel.Send(payload)
}

func parseArgs() (string, int) {
	var err error
	host := "127.0.0.1"
	port := 8000
	argv := os.Args[1:]

	for i := range(argv) {
		switch flag := argv[i]; flag {
		case "-h":
			host = argv[i + 1]

		case "-p":
			port, err = strconv.Atoi(argv[i + 1])
			if err != nil {
				panic(fmt.Sprintf("Invalid port: %s, %s", argv[i + 1], err.Error()))
			}
		}
	}

	return host, port
}

func main() {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
		// Parse channel name from path.
		name, err := parseChannelName(r.URL.Path)
		if err != nil {
			slog.Error("Could not parse channel name")			
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check if websocket (has Upgrade and Connection headers)
		if isWebsocketRequest(r) {
			channel, _ := getOrCreateChannel(name)
			handleWebsocket(w, r, channel)
			return
		}

		// Not websocket, handle dispatch.
		channel := getChannel(name)
		if channel == nil {
			http.NotFound(w, r)
			return
		}

		err = handleDispatch(r, channel)
		if err != nil {
			slog.Error("Error dispatching message", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	slog.SetLogLoggerLevel(slog.LevelDebug)

	host, port := parseArgs()
	addr := fmt.Sprintf("%s:%d", host, port)
	slog.Info("Listening at", "address", addr)
	err := http.ListenAndServe(addr, handler)
	if err != nil {
		slog.Error("Could not start server", "err", err)
	}
}
