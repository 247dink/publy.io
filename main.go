package main

import (
	"os"
	"io"
	"fmt"
	"sync"
	"strconv"
	"log/slog"
	"net/http"
	"strings"
	"errors"
	"context"
	"encoding/json"

	"github.com/coder/websocket"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
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

type Stats struct  {
	Channels int `json:"channels"`
	Listeners int `json:"listeners"`
}

type Channel struct {
	Name string
	Listeners []chan string
	mu sync.RWMutex
}

type Channels struct {
	Channels map[string]*Channel
	mu sync.RWMutex
}

func (c *Channel) Send(message string) error {
	slog.Debug("Sending message", "message", message, "clients", len(c.Listeners))

	c.mu.RLock()
	for i := range c.Listeners {
		select {
		case c.Listeners[i]<-message:
			slog.Info("Message dispatched to listener")
		default:
			slog.Warn("Could not dispatch message to listener")
		}
	}
	c.mu.RUnlock()

	return nil
}

func (c *Channel) AddListener(queue chan string) {
	c.mu.Lock()
	c.Listeners = append(c.Listeners, queue)
	c.mu.Unlock()
}

func (c *Channel) RemoveListener(queue chan string) {
	c.mu.Lock()
	c.Listeners = remove(c.Listeners, queue)
	empty := len(c.Listeners) == 0
	c.mu.Unlock()

	if empty {
		slog.Info("Removing channel", "name", c.Name)
		CHANNELS.RemoveChannel(c.Name)
	}
}

func newChannels() (*Channels) {
	return &Channels{Channels: map[string]*Channel{}}
}

var CHANNELS = newChannels()

func (c *Channels) RemoveChannel(name string) {
	c.mu.Lock()
	delete(c.Channels, name)
	c.mu.Unlock()
}

func (c *Channels) AddChannel(name string) *Channel {
	slog.Info("Creating channel", "name", name)
	channel := &Channel{Name: name}
	c.mu.Lock()
	c.Channels[name] = channel
	c.mu.Unlock()
	return channel
}

func (c *Channels) GetChannel(name string)  *Channel {
	c.mu.RLock()
	channel, ok := c.Channels[name]
	c.mu.RUnlock()

	if !ok {
		return nil
	}

	return channel
}

func (c *Channels) GetOrCreateChannel(name string) (*Channel, bool) {
	c.mu.Lock()
	created := false
	channel, ok := c.Channels[name]

	if !ok {
		channel = &Channel{Name: name}
		c.Channels[name] = channel
		created = true
	}
	c.mu.Unlock()

	return channel, created
}

func parseChannelName(path string) (string, error) {
	name := strings.Trim(path, "/")

	if len(name) < 16 {
		return "", errors.New("Channel name must be at least 16 chars")
	}

	return name, nil
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

func handleWebsocket(w http.ResponseWriter, r *http.Request, channel *Channel) {
	slog.Debug("Handling websocket", "channel", channel.Name)

	hub := sentry.GetHubFromContext(r.Context())
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		if hub != nil {
			hub.CaptureException(err)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	queue := make(chan string, 16)
	channel.AddListener(queue)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		channel.RemoveListener(queue)
		c.Close(websocket.StatusNormalClosure, "closing")
	}()

	go func() {
		for {
			_, _, err := c.Read(ctx)
			if err != nil {
				slog.Info("Client disconnected", "err", err);
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-queue:
			slog.Debug("Message to be sent via websocket", "message", msg)
			if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
				slog.Warn("Error sending message", "err", err)
				return
			}
		}
	}
}

func handleDispatch(r *http.Request, channel *Channel) error {
	slog.Debug("Handling dispatch", "channel", channel.Name)

	hub := sentry.GetHubFromContext(r.Context())
	payload := "__empty__"

	if r.Method == "GET" {
		payload = r.URL.RawQuery

	} else {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if hub != nil {
				hub.CaptureException(err)
			}
			slog.Error("Error reading body", "err", err)
		} else {
			payload = string(body)
		}
	}

	return channel.Send(payload)
}

func handleHealth(w http.ResponseWriter) {
	health := &Stats{
		Channels: len(CHANNELS.Channels),
		Listeners: 0,
	}

	for _, channel := range CHANNELS.Channels {
		health.Listeners += len(channel.Listeners)
	}

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(health)
	if err != nil {
		slog.Error("Error dispatching message", "err", err)
		w.Header().Set("Content-Type", "text/plain")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	f := func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())

		if r.URL.Path == "/health" || r.URL.Path == "/health/" {
			handleHealth(w)
			return
		}

		// Parse channel name from path.
		name, err := parseChannelName(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Check if websocket (has Upgrade and Connection headers)
		if isWebsocketRequest(r) {
			channel, _ := CHANNELS.GetOrCreateChannel(name)
			handleWebsocket(w, r, channel)
			return
		}

		// Not websocket, handle dispatch.
		channel := CHANNELS.GetChannel(name)
		if channel == nil {
			http.NotFound(w, r)
			return
		}

		err = handleDispatch(r, channel)
		if err != nil {
			if hub != nil {
				hub.CaptureException(err)
			}
			slog.Error("Error dispatching message", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}

	var handler http.Handler
	sentryDSN := os.Getenv("SENTRY_DSN")
	if sentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn: "https://24bf3eafe200b3720f99a1616bf1a309@o4506735924019200.ingest.us.sentry.io/4509170454102016",
		}); err != nil {
			slog.Error("Sentry initialization failed", "err", err)
		}

		sentryHandler := sentryhttp.New(sentryhttp.Options{})
		handler = sentryHandler.HandleFunc(f)

	} else {
		handler = http.HandlerFunc(f)
	}

	slog.SetLogLoggerLevel(slog.LevelDebug)

	host, port := parseArgs()
	addr := fmt.Sprintf("%s:%d", host, port)
	slog.Info("Listening at", "address", addr)
	err := http.ListenAndServe(addr, handler)
	if err != nil {
		slog.Error("Could not start server", "err", err)
	}
}
